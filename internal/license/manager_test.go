// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// mockCloudServerConfig holds per-path handlers for the mock cloud API server.
type mockCloudServerConfig struct {
	activateHandler  http.HandlerFunc
	heartbeatHandler http.HandlerFunc
}

// newMockCloudServer starts an httptest.Server that dispatches to the given
// per-path handlers.  The server is automatically shut down when the test ends.
func newMockCloudServer(t *testing.T, cfg mockCloudServerConfig) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	if cfg.activateHandler != nil {
		mux.HandleFunc("/api/v1/licenses/activate", cfg.activateHandler)
	}
	if cfg.heartbeatHandler != nil {
		mux.HandleFunc("/api/v1/licenses/heartbeat", cfg.heartbeatHandler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// activateHandlerFn returns an HTTP handler that responds 200 with a signed token.
func activateHandlerFn(token, heartbeatSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ActivateResponse{
			Token:           token,
			HeartbeatSecret: heartbeatSecret,
		})
	}
}

// heartbeatHandlerFn returns an HTTP handler that responds 200 with a refreshed token.
func heartbeatHandlerFn(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{Token: token})
	}
}

// errorHandlerFn returns an HTTP handler that responds with the given status code
// and a JSON body carrying the given message.
func errorHandlerFn(statusCode int, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
	}
}

// stopWithTimeout calls m.Stop() in a goroutine and fails the test if it does
// not return within the given timeout.  Used to detect goroutine leaks.
func stopWithTimeout(t *testing.T, m *Manager, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		m.Stop()
		close(done)
	}()
	select {
	case <-done:
		// OK
	case <-time.After(timeout):
		t.Fatal("Stop() did not return within timeout — possible goroutine leak")
	}
}

// makeAD is a shorthand for building a minimal ActivationData for heartbeat calls.
func makeAD(serverID string) *ActivationData {
	return &ActivationData{
		Token:           "initial-token",
		HeartbeatSecret: "hb-secret",
		LicenseKey:      "lic-key",
		ServerID:        serverID,
	}
}

// ---------------------------------------------------------------------------
// NewManager
// ---------------------------------------------------------------------------

func TestNewManager_NilLogger(t *testing.T) {
	t.Parallel()

	t.Run("nil logger does not panic and manager is usable", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		require.NotPanics(t, func() {
			m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, nil)
			require.NotNil(t, m)
			// The logger field must not be nil — NewManager defaults to slog.Default().
			assert.NotNil(t, m.logger)
		})
	})
}

func TestNewManager_FieldsSetCorrectly(t *testing.T) {
	t.Parallel()

	t.Run("Checker returns non-nil state in community mode", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		store := &mockActivationStore{}
		cfg := ManagerConfig{LicenseDir: t.TempDir(), CloudURL: "https://example.com"}

		m := NewManager(cfg, pub, store, slog.Default())

		require.NotNil(t, m)
		checker := m.Checker()
		require.NotNil(t, checker)
		assert.True(t, checker.IsCommunity(), "freshly constructed manager must be in community mode")
		assert.Equal(t, "", checker.Plan())
		assert.Nil(t, checker.Claims())
	})
}

// ---------------------------------------------------------------------------
// Start — no license
// ---------------------------------------------------------------------------

func TestManager_Start_CommunityMode(t *testing.T) {
	// Not parallel at the top level because subtests use t.Setenv.

	t.Run("no env vars no config key no store data yields community mode", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		pub, _ := testKeyPair(t)
		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		checker := m.Checker()
		assert.True(t, checker.IsCommunity())
		assert.Equal(t, "", checker.Plan())
	})
}

// ---------------------------------------------------------------------------
// Start — inline JWT via DAGU_LICENSE env
// ---------------------------------------------------------------------------

