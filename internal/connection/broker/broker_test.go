// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package broker_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

const (
	minTTL       = 5 * time.Minute
	tokenTimeout = 30 * time.Second
)

func TestTokenRefreshesExpired(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
		grantToken(w, "at-2", "rt-2")
	})

	token, expiresIn, err := f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
	require.NoError(t, err)
	assert.Equal(t, "at-2", token)
	assert.Greater(t, expiresIn, 55*time.Minute)
	assert.Equal(t, []string{"rt-1"}, f.endpoint.refreshTokens())

	state := f.state(t)
	assert.Equal(t, "rt-2", state.RefreshToken)
	assert.Equal(t, "at-2", state.AccessTokens[""].Token)
	assert.False(t, state.RefreshedAt.IsZero())

	// Another process reads the refreshed token without a refresh.
	token, _, err = f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
	require.NoError(t, err)
	assert.Equal(t, "at-2", token)
	assert.Equal(t, 1, f.endpoint.calls())
}

// The minTTL is cut to half of a 10-minute lifetime, so a token with 6
// minutes left serves a 50-minute request and one with 4 minutes does not.
func TestTokenCapsMinTTL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		left        time.Duration
		wantToken   string
		wantRefresh bool
	}{
		{name: "Met", left: 6 * time.Minute, wantToken: "at-1"},
		{name: "Missed", left: 4 * time.Minute, wantToken: "at-2", wantRefresh: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
				grantToken(w, "at-2", "rt-1")
			})
			now := time.Now()
			f.update(t, func(st *connection.TokenState) {
				st.AccessTokens[""] = connection.AccessToken{
					Token:     "at-1",
					ExpiresAt: now.Add(tt.left),
					IssuedAt:  now.Add(tt.left - 10*time.Minute),
				}
			})

			token, _, err := f.process(t).Token(f.ctx(t), f.conn, "", 50*time.Minute)
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
			assert.Equal(t, tt.wantRefresh, f.endpoint.calls() == 1)
		})
	}
}

// Processes that find a refresh in flight wait for it and take its
// outcome, so the provider receives one request.
func TestConcurrentRefresh(t *testing.T) {
	t.Parallel()
	const processes = 5
	tests := []struct {
		name              string
		callersPerProcess int
		respond           func(http.ResponseWriter)
		check             func(t *testing.T, token string, err error)
	}{
		{
			name:              "Success",
			callersPerProcess: 4,
			respond:           func(w http.ResponseWriter) { grantToken(w, "at-2", "rt-1") },
			check: func(t *testing.T, token string, err error) {
				require.NoError(t, err)
				assert.Equal(t, "at-2", token)
			},
		},
		{
			// A caller that starts after a failure refreshes again, so each
			// process has one caller, which is waiting when the refresh fails.
			name:              "Transient",
			callersPerProcess: 1,
			respond:           func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) },
			check: func(t *testing.T, _ string, err error) {
				assert.ErrorContains(t, err, "status 503")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			waits := newWaitCounter(processes - 1)
			f := newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
				waits.waitAll(r.ctx)
				tt.respond(w)
			})

			type result struct {
				token string
				err   error
			}
			results := make(chan result, processes*tt.callersPerProcess)
			var wg sync.WaitGroup
			for range processes {
				b := f.process(t, waits.observe)
				for range tt.callersPerProcess {
					wg.Go(func() {
						token, _, err := b.Token(f.ctx(t), f.conn, "", minTTL)
						results <- result{token: token, err: err}
					})
				}
			}
			wg.Wait()
			close(results)

			for r := range results {
				tt.check(t, r.token, r.err)
			}
			assert.Equal(t, 1, f.endpoint.calls())
			assert.Equal(t, connection.TokenStatusConnected, f.state(t).Status)
		})
	}
}

// A process that waited on a refresh of another resource refreshes its own
// once the lock is released.
func TestWaiterForOtherResource(t *testing.T) {
	t.Parallel()
	waits := newWaitCounter(1)
	f := newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
		waits.waitAll(r.ctx)
		grantToken(w, "at-2", "rt-1")
	})

	var wg sync.WaitGroup
	for _, resource := range []string{"a", "b"} {
		b := f.process(t, waits.observe)
		wg.Go(func() {
			token, _, err := b.Token(f.ctx(t), f.conn, resource, minTTL)
			assert.NoError(t, err)
			assert.Equal(t, "at-2", token)
		})
	}
	wg.Wait()
	assert.Equal(t, 2, f.endpoint.calls())
}

