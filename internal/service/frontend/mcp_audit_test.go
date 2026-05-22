// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/stretchr/testify/require"
)

func TestMCPAuditSeedMiddlewareDefaultsWorkspace(t *testing.T) {
	srv := &Server{}
	handler := srv.mcpAuditSeedMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		source, ok := audit.SourceContextFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, "default", source.RequestedWorkspace)
		require.Equal(t, "default", source.ResolvedWorkspace)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", nil))
}

func TestCredentialTypeFromRequestUsesDocumentedValues(t *testing.T) {
	basicReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	basicReq.SetBasicAuth("admin", "secret")
	require.Equal(t, "basic", credentialTypeFromRequest(basicReq))

	apiKeyReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	apiKeyReq.Header.Set("Authorization", "Bearer dagu_invalid")
	require.Equal(t, "api_key", credentialTypeFromRequest(apiKeyReq))

	jwtReq := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	jwtReq.Header.Set("Authorization", "Bearer jwt-token")
	require.Equal(t, "jwt", credentialTypeFromRequest(jwtReq))

	require.Equal(t, "none", credentialTypeFromRequest(httptest.NewRequest(http.MethodGet, "/mcp", nil)))
}
