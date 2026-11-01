// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cloudaccess"
	"github.com/dagucloud/dagu/v2/internal/cloudreport"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
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
	return signNamedTestLicense(t, priv, "")
}

// signNamedTestLicense signs the license signTestLicense does, on which Dagu
// Console names the server serverName.
func signNamedTestLicense(t *testing.T, priv ed25519.PrivateKey, serverName string) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, &license.LicenseClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "lic-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		},
		Plan:       "pro",
		Features:   []string{license.FeatureAudit},
		ServerName: serverName,
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

// Choosing what to report when connecting starts reporting once the license
// is installed, and the approval page learns the level in effect from the
// connect URL. Uses t.Setenv because a license set in the environment blocks
// connecting.
func TestLicenseConnect_Report(t *testing.T) {
	t.Setenv("DAGU_LICENSE", "")
	t.Setenv("DAGU_LICENSE_KEY", "")

	connectServer := func(t *testing.T, manager *license.Manager, monitoring *cloudreport.Monitoring) test.Server {
		t.Helper()
		return test.SetupServer(t, test.WithServerOptions(
			frontend.WithLicenseManager(manager),
			frontend.WithAPIOption(apiv1.WithCloudReport(monitoring)),
		))
	}
	pendingManager := func(t *testing.T) *license.Manager {
		t.Helper()
		manager, _ := newCloudLicenseManager(t, pendingConsole())
		return manager
	}
	reportParam := func(t *testing.T, status api.LicenseConnectStatus) string {
		t.Helper()
		require.NotNil(t, status.ConnectUrl)
		connectURL, err := url.Parse(*status.ConnectUrl)
		require.NoError(t, err)
		return connectURL.Query().Get("report")
	}
	monitoringOf := func(t *testing.T, server test.Server) api.LicenseMonitoring {
		t.Helper()
		var monitoring api.LicenseMonitoring
		server.Client().Get("/api/v1/license/monitoring").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
		return monitoring
	}

	t.Run("chosen level", func(t *testing.T) {
		server := connectServer(t, pendingManager(t), newMonitoring(t, ""))

		var started api.LicenseConnectStatus
		server.Client().Post("/api/v1/license/connect", map[string]string{"report": "runs"}).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)

		assert.Equal(t, "runs", reportParam(t, started))
		assert.Equal(t, api.LicenseMonitoring{Level: api.LicenseMonitoringLevelRuns, Chosen: true, ErrorsOffered: true}, monitoringOf(t, server))
		var current api.LicenseConnectStatus
		server.Client().Get("/api/v1/license/connect").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &current)
		assert.Equal(t, started, current)
	})

	t.Run("configured level wins", func(t *testing.T) {
		server := connectServer(t, pendingManager(t), newMonitoring(t, config.ReportHealth))

		var started api.LicenseConnectStatus
		server.Client().Post("/api/v1/license/connect", map[string]string{"report": "runs"}).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)

		assert.Equal(t, "health", reportParam(t, started))
		assert.Equal(t, api.LicenseMonitoringLevelHealth, monitoringOf(t, server).Level)
	})

	t.Run("unknown level", func(t *testing.T) {
		server := connectServer(t, pendingManager(t), newMonitoring(t, ""))

		server.Client().Post("/api/v1/license/connect", map[string]string{"report": "everything"}).
			ExpectStatus(http.StatusBadRequest).Send(t)

		var current api.LicenseConnectStatus
		server.Client().Get("/api/v1/license/connect").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &current)
		assert.Equal(t, api.LicenseConnectStatusStateIdle, current.State)
		assert.False(t, monitoringOf(t, server).Chosen)
	})

	t.Run("already licensed", func(t *testing.T) {
		server := connectServer(t, defaultTestLicenseManager(), newMonitoring(t, ""))

		server.Client().Post("/api/v1/license/connect", map[string]string{"report": "runs"}).
			ExpectStatus(http.StatusConflict).Send(t)

		assert.Equal(t, api.LicenseMonitoring{Level: api.LicenseMonitoringLevelOff}, monitoringOf(t, server))
	})

	// A link without the level would let the approval page say that
	// nothing is reported when the server may report.
	t.Run("unreadable level", func(t *testing.T) {
		settings := filepath.Join(t.TempDir(), "report-settings.json")
		require.NoError(t, os.WriteFile(settings, []byte("{"), 0o600))
		monitoring := cloudreport.NewMonitoring("", filemonitor.NewStateStore(settings))
		server := connectServer(t, pendingManager(t), monitoring)

		server.Client().Post("/api/v1/license/connect", nil).ExpectStatus(http.StatusInternalServerError).Send(t)
		server.Client().Get("/api/v1/license/connect").ExpectStatus(http.StatusInternalServerError).Send(t)
	})

	t.Run("nothing reported", func(t *testing.T) {
		server := connectServer(t, pendingManager(t), newMonitoring(t, ""))

		var started api.LicenseConnectStatus
		server.Client().Post("/api/v1/license/connect", nil).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)

		require.NotNil(t, started.ConnectUrl)
		assert.NotContains(t, *started.ConnectUrl, "report=")
		assert.False(t, monitoringOf(t, server).Chosen)
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

// renamingConsole is a fake Dagu Console that activates servers and answers
// renames with rename, and the license manager connected to it.
func renamingConsole(t *testing.T, rename func(w http.ResponseWriter, name string, priv ed25519.PrivateKey)) *license.Manager {
	t.Helper()
	var token string
	var priv ed25519.PrivateKey
	mux := http.NewServeMux()
	mux.Handle("/api/v1/licenses/", fakeConsole(&token, http.StatusOK))
	mux.HandleFunc("/api/v1/licenses/rename", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rename(w, req.Name, priv)
	})
	manager, key := newCloudLicenseManager(t, mux)
	priv = key
	token = signTestLicense(t, priv)
	return manager
}

