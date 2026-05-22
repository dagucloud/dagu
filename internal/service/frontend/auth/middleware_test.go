// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ APIKeyValidator = (*mockAPIKeyValidator)(nil)

// mockAPIKeyValidator is a mock implementation of APIKeyValidator for testing.
type mockAPIKeyValidator struct {
	keys map[string]*auth.APIKey
}

func newMockAPIKeyValidator() *mockAPIKeyValidator {
	return &mockAPIKeyValidator{
		keys: make(map[string]*auth.APIKey),
	}
}

func (m *mockAPIKeyValidator) AddKey(secret string, key *auth.APIKey) {
	m.keys[secret] = key
}

func (m *mockAPIKeyValidator) ValidateAPIKey(_ context.Context, keySecret string) (*auth.APIKey, error) {
	if key, ok := m.keys[keySecret]; ok {
		return key, nil
	}
	return nil, auth.ErrAPIKeyNotFound
}

var _ TokenValidator = (*mockTokenValidator)(nil)

// mockTokenValidator is a mock implementation of TokenValidator for testing.
type mockTokenValidator struct {
	users map[string]*auth.User
}

func newMockTokenValidator() *mockTokenValidator {
	return &mockTokenValidator{
		users: make(map[string]*auth.User),
	}
}

func (m *mockTokenValidator) AddUser(token string, user *auth.User) {
	m.users[token] = user
}

func (m *mockTokenValidator) GetUserFromToken(_ context.Context, token string) (*auth.User, error) {
	if user, ok := m.users[token]; ok {
		return user, nil
	}
	return nil, auth.ErrUserNotFound
}

// testHandler is a simple handler that extracts and stores the authenticated user.
type testHandler struct {
	user   *auth.User
	apiKey *auth.APIKey
}

func (h *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.user, _ = auth.UserFromContext(r.Context())
	h.apiKey, _ = auth.APIKeyFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
}