func TestManager_Start_EnvInlineJWT(t *testing.T) {
	// Subtests use t.Setenv so the parent must not call t.Parallel.

	t.Run("valid token updates state with claims", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		t.Setenv("DAGU_LICENSE", token)
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		checker := m.Checker()
		assert.False(t, checker.IsCommunity())
		assert.Equal(t, "pro", checker.Plan())
		assert.True(t, checker.IsFeatureEnabled(FeatureAudit))
		assert.True(t, checker.IsFeatureEnabled(FeatureRBAC))
		assert.True(t, checker.IsFeatureEnabled(FeatureSSO))
	})

	t.Run("garbage token gracefully degrades to community mode", func(t *testing.T) {
		pub, _ := testKeyPair(t)

		t.Setenv("DAGU_LICENSE", "this.is.not.a.valid.jwt")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err, "invalid token must not cause Start to return an error")

		assert.True(t, m.Checker().IsCommunity(), "bad token should fall back to community mode")
	})

	t.Run("expired-in-grace token uses lenient verify and updates state", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		claims := expiredInGraceClaims()
		token := signToken(t, priv, claims)

		t.Setenv("DAGU_LICENSE", token)
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		checker := m.Checker()
		assert.False(t, checker.IsCommunity())
		assert.Equal(t, "pro", checker.Plan())
		assert.True(t, checker.IsGracePeriod(), "expired-in-grace token should put state in grace period")
	})
}

// ---------------------------------------------------------------------------
// Start — license key via DAGU_LICENSE_KEY env
// ---------------------------------------------------------------------------

func TestManager_Start_EnvLicenseKey(t *testing.T) {
	// Subtests use t.Setenv so the parent must not call t.Parallel.

	t.Run("activation success updates state and starts heartbeat", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "test-license-key-123")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		checker := m.Checker()
		assert.False(t, checker.IsCommunity())
		assert.Equal(t, "pro", checker.Plan())

		// Stop must return quickly — heartbeat goroutine must terminate.
		stopWithTimeout(t, m, 5*time.Second)
	})

	t.Run("activation HTTP error gracefully degrades to community mode", func(t *testing.T) {
		pub, _ := testKeyPair(t)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: errorHandlerFn(http.StatusInternalServerError, "server error"),
		})

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "bad-license-key")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err, "activation failure must not propagate as an error from Start")

		assert.True(t, m.Checker().IsCommunity())
	})
}

// ---------------------------------------------------------------------------
// Start — offline fallback via cached activation
// ---------------------------------------------------------------------------

func TestManager_Start_OfflineFallback(t *testing.T) {
	// Subtests use t.Setenv so the parent must not call t.Parallel.

	t.Run("offline with cached activation loads license", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: errorHandlerFn(http.StatusInternalServerError, "server unreachable"),
		})

		store := &mockActivationStore{data: &ActivationData{
			Token:           token,
			HeartbeatSecret: "hb-secret",
			LicenseKey:      "my-license-key",
			ServerID:        "server-001",
		}}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "my-license-key")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		checker := m.Checker()
		assert.False(t, checker.IsCommunity(), "cached activation should yield non-community mode")
		assert.Equal(t, "pro", checker.Plan())

		stopWithTimeout(t, m, 5*time.Second)
	})

	t.Run("offline without cache falls back to community mode", func(t *testing.T) {
		pub, _ := testKeyPair(t)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: errorHandlerFn(http.StatusInternalServerError, "server unreachable"),
		})

		store := &mockActivationStore{data: nil}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "my-license-key")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		assert.True(t, m.Checker().IsCommunity(), "no cached data should fall back to community mode")
	})

	t.Run("offline with key mismatch falls back to community mode", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: errorHandlerFn(http.StatusInternalServerError, "server unreachable"),
		})

		store := &mockActivationStore{data: &ActivationData{
			Token:           token,
			HeartbeatSecret: "hb-secret",
			LicenseKey:      "different-key",
			ServerID:        "server-001",
		}}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "my-license-key")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		assert.True(t, m.Checker().IsCommunity(), "mismatched key should fall back to community mode")
	})

	t.Run("offline with store load error falls back to community mode", func(t *testing.T) {
		pub, _ := testKeyPair(t)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: errorHandlerFn(http.StatusInternalServerError, "server unreachable"),
		})

		store := &mockActivationStore{loadErr: errors.New("disk read failure")}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "my-license-key")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		assert.True(t, m.Checker().IsCommunity(), "store load error should fall back to community mode")
	})
}

