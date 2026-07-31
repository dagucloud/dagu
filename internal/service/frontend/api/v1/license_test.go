// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
