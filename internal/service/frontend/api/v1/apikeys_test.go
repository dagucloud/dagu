// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getAdminToken authenticates as admin and returns the JWT token
func getAdminToken(t *testing.T, server test.Server) string {
	t.Helper()
	resp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	var loginResult api.LoginResponse
	resp.Unmarshal(t, &loginResult)
	require.NotEmpty(t, loginResult.Token)
	return loginResult.Token
}

func setupBuiltinAuthServer(t *testing.T) test.Server {
	t.Helper()
	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
		}),
		test.WithServerOptions(frontend.WithLicenseManager(defaultTestLicenseManager())),
	)

	// Create admin via setup endpoint
	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	return server
}

// TestAPIKeys_ListEmpty tests listing API keys when none exist
func TestAPIKeys_ListEmpty(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	resp := server.Client().Get("/api/v1/api-keys").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	var result api.APIKeysListResponse
	resp.Unmarshal(t, &result)
	assert.Empty(t, result.ApiKeys, "expected no API keys")
}

// TestAPIKeys_RequiresAuth tests that API key endpoints require authentication
func TestAPIKeys_RequiresAuth(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)

	// Without auth - should fail
	server.Client().Get("/api/v1/api-keys").
		ExpectStatus(http.StatusUnauthorized).Send(t)

	server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "test-key",
		Role: api.UserRoleViewer,
	}).ExpectStatus(http.StatusUnauthorized).Send(t)
}

// TestAPIKeys_RequiresAdmin tests that non-admin users cannot access API key endpoints
func TestAPIKeys_RequiresAdmin(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)

	// Create a non-admin user
	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "viewer-user",
		Password: "viewerpass1",
		Role:     api.UserRoleViewer,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	// Login as viewer
	viewerResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "viewer-user",
		Password: "viewerpass1",
	}).ExpectStatus(http.StatusOK).Send(t)

	var viewerLogin api.LoginResponse
	viewerResp.Unmarshal(t, &viewerLogin)

	// Viewer should get forbidden
	server.Client().Get("/api/v1/api-keys").
		WithBearerToken(viewerLogin.Token).
		ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "test-key",
		Role: api.UserRoleViewer,
	}).WithBearerToken(viewerLogin.Token).ExpectStatus(http.StatusForbidden).Send(t)
}

// TestAPIKeys_CRUD tests the full CRUD lifecycle of API keys
func TestAPIKeys_CRUD(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	// Create an API key
	description := "Test API key description"
	createResp := server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name:        "my-test-key",
		Description: &description,
		Role:        api.UserRoleManager,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.CreateAPIKeyResponse
	createResp.Unmarshal(t, &createResult)

	assert.NotEmpty(t, createResult.Key, "expected full key to be returned")
	assert.NotEmpty(t, createResult.ApiKey.Id, "expected API key ID")
	assert.Equal(t, "my-test-key", createResult.ApiKey.Name)
	assert.Equal(t, api.UserRoleManager, createResult.ApiKey.Role)
	require.NotNil(t, createResult.ApiKey.Description)
	assert.Equal(t, "Test API key description", *createResult.ApiKey.Description)
	assert.NotEmpty(t, createResult.ApiKey.KeyPrefix, "expected key prefix")

	keyID := createResult.ApiKey.Id
	fullKey := createResult.Key

	// List API keys
	listResp := server.Client().Get("/api/v1/api-keys").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	var listResult api.APIKeysListResponse
	listResp.Unmarshal(t, &listResult)
	assert.Len(t, listResult.ApiKeys, 1)
	assert.Equal(t, "my-test-key", listResult.ApiKeys[0].Name)

	// Get specific API key
	getResp := server.Client().Get("/api/v1/api-keys/" + keyID).
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	var getResult api.APIKeyResponse
	getResp.Unmarshal(t, &getResult)
	assert.Equal(t, keyID, getResult.ApiKey.Id)
	assert.Equal(t, "my-test-key", getResult.ApiKey.Name)

	// Update API key
	newName := "updated-key-name"
	newDesc := "Updated description"
	newRole := api.UserRoleAdmin
	updateResp := server.Client().Patch("/api/v1/api-keys/"+keyID, api.UpdateAPIKeyRequest{
		Name:        &newName,
		Description: &newDesc,
		Role:        &newRole,
	}).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	var updateResult api.APIKeyResponse
	updateResp.Unmarshal(t, &updateResult)
	assert.Equal(t, "updated-key-name", updateResult.ApiKey.Name)
	require.NotNil(t, updateResult.ApiKey.Description)
	assert.Equal(t, "Updated description", *updateResult.ApiKey.Description)
	assert.Equal(t, api.UserRoleAdmin, updateResult.ApiKey.Role)

	// Verify the key still works after update
	server.Client().Get("/api/v1/dag-runs").
		WithBearerToken(fullKey).
		ExpectStatus(http.StatusOK).Send(t)

	// Delete API key
	server.Client().Delete("/api/v1/api-keys/" + keyID).
		WithBearerToken(token).
		ExpectStatus(http.StatusNoContent).Send(t)

	// Verify it's deleted
	server.Client().Get("/api/v1/api-keys/" + keyID).
		WithBearerToken(token).
		ExpectStatus(http.StatusNotFound).Send(t)

	// Verify key no longer works
	server.Client().Get("/api/v1/dag-runs").
		WithBearerToken(fullKey).
		ExpectStatus(http.StatusUnauthorized).Send(t)
}

