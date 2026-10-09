// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLicenseStatus(t *testing.T) {
	t.Parallel()

	t.Run("community status is available without a license manager", func(t *testing.T) {
		t.Parallel()

		server := test.SetupServer(t)
		resp := server.Client().Get("/api/v1/license/status").ExpectStatus(http.StatusOK).Send(t)

		var status api.LicenseStatusResponse
		resp.Unmarshal(t, &status)
		assert.True(t, status.Community)
		assert.False(t, status.Valid)
		assert.Empty(t, status.Features)
		assert.Empty(t, status.Error)
	})

	t.Run("active license exposes its public status", func(t *testing.T) {
		t.Parallel()

		server := test.SetupServer(t, test.WithServerOptions(
			frontend.WithLicenseManager(defaultTestLicenseManager()),
		))
		resp := server.Client().Get("/api/v1/license/status").ExpectStatus(http.StatusOK).Send(t)

		var status api.LicenseStatusResponse
		resp.Unmarshal(t, &status)
		assert.False(t, status.Community)
		assert.True(t, status.Valid)
		assert.Equal(t, "pro", status.Plan)
		assert.ElementsMatch(t, []string{"rbac", "audit"}, status.Features)
		assert.Empty(t, status.Error)
		require.NotNil(t, status.ServerName)
		assert.NotEmpty(t, *status.ServerName)
		assert.Nil(t, status.ServerId, "a license that never checks in has no server ID")
	})
}

// newCloudLicenseManager returns a license manager that talks to a fake Dagu
// Console served by handler, and the key the console signs licenses with.
func newCloudLicenseManager(t *testing.T, handler http.Handler) (*license.Manager, ed25519.PrivateKey) {
	t.Helper()
	cloud := httptest.NewServer(handler)
	t.Cleanup(cloud.Close)
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	m := license.NewManager(license.ManagerConfig{
		LicenseDir: t.TempDir(),
		CloudURL:   cloud.URL,
	}, pub, nil, nil)
	t.Cleanup(m.Stop)
	return m, priv
}

// signTestLicense signs a one-day Pro license with priv.
func signTestLicense(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, &license.LicenseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "lic-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		},
		Plan:     "pro",
		Features: []string{license.FeatureAudit},
	}).SignedString(priv)
	require.NoError(t, err)
	return token
}

func TestActivateLicense_CloudRejection(t *testing.T) {
	t.Parallel()

	t.Run("shows Dagu Console's reason", func(t *testing.T) {
		t.Parallel()

		manager, _ := newCloudLicenseManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "All 3 servers in workspace \"acme\" are in use."})
		}))
		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))

		resp := server.Client().Post("/api/v1/license/activate", map[string]string{
			"key": "DAGU-TEST-0000-0000-0000",
		}).ExpectStatus(http.StatusBadRequest).Send(t)

		var errResp api.Error
		resp.Unmarshal(t, &errResp)
		assert.Equal(t, `All 3 servers in workspace "acme" are in use.`, errResp.Message)
	})

	t.Run("unknown key", func(t *testing.T) {
		t.Parallel()

		for body, want := range map[string]string{
			`{"error":"No license matches this key.","message":"No license matches this key."}`: "No license matches this key.",
			``: "Dagu Console does not recognize this license key.",
		} {
			manager, _ := newCloudLicenseManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(body))
			}))
			server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))

			resp := server.Client().Post("/api/v1/license/activate", map[string]string{
				"key": "DAGU-TEST-0000-0000-0000",
			}).ExpectStatus(http.StatusBadRequest).Send(t)

			var errResp api.Error
			resp.Unmarshal(t, &errResp)
			assert.Equal(t, want, errResp.Message)
		}
	})

	t.Run("server errors stay generic", func(t *testing.T) {
		t.Parallel()

		manager, _ := newCloudLicenseManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<html>upstream failure</html>"))
		}))
		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))

		resp := server.Client().Post("/api/v1/license/activate", map[string]string{
			"key": "DAGU-TEST-0000-0000-0000",
		}).ExpectStatus(http.StatusBadRequest).Send(t)

		var errResp api.Error
		resp.Unmarshal(t, &errResp)
		assert.Equal(t, "License activation failed. Please verify your license key and try again.", errResp.Message)
	})
}

// pendingConsole answers every connection poll with "pending".
func pendingConsole() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	})
}

