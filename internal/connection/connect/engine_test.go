// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

const (
	starter    = "alice"
	publicURL  = "https://dagu.example.com/base"
	authURL    = "https://provider.example/authorize"
	revokePath = "/revoke"
	sessionTTL = 10 * time.Minute
)

var serverCallback = connect.RedirectOptions{PublicURL: publicURL}

func TestConfirmMakesTokensLive(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	assert.Equal(t, connect.Redirect{Mode: connect.RedirectServerCallback, URI: publicURL + "/oauth/callback"}, auth.Redirect)

	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))
	status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.PhaseConfirm, status.Phase)
	assert.Equal(t, "user@example.com", status.Account)
	assert.Equal(t, "sub-1", status.AccountID)
	assert.Equal(t, []string{"scope-a"}, status.GrantedScopes)
	assert.Equal(t, []connection.Probe{{Service: "svc"}}, status.Probes)
	// Consent alone changes no live token.
	assert.Equal(t, connection.TokenStatusPending, f.state(t).Status)
	assert.Empty(t, f.state(t).RefreshToken)

	require.NoError(t, f.engine.Confirm(context.Background(), f.conn, auth.SessionID, starter))
	st := f.state(t)
	assert.Equal(t, connection.TokenStatusConnected, st.Status)
	assert.Equal(t, int64(1), st.Generation)
	assert.Equal(t, "user@example.com", st.Account)
	assert.Equal(t, "sub-1", st.AccountID)
	assert.Equal(t, "rt-1", st.RefreshToken)
	assert.Equal(t, "at-1", st.AccessTokens[""].Token)
	assert.Nil(t, st.Session)
	status, err = f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.PhaseConnected, status.Phase)
	// A first connect, from pending, records no event.
	assert.Empty(t, f.changes.list())
}

// Provider calls that run to their deadline still end the session: a slow
// probe reports a failed service and the session reaches confirm, and a
// slow exchange fails the session. The test ends the calls' context once
// the call blocks, so no timing decides the outcome.
func TestSlowProviderCallsEndSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		wantErr   error
		wantPhase connect.Phase
	}{
		{name: "Probe", wantPhase: connect.PhaseConfirm},
		{name: "Exchange", wantErr: connect.ErrConsentFailed, wantPhase: connect.PhaseFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			callCtx, endCalls := context.WithCancel(context.Background())
			f := newFixture(t, func(cfg *connect.Config) {
				connect.SetCallContext(cfg, func(context.Context) (context.Context, context.CancelFunc) {
					return callCtx, endCalls
				})
			})
			go func() {
				select {
				case <-f.provider.blocked:
					endCalls()
				case <-t.Context().Done():
				}
			}()
			if tt.name == "Probe" {
				f.provider.blockProbe.Store(true)
			} else {
				f.provider.blockExchange.Store(true)
			}
			auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})

			err := f.engine.Callback(context.Background(), f.provider.consent(t, auth))
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPhase, status.Phase)
			if tt.name == "Probe" {
				assert.Equal(t, []connection.Probe{{Service: "svc", Error: context.Canceled.Error()}}, status.Probes)
			}
		})
	}
}

// The refresh token's lifetime counts from the exchange, not from the end
// of the checks after it.
func TestRefreshTokenExpiryFromExchange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.provider.refreshExpiresIn = 60
	f.provider.onProbe = func() { f.clock.advance(20 * time.Second) }
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	exchangedAt := f.clock.Now()

	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))
	status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Equal(t, exchangedAt.Add(time.Minute), status.RefreshTokenExpiresAt)
}

// A refresh token is required only for an access token that expires.
func TestConsentWithoutRefreshToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		expires   bool
		wantPhase connect.Phase
	}{
		{name: "Expiring", expires: true, wantPhase: connect.PhaseFailed},
		{name: "NonExpiring", wantPhase: connect.PhaseConfirm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.provider.noRefreshToken = true
			f.provider.noExpiry = !tt.expires
			auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})

			_ = f.engine.Callback(context.Background(), f.provider.consent(t, auth))
			status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPhase, status.Phase)
		})
	}
}