// TestAPIKeys_CreateDuplicate tests that creating duplicate API keys fails
func TestAPIKeys_CreateDuplicate(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	// Create first key
	server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "duplicate-key",
		Role: api.UserRoleViewer,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	// Try to create duplicate
	server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "duplicate-key",
		Role: api.UserRoleViewer,
	}).WithBearerToken(token).ExpectStatus(http.StatusConflict).Send(t)
}

// TestAPIKeys_GetNotFound tests getting a non-existent API key
func TestAPIKeys_GetNotFound(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	server.Client().Get("/api/v1/api-keys/non-existent-id").
		WithBearerToken(token).
		ExpectStatus(http.StatusNotFound).Send(t)
}

// TestAPIKeys_UpdateNotFound tests updating a non-existent API key
func TestAPIKeys_UpdateNotFound(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	newName := "new-name"
	server.Client().Patch("/api/v1/api-keys/non-existent-id", api.UpdateAPIKeyRequest{
		Name: &newName,
	}).WithBearerToken(token).ExpectStatus(http.StatusNotFound).Send(t)
}

// TestAPIKeys_DeleteNotFound tests deleting a non-existent API key
func TestAPIKeys_DeleteNotFound(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	server.Client().Delete("/api/v1/api-keys/non-existent-id").
		WithBearerToken(token).
		ExpectStatus(http.StatusNotFound).Send(t)
}

// TestAPIKeys_AuthenticateWithAPIKey tests that API keys can be used for authentication
func TestAPIKeys_AuthenticateWithAPIKey(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	// Create an API key with manager role
	createResp := server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "auth-test-key",
		Role: api.UserRoleManager,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.CreateAPIKeyResponse
	createResp.Unmarshal(t, &createResult)

	apiKey := createResult.Key

	// Use the API key for authentication
	server.Client().Get("/api/v1/dag-runs").
		WithBearerToken(apiKey).
		ExpectStatus(http.StatusOK).Send(t)

	// API key should work for DAG operations
	spec := `
steps:
  - name: test
    run: echo hello
`
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "api_key_auth_test",
		Spec: &spec,
	}).WithBearerToken(apiKey).ExpectStatus(http.StatusCreated).Send(t)
}

