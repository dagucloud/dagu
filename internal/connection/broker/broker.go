// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package broker hands out the access tokens of connections. A process
// that has the connection store refreshes a token under the connection's
// lock, so callers on one host that need the same refresh cause one
// provider request:
//
//	Token -> in-process cache, in run processes only
//	           |  miss
//	           v
//	         token state usable? -- yes --> return it
//	           |  no: one caller per connection and resource
//	           v  in this process at a time
//	         TryLock -- held by another --> wait for release,
//	           |                            read the state once
//	           v
//	         read again, refresh, CAS write, unlock
package broker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/oauthrefresh"
)

//nolint:gosec // A fixed provider endpoint, not a credential.
const googleTokenURL = "https://oauth2.googleapis.com/token"

// ExportMinTTL is the minTTL that a token exported to a step's environment
// asks for, since the step cannot ask again when the token runs low.
const ExportMinTTL = 50 * time.Minute

// OAuth error codes that the broker handles apart from transient failures.
const (
	codeInvalidGrant       = "invalid_grant"
	codeInvalidClient      = "invalid_client"
	codeUnauthorizedClient = "unauthorized_client"
)

// redacted replaces a secret in a provider's error text.
const redacted = "[redacted]"

// StepMinTTL is the minTTL that a step using a token in process asks for.
const StepMinTTL = 5 * time.Minute

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
	// resource selects the connection's only resource; a resource the
	// connection does not serve fails. A minTTL longer than half the
	// token's lifetime is cut to half. expiresIn is zero when the provider
	// reported no expiry.
	Token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration) (token string, expiresIn time.Duration, err error)
	// Rejected reports that resource rejected token, and returns a token as
	// Token does. It refreshes only while token is still the stored token;
	// otherwise it returns the stored token.
	Rejected(ctx context.Context, conn *connection.Connection, resource, token string, minTTL time.Duration) (string, time.Duration, error)
}

// StatusChange is a status change that a broker's write made. Of the
// processes that see the same failure, only the one whose write changed the
// status reports it. It holds no secret.
type StatusChange struct {
	Connection   connection.Key
	ConnectionID string
	// App is the ID of the connection's app.
	App string
	// ConnectionFrom and ConnectionTo are set when the connection's status
	// changed.
	ConnectionFrom, ConnectionTo connection.TokenStatus
	// AppFrom and AppTo are set when the app's status changed.
	AppFrom, AppTo connection.AppStatus
	// Reason is the provider's error, without secrets.
	Reason string
}

// Config is what a Broker needs.
type Config struct {
	Store connection.Store
	Locks connection.Locks
	// CacheTokens keeps tokens in memory per connection and resource, so a
	// request that the cached token serves reads no state. A cached token
	// counts as one the process holds: it is handed out until it falls
	// below the minTTL asked for or is rejected, even after a reconnect or
	// a status change. Only a run process sets it.
	CacheTokens bool
	// OnStatusChange, when set, receives each status change that the
	// broker's own writes make, after the connection's lock is released.
	OnStatusChange func(context.Context, StatusChange)
}

var _ Source = (*Broker)(nil)

// Broker is the Source of a process that has the connection store.
type Broker struct {
	store          connection.Store
	locks          connection.Locks
	cache          bool
	onStatusChange func(context.Context, StatusChange)

	mu    sync.Mutex
	gates map[gateKey]*gateEntry
}

type gateKey struct {
	connectionID string
	resource     string
}

// gateEntry is the gate of one connection and resource, and how many calls
// use it.
type gateEntry struct {
	gate  *oauthrefresh.Gate
	users int
}

// request is what one call needs: a token for resource that stays valid
// for minTTL and is not the rejected one.
type request struct {
	// resource is the token's key in the token state.
	resource string
	minTTL   time.Duration
	rejected string
	// failures is the count of failed refreshes of resource that the call
	// read first. A higher count is the failure of a refresh the call
	// waited for.
	failures int64
}