// renameAsConsole answers a rename as Dagu Console does: with the license
// naming the server as asked.
func renameAsConsole(t *testing.T) func(w http.ResponseWriter, name string, priv ed25519.PrivateKey) {
	return func(w http.ResponseWriter, name string, priv ed25519.PrivateKey) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": signNamedTestLicense(t, priv, name)})
	}
}

func TestRenameLicenseServer(t *testing.T) {
	t.Parallel()

	activated := func(t *testing.T, manager *license.Manager) test.Server {
		t.Helper()
		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(manager)))
		server.Client().Post("/api/v1/license/activate", map[string]string{"key": "DAGU-TEST"}).
			ExpectStatus(http.StatusOK).Send(t)
		return server
	}

	t.Run("renames the server in Dagu Console", func(t *testing.T) {
		t.Parallel()

		server := activated(t, renamingConsole(t, renameAsConsole(t)))

		var status api.LicenseStatusResponse
		server.Client().Put("/api/v1/license/server-name", map[string]string{"name": " Build box "}).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &status)

		require.NotNil(t, status.ServerName)
		assert.Equal(t, "Build box", *status.ServerName)
		require.NotNil(t, status.ReportedName)
		assert.NotEmpty(t, *status.ReportedName)
		assert.NotEqual(t, "Build box", *status.ReportedName, "the server still reports its own name")
	})

	t.Run("says why Dagu Console refused", func(t *testing.T) {
		t.Parallel()

		server := activated(t, renamingConsole(t, func(w http.ResponseWriter, _ string, _ ed25519.PrivateKey) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "license has expired"})
		}))

		resp := server.Client().Put("/api/v1/license/server-name", map[string]string{"name": "Build box"}).
			ExpectStatus(http.StatusBadRequest).Send(t)

		assert.Contains(t, resp.Body, "Dagu Console refused the name: license has expired")
	})

	t.Run("Dagu Console unreachable", func(t *testing.T) {
		t.Parallel()

		server := activated(t, renamingConsole(t, func(w http.ResponseWriter, _ string, _ ed25519.PrivateKey) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))

		server.Client().Put("/api/v1/license/server-name", map[string]string{"name": "Build box"}).
			ExpectStatus(http.StatusBadGateway).Send(t)
	})

	t.Run("name too long", func(t *testing.T) {
		t.Parallel()

		server := activated(t, renamingConsole(t, renameAsConsole(t)))

		server.Client().Put("/api/v1/license/server-name", map[string]string{"name": strings.Repeat("a", license.MaxServerNameLength+1)}).
			ExpectStatus(http.StatusBadRequest).Send(t)
	})

	t.Run("license that never checks in", func(t *testing.T) {
		t.Parallel()

		server := test.SetupServer(t, test.WithServerOptions(frontend.WithLicenseManager(defaultTestLicenseManager())))

		resp := server.Client().Put("/api/v1/license/server-name", map[string]string{"name": "Build box"}).
			ExpectStatus(http.StatusBadRequest).Send(t)

		assert.Contains(t, resp.Body, "does not check in with Dagu Console")
	})
}

