// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		name      string
		left      time.Duration
		wantToken string
		wantCalls int
	}{
		{name: "Met", left: 6 * time.Minute, wantToken: "at-1"},
		{name: "Missed", left: 4 * time.Minute, wantToken: "at-2", wantCalls: 1},
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
			assert.Equal(t, tt.wantCalls, f.endpoint.calls())
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

func TestUnknownResource(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
		grantToken(w, "at-2", "rt-1")
	})

	_, _, err := f.process(t).Token(f.ctx(t), f.conn, "https://other.example.com", minTTL)
	assert.ErrorContains(t, err, "does not serve resource")
	assert.Zero(t, f.endpoint.calls())
	assert.Len(t, f.state(t).AccessTokens, 1)
}

func TestInvalidGrantNeedsReconnect(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token "+r.refreshToken+" was revoked")
	})
	changes := newStatusChanges(f)

	_, _, err := f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
	require.ErrorIs(t, err, broker.ErrReconnectRequired)
	assert.NotContains(t, err.Error(), "rt-1")
	state := f.state(t)
	assert.Equal(t, connection.TokenStatusNeedsReconnect, state.Status)
	assert.Contains(t, state.StatusReason, "invalid_grant")
	assert.NotContains(t, state.StatusReason, "rt-1")

	// The next call fails at once and changes nothing.
	_, _, err = f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
	require.ErrorIs(t, err, broker.ErrReconnectRequired)
	assert.Equal(t, 1, f.endpoint.calls())

	got := changes.all()
	require.Len(t, got, 1)
	assert.Equal(t, f.conn.Key(), got[0].Connection)
	assert.Equal(t, connection.TokenStatusConnected, got[0].ConnectionFrom)
	assert.Equal(t, connection.TokenStatusNeedsReconnect, got[0].ConnectionTo)
	assert.Contains(t, got[0].Reason, "invalid_grant")
	assert.False(t, changes.lockHeld(), "the hook runs after the lock is released")
}

// A provider's error text that echoes the refresh token or the client
// secret, raw or form-encoded, is stored and returned without them.
func TestReasonsRedactSecrets(t *testing.T) {
	t.Parallel()
	const (
		refreshToken = "rt+1/="
		clientSecret = "s+cret/="
	)
	tests := []struct {
		name   string
		status int
		code   string
		stored func(t *testing.T, f *fixture) string
	}{
		{
			name:   "Transient",
			status: http.StatusServiceUnavailable,
			code:   "temporarily_unavailable",
			stored: func(t *testing.T, f *fixture) string { return f.state(t).RefreshErrors[""].Reason },
		},
		{
			name:   "NeedsReconnect",
			status: http.StatusBadRequest,
			code:   "invalid_grant",
			stored: func(t *testing.T, f *fixture) string { return f.state(t).StatusReason },
		},
		{
			name:   "InvalidApp",
			status: http.StatusUnauthorized,
			code:   "invalid_client",
			stored: func(t *testing.T, f *fixture) string { return f.app(t).StatusReason },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
				echo := r.refreshToken + " " + url.QueryEscape(r.refreshToken) + " " +
					r.clientSecret + " " + url.QueryEscape(r.clientSecret)
				tokenError(w, tt.status, tt.code, "refused "+echo)
			})
			f.update(t, func(st *connection.TokenState) { st.RefreshToken = refreshToken })
			_, err := f.store.UpdateApp(context.Background(), f.conn.App, func(app *connection.App) error {
				app.ClientSecret = clientSecret
				return nil
			})
			require.NoError(t, err)

			_, _, err = f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
			require.Error(t, err)
			stored := tt.stored(t, f)
			assert.Contains(t, stored, tt.code)
			for _, secret := range []string{refreshToken, clientSecret} {
				for _, form := range []string{secret, url.QueryEscape(secret)} {
					assert.NotContains(t, stored, form)
					assert.NotContains(t, err.Error(), form)
				}
			}
		})
	}
}

