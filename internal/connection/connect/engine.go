// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package connect runs the connect flow, which gives a connection the
// tokens of a consent. A session lives in the connection's token state, so
// any frontend replica serves any request of it, and every move between
// phases is one compare-and-swap write:
//
//	Start ----------> waiting --- Callback: code exchanged ---> confirm
//	(replaces any      |  \                                       |
//	 earlier session)  |   `-- Callback: expected account,        | Confirm by
//	                   |       vouched for, same account_id --.   | the starter
//	                   |                                      v   v
//	                   |                        tokens live, generation + 1,
//	                   |                        connected, session cleared
//	                   v
//	                failed: consent error, cancel; expired after 10 minutes
//
// A callback first claims the waiting session in its own write, so that
// a second callback is refused before it exchanges a code.
package connect

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
)

const (
	// sessionTTL is how long a session waits for consent and confirmation.
	sessionTTL = 10 * time.Minute
	// consentTimeout bounds the provider calls of one callback: the code
	// exchange, the identity, and the probes. A callback's claim on its
	// session holds as long.
	consentTimeout = 45 * time.Second
	sessionIDBytes = 16
	// refreshTokenExpiresIn is the token response field in which a
	// provider reports the refresh token's lifetime, in seconds.
	refreshTokenExpiresIn = "refresh_token_expires_in"
	redacted              = "[redacted]"
)

// Reasons for a failed session that do not come from the provider.
const (
	ReasonExpired   = "expired"
	ReasonCancelled = "cancelled"
	ReasonReplaced  = "replaced"
)

var (
	// ErrUnsupported reports a provider or grant the engine cannot connect.
	ErrUnsupported = errors.New("connect is not supported")
	// ErrCallbackRefused reports a callback whose state is not valid, has
	// expired, or names a session that no longer waits for consent. It
	// changes nothing.
	ErrCallbackRefused = errors.New("callback refused")
	// ErrConsentFailed reports a consent that the provider refused or that
	// Dagu could not check. The session fails with the provider's error.
	ErrConsentFailed = errors.New("consent failed")
	// ErrSessionNotFound reports a session handle that names no session.
	ErrSessionNotFound = errors.New("connect session not found")
	// ErrSessionEnded reports a session that was confirmed, failed,
	// expired, was cancelled, or was replaced.
	ErrSessionEnded = errors.New("connect session has ended")
	// ErrAwaitingConsent reports a confirm of a session whose consent has
	// not completed.
	ErrAwaitingConsent = errors.New("connect session is waiting for consent")
	// ErrNotStarter reports a confirm by a user other than the starter.
	ErrNotStarter = errors.New("only the user who started the session may confirm it")
)

// Config is what an Engine needs.
type Config struct {
	Store connection.Store
	// Providers are keyed by the provider each connects.
	Providers map[connection.Provider]Provider
	// Encryptor holds the server's key, which seals the state parameter.
	Encryptor *crypto.Encryptor
	// AnyUserConfirms lets any caller confirm any session. It is set when
	// authentication is off.
	AnyUserConfirms bool
	// OnStatusChange, when set, receives each change of a connection's
	// status that the engine's own writes make.
	OnStatusChange func(context.Context, broker.StatusChange)
	// Now returns the current time. It defaults to time.Now.
	Now func() time.Time
}

// Engine runs connect sessions.
type Engine struct {
	store           connection.Store
	providers       map[connection.Provider]Provider
	enc             *crypto.Encryptor
	anyUserConfirms bool
	onStatusChange  func(context.Context, broker.StatusChange)
	now             func() time.Time
}

// StartOptions are what a starter gives for a session.
type StartOptions struct {
	// User is the starter.
	User     string
	Redirect RedirectOptions
	// ExpectedAccount is the email address the starter expects to sign
	// in, or empty.
	ExpectedAccount string
}

// Authorization is a started session.
type Authorization struct {
	// SessionID is the session handle.
	SessionID string
	// URL opens the provider's consent page.
	URL       string
	Redirect  Redirect
	ExpiresAt time.Time
}

// CallbackParams are the query parameters of a provider's redirect after
// consent.
type CallbackParams struct {
	State string
	Code  string
	// Error and ErrorDescription are set when consent failed.
	Error            string
	ErrorDescription string
}

// Phase is how far a session has come, as its starter sees it.
type Phase string

