// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/service/frontend"
	apiimpl "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func webhookParallel(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "windows" {
		t.Parallel()
	}
}

func TestExtractWebhookToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		authHeader string
		want       string
	}{
		{
			name:       "valid bearer token",
			authHeader: "Bearer my-secret-token",
			want:       "my-secret-token",
		},
		{
			name:       "empty header",
			authHeader: "",
			want:       "",
		},
		{
			name:       "no bearer prefix",
			authHeader: "my-secret-token",
			want:       "",
		},
		{
			name:       "wrong prefix",
			authHeader: "Basic my-secret-token",
			want:       "",
		},
		{
			name:       "bearer with extra spaces",
			authHeader: "Bearer  token-with-space",
			want:       " token-with-space",
		},
		{
			name:       "lowercase bearer",
			authHeader: "bearer my-token",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := apiimpl.ExtractWebhookToken(tt.authHeader)
			require.Equal(t, tt.want, got)
		})
	}
}

// defaultTestLicenseManager returns a license manager with all Pro features
// enabled, for use in tests that need licensed endpoints (user management, audit, etc.).
func defaultTestLicenseManager() *license.Manager {
	return license.NewTestManager(license.FeatureRBAC, license.FeatureAudit)
}

// setupWebhookTestServer creates a test server with builtin auth and a default
// Pro license. Extra config mutators are applied after the base auth config.
func setupWebhookTestServer(t *testing.T, extraMutators ...func(*config.Config)) test.Server {
	t.Helper()

	server := test.SetupServer(t,
		test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeBuiltin
			cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key"
			cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
			for _, m := range extraMutators {
				m(cfg)
			}
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

// getWebhookAdminToken authenticates as admin and returns the JWT token
func getWebhookAdminToken(t *testing.T, server test.Server) string {
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

// createTestDAG creates a simple DAG for webhook testing
func createTestDAG(t *testing.T, server test.Server, token, name string) {
	t.Helper()
	spec := `
steps:
  - name: test
    command: echo hello
`
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: name,
		Spec: &spec,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)
}

func signWebhookPayload(t *testing.T, secret string, body any) string {
	t.Helper()

	raw, err := json.Marshal(body)
	require.NoError(t, err)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func decodeBodyMap(t *testing.T, resp *test.Response) map[string]any {
	t.Helper()

	var decoded map[string]any
	resp.Unmarshal(t, &decoded)
	return decoded
}

func nestedMap(t *testing.T, input map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := input[key].(map[string]any)
	require.True(t, ok, "expected %q to be an object", key)
	return value
}

// TestWebhooks_ListEmpty tests listing webhooks when none exist
func TestWebhooks_ListEmpty(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	resp := server.Client().Get("/api/v1/webhooks").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	var result api.WebhookListResponse
	resp.Unmarshal(t, &result)
	assert.Empty(t, result.Webhooks, "expected no webhooks")
}

// TestWebhooks_RequiresAuth tests that webhook management endpoints require authentication
func TestWebhooks_RequiresAuth(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)

	// Without auth - should fail
	server.Client().Get("/api/v1/webhooks").
		ExpectStatus(http.StatusUnauthorized).Send(t)

	server.Client().Post("/api/v1/dags/test-dag/webhook", nil).
		ExpectStatus(http.StatusUnauthorized).Send(t)
}

// TestWebhooks_RequiresDeveloperOrAbove tests that webhook management requires developer or above.
func TestWebhooks_RequiresDeveloperOrAbove(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	adminToken := getWebhookAdminToken(t, server)

	// Create non-developer users.
	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "operator-user",
		Password: "operator1",
		Role:     api.UserRoleOperator,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "viewer-user",
		Password: "viewerpass1",
		Role:     api.UserRoleViewer,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	// Create developer user.
	server.Client().Post("/api/v1/users", api.CreateUserRequest{
		Username: "developer-user",
		Password: "developer1",
		Role:     api.UserRoleDeveloper,
	}).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	// Login as operator.
	operatorResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "operator-user",
		Password: "operator1",
	}).ExpectStatus(http.StatusOK).Send(t)

	var operatorLogin api.LoginResponse
	operatorResp.Unmarshal(t, &operatorLogin)

	// Login as viewer.
	viewerResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "viewer-user",
		Password: "viewerpass1",
	}).ExpectStatus(http.StatusOK).Send(t)

	var viewerLogin api.LoginResponse
	viewerResp.Unmarshal(t, &viewerLogin)

	// Login as developer.
	developerResp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "developer-user",
		Password: "developer1",
	}).ExpectStatus(http.StatusOK).Send(t)

	var developerLogin api.LoginResponse
	developerResp.Unmarshal(t, &developerLogin)

	// Operator and viewer should get forbidden for webhook management.
	server.Client().Get("/api/v1/webhooks").
		WithBearerToken(operatorLogin.Token).
		ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Post("/api/v1/dags/test-dag/webhook", nil).
		WithBearerToken(operatorLogin.Token).
		ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Get("/api/v1/webhooks").
		WithBearerToken(viewerLogin.Token).
		ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Post("/api/v1/dags/test-dag/webhook", nil).
		WithBearerToken(viewerLogin.Token).
		ExpectStatus(http.StatusForbidden).Send(t)

	// Developer can access webhook management endpoints.
	server.Client().Get("/api/v1/webhooks").
		WithBearerToken(developerLogin.Token).
		ExpectStatus(http.StatusOK).Send(t)

	// Developer can also create webhooks.
	dagName := "webhook_dev_access_test"
	createTestDAG(t, server, adminToken, dagName)
	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(developerLogin.Token).
		ExpectStatus(http.StatusCreated).Send(t)
}

