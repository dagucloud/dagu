// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Connection tests share the process environment, so none of them run in
// parallel.

func clearLicenseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DAGU_LICENSE", "")
	t.Setenv("DAGU_LICENSE_KEY", "")
	t.Setenv("DAGU_LICENSE_FILE", "")
}

// connectAnswers serves connection polls: pending until grant is closed,
// then the given response.
func connectAnswers(polls *atomic.Int32, grant <-chan struct{}, answer http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		polls.Add(1)
		select {
		case <-grant:
			answer(w, r)
		default:
			_, _ = w.Write([]byte(`{"status":"pending"}`))
		}
	}
}

func grantWith(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ConnectResponse{Status: connectResponseGranted, Token: token, HeartbeatSecret: "hb"})
	}
}

// newConnectManager returns a manager that polls every 10ms, a store, and
// the key its fake console signs licenses with.
func newConnectManager(t *testing.T, cfg mockCloudServerConfig, mutate func(*ManagerConfig)) (*Manager, *mockActivationStore, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := testKeyPair(t)
	srv := newMockCloudServer(t, cfg)
	mc := ManagerConfig{LicenseDir: t.TempDir(), CloudURL: srv.URL, ServerName: "build-01"}
	if mutate != nil {
		mutate(&mc)
	}
	store := &mockActivationStore{}
	m := NewManager(mc, pub, store, slog.Default())
	m.connectPoll = 10 * time.Millisecond
	t.Cleanup(func() { stopWithTimeout(t, m, 5*time.Second) })
	return m, store, priv
}

func waitConnectState(t *testing.T, m *Manager, want ConnectState) ConnectStatus {
	t.Helper()
	require.Eventually(t, func() bool { return m.ConnectStatus().State == want }, 5*time.Second, 5*time.Millisecond,
		"connection request never reached %q (last %+v)", want, m.ConnectStatus())
	return m.ConnectStatus()
}

func TestConnectionCode(t *testing.T) {
	// Dagu Console derives the same code from the secret it receives.
	assert.Equal(t, "271a413bd339c5709fdceaec41f14f11e9fbfb5042d72d331c65f32b284cd09a",
		connectionCode(strings.Repeat("ab", 32)))
}

