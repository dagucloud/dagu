// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCORSPolicy_DefaultDeniesCrossOriginRequests(t *testing.T) {
	t.Parallel()

	called := false
	handler := corsPolicy{setupPath: "/api/v1/auth/setup"}.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("preflight", func(t *testing.T) {
		called = false
		req := newPreflightRequest("/api/v1/dag-runs", "https://evil.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusForbidden, resp.Code)
		assert.Empty(t, resp.Header().Get("Access-Control-Allow-Origin"))
		assert.False(t, called)
	})

	t.Run("actual request", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodPost, "http://dagu.example/api/v1/dag-runs", nil)
		req.Header.Set("Origin", "https://evil.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusForbidden, resp.Code)
		assert.False(t, called)
	})

	t.Run("same origin", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodPost, "http://dagu.example/api/v1/dag-runs", nil)
		req.Header.Set("Origin", "http://dagu.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusNoContent, resp.Code)
		assert.True(t, called)
	})

	t.Run("non-browser client", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodPost, "http://dagu.example/api/v1/dag-runs", nil)
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusNoContent, resp.Code)
		assert.True(t, called)
	})
}

func TestCORSPolicy_ExplicitOrigin(t *testing.T) {
	t.Parallel()

	policy := corsPolicy{
		allowedOrigins: []string{
			"https://app.example",
			"http://local.example:80",
			"https://secure.example:443",
		},
		setupPath: "/api/v1/auth/setup",
	}
	handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("allowed", func(t *testing.T) {
		req := newPreflightRequest("/api/v1/dag-runs", "https://app.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		require.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, "https://app.example", resp.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", resp.Header().Get("Access-Control-Allow-Credentials"))
	})

	t.Run("equivalent default ports", func(t *testing.T) {
		tests := []struct {
			name   string
			origin string
		}{
			{name: "HTTP", origin: "http://local.example"},
			{name: "HTTPS", origin: "https://secure.example"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				req := newPreflightRequest("/api/v1/dag-runs", tt.origin)
				resp := httptest.NewRecorder()

				handler.ServeHTTP(resp, req)

				require.Equal(t, http.StatusOK, resp.Code)
				assert.Equal(t, tt.origin, resp.Header().Get("Access-Control-Allow-Origin"))
				assert.Equal(t, "true", resp.Header().Get("Access-Control-Allow-Credentials"))
			})
		}
	})

	t.Run("not allowed", func(t *testing.T) {
		req := newPreflightRequest("/api/v1/dag-runs", "https://evil.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusForbidden, resp.Code)
		assert.Empty(t, resp.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestCORSPolicy_WildcardPattern(t *testing.T) {
	t.Parallel()

	called := false
	policy := corsPolicy{
		allowedOrigins: []string{"https://a*a.com"},
		setupPath:      "/api/v1/auth/setup",
	}
	handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("matches complete prefix and suffix", func(t *testing.T) {
		called = false
		req := newPreflightRequest("/api/v1/dag-runs", "https://aa.com")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		require.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, "https://aa.com", resp.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", resp.Header().Get("Access-Control-Allow-Credentials"))
		assert.False(t, called)
	})

	t.Run("rejects overlapping prefix and suffix", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodPost, "http://dagu.example/api/v1/dag-runs", nil)
		req.Header.Set("Origin", "https://a.com")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusForbidden, resp.Code)
		assert.Empty(t, resp.Header().Get("Access-Control-Allow-Origin"))
		assert.False(t, called)
	})
}

func TestCORSPolicy_ExplicitWildcard(t *testing.T) {
	t.Parallel()

	called := false
	policy := corsPolicy{
		allowedOrigins: []string{"*"},
		setupPath:      "/api/v1/auth/setup",
	}
	handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("allows API preflight without credentials", func(t *testing.T) {
		called = false
		req := newPreflightRequest("/api/v1/dag-runs", "https://any.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		require.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, "*", resp.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, resp.Header().Get("Access-Control-Allow-Credentials"))
		assert.False(t, called)
	})

	t.Run("denies setup preflight", func(t *testing.T) {
		called = false
		req := newPreflightRequest("/api/v1/auth/setup", "https://any.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusForbidden, resp.Code)
		assert.Empty(t, resp.Header().Get("Access-Control-Allow-Origin"))
		assert.False(t, called)
	})

	t.Run("denies setup actual request", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodPost, "http://dagu.example/api/v1/auth/setup", nil)
		req.Header.Set("Origin", "https://any.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusForbidden, resp.Code)
		assert.False(t, called)
	})

	t.Run("allows same-origin setup", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodPost, "http://dagu.example/api/v1/auth/setup", nil)
		req.Header.Set("Origin", "http://dagu.example")
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		assert.Equal(t, http.StatusNoContent, resp.Code)
		assert.True(t, called)
	})
}

func TestCORSPolicy_SameOriginDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		publicURL    string
		requestURL   string
		origin       string
		secFetchSite string
	}{
		{
			name:       "public URL behind reverse proxy",
			publicURL:  "https://dagu.example/workflows",
			requestURL: "http://internal:8080/api/v1/dag-runs",
			origin:     "https://dagu.example",
		},
		{
			name:       "request URL when public URL differs",
			publicURL:  "https://dagu.example/workflows",
			requestURL: "http://internal:8080/api/v1/dag-runs",
			origin:     "http://internal:8080",
		},
		{
			name:       "default HTTP port",
			requestURL: "http://dagu.example:80/api/v1/dag-runs",
			origin:     "http://dagu.example",
		},
		{
			name:       "default HTTPS port",
			requestURL: "https://dagu.example:443/api/v1/dag-runs",
			origin:     "https://dagu.example",
		},
		{
			name:         "fetch metadata through TLS proxy",
			requestURL:   "http://dagu.example/api/v1/dag-runs",
			origin:       "https://dagu.example",
			secFetchSite: "same-origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			policy := corsPolicy{
				publicURL: tt.publicURL,
				setupPath: "/api/v1/auth/setup",
			}
			handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodPost, tt.requestURL, nil)
			req.Header.Set("Origin", tt.origin)
			if tt.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.secFetchSite)
			}
			resp := httptest.NewRecorder()

			handler.ServeHTTP(resp, req)

			assert.Equal(t, http.StatusNoContent, resp.Code)
			assert.True(t, called)
		})
	}
}

func newPreflightRequest(requestPath, origin string) *http.Request {
	req := httptest.NewRequest(http.MethodOptions, "http://dagu.example"+requestPath, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	return req
}