// New returns a broker over cfg's store and locks.
func New(cfg Config) (*Broker, error) {
	if cfg.Store == nil || cfg.Locks == nil {
		return nil, errors.New("token broker: store and locks are required")
	}
	return &Broker{
		store:          cfg.Store,
		locks:          cfg.Locks,
		cache:          cfg.CacheTokens,
		onStatusChange: cfg.OnStatusChange,
		gates:          make(map[gateKey]*gateEntry),
	}, nil
}

func (b *Broker) Token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration) (string, time.Duration, error) {
	return b.token(ctx, conn, resource, request{minTTL: minTTL})
}

func (b *Broker) Rejected(ctx context.Context, conn *connection.Connection, resource, token string, minTTL time.Duration) (string, time.Duration, error) {
	return b.token(ctx, conn, resource, request{minTTL: minTTL, rejected: token})
}

func (b *Broker) token(ctx context.Context, conn *connection.Connection, resource string, req request) (string, time.Duration, error) {
	if conn == nil {
		return "", 0, connection.ErrNotFound
	}
	key, err := resourceKey(conn, resource)
	if err != nil {
		return "", 0, err
	}
	req.resource = key
	gate, release := b.acquire(gateKey{connectionID: conn.ID, resource: key})
	defer release()

	// A rejection is settled against the stored token, never the cache,
	// and the rejected token leaves the cache.
	cached := b.cache && req.rejected == ""
	if req.rejected != "" {
		gate.Forget(req.rejected)
	}
	if token := gate.Cached(); cached && req.usableCached(token) {
		return result(token)
	}
	// The state is read before queuing behind a refresh of this process,
	// so that the call takes that refresh's failure as its own.
	app, state, err := b.read(ctx, conn)
	if err != nil {
		return "", 0, err
	}
	req.failures = state.RefreshErrors[key].Failures
	stored, done, err := req.settle(conn, app, state)
	if err != nil {
		return "", 0, err
	}
	if done && !cached {
		return result(toCached(stored))
	}

	usable := func(*oauth2.Token) bool { return false }
	if cached {
		usable = req.usableCached
	}
	var change *StatusChange
	token, err := gate.TokenIf(ctx, usable, func(ctx context.Context, _ *oauth2.Token) (*oauth2.Token, error) {
		// The stored token was read before this call's turn came.
		if done && req.usable(stored) {
			return toCached(stored), nil
		}
		at, c, err := b.fetch(ctx, conn, req)
		change = c
		if err != nil {
			return nil, err
		}
		return toCached(at), nil
	})
	// The report waits until the lock and this process's turn are released.
	if change != nil && b.onStatusChange != nil {
		b.onStatusChange(context.WithoutCancel(ctx), *change)
	}
	if err != nil {
		return "", 0, err
	}
	return result(token)
}

// acquire returns the gate of key. Without the cache a gate only coalesces
// the calls in flight, so release drops it after the last of them.
func (b *Broker) acquire(key gateKey) (*oauthrefresh.Gate, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.gates[key]
	if !ok {
		entry = &gateEntry{gate: oauthrefresh.NewGate()}
		b.gates[key] = entry
	}
	entry.users++
	return entry.gate, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		entry.users--
		if entry.users == 0 && !b.cache {
			delete(b.gates, key)
		}
	}
}

func (b *Broker) read(ctx context.Context, conn *connection.Connection) (*connection.App, *connection.TokenState, error) {
	app, err := b.store.GetApp(ctx, conn.App)
	if err != nil {
		return nil, nil, fmt.Errorf("connection %q: %w", conn.Name, err)
	}
	state, err := b.store.TokenState(ctx, conn)
	if err != nil {
		return nil, nil, err
	}
	return app, state, nil
}