// The provider refuses the refresh token while another host rotates it.
func TestInvalidGrantRetriesRotated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// onRetry runs when the provider receives the rotated token.
		onRetry   func(t *testing.T, f *fixture, w http.ResponseWriter)
		wantToken string
	}{
		{
			name:      "Success",
			onRetry:   func(_ *testing.T, _ *fixture, w http.ResponseWriter) { grantToken(w, "at-2", "rt-2") },
			wantToken: "at-2",
		},
		{
			// A reconnect lands while the retry fails too. The reconnect
			// wins, and its token is returned.
			name: "Reconnected",
			onRetry: func(t *testing.T, f *fixture, w http.ResponseWriter) {
				f.update(t, func(st *connection.TokenState) {
					st.Generation++
					st.RefreshToken = "rt-new"
					st.AccessTokens[""] = connection.AccessToken{Token: "reconnected", ExpiresAt: time.Now().Add(time.Hour)}
				})
				tokenError(w, http.StatusBadRequest, "invalid_grant", "")
			},
			wantToken: "reconnected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var f *fixture
			f = newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
				if r.refreshToken == "rt-1" {
					f.update(t, func(st *connection.TokenState) { st.RefreshToken = "rt-2" })
					tokenError(w, http.StatusBadRequest, "invalid_grant", "")
					return
				}
				tt.onRetry(t, f, w)
			})

			token, _, err := f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
			assert.Equal(t, []string{"rt-1", "rt-2"}, f.endpoint.refreshTokens())
			assert.Equal(t, connection.TokenStatusConnected, f.state(t).Status)
		})
	}
}

func TestInvalidClient(t *testing.T) {
	t.Parallel()

	t.Run("MarksApp", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
			tokenError(w, http.StatusUnauthorized, "invalid_client", "")
		})
		changes := newStatusChanges(f)

		_, _, err := f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
		require.ErrorIs(t, err, broker.ErrReconnectRequired)
		assert.Equal(t, connection.AppStatusInvalid, f.app(t).Status)

		_, _, err = f.process(t, changes.record).Token(f.ctx(t), f.conn, "", minTTL)
		require.ErrorIs(t, err, broker.ErrReconnectRequired)
		assert.Equal(t, 1, f.endpoint.calls())

		got := changes.all()
		require.Len(t, got, 1)
		assert.Equal(t, f.conn.App, got[0].App)
		assert.Equal(t, connection.AppStatusOK, got[0].AppFrom)
		assert.Equal(t, connection.AppStatusInvalid, got[0].AppTo)
		assert.Empty(t, got[0].ConnectionTo)
	})

	t.Run("RetriesChangedClient", func(t *testing.T) {
		t.Parallel()
		var f *fixture
		f = newFixture(t, func(w http.ResponseWriter, r tokenRequest) {
			switch r.clientSecret {
			case "secret":
				_, err := f.store.UpdateApp(r.ctx, f.conn.App, func(app *connection.App) error {
					app.ClientSecret = "new-secret"
					return nil
				})
				assert.NoError(t, err)
				tokenError(w, http.StatusUnauthorized, "invalid_client", "")
			case "new-secret":
				grantToken(w, "at-2", "rt-1")
			default:
				tokenError(w, http.StatusUnauthorized, "invalid_client", "")
			}
		})

		token, _, err := f.process(t).Token(f.ctx(t), f.conn, "", minTTL)
		require.NoError(t, err)
		assert.Equal(t, "at-2", token)
		assert.Equal(t, []string{"secret", "new-secret"}, f.endpoint.clientSecrets())
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

// A refresh that the provider accepted is saved, so its rotated refresh
// token is kept, unless another holder rotated the token since.
func TestRefreshKeepsAcceptedResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		lockLost bool
		// access is the access token the provider grants with rt-2.
		access string
		// during runs while the provider handles the refresh.
		during      func(t *testing.T, f *fixture, cancel context.CancelFunc)
		wantRefresh string
		// wantToken, when set, is the token the call returns.
		wantToken string
		wantErr   bool
	}{
		{
			name:        "CallerCanceled",
			access:      "at-2",
			during:      func(_ *testing.T, _ *fixture, cancel context.CancelFunc) { cancel() },
			wantRefresh: "rt-2",
		},
		{
			name:        "LockLost",
			lockLost:    true,
			access:      "at-2",
			during:      func(*testing.T, *fixture, context.CancelFunc) {},
			wantRefresh: "rt-2",
			wantToken:   "at-2",
		},
		{
			name:        "EmptyAccessToken",
			during:      func(*testing.T, *fixture, context.CancelFunc) {},
			wantRefresh: "rt-2",
			wantErr:     true,
		},
		{
			name:     "LockLostAfterRotation",
			lockLost: true,
			access:   "at-2",
			during: func(t *testing.T, f *fixture, _ context.CancelFunc) {
				f.update(t, func(st *connection.TokenState) { st.RefreshToken = "rt-other" })
			},
			wantRefresh: "rt-other",
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var f *fixture
			f = newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
				tt.during(t, f, cancel)
				grantToken(w, tt.access, "rt-2")
			})
			var opts []func(*broker.Config)
			if tt.lockLost {
				opts = append(opts, loseLocks)
			}

			token, _, err := f.process(t, opts...).Token(ctx, f.conn, "", minTTL)
			switch {
			case tt.wantErr:
				assert.Error(t, err)
			case tt.wantToken != "":
				require.NoError(t, err)
				assert.Equal(t, tt.wantToken, token)
			}
			assert.Equal(t, tt.wantRefresh, f.state(t).RefreshToken)
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
		name      string
		rejected  string
		wantToken string
		wantCalls int
	}{
		{name: "Stored", rejected: "at-1", wantToken: "at-2", wantCalls: 1},
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
			b := f.process(t, cacheTokens)
			token, _, err := b.Token(f.ctx(t), f.conn, "", minTTL)
			require.NoError(t, err)
			require.Equal(t, "at-1", token)

			token, _, err = b.Rejected(f.ctx(t), f.conn, "", tt.rejected, minTTL)
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
			assert.Equal(t, tt.wantCalls, f.endpoint.calls())
		})
	}
}