// ---------------------------------------------------------------------------
// Start — activation file (store has valid token)
// ---------------------------------------------------------------------------

func TestManager_Start_ActivationFile(t *testing.T) {
	// Subtests use t.Setenv so the parent must not call t.Parallel.

	t.Run("store has valid token updates state and starts heartbeat", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		ad := &ActivationData{
			Token:           token,
			HeartbeatSecret: "hb-secret",
			LicenseKey:      "lic-key",
			ServerID:        "server-001",
		}
		store := &mockActivationStore{data: ad}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		// Cloud URL is unreachable; the immediate heartbeat fails gracefully and state stays cached.
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   "http://127.0.0.1:0",
		}, pub, store, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		checker := m.Checker()
		assert.False(t, checker.IsCommunity())
		assert.Equal(t, "pro", checker.Plan())

		// Heartbeat goroutine is running — Stop must not hang.
		stopWithTimeout(t, m, 5*time.Second)
	})
}

// ---------------------------------------------------------------------------
// Start — file JWT (license.jwt)
// ---------------------------------------------------------------------------

func TestManager_Start_FileJWT(t *testing.T) {
	// Subtests use t.Setenv so the parent must not call t.Parallel.

	t.Run("license.jwt file exists updates state without heartbeat", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		dir := t.TempDir()
		jwtPath := filepath.Join(dir, "license.jwt")
		require.NoError(t, os.WriteFile(jwtPath, []byte(token), 0600))

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: dir}, pub, nil, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err)

		checker := m.Checker()
		assert.False(t, checker.IsCommunity())
		assert.Equal(t, "pro", checker.Plan())

		// SourceFileJWT does not start a heartbeat — Stop must be immediate.
		stopWithTimeout(t, m, 2*time.Second)
	})
}

// ---------------------------------------------------------------------------
// Start — discovery error
// ---------------------------------------------------------------------------

func TestManager_Start_DiscoveryError(t *testing.T) {
	// Subtests use t.Setenv so the parent must not call t.Parallel.

	t.Run("store load error gracefully degrades to community mode", func(t *testing.T) {
		pub, _ := testKeyPair(t)

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		store := &mockActivationStore{loadErr: errors.New("disk read failure")}
		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, store, slog.Default())

		err := m.Start(context.Background())
		require.NoError(t, err, "discovery error must not be returned; manager degrades gracefully")

		assert.True(t, m.Checker().IsCommunity())
	})
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

func TestManager_Stop(t *testing.T) {
	t.Run("Stop without heartbeat does not hang", func(t *testing.T) {
		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		pub, _ := testKeyPair(t)
		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())

		stopWithTimeout(t, m, 2*time.Second)
	})

	t.Run("Stop with active heartbeat goroutine terminates cleanly", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "lic-key-for-stop-test")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		require.NoError(t, m.Start(context.Background()))

		stopWithTimeout(t, m, 5*time.Second)
	})
}

// ---------------------------------------------------------------------------
// ActivateWithKey
// ---------------------------------------------------------------------------

func TestManager_ActivateWithKey(t *testing.T) {
	t.Parallel()

	t.Run("success returns correct plan features and expiry", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		result, err := m.ActivateWithKey(context.Background(), "my-license-key")
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, "pro", result.Plan)
		assert.Equal(t, []string{FeatureAudit, FeatureRBAC, FeatureSSO}, result.Features)
		assert.True(t, result.Expiry.After(time.Now()), "expiry should be in the future")

		checker := m.Checker()
		assert.False(t, checker.IsCommunity())
		assert.Equal(t, "pro", checker.Plan())

		stopWithTimeout(t, m, 5*time.Second)
	})

	t.Run("activation HTTP 500 returns error", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: errorHandlerFn(http.StatusInternalServerError, "server blew up"),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		result, err := m.ActivateWithKey(context.Background(), "bad-key")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "activation request failed")
	})

	t.Run("token signed with wrong key returns verification error", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)       // manager's public key
		_, wrongPriv := testKeyPair(t) // private key NOT corresponding to pub
		claims := validClaims()
		token := signToken(t, wrongPriv, claims) // signed with the wrong key

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		result, err := m.ActivateWithKey(context.Background(), "my-key")
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "activated token verification failed")
	})

	t.Run("nil ExpiresAt in claims yields zero Expiry", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		// Build claims without ExpiresAt.
		claims := &LicenseClaims{
			ClaimsVersion: 1,
			Plan:          "pro",
			Features:      []string{FeatureAudit},
			ActivationID:  "act-no-expiry",
		}
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		result, err := m.ActivateWithKey(context.Background(), "key-no-expiry")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.Expiry.IsZero(), "Expiry must be zero when ExpiresAt is nil in claims")

		stopWithTimeout(t, m, 5*time.Second)
	})
}