func TestManager_Connect(t *testing.T) {
	t.Run("approval installs the license from Dagu Console", func(t *testing.T) {
		clearLicenseEnv(t)
		var polls atomic.Int32
		grant := make(chan struct{})
		var token string
		var request ConnectRequest
		m, store, priv := newConnectManager(t, mockCloudServerConfig{
			connectHandler: connectAnswers(&polls, grant, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&request)
				grantWith(token)(w, r)
			}),
			heartbeatHandler: func(w http.ResponseWriter, r *http.Request) { heartbeatHandlerFn(token)(w, r) },
		}, nil)
		claims := validClaims()
		claims.Workspace = "acme"
		token = signToken(t, priv, claims)

		status, err := m.Connect()
		require.NoError(t, err)
		require.Equal(t, ConnectPending, status.State)

		link, err := url.Parse(status.URL)
		require.NoError(t, err)
		assert.Equal(t, "/servers/connect", link.Path)
		assert.Equal(t, "build-01", link.Query().Get("name"))
		code := link.Query().Get("code")
		assert.Len(t, code, 64)
		assert.Equal(t, strings.ToUpper(code[:8]), status.Code)
		assert.WithinDuration(t, time.Now().Add(connectTTL), status.ExpiresAt, time.Minute)

		again, err := m.Connect()
		require.NoError(t, err)
		assert.Equal(t, status.URL, again.URL, "a pending request is resumed, not replaced")

		require.Eventually(t, func() bool { return polls.Load() > 0 }, 5*time.Second, 5*time.Millisecond)
		assert.False(t, HasActiveLicense(m.Checker()), "nothing is installed before approval")
		close(grant)
		waitConnectState(t, m, ConnectGranted)

		assert.Equal(t, connectionCode(request.ConnectionSecret), code)
		assert.Equal(t, link.Query().Get("server_id"), request.ServerID)
		assert.Equal(t, "build-01", request.ServerName)
		licensed := m.Status()
		assert.True(t, licensed.Valid)
		assert.Equal(t, ConnectedViaConsole, licensed.ConnectedVia)
		assert.Equal(t, "acme", licensed.Workspace)
		assert.Equal(t, request.ServerID, licensed.ServerID)
		saved, err := store.Load()
		require.NoError(t, err)
		require.NotNil(t, saved)
		assert.Equal(t, ConnectedViaConsole, saved.Via)
		assert.Empty(t, saved.LicenseKey)

		_, err = m.Connect()
		assert.ErrorIs(t, err, ErrAlreadyLicensed)
	})

	t.Run("console rejection ends the request with its reason", func(t *testing.T) {
		clearLicenseEnv(t)
		m, _, _ := newConnectManager(t, mockCloudServerConfig{
			connectHandler: errorHandlerFn(http.StatusBadRequest, "All 3 servers are in use."),
		}, nil)

		_, err := m.Connect()
		require.NoError(t, err)

		assert.Equal(t, "All 3 servers are in use.", waitConnectState(t, m, ConnectFailed).Error)
		assert.True(t, m.Checker().IsCommunity())
	})

	t.Run("older console", func(t *testing.T) {
		clearLicenseEnv(t)
		m, _, _ := newConnectManager(t, mockCloudServerConfig{}, nil)

		_, err := m.Connect()
		require.NoError(t, err)

		assert.Equal(t, connectUnsupportedFailure, waitConnectState(t, m, ConnectFailed).Error)
	})

	t.Run("console outages are retried", func(t *testing.T) {
		clearLicenseEnv(t)
		var polls atomic.Int32
		var token string
		m, _, priv := newConnectManager(t, mockCloudServerConfig{
			connectHandler: func(w http.ResponseWriter, r *http.Request) {
				if polls.Add(1) < 3 {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				grantWith(token)(w, r)
			},
			heartbeatHandler: func(w http.ResponseWriter, r *http.Request) { heartbeatHandlerFn(token)(w, r) },
		}, nil)
		token = signToken(t, priv, validClaims())

		_, err := m.Connect()
		require.NoError(t, err)

		waitConnectState(t, m, ConnectGranted)
		assert.GreaterOrEqual(t, polls.Load(), int32(3))
	})

	t.Run("a license that does not verify is not installed", func(t *testing.T) {
		clearLicenseEnv(t)
		_, wrongKey := testKeyPair(t)
		forged := signToken(t, wrongKey, validClaims())
		m, store, _ := newConnectManager(t, mockCloudServerConfig{connectHandler: grantWith(forged)}, nil)

		_, err := m.Connect()
		require.NoError(t, err)

		assert.Equal(t, connectVerificationFailure, waitConnectState(t, m, ConnectFailed).Error)
		assert.True(t, m.Checker().IsCommunity())
		saved, err := store.Load()
		require.NoError(t, err)
		assert.Nil(t, saved)
	})

	t.Run("unapproved request expires", func(t *testing.T) {
		clearLicenseEnv(t)
		m, _, _ := newConnectManager(t, mockCloudServerConfig{
			connectHandler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"pending"}`)) },
		}, nil)
		m.connectTTL = 100 * time.Millisecond

		_, err := m.Connect()
		require.NoError(t, err)

		assert.Equal(t, connectExpiredFailure, waitConnectState(t, m, ConnectExpired).Error)

		next, err := m.Connect()
		require.NoError(t, err)
		assert.Equal(t, ConnectPending, next.State, "an expired request can be restarted")
	})

	t.Run("cancel stops polling", func(t *testing.T) {
		clearLicenseEnv(t)
		var polls atomic.Int32
		m, _, _ := newConnectManager(t, mockCloudServerConfig{
			connectHandler: connectAnswers(&polls, make(chan struct{}), nil),
		}, nil)

		_, err := m.Connect()
		require.NoError(t, err)
		require.Eventually(t, func() bool { return polls.Load() > 0 }, 5*time.Second, 5*time.Millisecond)

		m.CancelConnect()
		assert.Equal(t, ConnectIdle, m.ConnectStatus().State)
		stopped := polls.Load()
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, stopped, polls.Load())
	})

	t.Run("key activation cancels a pending request", func(t *testing.T) {
		clearLicenseEnv(t)
		var token string
		m, _, priv := newConnectManager(t, mockCloudServerConfig{
			connectHandler:   connectAnswers(new(atomic.Int32), make(chan struct{}), nil),
			activateHandler:  func(w http.ResponseWriter, r *http.Request) { activateHandlerFn(token, "hb")(w, r) },
			heartbeatHandler: func(w http.ResponseWriter, r *http.Request) { heartbeatHandlerFn(token)(w, r) },
		}, nil)
		token = signToken(t, priv, validClaims())

		_, err := m.Connect()
		require.NoError(t, err)
		_, err = m.ActivateWithKey(context.Background(), "key")
		require.NoError(t, err)

		assert.Equal(t, ConnectIdle, m.ConnectStatus().State)
		assert.Equal(t, ConnectedViaKey, m.Status().ConnectedVia)
	})

	t.Run("licenses set outside Dagu cannot be replaced", func(t *testing.T) {
		clearLicenseEnv(t)
		m, _, _ := newConnectManager(t, mockCloudServerConfig{}, func(c *ManagerConfig) { c.ConfigKey = "DAGU-KEY" })

		_, err := m.Connect()
		assert.ErrorIs(t, err, ErrManagedExternally)

		t.Setenv("DAGU_LICENSE_KEY", "DAGU-ENV")
		other, _, _ := newConnectManager(t, mockCloudServerConfig{}, nil)
		_, err = other.Connect()
		assert.ErrorIs(t, err, ErrManagedExternally)
	})

	t.Run("stop ends a pending request", func(t *testing.T) {
		clearLicenseEnv(t)
		m, _, _ := newConnectManager(t, mockCloudServerConfig{
			connectHandler: connectAnswers(new(atomic.Int32), make(chan struct{}), nil),
		}, nil)

		_, err := m.Connect()
		require.NoError(t, err)

		stopWithTimeout(t, m, 5*time.Second)
		assert.Equal(t, ConnectIdle, m.ConnectStatus().State)
	})
}

func TestManager_Refresh(t *testing.T) {
	t.Parallel()

	t.Run("checks in now", func(t *testing.T) {
		t.Parallel()

		pub, priv := testKeyPair(t)
		token := signToken(t, priv, validClaims())
		var heartbeats atomic.Int32
		srv := newMockCloudServer(t, mockCloudServerConfig{
			heartbeatHandler: func(w http.ResponseWriter, r *http.Request) {
				heartbeats.Add(1)
				heartbeatHandlerFn(token)(w, r)
			},
		})
		m := NewManager(ManagerConfig{LicenseDir: t.TempDir(), CloudURL: srv.URL}, pub, nil, nil)
		m.state.Update(validClaims(), token)
		m.setSource(SourceActivationFile)
		m.setActivation(makeAD("srv"))

		require.NoError(t, m.Refresh(context.Background()))

		assert.Equal(t, int32(1), heartbeats.Load())
		assert.WithinDuration(t, time.Now(), m.Status().LastCheckIn, time.Minute)
	})

	t.Run("unreachable console is an error", func(t *testing.T) {
		t.Parallel()

		pub, _ := testKeyPair(t)
		m := NewManager(ManagerConfig{LicenseDir: t.TempDir(), CloudURL: "http://127.0.0.1:0"}, pub, nil, nil)
		m.state.Update(validClaims(), "token")
		m.setSource(SourceActivationFile)
		m.setActivation(makeAD("srv"))

		require.Error(t, m.Refresh(context.Background()))
		assert.False(t, m.Checker().IsCommunity(), "the cached license stays in effect")
	})

	t.Run("licenses that never check in", func(t *testing.T) {
		t.Parallel()

		m := NewTestManager(FeatureAudit)
		m.setSource(SourceFileJWT)

		assert.ErrorIs(t, m.Refresh(context.Background()), ErrNoCheckIn)
	})
}