const (
	PhaseWaiting   Phase = "waiting"
	PhaseConfirm   Phase = "confirm"
	PhaseConnected Phase = "connected"
	PhaseFailed    Phase = "failed"
)

// SessionStatus is what the starter sees of a session. It holds no secret.
type SessionStatus struct {
	Phase Phase
	// Account and the fields after it are set in PhaseConfirm.
	Account               string
	AccountID             string
	GrantedScopes         []string
	RefreshTokenExpiresAt time.Time
	Probes                []connection.Probe
	// Reason is set in PhaseFailed: the provider's error, ReasonExpired,
	// ReasonCancelled, or ReasonReplaced.
	Reason    string
	ExpiresAt time.Time
}

// New returns an engine over cfg.
func New(cfg Config) (*Engine, error) {
	if cfg.Store == nil || cfg.Encryptor == nil {
		return nil, errors.New("connect engine: store and encryptor are required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{
		store:           cfg.Store,
		providers:       cfg.Providers,
		enc:             cfg.Encryptor,
		anyUserConfirms: cfg.AnyUserConfirms,
		onStatusChange:  cfg.OnStatusChange,
		now:             now,
	}, nil
}

// ParseClientFile returns the app that a client file of provider
// describes, without an ID or status.
func (e *Engine) ParseClientFile(provider connection.Provider, data []byte) (*connection.App, error) {
	p, ok := e.providers[provider]
	if !ok {
		return nil, fmt.Errorf("%w: provider %q", ErrUnsupported, provider)
	}
	return p.ParseClientFile(data)
}

// Start starts a session of conn, which replaces and fails an earlier one.
// The live tokens stay as they are until the session is confirmed.
func (e *Engine) Start(ctx context.Context, conn *connection.Connection, opts StartOptions) (*Authorization, error) {
	if conn.Grant != connection.GrantAuthorizationCode {
		return nil, fmt.Errorf("%w: the %s grant", ErrUnsupported, conn.Grant)
	}
	provider, app, err := e.client(ctx, conn)
	if err != nil {
		return nil, err
	}
	redirect, err := ChooseRedirect(app, opts.Redirect)
	if err != nil {
		return nil, err
	}
	cfg, err := oauthConfig(provider, app, conn, redirect.URI)
	if err != nil {
		return nil, err
	}
	sessionID, err := newSessionID()
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()
	expiresAt := e.now().Add(sessionTTL)
	state, err := sealState(e.enc, authState{
		Workspace:       conn.Workspace,
		Name:            conn.Name,
		ConnectionID:    conn.ID,
		SessionID:       sessionID,
		Verifier:        verifier,
		RedirectURI:     redirect.URI,
		ExpectedAccount: opts.ExpectedAccount,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("connection %q: seal the state parameter: %w", conn.Name, err)
	}
	_, err = e.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		st.Session = &connection.Session{
			ID:        sessionID,
			User:      opts.User,
			Phase:     connection.SessionPhaseWaiting,
			ExpiresAt: expiresAt,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("connection %q: start the session: %w", conn.Name, err)
	}
	authOpts := append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}, provider.AuthorizationParams(conn)...)
	return &Authorization{
		SessionID: sessionID,
		URL:       cfg.AuthCodeURL(state, authOpts...),
		Redirect:  redirect,
		ExpiresAt: expiresAt,
	}, nil
}

