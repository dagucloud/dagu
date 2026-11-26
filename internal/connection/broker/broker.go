// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package broker hands out the access tokens of connections. A process
// that has the connection store refreshes a token under the connection's
// lock, so callers on one host that need the same refresh cause one
// provider request:
//
//	Token -> in-process cache (one per connection and resource)
//	           |  miss: one caller per cache entry at a time
//	           v
//	         token state usable? -- yes --> return it
//	           |  no
//	           v
//	         TryLock -- held by another --> wait for release,
//	           |                            read the state once
//	           v
//	         read again, refresh, CAS write, unlock
package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/oauthrefresh"
)

//nolint:gosec // A fixed provider endpoint, not a credential.
const googleTokenURL = "https://oauth2.googleapis.com/token"

// OAuth error codes that the broker handles apart from transient failures.
const (
	codeInvalidGrant       = "invalid_grant"
	codeInvalidClient      = "invalid_client"
	codeUnauthorizedClient = "unauthorized_client"
)

// ErrReconnectRequired reports a connection that cannot hand out tokens
// until it is reconnected or its app is fixed: it is pending or needs a
// reconnect, or its app is invalid.
var ErrReconnectRequired = errors.New("reconnect required")

var (
	// errStartOver drops a refresh result that a concurrent change made
	// stale, so the call reads the token state again.
	errStartOver = errors.New("token state changed during the refresh")
	// errNoChange skips a status write that another process already made.
	errNoChange = errors.New("status already set")
)

// Source hands out the access tokens of connections. The local Broker
// refreshes them; a process without the connection store asks one that
// has it.
type Source interface {
	// Token returns an access token of conn, valid at resource, that stays
	// valid for at least minTTL, and how long it stays valid. An empty
	// resource selects the connection's only resource. A minTTL longer than
	// half the token's lifetime is cut to half. expiresIn is zero when the
	// provider reported no expiry.
	Token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration) (token string, expiresIn time.Duration, err error)
	// Rejected reports that resource rejected token, and returns a token as
	// Token does. It refreshes only while token is still the stored token;
	// otherwise it returns the stored token.
	Rejected(ctx context.Context, conn *connection.Connection, resource, token string, minTTL time.Duration) (string, time.Duration, error)
}

// StatusChange is a status change that a broker's write made. Of the
// processes that see the same failure, only the one whose write changed the
// status reports it.
type StatusChange struct {
	Connection *connection.Connection
	// State is set when the connection's status changed.
	State *connection.TokenState
	// App is set when the status of the connection's app changed.
	App *connection.App
}

// Config is what a Broker needs.
type Config struct {
	Store connection.Store
	Locks connection.Locks
	// OnStatusChange, when set, receives each status change that the
	// broker's own writes make.
	OnStatusChange func(context.Context, StatusChange)
}

var _ Source = (*Broker)(nil)

// Broker is the Source of a process that has the connection store. It
// caches tokens in memory per connection and resource.
type Broker struct {
	store          connection.Store
	locks          connection.Locks
	onStatusChange func(context.Context, StatusChange)

	mu    sync.Mutex
	gates map[gateKey]*oauthrefresh.Gate
}

type gateKey struct {
	connectionID string
	resource     string
}

// request is what one call needs: a token for resource that stays valid
// for minTTL and is not the rejected one.
type request struct {
	resource string
	minTTL   time.Duration
	rejected string
	// start is when the call began. A refresh error recorded since then
	// is the outcome of a refresh the call waited for.
	start time.Time
}

// New returns a broker over cfg's store and locks.
func New(cfg Config) (*Broker, error) {
	if cfg.Store == nil || cfg.Locks == nil {
		return nil, errors.New("token broker: store and locks are required")
	}
	return &Broker{
		store:          cfg.Store,
		locks:          cfg.Locks,
		onStatusChange: cfg.OnStatusChange,
		gates:          make(map[gateKey]*oauthrefresh.Gate),
	}, nil
}

func (b *Broker) Token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration) (string, time.Duration, error) {
	return b.token(ctx, conn, request{resource: resource, minTTL: minTTL})
}

func (b *Broker) Rejected(ctx context.Context, conn *connection.Connection, resource, token string, minTTL time.Duration) (string, time.Duration, error) {
	return b.token(ctx, conn, request{resource: resource, minTTL: minTTL, rejected: token})
}