// TestWebhooks_CRUD tests the full CRUD lifecycle of webhooks
func TestWebhooks_CRUD(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Create a DAG first
	dagName := "webhook_crud_test"
	createTestDAG(t, server, token, dagName)

	// Create a webhook
	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)

	assert.NotEmpty(t, createResult.Token, "expected full token to be returned")
	assert.NotEmpty(t, createResult.Webhook.Id, "expected webhook ID")
	assert.Equal(t, dagName, createResult.Webhook.DagName)
	assert.True(t, createResult.Webhook.Enabled)
	assert.NotEmpty(t, createResult.Webhook.TokenPrefix, "expected token prefix")
	assert.Contains(t, createResult.Token, "dagu_wh_", "token should have webhook prefix")

	webhookToken := createResult.Token

	// List webhooks
	listResp := server.Client().Get("/api/v1/webhooks").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	var listResult api.WebhookListResponse
	listResp.Unmarshal(t, &listResult)
	assert.Len(t, listResult.Webhooks, 1)
	assert.Equal(t, dagName, listResult.Webhooks[0].DagName)

	// Get specific webhook
	getResp := server.Client().Get("/api/v1/dags/" + dagName + "/webhook").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	var getResult api.WebhookDetails
	getResp.Unmarshal(t, &getResult)
	assert.Equal(t, createResult.Webhook.Id, getResult.Id)
	assert.Equal(t, dagName, getResult.DagName)

	// Trigger the webhook
	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusOK).Send(t)

	var triggerResult api.WebhookResponse
	triggerResp.Unmarshal(t, &triggerResult)
	assert.NotEmpty(t, triggerResult.DagRunId)
	assert.Equal(t, dagName, triggerResult.DagName)

	// Verify LastUsedAt is updated
	require.Eventually(t, func() bool {
		resp := server.Client().Get("/api/v1/dags/" + dagName + "/webhook").
			WithBearerToken(token).Send(t)
		if resp.Response.StatusCode() != http.StatusOK {
			return false
		}
		var result api.WebhookDetails
		resp.Unmarshal(t, &result)
		return result.LastUsedAt != nil
	}, 5*time.Second, 100*time.Millisecond, "LastUsedAt should be set after trigger")

	// Delete webhook
	server.Client().Delete("/api/v1/dags/" + dagName + "/webhook").
		WithBearerToken(token).
		ExpectStatus(http.StatusNoContent).Send(t)

	// Verify it's deleted
	server.Client().Get("/api/v1/dags/" + dagName + "/webhook").
		WithBearerToken(token).
		ExpectStatus(http.StatusNotFound).Send(t)

	// Verify webhook token no longer works
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusUnauthorized).Send(t)
}