// Callback completes the consent of the session that p.State names, while
// that session waits for it. It exchanges the code, reads the account, and
// probes the services, then moves the session to confirm. An expected
// account that the provider vouches for, with the account ID the
// connection already has, if any, is confirmed at once.
func (e *Engine) Callback(ctx context.Context, p CallbackParams) error {
	st, err := openState(e.enc, p.State)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCallbackRefused, err)
	}
	if !e.now().Before(st.ExpiresAt) {
		return fmt.Errorf("%w: the session has expired", ErrCallbackRefused)
	}
	conn, err := e.store.Get(ctx, st.key())
	if errors.Is(err, connection.ErrNotFound) || (err == nil && conn.ID != st.ConnectionID) {
		return fmt.Errorf("%w: the connection was removed", ErrCallbackRefused)
	}
	if err != nil {
		return err
	}
	claim, err := e.claim(ctx, conn, st.SessionID)
	if err != nil {
		return err
	}

	// A code that the provider accepted is spent, so the provider calls run
	// to their end even when the browser goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), consentTimeout)
	defer cancel()
	if p.Error != "" {
		return e.fail(ctx, conn, st.SessionID, claim, consentError(p.Error, p.ErrorDescription))
	}
	tokens, probes, err := e.consent(ctx, conn, st, p.Code)
	if err != nil {
		return e.fail(ctx, conn, st.SessionID, claim, err.Error())
	}

	var from connection.TokenStatus
	confirmed := false
	_, err = e.store.UpdateTokenState(ctx, conn, func(s *connection.TokenState) error {
		if err := e.claimed(s.Session, st.SessionID, claim); err != nil {
			return err
		}
		from = s.Status
		confirmed = confirmsAtOnce(st.ExpectedAccount, tokens, s.AccountID)
		if confirmed {
			goLive(s, tokens.Tokens)
			return nil
		}
		s.Session.Phase = connection.SessionPhaseConfirm
		s.Session.Tokens = tokens.Tokens
		s.Session.Probes = probes
		return nil
	})
	if err != nil {
		return err
	}
	if confirmed {
		e.reportConnected(ctx, conn, from)
	}
	return nil
}

// Confirm makes the tokens of a session in confirm live, in one write that
// also sets the account, increases the generation, sets connected, and
// clears the session. Only the starter confirms, unless any user may.
func (e *Engine) Confirm(ctx context.Context, conn *connection.Connection, sessionID, user string) error {
	var from connection.TokenStatus
	_, err := e.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		s := st.Session
		if err := e.current(s, sessionID); err != nil {
			return err
		}
		if s.Phase == connection.SessionPhaseWaiting {
			return ErrAwaitingConsent
		}
		if !e.anyUserConfirms && s.User != user {
			return ErrNotStarter
		}
		from = st.Status
		goLive(st, s.Tokens)
		return nil
	})
	if err != nil {
		return fmt.Errorf("connection %q: confirm: %w", conn.Name, err)
	}
	e.reportConnected(ctx, conn, from)
	return nil
}

// Cancel fails a session that has not ended. The live tokens stay.
func (e *Engine) Cancel(ctx context.Context, conn *connection.Connection, sessionID string) error {
	_, err := e.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := e.current(st.Session, sessionID); err != nil {
			return err
		}
		failSession(st.Session, ReasonCancelled)
		return nil
	})
	if err != nil {
		return fmt.Errorf("connection %q: cancel: %w", conn.Name, err)
	}
	return nil
}

// Session returns the status of the session that sessionID names. A
// session that is no longer stored reads as connected while the connection
// is connected, since a confirm clears it.
func (e *Engine) Session(ctx context.Context, conn *connection.Connection, sessionID string) (SessionStatus, error) {
	state, err := e.store.TokenState(ctx, conn)
	if err != nil {
		return SessionStatus{}, err
	}
	s := state.Session
	switch {
	case s == nil && state.Status == connection.TokenStatusConnected:
		return SessionStatus{Phase: PhaseConnected}, nil
	case s == nil:
		return SessionStatus{}, ErrSessionNotFound
	case !sameID(s.ID, sessionID):
		return SessionStatus{Phase: PhaseFailed, Reason: ReasonReplaced}, nil
	}
	status := SessionStatus{ExpiresAt: s.ExpiresAt}
	switch {
	case s.Phase == connection.SessionPhaseFailed:
		status.Phase, status.Reason = PhaseFailed, s.Reason
	case e.expired(s):
		status.Phase, status.Reason = PhaseFailed, ReasonExpired
	case s.Phase == connection.SessionPhaseWaiting:
		status.Phase = PhaseWaiting
	default:
		status.Phase = PhaseConfirm
		status.Account = s.Tokens.Account
		status.AccountID = s.Tokens.AccountID
		status.GrantedScopes = s.Tokens.GrantedScopes
		status.RefreshTokenExpiresAt = s.Tokens.RefreshTokenExpiresAt
		status.Probes = s.Probes
	}
	return status, nil
}

// consentTokens are the tokens of a consent and the account they signed
// in, with how far the provider vouches for its email.
type consentTokens struct {
	connection.Tokens
	vouchedEmail string
}