func (b *Broker) token(ctx context.Context, conn *connection.Connection, req request) (string, time.Duration, error) {
	if conn == nil {
		return "", 0, connection.ErrNotFound
	}
	req.start = time.Now()
	gate := b.gate(gateKey{connectionID: conn.ID, resource: req.resource})
	token, err := gate.TokenIf(ctx, req.usableCached, func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
		at, err := b.fetch(ctx, conn, req)
		if err != nil {
			return nil, err
		}
		return toCached(at), nil
	})
	if err != nil {
		return "", 0, err
	}
	var expiresIn time.Duration
	if !token.Expiry.IsZero() {
		expiresIn = time.Until(token.Expiry)
	}
	return token.AccessToken, expiresIn, nil
}

func (b *Broker) gate(key gateKey) *oauthrefresh.Gate {
	b.mu.Lock()
	defer b.mu.Unlock()
	gate, ok := b.gates[key]
	if !ok {
		gate = oauthrefresh.NewGate()
		b.gates[key] = gate
	}
	return gate
}

// fetch returns a token from the token state, refreshing it under the
// connection's lock when it is not usable. A caller that finds the lock
// held waits for the holder and takes its outcome instead of calling the
// provider.
func (b *Broker) fetch(ctx context.Context, conn *connection.Connection, req request) (connection.AccessToken, error) {
	for {
		app, err := b.store.GetApp(ctx, conn.App)
		if err != nil {
			return connection.AccessToken{}, fmt.Errorf("connection %q: %w", conn.Name, err)
		}
		if app.Status == connection.AppStatusInvalid {
			return connection.AccessToken{}, appInvalid(conn, app.Name, app.StatusReason)
		}
		state, err := b.store.TokenState(ctx, conn)
		if err != nil {
			return connection.AccessToken{}, err
		}
		if at, done, err := req.settle(conn, state); done {
			return at, err
		}
		if conn.Grant != connection.GrantAuthorizationCode && conn.Grant != connection.GrantDeviceCode {
			return connection.AccessToken{}, fmt.Errorf("connection %q: the %s grant cannot fetch tokens", conn.Name, conn.Grant)
		}

		lock, err := b.locks.TryLock(ctx, conn.Key())
		if errors.Is(err, connection.ErrLocked) {
			if err := b.locks.WaitUnlocked(ctx, conn.Key()); err != nil {
				return connection.AccessToken{}, err
			}
			state, err := b.store.TokenState(ctx, conn)
			if err != nil {
				return connection.AccessToken{}, err
			}
			if at, done, err := req.settle(conn, state); done {
				return at, err
			}
			// The holder refreshed another resource, or stopped without an
			// outcome.
			continue
		}
		if err != nil {
			return connection.AccessToken{}, err
		}
		at, err := b.refreshHeld(ctx, conn, app, lock, req)
		lock.Unlock()
		if !errors.Is(err, errStartOver) {
			return at, err
		}
	}
}

// refreshHeld refreshes the token of req.resource while lock is held.
func (b *Broker) refreshHeld(
	ctx context.Context,
	conn *connection.Connection,
	app *connection.App,
	lock connection.Lock,
	req request,
) (connection.AccessToken, error) {
	// Another process may have refreshed between the first read and the
	// lock.
	state, err := b.store.TokenState(ctx, conn)
	if err != nil {
		return connection.AccessToken{}, err
	}
	retriedGrant, retriedClient := false, false
	for {
		if at, done, err := req.settle(conn, state); done {
			return at, err
		}
		if state.RefreshToken == "" {
			return connection.AccessToken{}, reconnectRequired(conn, "the connection has no refresh token")
		}
		token, err := b.refresh(ctx, app, state)
		if err == nil {
			return b.save(ctx, conn, lock, state, req.resource, token)
		}

		var tokenErr *oauthrefresh.TokenError
		if !errors.As(err, &tokenErr) {
			return connection.AccessToken{}, b.recordFailure(ctx, conn, lock, state, req.resource, err)
		}
		switch tokenErr.Code {
		case codeInvalidGrant:
			// Another process may have rotated the refresh token, or a
			// reconnect replaced it, since this one read it.
			current, err := b.store.TokenState(ctx, conn)
			if err != nil {
				return connection.AccessToken{}, err
			}
			if !retriedGrant && (current.RefreshToken != state.RefreshToken || current.Generation != state.Generation) {
				retriedGrant, state = true, current
				continue
			}
			return connection.AccessToken{}, b.markNeedsReconnect(ctx, conn, lock, current, tokenErr)
		case codeInvalidClient, codeUnauthorizedClient:
			// An admin may have fixed the client during the refresh.
			current, err := b.store.GetApp(ctx, app.ID)
			if err != nil {
				return connection.AccessToken{}, fmt.Errorf("connection %q: %w", conn.Name, err)
			}
			if !retriedClient && !sameCredentials(current, app) {
				retriedClient, app = true, current
				continue
			}
			return connection.AccessToken{}, b.markAppInvalid(ctx, conn, app, tokenErr)
		default:
			return connection.AccessToken{}, b.recordFailure(ctx, conn, lock, state, req.resource, err)
		}
	}
}