func TestWebhooks_CreateDefaultsToTokenOnlyAuthShape(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_default_auth_shape_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).
		ExpectStatus(http.StatusCreated).Send(t)

	decoded := decodeBodyMap(t, createResp)
	webhook := nestedMap(t, decoded, "webhook")
	hmacDetails := nestedMap(t, webhook, "hmac")

	assert.Equal(t, "token_only", webhook["authMode"])
	assert.Equal(t, false, hmacDetails["enabled"])
	assert.Equal(t, false, hmacDetails["secretConfigured"])
}

// TestWebhooks_Toggle tests enabling and disabling webhooks
func TestWebhooks_Toggle(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Create a DAG and webhook
	dagName := "webhook_toggle_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)
	webhookToken := createResult.Token

	// Disable the webhook
	enabled := false
	toggleResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook/toggle", api.WebhookToggleRequest{
		Enabled: enabled,
	}).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	var toggleResult api.WebhookDetails
	toggleResp.Unmarshal(t, &toggleResult)
	assert.False(t, toggleResult.Enabled)

	// Try to trigger - should fail with forbidden
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusForbidden).Send(t)

	// Re-enable the webhook
	enabled = true
	server.Client().Post("/api/v1/dags/"+dagName+"/webhook/toggle", api.WebhookToggleRequest{
		Enabled: enabled,
	}).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	// Trigger should work again
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusOK).Send(t)
}

// TestWebhooks_RegenerateToken tests token regeneration
func TestWebhooks_RegenerateToken(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Create a DAG and webhook
	dagName := "webhook_regen_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)
	oldToken := createResult.Token

	// Regenerate the token
	regenResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook/regenerate", nil).
		WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	var regenResult api.WebhookCreateResponse
	regenResp.Unmarshal(t, &regenResult)
	newToken := regenResult.Token

	assert.NotEqual(t, oldToken, newToken, "new token should be different")
	assert.Contains(t, newToken, "dagu_wh_", "new token should have webhook prefix")

	// Old token should no longer work
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(oldToken).
		ExpectStatus(http.StatusUnauthorized).Send(t)

	// New token should work
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken(newToken).
		ExpectStatus(http.StatusOK).Send(t)
}

// TestWebhooks_DuplicateCreate tests that creating duplicate webhooks fails
func TestWebhooks_DuplicateCreate(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Create a DAG
	dagName := "webhook_dup_test"
	createTestDAG(t, server, token, dagName)

	// Create first webhook
	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	// Try to create duplicate - should fail
	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusConflict).Send(t)
}

// TestWebhooks_NotFound tests accessing webhooks for non-existent DAGs
func TestWebhooks_NotFound(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Get webhook for non-existent DAG
	server.Client().Get("/api/v1/dags/nonexistent-dag/webhook").
		WithBearerToken(token).
		ExpectStatus(http.StatusNotFound).Send(t)

	// Delete webhook for DAG without webhook
	dagName := "no_webhook_dag"
	createTestDAG(t, server, token, dagName)
	server.Client().Delete("/api/v1/dags/" + dagName + "/webhook").
		WithBearerToken(token).
		ExpectStatus(http.StatusNotFound).Send(t)
}