// consent exchanges code and checks the result. Its error is shown to
// people, so it carries no secret.
func (e *Engine) consent(ctx context.Context, conn *connection.Connection, st authState, code string) (consentTokens, []connection.Probe, error) {
	provider, app, err := e.client(ctx, conn)
	if err != nil {
		return consentTokens{}, nil, err
	}
	cfg, err := oauthConfig(provider, app, conn, st.RedirectURI)
	if err != nil {
		return consentTokens{}, nil, err
	}
	secrets := []string{code, st.Verifier, app.ClientSecret}
	token, err := provider.Exchange(ctx, cfg, conn, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.ErrorCode != "" {
			return consentTokens{}, nil, errors.New(redact(consentError(re.ErrorCode, re.ErrorDescription), secrets...))
		}
		return consentTokens{}, nil, fmt.Errorf("exchange the code: %s", redact(err.Error(), secrets...))
	}
	if token.RefreshToken == "" {
		return consentTokens{}, nil, errors.New("the provider returned no refresh token")
	}
	secrets = append(secrets, token.AccessToken, token.RefreshToken)
	id, err := provider.Identity(ctx, app, token)
	if err != nil {
		return consentTokens{}, nil, fmt.Errorf("read the account: %s", redact(err.Error(), secrets...))
	}
	if id.AccountID == "" {
		return consentTokens{}, nil, errors.New("the provider returned no account ID")
	}
	probes := provider.Probe(ctx, conn, token)
	for i := range probes {
		probes[i].Error = redact(probes[i].Error, secrets...)
	}
	return consentTokens{
		Tokens:       e.tokensOf(token, id, cfg.Scopes),
		vouchedEmail: id.VouchedEmail,
	}, probes, nil
}

func (e *Engine) tokensOf(token *oauth2.Token, id Identity, requested []string) connection.Tokens {
	now := e.now()
	tokens := connection.Tokens{
		Account:       id.Account,
		AccountID:     id.AccountID,
		RefreshToken:  token.RefreshToken,
		GrantedScopes: requested,
	}
	// A response without scope grants the requested scopes (RFC 6749,
	// section 5.1).
	if scope, ok := token.Extra("scope").(string); ok && scope != "" {
		tokens.GrantedScopes = strings.Fields(scope)
	}
	if token.AccessToken != "" {
		tokens.AccessTokens = map[string]connection.AccessToken{
			"": {Token: token.AccessToken, ExpiresAt: token.Expiry, IssuedAt: now},
		}
	}
	if secs := extraSeconds(token, refreshTokenExpiresIn); secs > 0 {
		tokens.RefreshTokenExpiresAt = now.Add(time.Duration(secs) * time.Second)
	}
	return tokens
}

// extraSeconds reads a number of seconds from a token response, which is
// a JSON number or, in a form-encoded response, a string.
func extraSeconds(token *oauth2.Token, field string) int64 {
	switch v := token.Extra(field).(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

// claim marks the session sessionID as being completed by one callback,
// and returns the claim's time. It refuses a session that does not wait
// for consent, or that another callback holds.
func (e *Engine) claim(ctx context.Context, conn *connection.Connection, sessionID string) (time.Time, error) {
	now := e.now()
	_, err := e.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := e.waiting(st.Session, sessionID); err != nil {
			return err
		}
		if c := st.Session.ClaimedAt; !c.IsZero() && now.Before(c.Add(consentTimeout)) {
			return fmt.Errorf("%w: another callback is completing the session", ErrCallbackRefused)
		}
		st.Session.ClaimedAt = now
		return nil
	})
	return now, err
}

// claimed returns nil when s is the session sessionID, waits for consent,
// and is held by claim.
func (e *Engine) claimed(s *connection.Session, sessionID string, claim time.Time) error {
	if err := e.waiting(s, sessionID); err != nil {
		return err
	}
	if !s.ClaimedAt.Equal(claim) {
		return fmt.Errorf("%w: another callback took over the session", ErrCallbackRefused)
	}
	return nil
}

// fail fails the session sessionID that claim holds, and returns the
// callback's error.
func (e *Engine) fail(ctx context.Context, conn *connection.Connection, sessionID string, claim time.Time, reason string) error {
	_, err := e.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := e.claimed(st.Session, sessionID, claim); err != nil {
			return err
		}
		failSession(st.Session, reason)
		return nil
	})
	return errors.Join(fmt.Errorf("%w: %s", ErrConsentFailed, reason), err)
}