// A loopback or paste session completes only through Complete for its own
// connection, and a server callback session only through Callback.
func TestCompleteServesItsModes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.useClientType(t, connection.ClientTypeDesktop)
	paste := connect.RedirectOptions{Mode: connect.RedirectPaste}
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: paste})
	params := f.provider.consent(t, auth)

	assert.ErrorIs(t, f.engine.Callback(context.Background(), params), connect.ErrCallbackRefused)
	other := *f.conn
	other.ID = "other"
	assert.ErrorIs(t, f.engine.Complete(context.Background(), &other, params), connect.ErrCallbackRefused)
	require.NoError(t, f.engine.Complete(context.Background(), f.conn, params))
	assert.Equal(t, connection.SessionPhaseConfirm, f.state(t).Session.Phase)

	f.useClientType(t, connection.ClientTypeWeb)
	auth = f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	assert.ErrorIs(t, f.engine.Complete(context.Background(), f.conn, f.provider.consent(t, auth)), connect.ErrCallbackRefused)
	assert.Equal(t, 1, f.provider.exchanges())
}

// The authorization URL carries an S256 challenge and the state, and the
// token endpoint accepts the code only with the matching verifier.
func TestAuthorizationURLUsesPKCE(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	u, err := url.Parse(auth.URL)
	require.NoError(t, err)
	q := u.Query()
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.NotEmpty(t, q.Get("code_challenge"))
	assert.Equal(t, "client", q.Get("client_id"))
	assert.Equal(t, publicURL+"/oauth/callback", q.Get("redirect_uri"))
	assert.Equal(t, "scope-a", q.Get("scope"))
	assert.Equal(t, "consent", q.Get("prompt"))

	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))
	assert.Equal(t, 1, f.provider.exchanges())
}

func TestCallbackRefusesSecond(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)

	require.NoError(t, f.engine.Callback(context.Background(), params))
	err := f.engine.Callback(context.Background(), params)
	assert.ErrorIs(t, err, connect.ErrCallbackRefused)
	assert.Equal(t, 1, f.provider.exchanges())
	assert.Equal(t, connection.SessionPhaseConfirm, f.state(t).Session.Phase)
}

// Callbacks for one session that arrive together exchange one code: one
// completes the session and every other is refused.
func TestCallbacksRace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)

	const callbacks = 8
	errs := make([]error, callbacks)
	var wg sync.WaitGroup
	for i := range callbacks {
		wg.Go(func() { errs[i] = f.engine.Callback(context.Background(), params) })
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		assert.ErrorIs(t, err, connect.ErrCallbackRefused)
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, f.provider.exchanges())
	assert.Equal(t, connection.SessionPhaseConfirm, f.state(t).Session.Phase)
}

func TestCallbackRefusesBadState(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)

	tampered := []byte(params.State)
	if tampered[10] == 'A' {
		tampered[10] = 'B'
	} else {
		tampered[10] = 'A'
	}
	for _, state := range []string{"", "not-a-state", string(tampered)} {
		err := f.engine.Callback(context.Background(), connect.CallbackParams{State: state, Code: params.Code})
		assert.ErrorIs(t, err, connect.ErrCallbackRefused)
	}
	assert.Equal(t, 0, f.provider.exchanges())
	assert.Equal(t, connection.SessionPhaseWaiting, f.state(t).Session.Phase)
}

func TestSessionExpires(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)
	f.clock.advance(sessionTTL)

	assert.ErrorIs(t, f.engine.Callback(context.Background(), params), connect.ErrCallbackRefused)
	assert.Equal(t, 0, f.provider.exchanges())
	status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.SessionStatus{Phase: connect.PhaseFailed, Reason: connect.ReasonExpired, ExpiresAt: auth.ExpiresAt}, status)
}

func TestConfirmAfterExpiryRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))
	f.clock.advance(sessionTTL)

	err := f.engine.Confirm(context.Background(), f.conn, auth.SessionID, starter)
	assert.ErrorIs(t, err, connect.ErrSessionEnded)
	assert.Equal(t, connection.TokenStatusPending, f.state(t).Status)
	assert.Empty(t, f.state(t).RefreshToken)
}

func TestConfirmByOtherUserRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))

	err := f.engine.Confirm(context.Background(), f.conn, auth.SessionID, "mallory")
	assert.ErrorIs(t, err, connect.ErrNotStarter)
	st := f.state(t)
	assert.Equal(t, connection.SessionPhaseConfirm, st.Session.Phase)
	assert.Equal(t, int64(0), st.Generation)

	require.NoError(t, f.engine.Confirm(context.Background(), f.conn, auth.SessionID, starter))
}

func TestConfirmByAnyUserWithoutAuth(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(cfg *connect.Config) { cfg.AnyUserConfirms = true })
	auth := f.start(t, connect.StartOptions{Redirect: serverCallback})
	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))

	require.NoError(t, f.engine.Confirm(context.Background(), f.conn, auth.SessionID, "bob"))
	assert.Equal(t, connection.TokenStatusConnected, f.state(t).Status)
}

func TestConfirmWhileWaitingRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})

	err := f.engine.Confirm(context.Background(), f.conn, auth.SessionID, starter)
	assert.ErrorIs(t, err, connect.ErrAwaitingConsent)
}

func TestStartReplacesSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	first := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, first)
	second := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})

	assert.ErrorIs(t, f.engine.Callback(context.Background(), params), connect.ErrCallbackRefused)
	status, err := f.engine.Session(context.Background(), f.conn, first.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.PhaseFailed, status.Phase)
	assert.Equal(t, connect.ReasonReplaced, status.Reason)
	status, err = f.engine.Session(context.Background(), f.conn, second.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.PhaseWaiting, status.Phase)
}

// A confirm clears its session, yet its handle still reads as connected,
// and the handle of a session it replaced still reads as replaced.
func TestSessionAfterConfirm(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.engine.Session(context.Background(), f.conn, "unknown")
	assert.ErrorIs(t, err, connect.ErrSessionNotFound)

	first := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	second := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, second)))
	require.NoError(t, f.engine.Confirm(context.Background(), f.conn, second.SessionID, starter))
	f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})

	status, err := f.engine.Session(context.Background(), f.conn, first.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.SessionStatus{Phase: connect.PhaseFailed, Reason: connect.ReasonReplaced}, status)
	status, err = f.engine.Session(context.Background(), f.conn, second.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.PhaseConnected, status.Phase)
}

// A confirm and a new session that race leave one consistent outcome:
// either the confirm went live and the new session waits, or the new
// session replaced the old one and the live tokens did not change.
func TestConfirmRacesNewSession(t *testing.T) {
	t.Parallel()
	for i := range 20 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			first := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
			require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, first)))

			var confirmErr, startErr error
			var second *connect.Authorization
			var wg sync.WaitGroup
			wg.Go(func() { confirmErr = f.engine.Confirm(context.Background(), f.conn, first.SessionID, starter) })
			wg.Go(func() {
				second, startErr = f.engine.Start(context.Background(), f.conn, connect.StartOptions{User: starter, Redirect: serverCallback})
			})
			wg.Wait()
			require.NoError(t, startErr)

			st := f.state(t)
			require.NotNil(t, st.Session)
			assert.Equal(t, second.SessionID, st.Session.ID)
			assert.Equal(t, connection.SessionPhaseWaiting, st.Session.Phase)
			if confirmErr == nil {
				assert.Equal(t, int64(1), st.Generation)
				assert.Equal(t, "rt-1", st.RefreshToken)
				return
			}
			assert.ErrorIs(t, confirmErr, connect.ErrSessionEnded)
			assert.Equal(t, int64(0), st.Generation)
			assert.Empty(t, st.RefreshToken)
		})
	}
}

func TestExpectedAccountConfirmsAtOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		expected  string
		vouched   string
		liveID    string
		wantPhase connect.Phase
	}{
		{name: "Vouched", expected: "User@Example.com", vouched: "user@example.com", wantPhase: connect.PhaseConnected},
		{name: "NotVouched", expected: "user@example.com", wantPhase: connect.PhaseConfirm},
		{name: "OtherEmail", expected: "other@example.com", vouched: "user@example.com", wantPhase: connect.PhaseConfirm},
		{name: "SameAccountID", expected: "user@example.com", vouched: "user@example.com", liveID: "sub-1", wantPhase: connect.PhaseConnected},
		// A reconnect to another account ID asks even when the email matches.
		{name: "OtherAccountID", expected: "user@example.com", vouched: "user@example.com", liveID: "sub-0", wantPhase: connect.PhaseConfirm},
		// Only ASCII letters fold: the Kelvin sign does not match k.
		{name: "UnicodeFold", expected: "\u212Aim@example.com", vouched: "kim@example.com", wantPhase: connect.PhaseConfirm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if tt.liveID != "" {
				f.connect(t, tt.liveID)
			}
			f.provider.vouched = tt.vouched
			auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback, ExpectedAccount: tt.expected})
			require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))

			status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPhase, status.Phase)
		})
	}
}

// A reconnect keeps the previous tokens live until its confirm, which
// reports the status change from needs_reconnect once.
func TestReconnectKeepsTokensUntilConfirm(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.connect(t, "sub-1")
	f.update(t, func(st *connection.TokenState) { st.Status = connection.TokenStatusNeedsReconnect })
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	require.NoError(t, f.engine.Callback(context.Background(), f.provider.consent(t, auth)))

	st := f.state(t)
	assert.Equal(t, "rt-0", st.RefreshToken)
	assert.Equal(t, int64(1), st.Generation)
	assert.Empty(t, f.changes.list())

	require.NoError(t, f.engine.Confirm(context.Background(), f.conn, auth.SessionID, starter))
	st = f.state(t)
	assert.Equal(t, "rt-1", st.RefreshToken)
	assert.Equal(t, int64(2), st.Generation)
	assert.Equal(t, connection.TokenStatusConnected, st.Status)
	changes := f.changes.list()
	require.Len(t, changes, 1)
	assert.Equal(t, connection.TokenStatusNeedsReconnect, changes[0].ConnectionFrom)
	assert.Equal(t, connection.TokenStatusConnected, changes[0].ConnectionTo)
}

func TestConsentErrorFailsSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)
	params.Code, params.Error, params.ErrorDescription = "", "access_denied", "The user declined"

	assert.ErrorIs(t, f.engine.Callback(context.Background(), params), connect.ErrConsentFailed)
	status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.PhaseFailed, status.Phase)
	assert.Equal(t, "access_denied: The user declined", status.Reason)
	assert.Equal(t, connection.TokenStatusPending, f.state(t).Status)
}

func TestConsentErrorReasonBounded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)
	params.Code, params.Error, params.ErrorDescription = "", "access_denied", strings.Repeat("x", 4096)

	assert.ErrorIs(t, f.engine.Callback(context.Background(), params), connect.ErrConsentFailed)
	status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Len(t, status.Reason, 512)
}

// A failed exchange fails the session with the provider's error code,
// without the code or the verifier.
func TestExchangeErrorFailsSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)
	f.provider.failExchange.Store(true)

	assert.ErrorIs(t, f.engine.Callback(context.Background(), params), connect.ErrConsentFailed)
	status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.PhaseFailed, status.Phase)
	assert.Equal(t, "invalid_client: bad client [redacted]", status.Reason)
}