func TestRenameLicenseServer_Audit(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-license-rename"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
			cfg.Server.Audit.Enabled = true
		}),
		test.WithServerOptions(frontend.WithLicenseManager(renamingConsole(t, renameAsConsole(t)))),
	)
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{Username: "admin", Password: "adminpass"}).
		ExpectStatus(http.StatusOK).Send(t)
	adminToken := getAdminToken(t, server)
	server.Client().Post("/api/v1/license/activate", map[string]string{"key": "DAGU-TEST"}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	server.Client().Put("/api/v1/license/server-name", map[string]string{"name": " Build box "}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	var logs api.AuditLogsResponse
	server.Client().Get("/api/v1/audit?category=system&action=license_server_rename").
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &logs)
	require.Len(t, logs.Entries, 1)
	entry := logs.Entries[0]
	assert.Equal(t, "admin", entry.Username)
	require.NotNil(t, entry.Details)
	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(*entry.Details), &details))
	assert.Equal(t, "Build box", details["name"])
}

func TestLicenseEndpoints_RequireAdmin(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-license-endpoints"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		}),
		test.WithServerOptions(
			frontend.WithLicenseManager(defaultTestLicenseManager()),
			frontend.WithAPIOption(apiv1.WithCloudReport(newMonitoring(t, ""))),
			frontend.WithAPIOption(apiv1.WithCloudAccess(newRemoteAccess(t, config.CloudConfig{}))),
		),
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
		client.Put("/api/v1/license/server-name", map[string]string{"name": "Build box"}),
		client.Get("/api/v1/license/monitoring"),
		client.Put("/api/v1/license/monitoring", map[string]string{"level": "runs"}),
		client.Post("/api/v1/license/monitoring/dismiss-notice", nil),
		client.Get("/api/v1/license/remote-access"),
		client.Put("/api/v1/license/remote-access", map[string]string{"level": "developer"}),
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

// newMonitoring returns what a server reports to Dagu Console, with the
// choice persisted in a temporary directory.
func newMonitoring(t *testing.T, configured config.ReportLevel) *cloudreport.Monitoring {
	t.Helper()
	return cloudreport.NewMonitoring(configured, filemonitor.NewStateStore(filepath.Join(t.TempDir(), "report-settings.json")))
}

func TestLicenseMonitoring(t *testing.T) {
	t.Parallel()

	settings := filepath.Join(t.TempDir(), "report-settings.json")
	server := test.SetupServer(t, test.WithServerOptions(frontend.WithAPIOption(apiv1.WithCloudReport(
		cloudreport.NewMonitoring("", filemonitor.NewStateStore(settings)),
	))))

	var monitoring api.LicenseMonitoring
	server.Client().Get("/api/v1/license/monitoring").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
	assert.Equal(t, api.LicenseMonitoring{Level: api.LicenseMonitoringLevelOff}, monitoring)

	server.Client().Put("/api/v1/license/monitoring", map[string]string{"level": "runs"}).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
	assert.Equal(t, api.LicenseMonitoring{Level: api.LicenseMonitoringLevelRuns, Chosen: true, ErrorsOffered: true}, monitoring)

	server.Client().Post("/api/v1/license/monitoring/dismiss-notice", nil).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
	assert.True(t, monitoring.NoticeDismissed)

	server.Client().Put("/api/v1/license/monitoring", map[string]string{"level": "everything"}).
		ExpectStatus(http.StatusBadRequest).Send(t)

	// The choice outlives the server.
	status, err := cloudreport.NewMonitoring("", filemonitor.NewStateStore(settings)).Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, config.ReportRuns, status.Level)
	assert.True(t, status.NoticeDismissed)
}