func (e *Engine) client(ctx context.Context, conn *connection.Connection) (Provider, *connection.App, error) {
	provider, ok := e.providers[conn.Provider]
	if !ok {
		return nil, nil, fmt.Errorf("%w: provider %q", ErrUnsupported, conn.Provider)
	}
	app, err := e.store.GetApp(ctx, conn.App)
	if err != nil {
		return nil, nil, fmt.Errorf("connection %q: %w", conn.Name, err)
	}
	return provider, app, nil
}

// current returns nil when s is the session sessionID and has not ended.
func (e *Engine) current(s *connection.Session, sessionID string) error {
	switch {
	case s == nil || !sameID(s.ID, sessionID):
		return fmt.Errorf("%w: it was confirmed or replaced", ErrSessionEnded)
	case s.Phase == connection.SessionPhaseFailed:
		return fmt.Errorf("%w: %s", ErrSessionEnded, s.Reason)
	case e.expired(s):
		return fmt.Errorf("%w: %s", ErrSessionEnded, ReasonExpired)
	}
	return nil
}

// waiting returns nil when s is the session sessionID and waits for
// consent, and the callback's refusal otherwise.
func (e *Engine) waiting(s *connection.Session, sessionID string) error {
	if err := e.current(s, sessionID); err != nil {
		return fmt.Errorf("%w: %w", ErrCallbackRefused, err)
	}
	if s.Phase != connection.SessionPhaseWaiting {
		return fmt.Errorf("%w: the session already received its callback", ErrCallbackRefused)
	}
	return nil
}

func (e *Engine) expired(s *connection.Session) bool {
	return !e.now().Before(s.ExpiresAt)
}

// reportConnected reports a confirm that changed the status from from.
func (e *Engine) reportConnected(ctx context.Context, conn *connection.Connection, from connection.TokenStatus) {
	if e.onStatusChange == nil || from == connection.TokenStatusConnected {
		return
	}
	e.onStatusChange(context.WithoutCancel(ctx), broker.StatusChange{
		Connection:     conn.Key(),
		ConnectionID:   conn.ID,
		App:            conn.App,
		ConnectionFrom: from,
		ConnectionTo:   connection.TokenStatusConnected,
	})
}

// goLive makes tokens the live tokens of st, with their account, in the
// one write that confirms a session.
func goLive(st *connection.TokenState, tokens connection.Tokens) {
	st.Tokens = tokens
	st.Generation++
	st.Status = connection.TokenStatusConnected
	st.StatusReason = ""
	st.Session = nil
}

// confirmsAtOnce reports whether a consent needs no confirm: its account
// is the expected one, by an email that the provider vouches for, and has
// the account ID that the connection already has, if any.
func confirmsAtOnce(expected string, tokens consentTokens, liveAccountID string) bool {
	if expected == "" || tokens.vouchedEmail == "" || !strings.EqualFold(expected, tokens.vouchedEmail) {
		return false
	}
	return liveAccountID == "" || liveAccountID == tokens.AccountID
}

func failSession(s *connection.Session, reason string) {
	s.Phase = connection.SessionPhaseFailed
	s.Reason = reason
	s.Tokens = connection.Tokens{}
	s.Probes = nil
}

func oauthConfig(provider Provider, app *connection.App, conn *connection.Connection, redirectURI string) (*oauth2.Config, error) {
	endpoint, err := provider.Endpoint(app)
	if err != nil {
		return nil, fmt.Errorf("OAuth app %q: %w", app.Name, err)
	}
	return &oauth2.Config{
		ClientID:     app.ClientID,
		ClientSecret: app.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  redirectURI,
		Scopes:       conn.RequestedScopes(),
	}, nil
}

func newSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate a session ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// sameID compares session IDs in constant time, since a session handle
// lets its holder act on the session.
func sameID(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// consentError is the reason of a consent that the provider reported as
// failed, such as access_denied.
func consentError(code, description string) string {
	description, _, _ = strings.Cut(strings.TrimSpace(description), "\n")
	if description == "" {
		return code
	}
	return code + ": " + description
}

// redact removes secrets from a provider's error text, which is stored and
// shown to people, also in the form encoding that carried them.
func redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for _, form := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			text = strings.ReplaceAll(text, form, redacted)
		}
	}
	return text
}