// Uses t.Setenv because a license set in the environment blocks connecting.
func TestLicenseConnect(t *testing.T) {
	t.Setenv("DAGU_LICENSE", "")
	t.Setenv("DAGU_LICENSE_KEY", "")

	manager, _ := newCloudLicenseManager(t, pendingConsole())
	server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))

	var started api.LicenseConnectStatus
	server.Client().Post("/api/v1/license/connect", nil).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)
	assert.Equal(t, api.LicenseConnectStatusStatePending, started.State)
	require.NotNil(t, started.ConnectUrl)
	assert.Contains(t, *started.ConnectUrl, "/servers/connect?")
	require.NotNil(t, started.Code)
	assert.Len(t, *started.Code, 8)
	assert.NotNil(t, started.ExpiresAt)

	var current api.LicenseConnectStatus
	server.Client().Get("/api/v1/license/connect").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &current)
	assert.Equal(t, started, current)

	var cancelled api.LicenseConnectStatus
	server.Client().Delete("/api/v1/license/connect").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &cancelled)
	assert.Equal(t, api.LicenseConnectStatusStateIdle, cancelled.State)
	assert.Nil(t, cancelled.ConnectUrl)
}

func TestLicenseConnect_Conflicts(t *testing.T) {
	t.Parallel()

	t.Run("license key in the config", func(t *testing.T) {
		t.Parallel()

		pub, _, err := ed25519.GenerateKey(nil)
		require.NoError(t, err)
		manager := license.NewManager(license.ManagerConfig{LicenseDir: t.TempDir(), ConfigKey: "DAGU-KEY"}, pub, nil, nil)
		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))

		server.Client().Post("/api/v1/license/connect", nil).ExpectStatus(http.StatusConflict).Send(t)
	})

	t.Run("already licensed", func(t *testing.T) {
		t.Parallel()

		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(defaultTestLicenseManager())))

		server.Client().Post("/api/v1/license/connect", nil).ExpectStatus(http.StatusConflict).Send(t)
	})
}

func TestRefreshLicense(t *testing.T) {
	t.Parallel()

	t.Run("checks in and returns the status", func(t *testing.T) {
		t.Parallel()

		var token string
		manager, priv := newCloudLicenseManager(t, fakeConsole(&token, http.StatusOK))
		token = signTestLicense(t, priv)
		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))
		server.Client().Post("/api/v1/license/activate", map[string]string{"key": "DAGU-TEST"}).
			ExpectStatus(http.StatusOK).Send(t)

		var status api.LicenseStatusResponse
		server.Client().Post("/api/v1/license/refresh", nil).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &status)

		assert.True(t, status.Valid)
		require.NotNil(t, status.LastCheckIn)
		require.NotNil(t, status.ConnectedVia)
		assert.Equal(t, api.LicenseStatusResponseConnectedViaKey, *status.ConnectedVia)
	})

	t.Run("license that never checks in", func(t *testing.T) {
		t.Parallel()

		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(defaultTestLicenseManager())))

		server.Client().Post("/api/v1/license/refresh", nil).ExpectStatus(http.StatusBadRequest).Send(t)
	})
}

func TestLicenseEndpoints_RequireAdmin(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-license-endpoints"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		}),
		test.WithServerOptions(frontend.WithLicenseManager(defaultTestLicenseManager())),
	)
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{Username: "admin", Password: "adminpass"}).
		ExpectStatus(http.StatusOK).Send(t)
	adminToken := getAdminToken(t, server)
	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "viewer-connect",
		Password: "viewerpass1",
		Role:     api.UserRoleViewer,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	var viewer api.LoginResponse
	server.Client().Post("/api/v1/auth/login", api.LoginRequest{Username: "viewer-connect", Password: "viewerpass1"}).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &viewer)

	client := server.Client()
	for _, req := range []*test.Request{
		client.Post("/api/v1/license/connect", nil),
		client.Get("/api/v1/license/connect"),
		client.Delete("/api/v1/license/connect"),
		client.Post("/api/v1/license/refresh", nil),
	} {
		req.WithBearerToken(viewer.Token).ExpectStatus(http.StatusForbidden).Send(t)
	}
}

// TestActivateLicense_NoLicenseManager verifies that when no license manager is
// configured (the default in tests), the endpoint returns 400 with the "License
// management is not available" message, even when a valid-looking key is supplied.
func TestActivateLicense_NoLicenseManager(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)

	resp := server.Client().Post("/api/v1/license/activate", map[string]string{
		"key": "DAGU-TEST-0000-0000-0000",
	}).ExpectStatus(http.StatusBadRequest).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
	assert.Contains(t, errResp.Message, "License management is not available")
}