// TestWebhooks_TriggerWithPayload tests triggering webhooks with payload
func TestWebhooks_TriggerWithPayload(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Create a DAG and webhook
	dagName := "webhook_payload_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)
	webhookToken := createResult.Token

	// Trigger with payload
	payload := map[string]any{
		"key":    "value",
		"number": 42,
		"nested": map[string]any{
			"inner": "data",
		},
	}
	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{
		Payload: &payload,
	}).WithBearerToken(webhookToken).ExpectStatus(http.StatusOK).Send(t)

	var triggerResult api.WebhookResponse
	triggerResp.Unmarshal(t, &triggerResult)
	assert.NotEmpty(t, triggerResult.DagRunId)
}

// TestWebhooks_TriggerWithDagRunID tests idempotency with dag-run ID
func TestWebhooks_TriggerWithDagRunID(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Create a DAG and webhook
	dagName := "webhook_idempotent_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)
	webhookToken := createResult.Token

	// Trigger with custom dag-run ID
	customID := "custom-dag-run-id-123"
	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{
		DagRunId: &customID,
	}).WithBearerToken(webhookToken).ExpectStatus(http.StatusOK).Send(t)

	var triggerResult api.WebhookResponse
	triggerResp.Unmarshal(t, &triggerResult)
	assert.Equal(t, customID, triggerResult.DagRunId)

	// Wait for the DAG run to be recorded, then verify duplicate returns 409 Conflict
	require.Eventually(t, func() bool {
		resp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{
			DagRunId: &customID,
		}).WithBearerToken(webhookToken).Send(t)
		return resp.Response.StatusCode() == http.StatusConflict
	}, 30*time.Second, 100*time.Millisecond, "duplicate dag-run ID should return 409 Conflict")
}

// TestWebhooks_TriggerInvalidToken tests webhook trigger with invalid tokens
func TestWebhooks_TriggerInvalidToken(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	// Create a DAG and webhook
	dagName := "webhook_invalid_token_test"
	createTestDAG(t, server, token, dagName)

	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	// Try with no Authorization header
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		ExpectStatus(http.StatusUnauthorized).Send(t)

	// Try with wrong token format
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken("wrong_prefix_token").
		ExpectStatus(http.StatusUnauthorized).Send(t)

	// Try with valid prefix but wrong token
	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{}).
		WithBearerToken("dagu_wh_invalidtoken12345678901234567890").
		ExpectStatus(http.StatusUnauthorized).Send(t)
}

func TestWebhooks_EnableHMAC_StrictRequiresSignature(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_hmac_strict_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)
	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)

	enableResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook/hmac/enable", map[string]any{
		"authMode":        "token_and_hmac",
		"enforcementMode": "strict",
	}).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	enabled := decodeBodyMap(t, enableResp)
	hmacSecret, ok := enabled["hmacSecret"].(string)
	require.True(t, ok, "expected hmacSecret in enable response")
	require.NotEmpty(t, hmacSecret)

	body := api.WebhookRequest{
		Payload: &map[string]any{"event": "push"},
	}
	signature := signWebhookPayload(t, hmacSecret, body)

	server.Client().Post("/api/v1/webhooks/"+dagName, body).
		WithBearerToken(createResult.Token).
		ExpectStatus(http.StatusUnauthorized).Send(t)

	server.Client().Post("/api/v1/webhooks/"+dagName, body).
		WithBearerToken(createResult.Token).
		WithHeader("X-Dagu-Signature", signature).
		ExpectStatus(http.StatusOK).Send(t)
}

func TestWebhooks_EnableHMAC_NotSupportedReturnsNotImplemented(t *testing.T) {
	webhookParallel(t)

	tmpDir := t.TempDir()
	brokenDataDir := filepath.Join(tmpDir, "broken-data-dir")
	require.NoError(t, os.WriteFile(brokenDataDir, []byte("not-a-directory"), 0600))

	server := setupWebhookTestServer(t, func(cfg *config.Config) {
		cfg.Paths.DataDir = brokenDataDir
		cfg.Paths.WebhooksDir = filepath.Join(tmpDir, "webhooks")
	})
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_hmac_not_supported_test"
	createTestDAG(t, server, token, dagName)

	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	resp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook/hmac/enable", map[string]any{
		"authMode":        "token_and_hmac",
		"enforcementMode": "strict",
	}).WithBearerToken(token).ExpectStatus(http.StatusNotImplemented).Send(t)

	var apiErr api.Error
	resp.Unmarshal(t, &apiErr)
	assert.Equal(t, "webhook HMAC is not supported by this store", apiErr.Message)
}