// A failure that cannot be stored is not reported as a consent failure:
// the store's error, or the refusal of a session replaced meanwhile, comes
// back instead.
func TestFailureWriteErrors(t *testing.T) {
	t.Parallel()
	errDisk := errors.New("disk full")
	tests := []struct {
		name    string
		during  func(*fixture)
		wantErr error
	}{
		{name: "StoreError", during: func(f *fixture) { f.writes.fail(errDisk) }, wantErr: errDisk},
		{name: "Replaced", during: func(f *fixture) {
			_, _ = f.engine.Start(context.Background(), f.conn, connect.StartOptions{User: starter, Redirect: serverCallback})
		}, wantErr: connect.ErrCallbackRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
			params := f.provider.consent(t, auth)
			f.provider.failExchange.Store(true)
			f.provider.onExchange = func() { tt.during(f) }

			err := f.engine.Callback(context.Background(), params)
			assert.ErrorIs(t, err, tt.wantErr)
			assert.NotErrorIs(t, err, connect.ErrConsentFailed)
		})
	}
}

// A callback's claim holds through the provider calls and the write after
// them, so a second callback just before that ends is still refused.
func TestClaimOutlivesProviderCalls(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)
	var second error
	f.provider.onExchange = func() {
		f.provider.onExchange = nil
		// The consent timeout and the write timeout have both passed.
		f.clock.advance(50 * time.Second)
		second = f.engine.Callback(context.Background(), params)
	}

	require.NoError(t, f.engine.Callback(context.Background(), params))
	assert.ErrorIs(t, second, connect.ErrCallbackRefused)
	assert.Equal(t, 1, f.provider.exchanges())
	assert.Equal(t, connection.SessionPhaseConfirm, f.state(t).Session.Phase)
}

func TestCancel(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	auth := f.start(t, connect.StartOptions{User: starter, Redirect: serverCallback})
	params := f.provider.consent(t, auth)
	require.NoError(t, f.engine.Cancel(context.Background(), f.conn, auth.SessionID))

	assert.ErrorIs(t, f.engine.Callback(context.Background(), params), connect.ErrCallbackRefused)
	status, err := f.engine.Session(context.Background(), f.conn, auth.SessionID)
	require.NoError(t, err)
	assert.Equal(t, connect.ReasonCancelled, status.Reason)
	assert.ErrorIs(t, f.engine.Cancel(context.Background(), f.conn, auth.SessionID), connect.ErrSessionEnded)
}