func TestInvalidGrantNeedsReconnect(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
	})
	var changes statusChanges

	_, _, err := f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
	require.ErrorIs(t, err, broker.ErrReconnectRequired)
	state := f.state(t)
	assert.Equal(t, connection.TokenStatusNeedsReconnect, state.Status)
	assert.Contains(t, state.StatusReason, "invalid_grant")

	// The next call fails at once and changes nothing.
	_, _, err = f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
	require.ErrorIs(t, err, broker.ErrReconnectRequired)
	assert.Equal(t, 1, f.endpoint.calls())

	got := changes.all()
	require.Len(t, got, 1)
	require.NotNil(t, got[0].State)
	assert.Equal(t, connection.TokenStatusNeedsReconnect, got[0].State.Status)
}

// Another host rotates the refresh token while this one refreshes with the
// old one, which the provider then refuses.
func TestInvalidGrantRetriesRotated(t *testing.T) {
	t.Parallel()
	var f *fixture
	f = newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
		if r.refreshToken == "rt-1" {
			f.update(t, func(st *connection.TokenState) { st.RefreshToken = "rt-2" })
			tokenError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		grantToken(w, "at-2", "rt-2")
	})

	token, _, err := f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
	require.NoError(t, err)
	assert.Equal(t, "at-2", token)
	assert.Equal(t, []string{"rt-1", "rt-2"}, f.endpoint.refreshTokens())
	assert.Equal(t, connection.TokenStatusConnected, f.state(t).Status)
}

func TestInvalidClient(t *testing.T) {
	t.Parallel()

	t.Run("MarksApp", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
			tokenError(w, http.StatusUnauthorized, "invalid_client")
		})
		var changes statusChanges

		_, _, err := f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
		require.ErrorIs(t, err, broker.ErrReconnectRequired)
		assert.Equal(t, connection.AppStatusInvalid, f.app(t).Status)

		_, _, err = f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
		require.ErrorIs(t, err, broker.ErrReconnectRequired)
		assert.Equal(t, 1, f.endpoint.calls())

		got := changes.all()
		require.Len(t, got, 1)
		require.NotNil(t, got[0].App)
		assert.Equal(t, connection.AppStatusInvalid, got[0].App.Status)
	})

	t.Run("RetriesChangedClient", func(t *testing.T) {
		t.Parallel()
		var f *fixture
		f = newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
			if r.clientSecret == "secret" {
				_, err := f.store.UpdateApp(r.ctx, f.conn.App, func(app *connection.App) error {
					app.ClientSecret = "new-secret"
					return nil
				})
				assert.NoError(t, err)
				tokenError(w, http.StatusUnauthorized, "invalid_client")
				return
			}
			grantToken(w, "at-2", "rt-1")
		})

		token, _, err := f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
		require.NoError(t, err)
		assert.Equal(t, "at-2", token)
		assert.Equal(t, 2, f.endpoint.calls())
		assert.Equal(t, connection.AppStatusOK, f.app(t).Status)
	})
}

// Writes that land during a refresh decide what the refresh keeps.
func TestConcurrentWrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		write func(t *testing.T, f *fixture)
		check func(t *testing.T, f *fixture, token string, err error)
	}{
		{
			name: "Reconnect",
			write: func(t *testing.T, f *fixture) {
				f.update(t, func(st *connection.TokenState) {
					st.Generation++
					st.RefreshToken = "rt-new"
					st.AccessTokens[""] = connection.AccessToken{Token: "reconnected", ExpiresAt: time.Now().Add(time.Hour)}
				})
			},
			check: func(t *testing.T, f *fixture, token string, err error) {
				require.NoError(t, err)
				assert.Equal(t, "reconnected", token)
				state := f.state(t)
				assert.Equal(t, "reconnected", state.AccessTokens[""].Token)
				assert.Equal(t, "rt-new", state.RefreshToken)
			},
		},
		{
			name: "Session",
			write: func(t *testing.T, f *fixture) {
				f.update(t, func(st *connection.TokenState) {
					st.Session = &connection.Session{ID: "s-1", Phase: connection.SessionPhaseWaiting}
				})
			},
			check: func(t *testing.T, f *fixture, token string, err error) {
				require.NoError(t, err)
				assert.Equal(t, "at-2", token)
				state := f.state(t)
				require.NotNil(t, state.Session)
				assert.Equal(t, "s-1", state.Session.ID)
				assert.Equal(t, "rt-2", state.RefreshToken)
			},
		},
		{
			name: "Remove",
			write: func(t *testing.T, f *fixture) {
				assert.NoError(t, f.store.Delete(context.Background(), f.conn.Key()))
			},
			check: func(t *testing.T, _ *fixture, _ string, err error) {
				assert.ErrorIs(t, err, connection.ErrNotFound)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var f *fixture
			f = newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
				tt.write(t, f)
				grantToken(w, "at-2", "rt-2")
			})

			token, _, err := f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
			tt.check(t, f, token, err)
		})
	}
}

func TestFailsAtOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
	}{
		{
			name: "Pending",
			setup: func(t *testing.T, f *fixture) {
				f.update(t, func(st *connection.TokenState) { st.Status = connection.TokenStatusPending })
			},
		},
		{
			name: "NeedsReconnect",
			setup: func(t *testing.T, f *fixture) {
				f.update(t, func(st *connection.TokenState) { st.Status = connection.TokenStatusNeedsReconnect })
			},
		},
		{
			name: "InvalidApp",
			setup: func(t *testing.T, f *fixture) {
				_, err := f.store.UpdateApp(context.Background(), f.conn.App, func(app *connection.App) error {
					app.Status = connection.AppStatusInvalid
					return nil
				})
				require.NoError(t, err)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
				grantToken(w, "at-2", "rt-1")
			})
			tt.setup(t, f)

			_, _, err := f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
			assert.ErrorIs(t, err, broker.ErrReconnectRequired)
			assert.Zero(t, f.endpoint.calls())
		})
	}
}

func TestRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		rejected    string
		wantToken   string
		wantRefresh bool
	}{
		{name: "Stored", rejected: "at-1", wantToken: "at-2", wantRefresh: true},
		{name: "Older", rejected: "at-0", wantToken: "at-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
				grantToken(w, "at-2", "rt-1")
			})
			f.update(t, func(st *connection.TokenState) {
				st.AccessTokens[""] = connection.AccessToken{Token: "at-1", ExpiresAt: time.Now().Add(time.Hour)}
			})
			b := f.process(t)
			token, _, err := b.Token(f.ctx(t), f.conn, "", minTTL)
			require.NoError(t, err)
			require.Equal(t, "at-1", token)

			token, _, err = b.Rejected(f.ctx(t), f.conn, "", tt.rejected, minTTL)
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
			assert.Equal(t, tt.wantRefresh, f.endpoint.calls() == 1)
		})
	}
}

// A process reads the token state once per connection and resource while
// its cached token stays usable.
func TestTokenCachedInProcess(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
		grantToken(w, "at-2", "rt-1")
	})
	var reads atomic.Int32
	b := f.process(t, func(cfg *broker.Config) {
		cfg.Store = countingStore{Store: cfg.Store, reads: &reads}
	})

	for range 3 {
		token, _, err := b.Token(f.ctx(t), f.conn, "", minTTL)
		require.NoError(t, err)
		assert.Equal(t, "at-2", token)
	}
	// One read before the lock, and one holding it.
	assert.Equal(t, int32(2), reads.Load())
}

type fixture struct {
	cfg      *config.Config
	endpoint *tokenEndpoint
	// store is a process of its own that tests write through.
	store connection.Store
	conn  *connection.Connection
}

// newFixture returns a connected connection whose access token expired,
// with refresh token rt-1, on an app whose token endpoint answers with
// respond.
func newFixture(t *testing.T, respond func(http.ResponseWriter, tokenRequest)) *fixture {
	t.Helper()
	ctx := context.Background()
	f := &fixture{
		cfg:      &config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}},
		endpoint: newTokenEndpoint(t, respond),
	}
	stores := file.NewServerStores(f.cfg)
	s, err := stores.ConnectionStore()
	require.NoError(t, err)
	f.store = s

	app, err := f.store.CreateApp(ctx, &connection.App{
		Name:         "Example",
		Provider:     connection.ProviderOAuth2,
		Kind:         connection.AppKindBYO,
		ClientID:     "client",
		ClientSecret: "secret",
		ClientType:   connection.ClientTypeWeb,
		Endpoints:    &connection.Endpoints{Token: f.endpoint.url},
	})
	require.NoError(t, err)
	f.conn, err = f.store.Create(ctx, &connection.Connection{
		Workspace: "ops",
		Name:      "work",
		Provider:  connection.ProviderOAuth2,
		App:       app.ID,
		Grant:     connection.GrantAuthorizationCode,
	})
	require.NoError(t, err)
	f.update(t, func(st *connection.TokenState) {
		now := time.Now()
		st.Status = connection.TokenStatusConnected
		st.Generation = 1
		st.RefreshToken = "rt-1"
		st.AccessTokens = map[string]connection.AccessToken{
			"": {Token: "at-1", ExpiresAt: now.Add(-time.Minute), IssuedAt: now.Add(-time.Hour)},
		}
	})
	return f
}