// TestAPIKeys_RoleEnforcement tests that API key roles are enforced
func TestAPIKeys_RoleEnforcement(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	// Create an API key with viewer role
	createResp := server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "viewer-key",
		Role: api.UserRoleViewer,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.CreateAPIKeyResponse
	createResp.Unmarshal(t, &createResult)

	viewerKey := createResult.Key

	// Viewer key should be able to read
	server.Client().Get("/api/v1/dag-runs").
		WithBearerToken(viewerKey).
		ExpectStatus(http.StatusOK).Send(t)

	// Viewer key should NOT be able to write
	spec := `
steps:
  - name: test
    run: echo hello
`
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "viewer_test_dag",
		Spec: &spec,
	}).WithBearerToken(viewerKey).ExpectStatus(http.StatusForbidden).Send(t)

	// Viewer key should NOT be able to access API key management
	server.Client().Get("/api/v1/api-keys").
		WithBearerToken(viewerKey).
		ExpectStatus(http.StatusForbidden).Send(t)
}

// TestAPIKeys_InvalidRole tests creating an API key with an invalid role
func TestAPIKeys_InvalidRole(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	// The schema validation should catch this at the server level
	// We're using a valid role type but testing the handler's validation
	server.Client().Post("/api/v1/api-keys", map[string]any{
		"name": "invalid-role-key",
		"role": "superadmin", // invalid role
	}).WithBearerToken(token).ExpectStatus(http.StatusBadRequest).Send(t)
}

// TestAPIKeys_PartialUpdate tests partial updates to API keys
func TestAPIKeys_PartialUpdate(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	// Create an API key
	description := "Original description"
	createResp := server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name:        "partial-update-key",
		Description: &description,
		Role:        api.UserRoleViewer,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.CreateAPIKeyResponse
	createResp.Unmarshal(t, &createResult)
	keyID := createResult.ApiKey.Id

	// Update only the name
	newName := "updated-name"
	updateResp := server.Client().Patch("/api/v1/api-keys/"+keyID, api.UpdateAPIKeyRequest{
		Name: &newName,
	}).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	var updateResult api.APIKeyResponse
	updateResp.Unmarshal(t, &updateResult)

	// Name should be updated
	assert.Equal(t, "updated-name", updateResult.ApiKey.Name)
	// Description and role should remain unchanged
	require.NotNil(t, updateResult.ApiKey.Description)
	assert.Equal(t, "Original description", *updateResult.ApiKey.Description)
	assert.Equal(t, api.UserRoleViewer, updateResult.ApiKey.Role)
}

// TestAPIKeys_LastUsedAtUpdated tests that the last_used_at timestamp is updated when API key is used
func TestAPIKeys_LastUsedAtUpdated(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	token := getAdminToken(t, server)

	// Create an API key
	createResp := server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name: "lastused-test-key",
		Role: api.UserRoleManager,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.CreateAPIKeyResponse
	createResp.Unmarshal(t, &createResult)

	keyID := createResult.ApiKey.Id
	apiKey := createResult.Key

	// Verify last_used_at is nil initially
	getResp := server.Client().Get("/api/v1/api-keys/" + keyID).
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	var getResult api.APIKeyResponse
	getResp.Unmarshal(t, &getResult)
	assert.Nil(t, getResult.ApiKey.LastUsedAt, "last_used_at should be nil initially")

	// Use the API key to make a request
	server.Client().Get("/api/v1/dag-runs").
		WithBearerToken(apiKey).
		ExpectStatus(http.StatusOK).Send(t)

	// Wait for async update to complete
	require.Eventually(t, func() bool {
		resp := server.Client().Get("/api/v1/api-keys/" + keyID).
			WithBearerToken(token).
			ExpectStatus(http.StatusOK).Send(t)
		var result api.APIKeyResponse
		resp.Unmarshal(t, &result)
		return result.ApiKey.LastUsedAt != nil
	}, 5*time.Second, 50*time.Millisecond, "last_used_at should be populated after API key usage")
}