// TestActivateLicense_EmptyKey verifies that when no license manager is configured,
// an empty key still returns 400 — the nil manager guard fires before the key check.
func TestActivateLicense_EmptyKey(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)

	resp := server.Client().Post("/api/v1/license/activate", map[string]string{
		"key": "",
	}).ExpectStatus(http.StatusBadRequest).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
	// The nil manager check fires first, before the key-empty check.
	assert.NotEmpty(t, errResp.Message)
}

// TestActivateLicense_MissingKeyField verifies that sending a JSON object without
// the "key" field (an empty object) results in 400. Because the nil manager check
// runs first, the response message is "License management is not available" rather
// than "License key is required".
func TestActivateLicense_MissingKeyField(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)

	// Send an empty JSON object — the "key" field will be decoded as an empty
	// string, reaching the handler logic rather than triggering a decode error.
	resp := server.Client().Post("/api/v1/license/activate", map[string]string{}).
		ExpectStatus(http.StatusBadRequest).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
	assert.NotEmpty(t, errResp.Message)
}

// TestActivateLicense_RequiresAuth_BasicMode verifies that a server configured
// with HTTP Basic auth rejects unauthenticated requests to the license endpoint
// before any handler logic runs.
func TestActivateLicense_RequiresAuth_BasicMode(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBasic
		cfg.Server.Auth.Basic.Username = "admin"
		cfg.Server.Auth.Basic.Password = "secret"
	}))

	// No credentials at all — must be rejected by auth middleware.
	server.Client().Post("/api/v1/license/activate", map[string]string{
		"key": "DAGU-TEST-0000-0000-0000",
	}).ExpectStatus(http.StatusUnauthorized).Send(t)
}

// TestActivateLicense_ValidBasicAuth verifies that when Basic auth is satisfied
// the request reaches handler logic and (in the absence of a license manager)
// returns 400 "License management is not available", not an auth error.
func TestActivateLicense_ValidBasicAuth(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBasic
		cfg.Server.Auth.Basic.Username = "admin"
		cfg.Server.Auth.Basic.Password = "secret"
	}))

	resp := server.Client().Post("/api/v1/license/activate", map[string]string{
		"key": "DAGU-TEST-0000-0000-0000",
	}).WithBasicAuth("admin", "secret").
		ExpectStatus(http.StatusBadRequest).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
	assert.Contains(t, errResp.Message, "License management is not available")
}

// TestActivateLicense_RequiresAuth_BuiltinMode verifies that a server configured
// with builtin JWT auth rejects unauthenticated requests to the license endpoint.
func TestActivateLicense_RequiresAuth_BuiltinMode(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBuiltin
		cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key-license-test"
		cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
	}))

	// Create admin so the server is fully initialized.
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	// No bearer token — must be rejected.
	server.Client().Post("/api/v1/license/activate", map[string]string{
		"key": "DAGU-TEST-0000-0000-0000",
	}).ExpectStatus(http.StatusUnauthorized).Send(t)
}

// TestActivateLicense_RequiresAdmin_BuiltinMode verifies that a non-admin user
// (viewer role) is forbidden from calling the license activation endpoint.
// The requireAdmin check returns 403 before any license-manager logic executes.
func TestActivateLicense_RequiresAdmin_BuiltinMode(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key-license-admin"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		}),
		test.WithServerOptions(frontend.WithLicenseManager(defaultTestLicenseManager())),
	)

	// Bootstrap admin.
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	adminToken := getAdminToken(t, server)

	// Create a viewer user.
	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "viewer-user",
		Password: "viewerpass1",
		Role:     api.UserRoleViewer,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	// Obtain a viewer token.
	viewerResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "viewer-user",
		Password: "viewerpass1",
	}).ExpectStatus(http.StatusOK).Send(t)

	var viewerLogin api.LoginResponse
	viewerResp.Unmarshal(t, &viewerLogin)
	require.NotEmpty(t, viewerLogin.Token)

	// Viewer must be forbidden from activating a license.
	server.Client().Post("/api/v1/license/activate", map[string]string{
		"key": "DAGU-TEST-0000-0000-0000",
	}).WithBearerToken(viewerLogin.Token).
		ExpectStatus(http.StatusForbidden).Send(t)
}