// ---------------------------------------------------------------------------
// doHeartbeat (exercised directly)
// ---------------------------------------------------------------------------

func TestManager_doHeartbeat(t *testing.T) {
	t.Parallel()

	t.Run("success updates state and store with refreshed token", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		initialClaims := validClaims()
		refreshedToken := signToken(t, priv, validClaims())

		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: heartbeatHandlerFn(refreshedToken),
		})

		store := &mockActivationStore{}
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())
		m.state.Update(initialClaims, "initial-token")

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		checker := m.Checker()
		assert.False(t, checker.IsCommunity())
		assert.Equal(t, "pro", checker.Plan())
		assert.Equal(t, 1, store.saveCalls)
		assert.Equal(t, refreshedToken, store.data.Token)
	})

	t.Run("nil claims is a no-op — no HTTP call is made", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		callCount := 0
		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: func(w http.ResponseWriter, _ *http.Request) {
				callCount++
				w.WriteHeader(http.StatusOK)
			},
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())
		// state.claims is nil — community mode, no ActivationID to send

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		assert.Equal(t, 0, callCount, "heartbeat HTTP call must not be made when claims are nil")
		assert.True(t, m.Checker().IsCommunity())
	})

	t.Run("410 Gone clears in-memory state but does not remove stored activation", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		claims := validClaims()

		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: errorHandlerFn(http.StatusGone, "license revoked"),
		})

		store := &mockActivationStore{}
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())
		m.state.Update(claims, "active-token")

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		assert.True(t, m.Checker().IsCommunity(), "in-memory state must be cleared after 410 Gone")
		assert.Equal(t, 0, store.removeCalls, "stored activation must NOT be removed by heartbeat failure")
	})

	t.Run("401 Unauthorized clears in-memory state but does not remove stored activation", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: errorHandlerFn(http.StatusUnauthorized, "unauthorized"),
		})

		store := &mockActivationStore{}
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())
		m.state.Update(claims, token)

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		assert.True(t, m.Checker().IsCommunity(), "in-memory state must be cleared after 401")
		assert.Equal(t, 0, store.removeCalls, "stored activation must NOT be removed by heartbeat failure")
	})

	t.Run("network error leaves state unchanged with cached token", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		// Start and immediately close the server to produce connection-refused errors.
		deadSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
		deadSrv.Close()

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   deadSrv.URL,
		}, pub, nil, slog.Default())
		m.state.Update(claims, token)

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		// Cached token must still be in effect.
		assert.False(t, m.Checker().IsCommunity())
		assert.Equal(t, "pro", m.Checker().Plan())
	})

	t.Run("400 Bad Request keeps expired token state for local enforcement", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := expiredInGraceClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: errorHandlerFn(http.StatusBadRequest, "license has expired"),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())
		m.state.Update(claims, token)

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		assert.False(t, m.Checker().IsCommunity())
		assert.Equal(t, "pro", m.Checker().Plan())
		assert.True(t, m.Checker().IsGracePeriod())
	})

	t.Run("invalid refreshed token leaves state unchanged", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		_, wrongPriv := testKeyPair(t)

		initialClaims := validClaims()
		initialToken := signToken(t, priv, initialClaims)

		// Server returns a token signed with the wrong key.
		badToken := signToken(t, wrongPriv, validClaims())
		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: heartbeatHandlerFn(badToken),
		})

		store := &mockActivationStore{}
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())
		m.state.Update(initialClaims, initialToken)

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		assert.False(t, m.Checker().IsCommunity(), "state must not change after failed token verification")
		assert.Equal(t, "pro", m.Checker().Plan())
		assert.Equal(t, 0, store.saveCalls, "store must not be saved with an invalid token")
	})

	t.Run("store save error still updates state (best-effort save)", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		initialClaims := validClaims()
		refreshedToken := signToken(t, priv, validClaims())

		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: heartbeatHandlerFn(refreshedToken),
		})

		store := &mockActivationStore{saveErr: errors.New("disk full")}
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())
		m.state.Update(initialClaims, "initial-token")

		m.doHeartbeat(context.Background(), makeAD("server-001"))

		// State should be updated even though persistence failed.
		assert.False(t, m.Checker().IsCommunity())
		assert.Equal(t, "pro", m.Checker().Plan())
		assert.Equal(t, 1, store.saveCalls, "save must have been attempted")
	})

	t.Run("nil store does not panic when attempting to persist", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		refreshedToken := signToken(t, priv, validClaims())

		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: heartbeatHandlerFn(refreshedToken),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default()) // nil store
		m.state.Update(claims, "initial-token")

		require.NotPanics(t, func() {
			m.doHeartbeat(context.Background(), makeAD("server-001"))
		})

		assert.False(t, m.Checker().IsCommunity())
	})
}

