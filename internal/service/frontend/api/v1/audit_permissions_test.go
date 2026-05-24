// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

// setupAuditTestServer creates a test server with audit enabled but NO license
// manager so it can be used to verify license gating.
func setupAuditTestServer(t *testing.T) test.Server {
	t.Helper()
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBuiltin
		cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key"
		cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		cfg.Server.Audit.Enabled = true
	}))
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)
	return server
}

func TestAudit_RequiresManagerOrAbove(t *testing.T) {
	t.Parallel()
	server := setupLicensedAuditTestServer(t)
	adminToken := getWebhookAdminToken(t, server)

	// Create users for each role below manager.
	for _, u := range []struct {
		username string
		password string
		role     api.UserRole
	}{
		{"manager-user", "manager1", api.UserRoleManager},
		{"developer-user", "developer1", api.UserRoleDeveloper},
		{"operator-user", "operator1", api.UserRoleOperator},
		{"viewer-user", "viewerpass1", api.UserRoleViewer},
	} {
		server.Client().Post("/api/v1/users", api.CreateUserRequest{
			Username: u.username,
			Password: u.password,
			Role:     u.role,
		}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	}

	login := func(username, password string) string {
		resp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
			Username: username,
			Password: password,
		}).ExpectStatus(http.StatusOK).Send(t)
		var result api.LoginResponse
		resp.Unmarshal(t, &result)
		return result.Token
	}

	managerToken := login("manager-user", "manager1")
	developerToken := login("developer-user", "developer1")
	operatorToken := login("operator-user", "operator1")
	viewerToken := login("viewer-user", "viewerpass1")

	// Manager can access audit endpoint.
	server.Client().Get("/api/v1/audit").
		WithBearerToken(managerToken).
		ExpectStatus(http.StatusOK).Send(t)

	// Developer, operator, and viewer are forbidden.
	server.Client().Get("/api/v1/audit").
		WithBearerToken(developerToken).
		ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Get("/api/v1/audit").
		WithBearerToken(operatorToken).
		ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Get("/api/v1/audit").
		WithBearerToken(viewerToken).
		ExpectStatus(http.StatusForbidden).Send(t)
}

// setupLicensedAuditTestServer creates a test server with audit enabled and a
// license manager that has both "audit" and "rbac" features (via setupWebhookTestServer defaults).
func setupLicensedAuditTestServer(t *testing.T) test.Server {
	t.Helper()
	return setupWebhookTestServer(t, func(cfg *config.Config) {
		cfg.Server.Audit.Enabled = true
	})
}

func TestAudit_RequiresLicense(t *testing.T) {
	t.Parallel()
	// Server with audit enabled but NO license manager — community mode.
	server := setupAuditTestServer(t)
	adminToken := getWebhookAdminToken(t, server)

	server.Client().Get("/api/v1/audit").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusForbidden).Send(t)
}

// TestCommunityMode_ListUsersAndResetPassword verifies that community-mode admins
// can list users and reset passwords without an RBAC license.
func TestCommunityMode_ListUsersAndResetPassword(t *testing.T) {
	t.Parallel()
	// Community mode: no license manager.
	server := setupAuditTestServer(t)
	adminToken := getWebhookAdminToken(t, server)

	// ListUsers should succeed (no RBAC license required).
	resp := server.Client().Get("/api/v1/users").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).Send(t)

	var listResult api.UsersListResponse
	resp.Unmarshal(t, &listResult)
	require.NotEmpty(t, listResult.Users, "should have at least the admin user")

	// ResetUserPassword should succeed (no RBAC license required).
	adminUserID := listResult.Users[0].Id
	const newAdminPass = "newadminpass1"
	server.Client().Post("/api/v1/users/"+adminUserID+"/reset-password", api.ResetPasswordRequest{
		NewPassword: newAdminPass,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	// Re-authenticate with the new password — the old token is invalidated.
	resp = server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "admin",
		Password: newAdminPass,
	}).ExpectStatus(http.StatusOK).Send(t)
	var newLoginResult api.LoginResponse
	resp.Unmarshal(t, &newLoginResult)
	require.NotEmpty(t, newLoginResult.Token)
	freshToken := newLoginResult.Token

	// CreateUser should fail — RBAC-gated (community mode has no license for user management).
	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "should-fail",
		Password: "password123",
		Role:     api.UserRoleViewer,
	}).WithBearerToken(freshToken).ExpectStatus(http.StatusForbidden).Send(t)
}