// fetch returns a token from the token state, refreshing it under the
// connection's lock when it is not usable, and the status change that its
// refresh made, if any. A caller that finds the lock held waits for the
// holder and takes its outcome instead of calling the provider.
func (b *Broker) fetch(ctx context.Context, conn *connection.Connection, req request) (connection.AccessToken, *StatusChange, error) {
	var change *StatusChange
	for {
		app, state, err := b.read(ctx, conn)
		if err != nil {
			return connection.AccessToken{}, change, err
		}
		if at, done, err := req.settle(conn, app, state); done || err != nil {
			return at, change, err
		}
		if conn.Grant != connection.GrantAuthorizationCode && conn.Grant != connection.GrantDeviceCode {
			return connection.AccessToken{}, change, fmt.Errorf("connection %q: the %s grant cannot fetch tokens", conn.Name, conn.Grant)
		}

		// A refresh, once started, runs to its end even when the caller
		// gives up, so a token the provider rotated is never lost. The
		// token call's timeout bounds it.
		heldCtx := context.WithoutCancel(ctx)
		lock, err := b.locks.TryLock(heldCtx, conn.Key())
		if errors.Is(err, connection.ErrLocked) {
			if err := b.locks.WaitUnlocked(ctx, conn.Key()); err != nil {
				return connection.AccessToken{}, change, err
			}
			// An app the holder made invalid fails on the next iteration's
			// read, so this read is of the token state alone.
			state, err := b.store.TokenState(ctx, conn)
			if err != nil {
				return connection.AccessToken{}, change, err
			}
			if at, done, err := req.settle(conn, app, state); done || err != nil {
				return at, change, err
			}
			// The holder refreshed another resource, failed for its app, or
			// stopped without an outcome.
			continue
		}
		if err != nil {
			return connection.AccessToken{}, change, err
		}
		at, c, err := b.refreshLocked(heldCtx, conn, lock, req)
		change = cmp.Or(c, change)
		if !errors.Is(err, errStartOver) {
			return at, change, err
		}
	}
}

// refreshLocked refreshes while holding lock and releases it.
func (b *Broker) refreshLocked(
	ctx context.Context,
	conn *connection.Connection,
	lock connection.Lock,
	req request,
) (connection.AccessToken, *StatusChange, error) {
	defer lock.Unlock()
	at, change, err := b.refreshHeld(ctx, conn, lock, req)
	if err == nil && !req.usable(at) {
		err = fmt.Errorf("connection %q: the refreshed token expires before the minimum lifetime asked for", conn.Name)
	}
	return at, change, err
}