// ---------------------------------------------------------------------------
// startHeartbeat — idempotency
// ---------------------------------------------------------------------------

func TestManager_startHeartbeat_Idempotent(t *testing.T) {
	t.Parallel()

	t.Run("calling startHeartbeat twice starts only one goroutine", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())

		ad := &ActivationData{
			Token:           "tok",
			HeartbeatSecret: "secret",
			LicenseKey:      "key",
			ServerID:        "srv",
		}

		m.startHeartbeat(ad)
		m.startHeartbeat(ad) // second call must be a no-op via heartbeatOnce

		// If two goroutines were started the wg counter would be 2.
		// Stop cancels the context and waits — an incorrect count would deadlock.
		stopWithTimeout(t, m, 5*time.Second)
	})
}

func TestManager_heartbeatLoop_immediate(t *testing.T) {
	t.Parallel()

	t.Run("heartbeat fires immediately on startup before ticker", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)
		refreshedToken := signToken(t, priv, validClaims())

		var callCount atomic.Int32
		handler := heartbeatHandlerFn(refreshedToken)
		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: func(w http.ResponseWriter, _ *http.Request) {
				callCount.Add(1)
				handler(w, nil)
			},
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())
		m.state.Update(claims, token)

		m.startHeartbeat(makeAD("server-001"))

		// Give the goroutine time to execute the immediate heartbeat.
		require.Eventually(t, func() bool {
			return callCount.Load() >= 1
		}, 2*time.Second, 50*time.Millisecond,
			"immediate heartbeat must fire on startup")

		assert.Equal(t, int32(1), callCount.Load(),
			"exactly one heartbeat call expected (immediate); ticker has not fired yet")

		stopWithTimeout(t, m, 5*time.Second)
	})
}

// ---------------------------------------------------------------------------
// activate — edge cases tested through ActivateWithKey
// ---------------------------------------------------------------------------

func TestManager_activate_StoreEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("store save error during activate still returns success (best-effort save)", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		store := &mockActivationStore{saveErr: errors.New("write failed")}
		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, store, slog.Default())

		result, err := m.ActivateWithKey(context.Background(), "my-key")
		require.NoError(t, err, "activation must succeed even if persistence fails")
		require.NotNil(t, result)
		assert.Equal(t, "pro", result.Plan)

		stopWithTimeout(t, m, 5*time.Second)
	})

	t.Run("nil store during activate does not panic", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		claims := validClaims()
		token := signToken(t, priv, claims)

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default()) // nil store

		var result *ActivationResult
		var err error
		require.NotPanics(t, func() {
			result, err = m.ActivateWithKey(context.Background(), "my-key")
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "pro", result.Plan)

		stopWithTimeout(t, m, 5*time.Second)
	})
}

// ---------------------------------------------------------------------------
// Source
// ---------------------------------------------------------------------------