func TestWebhooks_EnableHMAC_ObserveAllowsTokenOnly(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_hmac_observe_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)
	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)

	server.Client().Post("/api/v1/dags/"+dagName+"/webhook/hmac/enable", map[string]any{
		"authMode":        "token_and_hmac",
		"enforcementMode": "observe",
	}).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{
		Payload: &map[string]any{"event": "push"},
	}).
		WithBearerToken(createResult.Token).
		ExpectStatus(http.StatusOK).Send(t)
}

func TestWebhooks_EnableHMAC_HMACOnlyAllowsSignedRequestWithoutToken(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_hmac_only_test"
	createTestDAG(t, server, token, dagName)

	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	enableResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook/hmac/enable", map[string]any{
		"authMode":        "hmac_only",
		"enforcementMode": "strict",
	}).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)

	enabled := decodeBodyMap(t, enableResp)
	hmacSecret, ok := enabled["hmacSecret"].(string)
	require.True(t, ok, "expected hmacSecret in enable response")
	require.NotEmpty(t, hmacSecret)

	body := api.WebhookRequest{
		Payload: &map[string]any{"event": "push"},
	}
	signature := signWebhookPayload(t, hmacSecret, body)

	server.Client().Post("/api/v1/webhooks/"+dagName, body).
		WithHeader("X-Dagu-Signature", signature).
		ExpectStatus(http.StatusOK).Send(t)

	server.Client().Post("/api/v1/webhooks/"+dagName, body).
		WithBearerToken("dagu_wh_not_the_real_token").
		ExpectStatus(http.StatusUnauthorized).Send(t)
}

func TestWebhooks_TriggerOversizedBodyRejectedBeforeAuth(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_oversized_body_test"
	createTestDAG(t, server, token, dagName)

	server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	body := api.WebhookRequest{
		Payload: &map[string]any{
			"blob": strings.Repeat("x", 1024*1024),
		},
	}

	server.Client().Post("/api/v1/webhooks/"+dagName, body).
		ExpectStatus(http.StatusRequestEntityTooLarge).Send(t)
}

func TestWebhooks_TriggerUsesConfiguredMaxPayloadSize(t *testing.T) {
	webhookParallel(t)
	server := setupWebhookTestServer(t, func(cfg *config.Config) {
		cfg.Webhooks.MaxPayloadSize = 2 * config.DefaultWebhookMaxPayloadSize
	})
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_custom_payload_limit_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)
	webhookToken := createResult.Token
	require.NotEmpty(t, webhookToken)

	bodyAboveDefault := api.WebhookRequest{
		Payload: &map[string]any{
			"blob": strings.Repeat("x", config.DefaultWebhookMaxPayloadSize+1024),
		},
	}

	server.Client().Post("/api/v1/webhooks/"+dagName, bodyAboveDefault).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusOK).Send(t)

	bodyAboveConfiguredLimit := api.WebhookRequest{
		Payload: &map[string]any{
			"blob": strings.Repeat("x", 3*config.DefaultWebhookMaxPayloadSize),
		},
	}

	server.Client().Post("/api/v1/webhooks/"+dagName, bodyAboveConfiguredLimit).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusRequestEntityTooLarge).Send(t)
}