// A level chosen before servers could send error messages is offered them
// until an administrator dismisses the offer.
func TestLicenseMonitoring_DismissErrorsNotice(t *testing.T) {
	t.Parallel()

	settings := filepath.Join(t.TempDir(), "report-settings.json")
	require.NoError(t, os.WriteFile(settings, []byte(`{"level":"runs"}`), 0o600))
	server := test.SetupServer(t, test.WithServerOptions(frontend.WithAPIOption(apiv1.WithCloudReport(
		cloudreport.NewMonitoring("", filemonitor.NewStateStore(settings)),
	))))

	var monitoring api.LicenseMonitoring
	server.Client().Get("/api/v1/license/monitoring").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
	assert.False(t, monitoring.ErrorsOffered)

	server.Client().Post("/api/v1/license/monitoring/dismiss-errors-notice", nil).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
	assert.Equal(t, api.LicenseMonitoring{Level: api.LicenseMonitoringLevelRuns, Chosen: true, ErrorsOffered: true}, monitoring)
}

func TestLicenseMonitoring_Configured(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithServerOptions(frontend.WithAPIOption(apiv1.WithCloudReport(
		newMonitoring(t, config.ReportHealth),
	))))

	var errResp api.Error
	server.Client().Put("/api/v1/license/monitoring", map[string]string{"level": "runs"}).
		ExpectStatus(http.StatusConflict).Send(t).Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeConflict, errResp.Code)

	var monitoring api.LicenseMonitoring
	server.Client().Get("/api/v1/license/monitoring").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
	assert.Equal(t, api.LicenseMonitoring{Level: api.LicenseMonitoringLevelHealth, Configured: true}, monitoring)
}

func TestLicenseMonitoring_Unavailable(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t)

	server.Client().Get("/api/v1/license/monitoring").ExpectStatus(http.StatusBadRequest).Send(t)
}

// stubCloudReport has sent one report, whose answer paused storing runs.
type stubCloudReport struct{}

func (stubCloudReport) Status(context.Context) (cloudreport.Status, error) {
	return cloudreport.Status{
		Level:           config.ReportRuns,
		Chosen:          true,
		LastReportAt:    time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC),
		LastReport:      []byte(`{"protocol":2,"heartbeat_secret":"[redacted]"}`),
		RunsPausedUntil: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (stubCloudReport) SetLevel(context.Context, config.ReportLevel) error { return nil }

func (stubCloudReport) DismissNotice(context.Context) error { return nil }

func (stubCloudReport) DismissErrorsNotice(context.Context) error { return nil }

func TestLicenseMonitoring_LastReport(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithServerOptions(frontend.WithAPIOption(apiv1.WithCloudReport(stubCloudReport{}))))

	var monitoring api.LicenseMonitoring
	server.Client().Get("/api/v1/license/monitoring").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &monitoring)
	require.NotNil(t, monitoring.LastReport)
	assert.Equal(t, `{"protocol":2,"heartbeat_secret":"[redacted]"}`, *monitoring.LastReport)
	require.NotNil(t, monitoring.LastReportAt)
	assert.Equal(t, "2026-10-10T09:00:00Z", *monitoring.LastReportAt)
	require.NotNil(t, monitoring.RunsPausedUntil)
	assert.True(t, time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC).Equal(*monitoring.RunsPausedUntil))
}