// refreshHeld refreshes the token of req.resource while lock is held, and
// returns the status change it made, if any.
func (b *Broker) refreshHeld(
	ctx context.Context,
	conn *connection.Connection,
	lock connection.Lock,
	req request,
) (connection.AccessToken, *StatusChange, error) {
	// Another process may have refreshed, or made the app invalid, between
	// the first read and the lock.
	app, state, err := b.read(ctx, conn)
	if err != nil {
		return connection.AccessToken{}, nil, err
	}
	retriedGrant, retriedClient := false, false
	for {
		if at, done, err := req.settle(conn, app, state); done || err != nil {
			return at, nil, err
		}
		if state.RefreshToken == "" {
			return connection.AccessToken{}, nil, reconnectRequired(conn, "the connection has no refresh token")
		}
		token, err := b.refresh(ctx, app, state)
		if err == nil {
			at, err := b.save(ctx, conn, lock, state, req.resource, token)
			return at, nil, err
		}
		reason := redact(err.Error(), state.RefreshToken, app.ClientSecret)

		var tokenErr *oauthrefresh.TokenError
		if !errors.As(err, &tokenErr) {
			return connection.AccessToken{}, nil, b.recordFailure(ctx, conn, lock, state, req.resource, reason)
		}
		switch tokenErr.Code {
		case codeInvalidGrant:
			// Another process may have rotated the refresh token, or a
			// reconnect replaced it, since this one read it.
			current, err := b.store.TokenState(ctx, conn)
			if err != nil {
				return connection.AccessToken{}, nil, err
			}
			if !retriedGrant && (current.RefreshToken != state.RefreshToken || current.Generation != state.Generation) {
				retriedGrant, state = true, current
				continue
			}
			change, err := b.markNeedsReconnect(ctx, conn, lock, state, reason)
			return connection.AccessToken{}, change, err
		case codeInvalidClient, codeUnauthorizedClient:
			// An admin may have fixed the client during the refresh.
			current, err := b.store.GetApp(ctx, app.ID)
			if err != nil {
				return connection.AccessToken{}, nil, fmt.Errorf("connection %q: %w", conn.Name, err)
			}
			if !retriedClient && !sameCredentials(current, app) {
				retriedClient, app = true, current
				continue
			}
			change, err := b.markAppInvalid(ctx, conn, app, reason)
			return connection.AccessToken{}, change, err
		default:
			return connection.AccessToken{}, nil, b.recordFailure(ctx, conn, lock, state, req.resource, reason)
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
	// An empty access token fails the refresh, but a refresh token that
	// the provider rotated is kept all the same.
	const emptyReason = "token endpoint returned an empty access token"
	empty := strings.TrimSpace(token.AccessToken) == ""
	now := time.Now()
	at := connection.AccessToken{Token: token.AccessToken, ExpiresAt: token.Expiry, IssuedAt: now}
	_, err := b.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		// A reconnect confirmed during the refresh wins over its result.
		if st.Generation != base.Generation {
			return errStartOver
		}
		// The provider has accepted the refresh, so a lost lock refuses
		// the result only when another holder has rotated the refresh
		// token since: the result may then be older than the stored one.
		if err := lock.Held(); err != nil && st.RefreshToken != base.RefreshToken {
			return err
		}
		if token.RefreshToken != base.RefreshToken {
			st.RefreshToken = token.RefreshToken
		}
		if empty {
			addFailure(st, resource, emptyReason)
			return nil
		}
		if st.AccessTokens == nil {
			st.AccessTokens = make(map[string]connection.AccessToken, 1)
		}
		st.AccessTokens[resource] = at
		if e, ok := st.RefreshErrors[resource]; ok {
			e.Reason = ""
			st.RefreshErrors[resource] = e
		}
		st.RefreshedAt = now
		return nil
	})
	if err != nil {
		return connection.AccessToken{}, fmt.Errorf("connection %q: save the refreshed token: %w", conn.Name, err)
	}
	if empty {
		return connection.AccessToken{}, refreshFailed(conn, emptyReason)
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
	resource, reason string,
) error {
	failure := refreshFailed(conn, reason)
	_, err := b.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := lock.Held(); err != nil {
			return err
		}
		if st.Generation != base.Generation {
			return errStartOver
		}
		addFailure(st, resource, reason)
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

// addFailure records a failed refresh of resource in st.
func addFailure(st *connection.TokenState, resource, reason string) {
	if st.RefreshErrors == nil {
		st.RefreshErrors = make(map[string]connection.RefreshError, 1)
	}
	e := st.RefreshErrors[resource]
	e.Reason = reason
	e.Failures++
	st.RefreshErrors[resource] = e
}

// markNeedsReconnect sets needs_reconnect once for the refresh token that
// base holds and the provider refused, and returns the call's error.
func (b *Broker) markNeedsReconnect(
	ctx context.Context,
	conn *connection.Connection,
	lock connection.Lock,
	base *connection.TokenState,
	reason string,
) (*StatusChange, error) {
	var from connection.TokenStatus
	_, err := b.store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		if err := lock.Held(); err != nil {
			return err
		}
		// A reconnect or a rotation since the refused refresh wins.
		if st.Generation != base.Generation || st.RefreshToken != base.RefreshToken {
			return errStartOver
		}
		if st.Status == connection.TokenStatusNeedsReconnect {
			return errNoChange
		}
		from = st.Status
		st.Status = connection.TokenStatusNeedsReconnect
		st.StatusReason = reason
		return nil
	})
	var change *StatusChange
	switch {
	case err == nil:
		change = newStatusChange(conn, reason)
		change.ConnectionFrom, change.ConnectionTo = from, connection.TokenStatusNeedsReconnect
	case !errors.Is(err, errNoChange):
		return nil, err
	}
	return change, reconnectRequired(conn, reason)
}