// A run process stops serving a cached token once a resource rejected it.
func TestRejectedLeavesCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
		grantToken(w, "at-2", "rt-1")
	})
	f.update(t, func(st *connection.TokenState) {
		st.AccessTokens[""] = connection.AccessToken{Token: "at-1", ExpiresAt: time.Now().Add(time.Hour)}
	})
	b := f.process(t, cacheTokens)
	token, _, err := b.Token(f.ctx(t), f.conn, "", minTTL)
	require.NoError(t, err)
	require.Equal(t, "at-1", token)

	// Another process stored a newer token.
	f.update(t, func(st *connection.TokenState) {
		st.AccessTokens[""] = connection.AccessToken{Token: "at-3", ExpiresAt: time.Now().Add(time.Hour)}
	})
	token, _, err = b.Rejected(f.ctx(t), f.conn, "", "at-1", minTTL)
	require.NoError(t, err)
	assert.Equal(t, "at-3", token)
	token, _, err = b.Token(f.ctx(t), f.conn, "", minTTL)
	require.NoError(t, err)
	assert.Equal(t, "at-3", token)
	assert.Zero(t, f.endpoint.calls())
}

// A run process caches the token it holds. Any other process reads the
// token state on each call, so it sees a reconnect at once.
func TestTokenAfterReconnect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cache     bool
		wantToken string
	}{
		{name: "Uncached", wantToken: "reconnected"},
		{name: "Cached", cache: true, wantToken: "at-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
				grantToken(w, "at-2", "rt-1")
			})
			var opts []func(*broker.Config)
			if tt.cache {
				opts = append(opts, cacheTokens)
			}
			b := f.process(t, opts...)
			token, _, err := b.Token(f.ctx(t), f.conn, "", minTTL)
			require.NoError(t, err)
			require.Equal(t, "at-2", token)

			f.update(t, func(st *connection.TokenState) {
				st.Generation++
				st.AccessTokens[""] = connection.AccessToken{Token: "reconnected", ExpiresAt: time.Now().Add(time.Hour)}
			})
			token, _, err = b.Token(f.ctx(t), f.conn, "", minTTL)
			require.NoError(t, err)
			assert.Equal(t, tt.wantToken, token)
		})
	}
}