func (b *Broker) refresh(ctx context.Context, app *connection.App, state *connection.TokenState) (*oauth2.Token, error) {
	grant := oauthrefresh.Grant{
		ClientID:     app.ClientID,
		ClientSecret: app.ClientSecret,
		RefreshToken: state.RefreshToken,
	}
	// Microsoft requires scopes on a refresh. Other providers grant the
	// scopes of the original grant when none are sent (RFC 6749, section
	// 6).
	switch app.Provider {
	case connection.ProviderGoogle:
		grant.TokenURL = googleTokenURL
	case connection.ProviderMicrosoft:
		grant.TokenURL = oauthrefresh.MicrosoftTokenURL(app.Tenant)
		grant.Scopes = oauthrefresh.MicrosoftScopes(state.GrantedScopes)
	case connection.ProviderOAuth2, connection.ProviderMCP:
		if app.Endpoints == nil || app.Endpoints.Token == "" {
			return nil, fmt.Errorf("OAuth app %q has no token endpoint", app.Name)
		}
		grant.TokenURL = app.Endpoints.Token
	default:
		return nil, fmt.Errorf("OAuth app %q has unknown provider %q", app.Name, app.Provider)
	}
	// The call ends before three quarters of the stale threshold, after
	// which a holder whose renewals failed may no longer write, and a
	// hanging endpoint delays only this connection's callers.
	ctx, cancel := context.WithTimeout(ctx, b.locks.StaleThreshold()/2)
	defer cancel()
	return grant.Refresh(ctx, nil)
}

// save writes the refreshed token of resource. A concurrent write, such as
// a connect session, is kept: only the token fields are reapplied.
func (b *Broker) save(
	ctx context.Context,
	conn *connection.Connection,
	lock connection.Lock,
	base *connection.TokenState,
	resource string,
	token *oauth2.Token,
) (connection.AccessToken, error) {
	if strings.TrimSpace(token.AccessToken) == "" {
		return connection.AccessToken{}, b.recordFailure(ctx, conn, lock, base, resource, errors.New("token endpoint returned an empty access token"))
	}
	now := time.Now()
	at := connection.AccessToken{Token: token.AccessToken, ExpiresAt: token.Expiry, IssuedAt: now}
	_, err := b.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := lock.Held(); err != nil {
			return err
		}
		// A reconnect confirmed during the refresh wins over its result.
		if st.Generation != base.Generation {
			return errStartOver
		}
		if st.AccessTokens == nil {
			st.AccessTokens = make(map[string]connection.AccessToken, 1)
		}
		st.AccessTokens[resource] = at
		if token.RefreshToken != base.RefreshToken {
			st.RefreshToken = token.RefreshToken
		}
		delete(st.RefreshErrors, resource)
		st.RefreshedAt = now
		return nil
	})
	if err != nil {
		return connection.AccessToken{}, fmt.Errorf("connection %q: save the refreshed token: %w", conn.Name, err)
	}
	return at, nil
}

// recordFailure records a transient refresh failure of resource for the
// callers that waited on the refresh, and returns it. The status stays.
func (b *Broker) recordFailure(
	ctx context.Context,
	conn *connection.Connection,
	lock connection.Lock,
	base *connection.TokenState,
	resource string,
	cause error,
) error {
	failure := refreshFailed(conn, cause.Error())
	_, err := b.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := lock.Held(); err != nil {
			return err
		}
		if st.Generation != base.Generation {
			return errStartOver
		}
		if st.RefreshErrors == nil {
			st.RefreshErrors = make(map[string]connection.RefreshError, 1)
		}
		st.RefreshErrors[resource] = connection.RefreshError{Reason: cause.Error(), At: time.Now()}
		return nil
	})
	switch {
	case errors.Is(err, errStartOver):
		return errStartOver
	case err != nil:
		return errors.Join(failure, err)
	}
	return failure
}