func TestChooseRedirect(t *testing.T) {
	t.Parallel()
	web := &connection.App{ClientType: connection.ClientTypeWeb}
	desktop := &connection.App{ClientType: connection.ClientTypeDesktop}
	loopback := "http://127.0.0.1:53682/callback"
	tests := []struct {
		name    string
		app     *connection.App
		opts    connect.RedirectOptions
		want    connect.Redirect
		wantErr bool
	}{
		{name: "ServerCallback", app: web, opts: connect.RedirectOptions{PublicURL: publicURL + "/"},
			want: connect.Redirect{Mode: connect.RedirectServerCallback, URI: publicURL + "/oauth/callback"}},
		{name: "WebWithoutPublicURL", app: web, wantErr: true},
		{name: "Loopback", app: desktop, opts: connect.RedirectOptions{PublicURL: publicURL, LoopbackURI: loopback},
			want: connect.Redirect{Mode: connect.RedirectLoopback, URI: loopback}},
		{name: "Paste", app: desktop, opts: connect.RedirectOptions{PublicURL: publicURL},
			want: connect.Redirect{Mode: connect.RedirectPaste, URI: "http://127.0.0.1:1/oauth/callback"}},
		{name: "NamedPaste", app: desktop, opts: connect.RedirectOptions{Mode: connect.RedirectPaste, LoopbackURI: loopback},
			want: connect.Redirect{Mode: connect.RedirectPaste, URI: "http://127.0.0.1:1/oauth/callback"}},
		{name: "NamedModeForOtherClient", app: desktop, opts: connect.RedirectOptions{Mode: connect.RedirectServerCallback, PublicURL: publicURL}, wantErr: true},
		{name: "LoopbackPortOutOfRange", app: desktop, opts: connect.RedirectOptions{Mode: connect.RedirectLoopback, LoopbackURI: "http://127.0.0.1:65536/"}, wantErr: true},
		{name: "AutoSkipsBadLoopback", app: desktop, opts: connect.RedirectOptions{LoopbackURI: "http://127.0.0.1:0/"},
			want: connect.Redirect{Mode: connect.RedirectPaste, URI: "http://127.0.0.1:1/oauth/callback"}},
		{name: "LoopbackNotLocal", app: desktop, opts: connect.RedirectOptions{Mode: connect.RedirectLoopback, LoopbackURI: "http://example.com:8080/"}, wantErr: true},
		{name: "PublicClient", app: &connection.App{ClientType: connection.ClientTypePublic}, opts: connect.RedirectOptions{PublicURL: publicURL}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := connect.ChooseRedirect(tt.app, tt.opts)
			if tt.wantErr {
				assert.ErrorIs(t, err, connect.ErrRedirectUnavailable)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStartRefusesUnavailableRedirect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.engine.Start(context.Background(), f.conn, connect.StartOptions{User: starter})
	assert.ErrorIs(t, err, connect.ErrRedirectUnavailable)
	assert.Nil(t, f.state(t).Session)
}

type fixture struct {
	dataDir  string
	stores   *file.ServerStores
	store    connection.Store
	writes   *failingStore
	conn     *connection.Connection
	engine   *connect.Engine
	provider *fakeProvider
	clock    *clock
	changes  *changes
	revoked  *revocations
}

func newFixture(t *testing.T, opts ...func(*connect.Config)) *fixture {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	stores := file.NewServerStores(&config.Config{Paths: config.PathsConfig{DataDir: dataDir}})
	s, err := stores.ConnectionStore()
	require.NoError(t, err)
	enc, err := stores.Encryptor()
	require.NoError(t, err)

	f := &fixture{
		dataDir:  dataDir,
		stores:   stores,
		store:    s,
		writes:   &failingStore{Store: s},
		provider: newFakeProvider(t),
		clock:    &clock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		changes:  &changes{},
		revoked:  &revocations{},
	}
	app, err := s.CreateApp(ctx, &connection.App{
		Name:         "Example",
		Provider:     connection.ProviderOAuth2,
		Kind:         connection.AppKindBYO,
		ClientID:     "client",
		ClientSecret: "secret",
		ClientType:   connection.ClientTypeWeb,
		// The token broker refreshes at this endpoint.
		Endpoints: &connection.Endpoints{Token: f.provider.server.URL},
	})
	require.NoError(t, err)
	f.conn, err = s.Create(ctx, &connection.Connection{
		Workspace: "ops",
		Name:      "work",
		Provider:  connection.ProviderOAuth2,
		App:       app.ID,
		Grant:     connection.GrantAuthorizationCode,
		Scopes:    []string{"scope-a"},
		Services:  []connection.Service{{Name: "svc", Access: connection.AccessRead}},
	})
	require.NoError(t, err)

	cfg := connect.Config{
		Store:          f.writes,
		Locks:          stores.ConnectionLocks(),
		Providers:      map[connection.Provider]connect.Provider{connection.ProviderOAuth2: f.provider},
		Encryptor:      enc,
		OnStatusChange: f.changes.add,
		OnRevoke:       f.revoked.add,
	}
	connect.SetClock(&cfg, f.clock.Now)
	for _, opt := range opts {
		opt(&cfg)
	}
	f.engine, err = connect.New(cfg)
	require.NoError(t, err)
	return f
}

func (f *fixture) start(t *testing.T, opts connect.StartOptions) *connect.Authorization {
	t.Helper()
	auth, err := f.engine.Start(context.Background(), f.conn, opts)
	require.NoError(t, err)
	return auth
}

// connect gives the connection live tokens of accountID, as a confirmed
// connect would.
func (f *fixture) connect(t *testing.T, accountID string) {
	t.Helper()
	f.update(t, func(st *connection.TokenState) {
		st.Status = connection.TokenStatusConnected
		st.Generation = 1
		st.AccountID = accountID
		st.RefreshToken = "rt-0"
	})
}

func (f *fixture) useClientType(t *testing.T, clientType connection.ClientType) {
	t.Helper()
	_, err := f.store.UpdateApp(context.Background(), f.conn.App, func(app *connection.App) error {
		app.ClientType = clientType
		return nil
	})
	require.NoError(t, err)
}

func (f *fixture) update(t *testing.T, fn func(*connection.TokenState)) {
	t.Helper()
	_, err := f.store.UpdateTokenState(context.Background(), f.conn, func(st *connection.TokenState) error {
		fn(st)
		return nil
	})
	require.NoError(t, err)
}

func (f *fixture) state(t *testing.T) *connection.TokenState {
	t.Helper()
	st, err := f.store.TokenState(context.Background(), f.conn)
	require.NoError(t, err)
	return st
}

// fakeProvider consents in place of a browser and serves a token endpoint
// that checks the PKCE verifier and the redirect URI. Each code it issues
// grants its own refresh token: rt-1 for the first, rt-2 for the next. Its
// revocation endpoint records the tokens it revokes.
type fakeProvider struct {
	server *httptest.Server
	// Tests set these before the first callback.
	vouched          string
	refreshExpiresIn int
	noRefreshToken   bool
	noExpiry         bool
	onProbe          func()
	// onExchange runs in Exchange before the token request.
	onExchange func()

	failExchange atomic.Bool
	// blockExchange and blockProbe make the call signal blocked and wait
	// for its context.
	blockExchange atomic.Bool
	blockProbe    atomic.Bool
	blocked       chan struct{}

	failRevoke atomic.Bool

	mu       sync.Mutex
	grants   map[string]grant
	nextCode int
	calls    int
	// scope is what revoking a token ends. Empty counts as RevokesGrant.
	scope connect.RevokeScope
	// rotateTo is the refresh token that a refresh returns, or empty to
	// keep the one refreshed.
	rotateTo string
	// onRefresh and onRevoke run in the handler before it answers.
	onRefresh func()
	onRevoke  func()
	refreshes int
	revoked   []string
}

// grant is what the token endpoint expects with a code.
type grant struct {
	challenge   string
	redirectURI string
	// refreshToken is what the code grants.
	refreshToken string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	p := &fakeProvider{grants: make(map[string]grant), blocked: make(chan struct{}, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.token)
	mux.HandleFunc(revokePath, p.revokeEndpoint)
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeProvider) set(fn func(*fakeProvider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

// consent returns the callback that the provider redirects to after a
// user consents on the page of auth.
func (p *fakeProvider) consent(t *testing.T, auth *connect.Authorization) connect.CallbackParams {
	t.Helper()
	u, err := url.Parse(auth.URL)
	require.NoError(t, err)
	q := u.Query()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextCode++
	code := "code-" + strconv.Itoa(p.nextCode)
	p.grants[code] = grant{
		challenge:    q.Get("code_challenge"),
		redirectURI:  q.Get("redirect_uri"),
		refreshToken: "rt-" + strconv.Itoa(p.nextCode),
	}
	return connect.CallbackParams{State: q.Get("state"), Code: code}
}

func (p *fakeProvider) token(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("grant_type") == "refresh_token" {
		p.refresh(w, r)
		return
	}
	p.mu.Lock()
	p.calls++
	g, ok := p.grants[r.FormValue("code")]
	delete(p.grants, r.FormValue("code"))
	p.mu.Unlock()

	sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
	valid := ok && base64.RawURLEncoding.EncodeToString(sum[:]) == g.challenge && r.FormValue("redirect_uri") == g.redirectURI
	w.Header().Set("Content-Type", "application/json")
	switch {
	case p.failExchange.Load():
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "bad client " + r.FormValue("code")})
	case !valid:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
	default:
		body := map[string]any{"access_token": "at-1", "token_type": "Bearer", "scope": "scope-a"}
		if !p.noRefreshToken {
			body["refresh_token"] = g.refreshToken
		}
		if !p.noExpiry {
			body["expires_in"] = 3600
		}
		if p.refreshExpiresIn > 0 {
			body["refresh_token_expires_in"] = p.refreshExpiresIn
		}
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (p *fakeProvider) refresh(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.refreshes++
	hook, rotateTo := p.onRefresh, p.rotateTo
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	body := map[string]any{"access_token": "at-refreshed", "token_type": "Bearer", "expires_in": 3600}
	if rotateTo != "" {
		body["refresh_token"] = rotateTo
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// revokeEndpoint answers as RFC 7009 says. A failure names the token, so
// tests see that the engine keeps it out of what it reports.
func (p *fakeProvider) revokeEndpoint(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	p.mu.Lock()
	hook := p.onRevoke
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	if p.failRevoke.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("cannot revoke " + token))
		return
	}
	p.mu.Lock()
	p.revoked = append(p.revoked, token)
	p.mu.Unlock()
}

func (p *fakeProvider) revokedTokens() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.revoked)
}

func (p *fakeProvider) refreshCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refreshes
}

func (p *fakeProvider) exchanges() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *fakeProvider) ParseClientFile([]byte) (*connection.App, error) {
	return nil, errors.New("not supported")
}

func (p *fakeProvider) Endpoint(*connection.App) (oauth2.Endpoint, error) {
	return oauth2.Endpoint{AuthURL: authURL, TokenURL: p.server.URL, AuthStyle: oauth2.AuthStyleInParams}, nil
}

func (p *fakeProvider) AuthorizationParams(*connection.Connection) []oauth2.AuthCodeOption {
	return []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("prompt", "consent")}
}

func (p *fakeProvider) Exchange(ctx context.Context, cfg *oauth2.Config, _ *connection.Connection, code string, opts ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	if p.onExchange != nil {
		p.onExchange()
	}
	if p.blockExchange.Load() {
		p.blocked <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return cfg.Exchange(ctx, code, opts...)
}

func (p *fakeProvider) Identity(context.Context, *connection.App, *oauth2.Token) (connect.Identity, error) {
	return connect.Identity{Account: "user@example.com", AccountID: "sub-1", VouchedEmail: p.vouched}, nil
}

func (p *fakeProvider) RevokeScope() connect.RevokeScope {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scope
}

func (p *fakeProvider) Revoke(ctx context.Context, _ *connection.App, refreshToken string) error {
	form := url.Values{"token": {refreshToken}, "token_type_hint": {"refresh_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.server.URL+revokePath, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.server.Client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke: %s: %s", resp.Status, body)
	}
	return nil
}

func (p *fakeProvider) Probe(ctx context.Context, conn *connection.Connection, _ *oauth2.Token) []connection.Probe {
	if p.onProbe != nil {
		p.onProbe()
	}
	probes := make([]connection.Probe, len(conn.Services))
	for i, svc := range conn.Services {
		probes[i] = connection.Probe{Service: svc.Name}
		if p.blockProbe.Load() {
			p.blocked <- struct{}{}
			<-ctx.Done()
			probes[i].Error = ctx.Err().Error()
		}
	}
	return probes
}

// failingStore fails the engine's token state writes once a test arms it.
type failingStore struct {
	connection.Store

	mu  sync.Mutex
	err error
}

func (s *failingStore) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *failingStore) UpdateTokenState(ctx context.Context, conn *connection.Connection, fn func(*connection.TokenState) error) (*connection.TokenState, error) {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Store.UpdateTokenState(ctx, conn, fn)
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type revocations struct {
	mu   sync.Mutex
	seen []connect.Revocation
}

func (r *revocations) add(_ context.Context, rev connect.Revocation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, rev)
}

func (r *revocations) list() []connect.Revocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

type changes struct {
	mu   sync.Mutex
	seen []broker.StatusChange
}

func (c *changes) add(_ context.Context, change broker.StatusChange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, change)
}

func (c *changes) list() []broker.StatusChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen
}