// process returns a broker as another process on the host has it, with
// its own store, locks, and cache.
func (f *fixture) process(t *testing.T, opts ...func(*broker.Config)) *broker.Broker {
	t.Helper()
	stores := file.NewServerStores(f.cfg)
	s, err := stores.ConnectionStore()
	require.NoError(t, err)
	cfg := broker.Config{Store: s, Locks: stores.ConnectionLocks()}
	for _, opt := range opts {
		opt(&cfg)
	}
	b, err := broker.New(cfg)
	require.NoError(t, err)
	return b
}

func (f *fixture) ctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), tokenTimeout)
	t.Cleanup(cancel)
	return ctx
}

// update writes the token state. Token endpoints call it too, so it does
// not stop the test.
func (f *fixture) update(t *testing.T, fn func(*connection.TokenState)) {
	t.Helper()
	_, err := f.store.UpdateTokenState(context.Background(), f.conn, func(st *connection.TokenState) error {
		fn(st)
		return nil
	})
	assert.NoError(t, err)
}

func (f *fixture) state(t *testing.T) *connection.TokenState {
	t.Helper()
	state, err := f.store.TokenState(context.Background(), f.conn)
	require.NoError(t, err)
	return state
}

func (f *fixture) app(t *testing.T) *connection.App {
	t.Helper()
	app, err := f.store.GetApp(context.Background(), f.conn.App)
	require.NoError(t, err)
	return app
}

// tokenEndpoint is a provider's token endpoint that answers refreshes.
type tokenEndpoint struct {
	url  string
	mu   sync.Mutex
	sent []string
}

type tokenRequest struct {
	ctx          context.Context
	refreshToken string
	clientSecret string
}

func newTokenEndpoint(t *testing.T, respond func(http.ResponseWriter, tokenRequest)) *tokenEndpoint {
	t.Helper()
	e := &tokenEndpoint{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "refresh_token" {
			http.Error(w, "unexpected grant", http.StatusBadRequest)
			return
		}
		req := tokenRequest{ctx: r.Context(), refreshToken: r.FormValue("refresh_token"), clientSecret: r.FormValue("client_secret")}
		e.mu.Lock()
		e.sent = append(e.sent, req.refreshToken)
		e.mu.Unlock()
		respond(w, req)
	}))
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

func (e *tokenEndpoint) refreshTokens() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.sent...)
}

func (e *tokenEndpoint) calls() int {
	return len(e.refreshTokens())
}

func grantToken(w http.ResponseWriter, access, refresh string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func tokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// waitCounter lets a token endpoint answer only once the given number of
// callers wait for the lock its refresh holds.
type waitCounter struct {
	want       int32
	waiting    atomic.Int32
	allWaiting chan struct{}
}

func newWaitCounter(want int) *waitCounter {
	return &waitCounter{want: int32(want), allWaiting: make(chan struct{})}
}

func (c *waitCounter) observe(cfg *broker.Config) {
	cfg.Locks = observedLocks{Locks: cfg.Locks, counter: c}
}

func (c *waitCounter) waitAll(ctx context.Context) {
	select {
	case <-c.allWaiting:
	case <-ctx.Done():
	}
}

type observedLocks struct {
	connection.Locks
	counter *waitCounter
}

func (l observedLocks) WaitUnlocked(ctx context.Context, key connection.Key) error {
	if l.counter.waiting.Add(1) == l.counter.want {
		close(l.counter.allWaiting)
	}
	return l.Locks.WaitUnlocked(ctx, key)
}

type countingStore struct {
	connection.Store
	reads *atomic.Int32
}

func (s countingStore) TokenState(ctx context.Context, conn *connection.Connection) (*connection.TokenState, error) {
	s.reads.Add(1)
	return s.Store.TokenState(ctx, conn)
}

type statusChanges struct {
	mu      sync.Mutex
	changes []broker.StatusChange
}

func (c *statusChanges) record(cfg *broker.Config) {
	cfg.OnStatusChange = func(_ context.Context, change broker.StatusChange) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.changes = append(c.changes, change)
	}
}

func (c *statusChanges) all() []broker.StatusChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]broker.StatusChange(nil), c.changes...)
}