// markNeedsReconnect sets needs_reconnect once for the refresh token in
// base, and returns the call's error.
func (b *Broker) markNeedsReconnect(
	ctx context.Context,
	conn *connection.Connection,
	lock connection.Lock,
	base *connection.TokenState,
	cause *oauthrefresh.TokenError,
) error {
	state, err := b.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := lock.Held(); err != nil {
			return err
		}
		if st.Generation != base.Generation || st.RefreshToken != base.RefreshToken {
			return errStartOver
		}
		if st.Status == connection.TokenStatusNeedsReconnect {
			return errNoChange
		}
		st.Status = connection.TokenStatusNeedsReconnect
		st.StatusReason = cause.Error()
		return nil
	})
	switch {
	case err == nil:
		b.report(ctx, StatusChange{Connection: conn, State: state})
	case !errors.Is(err, errNoChange):
		return err
	}
	return reconnectRequired(conn, cause.Error())
}

// markAppInvalid sets the app invalid once, unless an admin changed its
// client since used was read, and returns the call's error.
func (b *Broker) markAppInvalid(
	ctx context.Context,
	conn *connection.Connection,
	used *connection.App,
	cause *oauthrefresh.TokenError,
) error {
	app, err := b.store.UpdateApp(ctx, used.ID, func(a *connection.App) error {
		if !sameCredentials(a, used) {
			return errStartOver
		}
		if a.Status == connection.AppStatusInvalid {
			return errNoChange
		}
		a.Status = connection.AppStatusInvalid
		a.StatusReason = cause.Error()
		return nil
	})
	switch {
	case err == nil:
		b.report(ctx, StatusChange{Connection: conn, App: app})
	case !errors.Is(err, errNoChange):
		return err
	}
	return appInvalid(conn, used.Name, cause.Error())
}

func (b *Broker) report(ctx context.Context, change StatusChange) {
	if b.onStatusChange != nil {
		b.onStatusChange(ctx, change)
	}
}

// settle decides the call from state. With done set, it returns the stored
// token or the call's error; without it, the token needs a refresh.
func (r request) settle(conn *connection.Connection, state *connection.TokenState) (connection.AccessToken, bool, error) {
	switch state.Status {
	case connection.TokenStatusConnected:
	case connection.TokenStatusPending:
		return connection.AccessToken{}, true, reconnectRequired(conn, "the connection was never connected")
	case connection.TokenStatusNeedsReconnect:
		return connection.AccessToken{}, true, reconnectRequired(conn, state.StatusReason)
	}
	if at, ok := state.AccessTokens[r.resource]; ok && r.usable(at) {
		return at, true, nil
	}
	if e, ok := state.RefreshErrors[r.resource]; ok && !e.At.Before(r.start) {
		return connection.AccessToken{}, true, refreshFailed(conn, e.Reason)
	}
	return connection.AccessToken{}, false, nil
}

// usable reports whether at serves the call without a refresh.
func (r request) usable(at connection.AccessToken) bool {
	if at.Token == "" || at.Token == r.rejected {
		return false
	}
	if at.ExpiresAt.IsZero() {
		return true
	}
	minTTL := r.minTTL
	if lifetime := at.Lifetime(); lifetime > 0 {
		minTTL = min(minTTL, lifetime/2)
	}
	return time.Until(at.ExpiresAt) >= minTTL
}

func (r request) usableCached(token *oauth2.Token) bool {
	return token != nil && r.usable(fromCached(token))
}

// toCached keeps at in the gate, with its lifetime in ExpiresIn.
func toCached(at connection.AccessToken) *oauth2.Token {
	return &oauth2.Token{
		AccessToken: at.Token,
		Expiry:      at.ExpiresAt,
		ExpiresIn:   int64(at.Lifetime() / time.Second),
	}
}

func fromCached(token *oauth2.Token) connection.AccessToken {
	at := connection.AccessToken{Token: token.AccessToken, ExpiresAt: token.Expiry}
	if token.ExpiresIn > 0 {
		at.IssuedAt = token.Expiry.Add(-time.Duration(token.ExpiresIn) * time.Second)
	}
	return at
}

func sameCredentials(a, b *connection.App) bool {
	return a.ClientID == b.ClientID && a.ClientSecret == b.ClientSecret
}

func reconnectRequired(conn *connection.Connection, reason string) error {
	return fmt.Errorf("%w: connection %q: %s", ErrReconnectRequired, conn.Name, reason)
}

func appInvalid(conn *connection.Connection, app, reason string) error {
	return reconnectRequired(conn, fmt.Sprintf("OAuth app %q is invalid: %s", app, reason))
}

func refreshFailed(conn *connection.Connection, reason string) error {
	return fmt.Errorf("refresh connection %q: %s", conn.Name, reason)
}