// Three calls read the token state once each without the cache, and with
// it only on the first, before and while it holds the lock.
func TestTokenStateReads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cache     bool
		wantReads int32
	}{
		{name: "Uncached", wantReads: 5},
		{name: "Cached", cache: true, wantReads: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, func(w http.ResponseWriter, _ tokenRequest) {
				grantToken(w, "at-2", "rt-1")
			})
			var reads atomic.Int32
			b := f.process(t, func(cfg *broker.Config) {
				cfg.CacheTokens = tt.cache
				cfg.Store = countingStore{Store: cfg.Store, reads: &reads}
			})

			for range 3 {
				token, _, err := b.Token(f.ctx(t), f.conn, "", minTTL)
				require.NoError(t, err)
				assert.Equal(t, "at-2", token)
			}
			assert.Equal(t, tt.wantReads, reads.Load())
			assert.Equal(t, 1, f.endpoint.calls())
		})
	}
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

func cacheTokens(cfg *broker.Config) {
	cfg.CacheTokens = true
}

// tokenEndpoint is a provider's token endpoint that answers refreshes.
type tokenEndpoint struct {
	url      string
	mu       sync.Mutex
	requests []tokenRequest
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
		e.requests = append(e.requests, req)
		e.mu.Unlock()
		respond(w, req)
	}))
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

func (e *tokenEndpoint) refreshTokens() []string {
	return e.collect(func(r tokenRequest) string { return r.refreshToken })
}

func (e *tokenEndpoint) clientSecrets() []string {
	return e.collect(func(r tokenRequest) string { return r.clientSecret })
}

func (e *tokenEndpoint) collect(field func(tokenRequest) string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.requests))
	for _, r := range e.requests {
		out = append(out, field(r))
	}
	return out
}

func (e *tokenEndpoint) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.requests)
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

func tokenError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
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

var errLockLost = errors.New("lock lost")

// loseLocks gives a process locks that report themselves lost before each
// write, as after renewals failed.
func loseLocks(cfg *broker.Config) {
	cfg.Locks = lostLocks{Locks: cfg.Locks}
}

type lostLocks struct {
	connection.Locks
}

func (l lostLocks) TryLock(ctx context.Context, key connection.Key) (connection.Lock, error) {
	lock, err := l.Locks.TryLock(ctx, key)
	if err != nil {
		return nil, err
	}
	return lostLock{Lock: lock}, nil
}

type lostLock struct {
	connection.Lock
}

func (lostLock) Held() error { return errLockLost }

type countingStore struct {
	connection.Store
	reads *atomic.Int32
}

func (s countingStore) TokenState(ctx context.Context, conn *connection.Connection) (*connection.TokenState, error) {
	s.reads.Add(1)
	return s.Store.TokenState(ctx, conn)
}

// statusChanges records reported status changes, and whether the
// connection's lock was still held when each was reported.
type statusChanges struct {
	locks   connection.Locks
	mu      sync.Mutex
	changes []broker.StatusChange
	held    bool
}

func newStatusChanges(f *fixture) *statusChanges {
	return &statusChanges{locks: file.NewServerStores(f.cfg).ConnectionLocks()}
}

func (c *statusChanges) record(cfg *broker.Config) {
	cfg.OnStatusChange = func(ctx context.Context, change broker.StatusChange) {
		lock, err := c.locks.TryLock(ctx, change.Connection)
		if err == nil {
			lock.Unlock()
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.changes = append(c.changes, change)
		c.held = c.held || errors.Is(err, connection.ErrLocked)
	}
}

func (c *statusChanges) all() []broker.StatusChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]broker.StatusChange(nil), c.changes...)
}

func (c *statusChanges) lockHeld() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held
}
