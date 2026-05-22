// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"net/http"
	"strings"

	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/google/uuid"
)

func (srv *Server) mcpAuditSeedMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			source := &audit.SourceContext{
				Source:        "mcp",
				Surface:       string(authmodel.APIKeySurfaceMCP),
				RequestID:     uuid.NewString(),
				CorrelationID: uuid.NewString(),
				Transport:     "streamable_http",
			}
			if requestedWorkspace := strings.TrimSpace(r.URL.Query().Get("workspace")); requestedWorkspace != "" {
				source.RequestedWorkspace = requestedWorkspace
				source.ResolvedWorkspace = canonicalMCPWorkspace(requestedWorkspace)
			}
			next.ServeHTTP(w, r.WithContext(audit.WithSourceContext(r.Context(), source)))
		})
	}
}

func (srv *Server) mcpAuditSubjectMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := withMCPAuditCredentialContext(r.Context(), nil)
			if apiKey, ok := authmodel.APIKeyFromContext(ctx); ok {
				if user, ok := authmodel.UserForAPIKeyAttribution(apiKey); ok {
					ctx = authmodel.WithUser(ctx, user)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (srv *Server) logMCPAuthDenied(r *http.Request, reason string, apiKey *authmodel.APIKey) {
	if srv == nil || srv.apiV1 == nil || r == nil {
		return
	}
	ctx := withMCPAuditCredentialContext(r.Context(), apiKey)
	if user, ok := authmodel.UserForAPIKeyAttribution(apiKey); ok {
		ctx = authmodel.WithUser(ctx, user)
	}
	details := map[string]any{
		"result":        "denied",
		"denial_reason": reason,
		"resource_type": "mcp_request",
		"resource_id":   r.URL.Path,
		"http_method":   r.Method,
	}
	srv.apiV1.LogAudit(ctx, audit.CategoryMCP, "mcp.request.denied", details)
}

func withMCPAuditCredentialContext(ctx context.Context, deniedAPIKey *authmodel.APIKey) context.Context {
	source, ok := audit.SourceContextFromContext(ctx)
	if !ok || source == nil {
		source = &audit.SourceContext{
			Source:        "mcp",
			Surface:       string(authmodel.APIKeySurfaceMCP),
			RequestID:     uuid.NewString(),
			CorrelationID: uuid.NewString(),
			Transport:     "streamable_http",
		}
	}
	if source.Source == "" {
		source.Source = "mcp"
	}
	if source.Surface == "" {
		source.Surface = string(authmodel.APIKeySurfaceMCP)
	}
	if source.Transport == "" {
		source.Transport = "streamable_http"
	}

	if user, ok := authmodel.UserFromContext(ctx); ok && user != nil {
		source.SubjectID = user.ID
		source.SubjectName = user.Username
		source.SubjectType = "user"
	}

	apiKey := deniedAPIKey
	if apiKey == nil {
		apiKey, _ = authmodel.APIKeyFromContext(ctx)
	}
	if apiKey != nil {
		audit.ApplyAPIKeyCredential(source, apiKey)
	} else if source.CredentialType == "" {
		source.CredentialType = credentialTypeFromContext(ctx)
	}

	return audit.WithSourceContext(ctx, source)
}

func credentialTypeFromContext(ctx context.Context) string {
	if _, ok := authmodel.UserFromContext(ctx); ok {
		return "session"
	}
	return "none"
}

func canonicalMCPWorkspace(workspace string) string {
	if workspace == "" {
		return "default"
	}
	return workspace
}