// Changing the level is audited with the level it replaced.
func TestLicenseMonitoring_Audit(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-license-monitoring"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
			cfg.Server.Audit.Enabled = true
		}),
		test.WithServerOptions(
			frontend.WithLicenseManager(defaultTestLicenseManager()),
			frontend.WithAPIOption(apiv1.WithCloudReport(newMonitoring(t, ""))),
		),
	)
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{Username: "admin", Password: "adminpass"}).
		ExpectStatus(http.StatusOK).Send(t)
	adminToken := getAdminToken(t, server)

	server.Client().Put("/api/v1/license/monitoring", map[string]string{"level": "health"}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	var logs api.AuditLogsResponse
	server.Client().Get("/api/v1/audit?category=system&action=cloud_report_update").
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &logs)
	require.Len(t, logs.Entries, 1)
	entry := logs.Entries[0]
	assert.Equal(t, "admin", entry.Username)
	require.NotNil(t, entry.Details)
	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(*entry.Details), &details))
	assert.Equal(t, "health", details["level"])
	assert.Equal(t, "off", details["previous_level"])
}

func newRemoteAccess(t *testing.T, cloud config.CloudConfig) *cloudaccess.Service {
	t.Helper()
	return cloudaccess.New(cloudaccess.Config{
		Credentials: func() (license.CloudCredentials, bool) { return license.CloudCredentials{}, false },
		Settings:    cloudaccess.NewSettings(cloud, filemonitor.NewStateStore(filepath.Join(t.TempDir(), "access-settings.json"))),
	})
}

func TestLicenseRemoteAccess(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithServerOptions(frontend.WithAPIOption(apiv1.WithCloudAccess(
		newRemoteAccess(t, config.CloudConfig{}),
	))))

	var access api.LicenseRemoteAccess
	server.Client().Get("/api/v1/license/remote-access").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &access)
	assert.Equal(t, api.LicenseRemoteAccess{Level: api.LicenseRemoteAccessLevelOff}, access)

	server.Client().Put("/api/v1/license/remote-access", map[string]any{"level": "operator", "approvals": true}).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &access)
	assert.Equal(t, api.LicenseRemoteAccess{Level: api.LicenseRemoteAccessLevelOperator, Approvals: true}, access)

	server.Client().Put("/api/v1/license/remote-access", map[string]any{"level": "admin"}).
		ExpectStatus(http.StatusBadRequest).Send(t)
	server.Client().Put("/api/v1/license/remote-access", map[string]any{}).
		ExpectStatus(http.StatusBadRequest).Send(t)
}

// A change that configuration refuses in part changes nothing.
func TestLicenseRemoteAccess_Configured(t *testing.T) {
	t.Parallel()

	server := test.SetupServer(t, test.WithServerOptions(frontend.WithAPIOption(apiv1.WithCloudAccess(
		newRemoteAccess(t, config.CloudConfig{RemoteAccess: config.RemoteAccessViewer}),
	))))

	var errResp api.Error
	server.Client().Put("/api/v1/license/remote-access", map[string]any{"level": "developer", "approvals": true}).
		ExpectStatus(http.StatusConflict).Send(t).Unmarshal(t, &errResp)
	assert.Equal(t, api.ErrorCodeConflict, errResp.Code)

	var access api.LicenseRemoteAccess
	server.Client().Get("/api/v1/license/remote-access").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &access)
	assert.Equal(t, api.LicenseRemoteAccess{Level: api.LicenseRemoteAccessLevelViewer, LevelConfigured: true}, access)

	server.Client().Put("/api/v1/license/remote-access", map[string]any{"approvals": true}).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &access)
	assert.True(t, access.Approvals)
}