// TestWebhooks_TriggerNonExistentDAG tests triggering webhook for non-existent DAG
func TestWebhooks_TriggerNonExistentDAG(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)

	// Create a valid webhook token for a real DAG, then try on non-existent
	token := getWebhookAdminToken(t, server)
	dagName := "webhook_existing_dag"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)
	webhookToken := createResult.Token

	// Try to trigger on non-existent DAG (token won't match)
	server.Client().Post("/api/v1/webhooks/nonexistent-dag", api.WebhookRequest{}).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusUnauthorized).Send(t)
}

// TestWebhooks_TriggerWithArbitraryPayload tests triggering a webhook with
// an arbitrary JSON body (no "payload" wrapper), simulating external services.
func TestWebhooks_TriggerWithArbitraryPayload(t *testing.T) {
	t.Parallel()
	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_arbitrary_payload_test"
	createTestDAG(t, server, token, dagName)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)
	webhookToken := createResult.Token

	// Send an arbitrary JSON body WITHOUT the "payload" wrapper.
	// This simulates what external services like GitHub would send.
	arbitraryBody := map[string]any{
		"event": "push",
		"repo":  "foo/bar",
		"ref":   "refs/heads/main",
	}
	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, arbitraryBody).
		WithBearerToken(webhookToken).
		ExpectStatus(http.StatusOK).Send(t)

	var triggerResult api.WebhookResponse
	triggerResp.Unmarshal(t, &triggerResult)
	assert.NotEmpty(t, triggerResult.DagRunId)
	assert.Equal(t, dagName, triggerResult.DagName)
}

func TestWebhooks_TriggerForwardsConfiguredHeaders(t *testing.T) {
	webhookParallel(t)

	server := setupWebhookTestServer(t)
	token := getWebhookAdminToken(t, server)

	dagName := "webhook_forward_headers_test"
	headersFile := filepath.Join(t.TempDir(), "webhook-headers.json")
	spec := `
webhook:
  forward_headers:
    - X-GitHub-Event
    - X-GitHub-Delivery
steps:
  - name: capture-headers
    shell: /bin/sh
    command: |
      test -n "${WEBHOOK_HEADERS}"
      printf '%s' "$WEBHOOK_HEADERS" > ` + headersFile + `
`
	if runtime.GOOS == "windows" {
		spec = `
webhook:
  forward_headers:
    - X-GitHub-Event
    - X-GitHub-Delivery
steps:
  - name: capture-headers
    shell: powershell
    command: |
      if ($null -eq $env:WEBHOOK_HEADERS) { throw 'WEBHOOK_HEADERS not set' }
      [System.IO.File]::WriteAllText("` + strings.ReplaceAll(headersFile, `\`, `\\`) + `", $env:WEBHOOK_HEADERS)
`
	}

	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	createResp := server.Client().Post("/api/v1/dags/"+dagName+"/webhook", nil).
		WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var createResult api.WebhookCreateResponse
	createResp.Unmarshal(t, &createResult)

	triggerResp := server.Client().Post("/api/v1/webhooks/"+dagName, api.WebhookRequest{
		Payload: &map[string]any{"event": "push"},
	}).
		WithBearerToken(createResult.Token).
		WithHeader("X-GitHub-Event", "push").
		WithHeader("X-GitHub-Delivery", "delivery-1").
		ExpectStatus(http.StatusOK).Send(t)

	var triggerResult api.WebhookResponse
	triggerResp.Unmarshal(t, &triggerResult)
	assert.NotEmpty(t, triggerResult.DagRunId)
	assert.Equal(t, dagName, triggerResult.DagName)

	test.ProcessQueuedInlineRun(t, server, dagName)
	waitForStoredDAGRunStatus(t, server, dagName, triggerResult.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})

	data, err := os.ReadFile(headersFile)
	require.NoError(t, err)

	var decoded map[string][]string
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, []string{"push"}, decoded["x-github-event"])
	assert.Equal(t, []string{"delivery-1"}, decoded["x-github-delivery"])
	assert.False(t, slices.Contains(mapsKeys(decoded), "authorization"))
}

func mapsKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