// markAppInvalid sets the app invalid once, unless an admin changed its
// client since used was read, and returns the call's error.
func (b *Broker) markAppInvalid(
	ctx context.Context,
	conn *connection.Connection,
	used *connection.App,
	reason string,
) (*StatusChange, error) {
	var from connection.AppStatus
	_, err := b.store.UpdateApp(ctx, used.ID, func(a *connection.App) error {
		if !sameCredentials(a, used) {
			return errStartOver
		}
		if a.Status == connection.AppStatusInvalid {
			return errNoChange
		}
		from = a.Status
		a.Status = connection.AppStatusInvalid
		a.StatusReason = reason
		return nil
	})
	var change *StatusChange
	switch {
	case err == nil:
		change = newStatusChange(conn, reason)
		change.AppFrom, change.AppTo = from, connection.AppStatusInvalid
	case !errors.Is(err, errNoChange):
		return nil, err
	}
	return change, appInvalid(conn, used.Name, reason)
}

func newStatusChange(conn *connection.Connection, reason string) *StatusChange {
	return &StatusChange{Connection: conn.Key(), ConnectionID: conn.ID, App: conn.App, Reason: reason}
}

// settle decides the call from the app and the token state. It returns the
// stored token with done set, or the call's error. With neither, the token
// needs a refresh.
func (r request) settle(conn *connection.Connection, app *connection.App, state *connection.TokenState) (connection.AccessToken, bool, error) {
	if app.Status == connection.AppStatusInvalid {
		return connection.AccessToken{}, false, appInvalid(conn, app.Name, app.StatusReason)
	}
	switch state.Status {
	case connection.TokenStatusConnected:
	case connection.TokenStatusPending:
		return connection.AccessToken{}, false, reconnectRequired(conn, "the connection was never connected")
	case connection.TokenStatusNeedsReconnect:
		return connection.AccessToken{}, false, reconnectRequired(conn, state.StatusReason)
	}
	if at, ok := state.AccessTokens[r.resource]; ok && r.usable(at) {
		return at, true, nil
	}
	if e := state.RefreshErrors[r.resource]; e.Failures > r.failures && e.Reason != "" {
		return connection.AccessToken{}, false, refreshFailed(conn, e.Reason)
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

// resourceKey returns the key under which conn keeps the token of
// resource.
func resourceKey(conn *connection.Connection, resource string) (string, error) {
	switch conn.Provider {
	case connection.ProviderGoogle, connection.ProviderOAuth2:
		if resource == "" {
			return "", nil
		}
	case connection.ProviderMCP:
		if resource == "" || resource == conn.Resource {
			return "", nil
		}
	case connection.ProviderMicrosoft:
		// Microsoft resources come with its service presets.
	}
	return "", fmt.Errorf("connection %q does not serve resource %q", conn.Name, resource)
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

func result(token *oauth2.Token) (string, time.Duration, error) {
	var expiresIn time.Duration
	if !token.Expiry.IsZero() {
		expiresIn = time.Until(token.Expiry)
	}
	return token.AccessToken, expiresIn, nil
}

func sameCredentials(a, b *connection.App) bool {
	return a.ClientID == b.ClientID && a.ClientSecret == b.ClientSecret
}

// redact removes the secrets a refresh sent from a provider's error text,
// which is stored and shown to people. A secret is also removed in the form
// encoding the request carried it in.
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

func reconnectRequired(conn *connection.Connection, reason string) error {
	return fmt.Errorf("%w: connection %q: %s", ErrReconnectRequired, conn.Name, reason)
}

func appInvalid(conn *connection.Connection, app, reason string) error {
	return reconnectRequired(conn, fmt.Sprintf("OAuth app %q is invalid: %s", app, reason))
}

func refreshFailed(conn *connection.Connection, reason string) error {
	return fmt.Errorf("refresh connection %q: %s", conn.Name, reason)
}
