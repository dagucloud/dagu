// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/internal/auth"
)

// TokenValidator defines the interface for validating tokens and retrieving users.
// This allows the middleware to work with any token validation implementation.
type TokenValidator interface {
	GetUserFromToken(ctx context.Context, token string) (*auth.User, error)
}

// APIKeyValidator defines the interface for validating standalone API keys.
// API keys have their own role assignment and are used for programmatic access.
type APIKeyValidator interface {
	ValidateAPIKey(ctx context.Context, keySecret string) (*auth.APIKey, error)
}

// BuiltinAuthMiddleware creates middleware that validates JWT tokens
// and injects the authenticated user into the request context.
// BuiltinAuthMiddleware returns an HTTP middleware that enforces authentication for requests
// whose paths are not listed in publicPaths or do not start with a publicPathPrefix.
// For non-public requests it extracts a Bearer token, validates it via the provided TokenValidator,
// and on success injects the authenticated user into the request context;
// on failure it writes a 401 JSON error response.
func BuiltinAuthMiddleware(svc TokenValidator, publicPaths, publicPathPrefixes []string) func(http.Handler) http.Handler {
	// Build a set for O(1) lookup
	publicSet := make(map[string]struct{}, len(publicPaths))
	for _, p := range publicPaths {
		publicSet[p] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if path is public (exact match)
			if isPublicPath(r.URL.Path, publicSet) {
				next.ServeHTTP(w, r)
				return
			}

			// Check if path matches a public prefix
			for _, prefix := range publicPathPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			token := extractBearerToken(r)
			if token == "" {
				writeAuthError(w, http.StatusUnauthorized, "auth.unauthorized", "Authentication required")
				return
			}

			user, err := svc.GetUserFromToken(r.Context(), token)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "auth.token_invalid", "Invalid or expired token")
				return
			}

			// Inject user into context
			ctx := auth.WithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isPublicPath checks if the request path matches any public path.
// plus variants with a trailing slash added or removed.
func isPublicPath(path string, publicSet map[string]struct{}) bool {
	// Exact match
	if _, ok := publicSet[path]; ok {
		return true
	}
	// Try with trailing slash removed
	withoutSlash := strings.TrimSuffix(path, "/")
	if _, ok := publicSet[withoutSlash]; ok {
		return true
	}
	// Try with trailing slash added
	if path != "" && !strings.HasSuffix(path, "/") {
		if _, ok := publicSet[path+"/"]; ok {
			return true
		}
	}
	return false
}

// RequireRole creates middleware that checks if the authenticated user
// ("auth.forbidden").
func RequireRole(roles ...auth.Role) func(http.Handler) http.Handler {
	roleSet := make(map[auth.Role]struct{}, len(roles))
	for _, r := range roles {
		roleSet[r] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "auth.unauthorized", "Authentication required")
				return
			}

			if _, allowed := roleSet[user.Role]; !allowed {
				writeAuthError(w, http.StatusForbidden, "auth.forbidden", "Insufficient permissions")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin returns a middleware that allows only authenticated users with the admin role.
func RequireAdmin() func(http.Handler) http.Handler {
	return RequireRole(auth.RoleAdmin)
}

// RequireWrite returns middleware that permits requests only for users with the Admin, Manager, or Developer role.
func RequireWrite() func(http.Handler) http.Handler {
	return RequireRole(auth.RoleAdmin, auth.RoleManager, auth.RoleDeveloper)
}

// RequireExecute is middleware that permits requests only from users with the Admin, Manager, Developer, or Operator role.
func RequireExecute() func(http.Handler) http.Handler {
	return RequireRole(auth.RoleAdmin, auth.RoleManager, auth.RoleDeveloper, auth.RoleOperator)
}

// extractBearerToken extracts the bearer token from the request's Authorization header.
// It returns the token string without the "Bearer " prefix, or an empty string if the header is missing or not a Bearer token.
func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		return ""
	}

	return strings.TrimPrefix(authHeader, bearerPrefix)
}

// authErrorBody is the JSON payload for auth error responses. It matches the
// flat shape of #/components/schemas/Error in the OpenAPI spec so that
// generated clients and contract tests parse responses correctly.
type authErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeAuthError writes an HTTP JSON error response with the given status,
// error code, and message. The response body is a flat JSON object
// {"code":"…","message":"…"} matching the OpenAPI Error schema.
func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(authErrorBody{Code: code, Message: message})
}