func TestMiddleware_APIKeyValidation(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	apiKeyValidator.AddKey("dagu_testkey123456789", &auth.APIKey{
		ID:   "key-id-1",
		Name: "test-key",
		Role: auth.RoleManager,
	})

	opts := Options{
		APIKeyValidator: apiKeyValidator,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	// Test with valid API key
	req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dagu_testkey123456789")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, handler.user)
	assert.Equal(t, "apikey:key-id-1", handler.user.ID)
	assert.Equal(t, "apikey:test-key", handler.user.Username)
	assert.Equal(t, auth.RoleManager, handler.user.Role)
}

func TestMiddleware_APIKeyRequiredSurface(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	apiKeyValidator.AddKey("dagu_mcpkey", &auth.APIKey{
		ID:              "key-id-1",
		Name:            "mcp-key",
		Role:            auth.RoleViewer,
		AllowedSurfaces: []auth.APIKeySurface{auth.APIKeySurfaceMCP},
	})

	opts := Options{
		APIKeyValidator:       apiKeyValidator,
		RequiredAPIKeySurface: auth.APIKeySurfaceMCP,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dagu_mcpkey")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, handler.apiKey)
	assert.Equal(t, "key-id-1", handler.apiKey.ID)
}

func TestMiddleware_APIKeyRequiredSurfaceDenied(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	apiKeyValidator.AddKey("dagu_restkey", &auth.APIKey{
		ID:              "key-id-1",
		Name:            "rest-key",
		Role:            auth.RoleViewer,
		AllowedSurfaces: []auth.APIKeySurface{auth.APIKeySurfaceREST},
	})

	var deniedReason string
	var deniedKeyID string
	opts := Options{
		APIKeyValidator:       apiKeyValidator,
		AuthRequired:          true,
		RequiredAPIKeySurface: auth.APIKeySurfaceMCP,
		OnDenied: func(_ *http.Request, reason string, key *auth.APIKey) {
			deniedReason = reason
			if key != nil {
				deniedKeyID = key.ID
			}
		},
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dagu_restkey")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Nil(t, handler.user)
	assert.Equal(t, DenialReasonAPIKeySurfaceDenied, deniedReason)
	assert.Equal(t, "key-id-1", deniedKeyID)
}

func TestMiddleware_APIKeyValidation_InvalidKey(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	// No valid keys added

	opts := Options{
		APIKeyValidator: apiKeyValidator,
		AuthRequired:    true,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	// Test with invalid API key
	req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dagu_invalidkey")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMiddleware_APIKeyValidation_WrongPrefix(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	apiKeyValidator.AddKey("dagu_testkey123456789", &auth.APIKey{
		ID:   "key-id-1",
		Name: "test-key",
		Role: auth.RoleViewer,
	})

	opts := Options{
		APIKeyValidator: apiKeyValidator,
		AuthRequired:    true,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	// Test with token that doesn't have dagu_ prefix (should not use API key validator)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer some_other_token")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Should fail since no other auth method is configured
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMiddleware_APIKeyValidation_WithJWTFallback(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	jwtValidator := newMockTokenValidator()
	jwtValidator.AddUser("jwt-token", &auth.User{
		ID:       "user-id-1",
		Username: "jwtuser",
		Role:     auth.RoleAdmin,
	})

	opts := Options{
		JWTValidator:    jwtValidator,
		APIKeyValidator: apiKeyValidator,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	// Test with JWT token (should use JWT validator, not API key)
	req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer jwt-token")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, handler.user)
	assert.Equal(t, "user-id-1", handler.user.ID)
	assert.Equal(t, "jwtuser", handler.user.Username)
	assert.Equal(t, auth.RoleAdmin, handler.user.Role)
}

func TestMiddleware_APIKeyValidation_BearerToken(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	apiKeyValidator.AddKey("dagu_testkey123456789", &auth.APIKey{
		ID:   "key-id-1",
		Name: "test-key",
		Role: auth.RoleOperator,
	})

	opts := Options{
		APIKeyValidator: apiKeyValidator,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer dagu_testkey123456789")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, handler.user)
	assert.Equal(t, "apikey:key-id-1", handler.user.ID)
	assert.Equal(t, auth.RoleOperator, handler.user.Role)
}

func TestMiddleware_APIKeyValidation_RolesPreserved(t *testing.T) {
	tests := []struct {
		name     string
		role     auth.Role
		expected auth.Role
	}{
		{"admin", auth.RoleAdmin, auth.RoleAdmin},
		{"manager", auth.RoleManager, auth.RoleManager},
		{"developer", auth.RoleDeveloper, auth.RoleDeveloper},
		{"operator", auth.RoleOperator, auth.RoleOperator},
		{"viewer", auth.RoleViewer, auth.RoleViewer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiKeyValidator := newMockAPIKeyValidator()
			apiKeyValidator.AddKey("dagu_testkey", &auth.APIKey{
				ID:   "key-id",
				Name: "test-key",
				Role: tt.role,
			})

			opts := Options{
				APIKeyValidator: apiKeyValidator,
			}
			middleware := Middleware(opts)

			handler := &testHandler{}
			server := httptest.NewServer(middleware(handler))
			defer server.Close()

			req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer dagu_testkey")

			client := &http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotNil(t, handler.user)
			assert.Equal(t, tt.expected, handler.user.Role)
		})
	}
}

func TestMiddleware_PublicPaths(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	// No keys added

	opts := Options{
		APIKeyValidator: apiKeyValidator,
		PublicPaths:     []string{"/public", "/api/health"},
		AuthRequired:    true,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	// Test public path - should succeed without auth
	req, err := http.NewRequest(http.MethodGet, server.URL+"/public", nil)
	require.NoError(t, err)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Test non-public path - should fail without auth
	req, err = http.NewRequest(http.MethodGet, server.URL+"/protected", nil)
	require.NoError(t, err)

	resp, err = client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMiddleware_PublicPathPrefixes(t *testing.T) {
	apiKeyValidator := newMockAPIKeyValidator()
	// No keys added

	opts := Options{
		APIKeyValidator:    apiKeyValidator,
		PublicPathPrefixes: []string{"/api/v1/webhooks/"},
		AuthRequired:       true,
	}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	// Test public path prefix - should succeed without auth
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/webhooks/my-dag", nil)
	require.NoError(t, err)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Test another path with the same prefix
	req, err = http.NewRequest(http.MethodPost, server.URL+"/api/v1/webhooks/another-dag", nil)
	require.NoError(t, err)

	resp, err = client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Test non-matching path - should fail without auth
	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/v1/dags", nil)
	require.NoError(t, err)

	resp, err = client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestMiddleware_NoAuthEnabled(t *testing.T) {
	opts := Options{}
	middleware := Middleware(opts)

	handler := &testHandler{}
	server := httptest.NewServer(middleware(handler))
	defer server.Close()

	// When no auth is enabled, all requests should pass
	req, err := http.NewRequest(http.MethodGet, server.URL+"/any", nil)
	require.NoError(t, err)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestQueryTokenMiddleware(t *testing.T) {
	t.Parallel()

	// headerCapture is a handler that records the Authorization header it receives.
	type headerCapture struct {
		authHeader string
	}

	t.Run("promotes query token to Authorization header", func(t *testing.T) {
		t.Parallel()
		capture := &headerCapture{}
		handler := QueryTokenMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capture.authHeader = r.Header.Get("Authorization")
		}))

		req := httptest.NewRequest(http.MethodGet, "/test?token=mytoken123", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, "Bearer mytoken123", capture.authHeader)
	})

	t.Run("existing Authorization header takes precedence", func(t *testing.T) {
		t.Parallel()
		capture := &headerCapture{}
		handler := QueryTokenMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capture.authHeader = r.Header.Get("Authorization")
		}))

		req := httptest.NewRequest(http.MethodGet, "/test?token=querytoken", nil)
		req.Header.Set("Authorization", "Bearer headertoken")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Equal(t, "Bearer headertoken", capture.authHeader)
	})

	t.Run("no token no header passes through", func(t *testing.T) {
		t.Parallel()
		capture := &headerCapture{}
		handler := QueryTokenMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capture.authHeader = r.Header.Get("Authorization")
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Empty(t, capture.authHeader)
	})

	t.Run("empty token value passes through", func(t *testing.T) {
		t.Parallel()
		capture := &headerCapture{}
		handler := QueryTokenMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capture.authHeader = r.Header.Get("Authorization")
		}))

		req := httptest.NewRequest(http.MethodGet, "/test?token=", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		assert.Empty(t, capture.authHeader)
	})
}