// TestActivateLicense_AdminToken_NoLicenseManager verifies that an authenticated
// admin user receives 400 "License management is not available" when no license
// manager has been wired into the server — confirming that auth succeeds and the
// handler's nil-manager guard fires.
func TestActivateLicense_AdminToken_NoLicenseManager(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBuiltin
		cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key-admin-noLM"
		cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
	}))

	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	adminToken := getAdminToken(t, server)

	resp := server.Client().Post("/api/v1/license/activate", map[string]string{
		"key": "DAGU-TEST-0000-0000-0000",
	}).WithBearerToken(adminToken).
		ExpectStatus(http.StatusBadRequest).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
	assert.Contains(t, errResp.Message, "License management is not available")
}

// ---------------------------------------------------------------------------
// DeactivateLicense
// ---------------------------------------------------------------------------

// fakeConsole serves activation and heartbeats with token and answers release
// requests with releaseStatus.
func fakeConsole(token *string, releaseStatus int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/licenses/activate", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": *token, "heartbeat_secret": "hb"})
	})
	mux.HandleFunc("/api/v1/licenses/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": *token})
	})
	mux.HandleFunc("/api/v1/licenses/release", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(releaseStatus)
		_, _ = w.Write([]byte(`{"status":"released"}`))
	})
	return mux
}

func TestDeactivateLicense_ReleasesConsoleSlot(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		releaseStatus int
		wantFailed    bool
	}{
		"released":       {releaseStatus: http.StatusOK},
		"older console":  {releaseStatus: http.StatusNotFound, wantFailed: true},
		"console outage": {releaseStatus: http.StatusBadGateway, wantFailed: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var token string
			manager, priv := newCloudLicenseManager(t, fakeConsole(&token, tc.releaseStatus))
			token = signTestLicense(t, priv)
			server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))
			server.Client().Post("/api/v1/license/activate", map[string]string{"key": "DAGU-TEST"}).
				ExpectStatus(http.StatusOK).Send(t)

			resp := server.Client().Post("/api/v1/license/deactivate", nil).ExpectStatus(http.StatusOK).Send(t)

			var body api.DeactivateLicense200JSONResponse
			resp.Unmarshal(t, &body)
			require.NotNil(t, body.ReleaseFailed)
			assert.Equal(t, tc.wantFailed, *body.ReleaseFailed)
		})
	}
}

// TestDeactivateLicense_NoLicenseManager verifies that when no license manager
// is configured, the deactivate endpoint returns 400.
func TestDeactivateLicense_NoLicenseManager(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)

	resp := server.Client().Post("/api/v1/license/deactivate", nil).
		ExpectStatus(http.StatusBadRequest).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
	assert.Contains(t, errResp.Message, "License management is not available")
}

// TestDeactivateLicense_RequiresAdmin_BuiltinMode verifies that a non-admin user
// is forbidden from calling the license deactivation endpoint.
func TestDeactivateLicense_RequiresAdmin_BuiltinMode(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-deactivate-admin"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		}),
		test.WithServerOptions(frontend.WithLicenseManager(defaultTestLicenseManager())),
	)

	// Bootstrap admin.
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	adminToken := getAdminToken(t, server)

	// Create a viewer user.
	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "viewer-deactivate",
		Password: "viewerpass1",
		Role:     api.UserRoleViewer,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	// Obtain a viewer token.
	viewerResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "viewer-deactivate",
		Password: "viewerpass1",
	}).ExpectStatus(http.StatusOK).Send(t)

	var viewerLogin api.LoginResponse
	viewerResp.Unmarshal(t, &viewerLogin)
	require.NotEmpty(t, viewerLogin.Token)

	// Viewer must be forbidden from deactivating a license.
	server.Client().Post("/api/v1/license/deactivate", nil).
		WithBearerToken(viewerLogin.Token).
		ExpectStatus(http.StatusForbidden).Send(t)
}

// TestDeactivateLicense_AdminToken_NoLicenseManager verifies that an authenticated
// admin user receives 400 when no license manager is configured.
func TestDeactivateLicense_AdminToken_NoLicenseManager(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBuiltin
		cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-deactivate-noLM"
		cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
	}))

	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	adminToken := getAdminToken(t, server)

	resp := server.Client().Post("/api/v1/license/deactivate", nil).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusBadRequest).Send(t)

	var errResp api.Error
	resp.Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
	assert.Contains(t, errResp.Message, "License management is not available")
}