func TestManager_Source(t *testing.T) {
	t.Run("returns SourceEnvInline after Start with DAGU_LICENSE", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		t.Setenv("DAGU_LICENSE", token)
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		assert.Equal(t, SourceEnvInline, m.Source())
	})

	t.Run("returns SourceActivationFile after Start with store data", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		store := &mockActivationStore{data: &ActivationData{
			Token:           token,
			HeartbeatSecret: "hb",
			LicenseKey:      "key",
			ServerID:        "srv",
		}}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   "http://127.0.0.1:0",
		}, pub, store, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		assert.Equal(t, SourceActivationFile, m.Source())

		stopWithTimeout(t, m, 5*time.Second)
	})

	t.Run("returns SourceNone when no license configured", func(t *testing.T) {
		pub, _ := testKeyPair(t)

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		assert.Equal(t, SourceNone, m.Source())
	})

	t.Run("returns SourceActivationFile after ActivateWithKey", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		srv := newMockCloudServer(t, mockCloudServerConfig{
			activateHandler: activateHandlerFn(token, "hb-secret"),
		})

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   srv.URL,
		}, pub, nil, slog.Default())

		_, err := m.ActivateWithKey(context.Background(), "my-key")
		require.NoError(t, err)

		assert.Equal(t, SourceActivationFile, m.Source())

		stopWithTimeout(t, m, 5*time.Second)
	})
}

// ---------------------------------------------------------------------------
// Deactivate
// ---------------------------------------------------------------------------

func TestManager_Deactivate(t *testing.T) {
	t.Run("success clears state removes file and stops heartbeat", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		store := &mockActivationStore{data: &ActivationData{
			Token:           token,
			HeartbeatSecret: "hb",
			LicenseKey:      "key",
			ServerID:        "srv",
		}}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   "http://127.0.0.1:0",
		}, pub, store, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		assert.False(t, m.Checker().IsCommunity())

		err := m.Deactivate(context.Background())
		require.NoError(t, err)

		assert.True(t, m.Checker().IsCommunity(), "state must be cleared after deactivate")
		assert.Equal(t, SourceNone, m.Source(), "source must be reset to SourceNone")
		assert.Equal(t, 1, store.removeCalls, "store.Remove must be called")
	})

	t.Run("error when source is env var", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		t.Setenv("DAGU_LICENSE", token)
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		assert.False(t, m.Checker().IsCommunity())

		err := m.Deactivate(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "environment variable")
		assert.False(t, m.Checker().IsCommunity(), "state must not be cleared on env deactivate error")
	})

	t.Run("error when no active license", func(t *testing.T) {
		pub, _ := testKeyPair(t)

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{LicenseDir: t.TempDir()}, pub, nil, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		err := m.Deactivate(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no active license")
	})

	t.Run("idempotent second call returns no active license error", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		store := &mockActivationStore{data: &ActivationData{
			Token:           token,
			HeartbeatSecret: "hb",
			LicenseKey:      "key",
			ServerID:        "srv",
		}}

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		m := NewManager(ManagerConfig{
			LicenseDir: t.TempDir(),
			CloudURL:   "http://127.0.0.1:0",
		}, pub, store, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		err := m.Deactivate(context.Background())
		require.NoError(t, err)

		// Second call should return "no active license" error
		err = m.Deactivate(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no active license")
	})

	t.Run("nil store does not cause error", func(t *testing.T) {
		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())

		t.Setenv("DAGU_LICENSE", "")
		t.Setenv("DAGU_LICENSE_KEY", "")
		t.Setenv("DAGU_LICENSE_FILE", "")

		dir := t.TempDir()
		jwtPath := filepath.Join(dir, "license.jwt")
		require.NoError(t, os.WriteFile(jwtPath, []byte(token), 0600))

		m := NewManager(ManagerConfig{LicenseDir: dir}, pub, nil, slog.Default())
		require.NoError(t, m.Start(context.Background()))

		assert.False(t, m.Checker().IsCommunity())

		err := m.Deactivate(context.Background())
		require.NoError(t, err)
		assert.True(t, m.Checker().IsCommunity())
	})
}
