// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/service/frontend/api/pathutil"
)

// rawRemoteAddrKey is the context key for the pre-RealIP remote address.
type rawRemoteAddrKey struct{}

// PreserveRawRemoteAddr stores r.RemoteAddr in the request context before
// chi's middleware.RealIP (or any other middleware) can overwrite it.
// It must be registered before middleware.RealIP in the middleware chain so
// that LoginRateLimitMiddleware can derive the rate-limit key from the true
// TCP source address rather than an attacker-controlled forwarded header.
func PreserveRawRemoteAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), rawRemoteAddrKey{}, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Options configures the authentication middleware.
type Options struct {
	Realm            string
	BasicAuthEnabled bool
	Creds            map[string]string
	PublicPaths      []string
	// PublicPathPrefixes are path prefixes that bypass authentication.
	// Any path starting with one of these prefixes will be allowed without auth.
	PublicPathPrefixes []string
	// JWTValidator validates JWT tokens for builtin auth mode.
	// When set, JWT Bearer tokens are accepted as an authentication method.
	JWTValidator TokenValidator
	// APIKeyValidator validates API keys with roles.
	// When set, API keys with the "dagu_" prefix are accepted as an authentication method.
	APIKeyValidator APIKeyValidator
	// RequiredAPIKeySurface, when set, requires API keys to include this surface.
	RequiredAPIKeySurface auth.APIKeySurface
	// OnDenied is called when the middleware rejects a request after auth evaluation.
	OnDenied func(r *http.Request, reason string, apiKey *auth.APIKey)
	// AuthRequired indicates whether authentication is required.
	// When false (e.g., auth mode "none"), credentials are validated if provided
	// but unauthenticated requests are allowed through.
	AuthRequired bool
}

const (
	DenialReasonAPIKeySurfaceDenied = "api_key_surface_denied"
	DenialReasonAuthFailed          = "auth_failed"
	DenialReasonMissingCredentials  = "missing_credentials"
)

// QueryTokenMiddleware converts a "token" query parameter into an Authorization
// Bearer header. This bridges browser APIs that cannot set custom headers
// (EventSource, WebSocket) with the standard auth middleware.
// If an Authorization header is already present, the query parameter is ignored.
func QueryTokenMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				next.ServeHTTP(w, r)
				return
			}

			token := r.URL.Query().Get("token")
			if token != "" {
				r2 := r.Clone(r.Context())
				r2.Header.Set("Authorization", "Bearer "+token)
				next.ServeHTTP(w, r2)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ClientIPMiddleware creates an HTTP middleware that adds the client IP to the request context.
// This should be applied before authentication middleware to ensure IP is available for audit logging.
func ClientIPMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithClientIP(r.Context(), GetClientIP(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Middleware creates an HTTP middleware for authentication.
// It supports multiple authentication methods simultaneously:
// - JWT Bearer tokens (if JWTValidator is set)
// - API keys with "dagu_" prefix (if APIKeyValidator is set)
// - HTTP Basic Auth (if BasicAuthEnabled)
// All configured methods work at the same time.
func Middleware(opts Options) func(next http.Handler) http.Handler {
	publicPaths := make(map[string]struct{}, len(opts.PublicPaths))
	for _, p := range opts.PublicPaths {
		publicPaths[pathutil.NormalizePath(p)] = struct{}{}
	}

	// Process public path prefixes - ensure they have leading slash but preserve trailing slash
	// The trailing slash is important for prefixes: "/api/v1/webhooks/" should only match
	// paths like "/api/v1/webhooks/foo", not "/api/v1/webhooks" itself
	publicPrefixes := make([]string, 0, len(opts.PublicPathPrefixes))
	for _, p := range opts.PublicPathPrefixes {
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		publicPrefixes = append(publicPrefixes, p)
	}

	jwtEnabled := opts.JWTValidator != nil
	apiKeyEnabled := opts.APIKeyValidator != nil
	anyAuthEnabled := opts.BasicAuthEnabled || jwtEnabled || apiKeyEnabled

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			normalizedPath := pathutil.NormalizePath(r.URL.Path)

			// Allow unauthenticated access to explicitly configured public paths.
			if _, ok := publicPaths[normalizedPath]; ok {
				next.ServeHTTP(w, r)
				return
			}

			// Allow unauthenticated access to paths matching public prefixes.
			for _, prefix := range publicPrefixes {
				if strings.HasPrefix(normalizedPath, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// If no auth is enabled, skip authentication
			if !anyAuthEnabled {
				next.ServeHTTP(w, r)
				return
			}

			// Extract bearer token once for both JWT and API key checks
			var bearerToken string
			if jwtEnabled || apiKeyEnabled {
				bearerToken = extractBearerToken(r)
			}
			denialReason := DenialReasonMissingCredentials
			if bearerToken != "" {
				denialReason = DenialReasonAuthFailed
			}

			// Try JWT token authentication if enabled (for builtin auth mode)
			if jwtEnabled && bearerToken != "" {
				user, err := opts.JWTValidator.GetUserFromToken(r.Context(), bearerToken)
				if err == nil {
					ctx := auth.WithUser(r.Context(), user)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// JWT validation failed, continue to try other methods
			}

			// Try API key authentication if enabled
			// API keys have the "dagu_" prefix and have their own role assignment
			if apiKeyEnabled && bearerToken != "" && strings.HasPrefix(bearerToken, "dagu_") {
				apiKey, err := opts.APIKeyValidator.ValidateAPIKey(r.Context(), bearerToken)
				if err == nil {
					apiKey = auth.NormalizeAPIKeyMetadata(apiKey)
					if opts.RequiredAPIKeySurface != "" && !auth.HasAPIKeySurface(apiKey.AllowedSurfaces, opts.RequiredAPIKeySurface) {
						callDenied(opts, r, DenialReasonAPIKeySurfaceDenied, apiKey)
						http.Error(w, "API key is not allowed for this surface", http.StatusForbidden)
						return
					}
					syntheticUser := &auth.User{
						ID:              "apikey:" + apiKey.ID,
						Username:        "apikey:" + apiKey.Name,
						Role:            apiKey.Role,
						WorkspaceAccess: auth.CloneWorkspaceAccess(apiKey.WorkspaceAccess),
					}
					ctx := auth.WithAPIKey(auth.WithUser(r.Context(), syntheticUser), apiKey)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// API key validation failed, continue to try other methods
			}

			// Try Basic Auth if enabled
			if opts.BasicAuthEnabled {
				if user, pass, ok := r.BasicAuth(); ok {
					// Credentials were provided - must validate
					if checkBasicAuth(user, pass, opts.Creds) {
						basicUser := &auth.User{
							ID:              user,
							Username:        user,
							Role:            auth.RoleAdmin,
							WorkspaceAccess: auth.AllWorkspaceAccess(),
						}
						ctx := auth.WithUser(r.Context(), basicUser)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					// Invalid credentials - always reject
					callDenied(opts, r, DenialReasonAuthFailed, nil)
					requireBasicAuth(w, opts.Realm)
					return
				}
			}

			// No credentials provided
			// If auth is not required (e.g., mode "none"), allow the request through
			if !opts.AuthRequired {
				next.ServeHTTP(w, r)
				return
			}

			// Auth is required - send appropriate challenge
			if opts.BasicAuthEnabled {
				callDenied(opts, r, denialReason, nil)
				requireBasicAuth(w, opts.Realm)
				return
			}

			callDenied(opts, r, denialReason, nil)
			requireBearerAuth(w, opts.Realm)
		})
	}
}

func callDenied(opts Options, r *http.Request, reason string, apiKey *auth.APIKey) {
	if opts.OnDenied != nil {
		opts.OnDenied(r, reason, apiKey)
	}
}

// checkBasicAuth validates the username and password.
func checkBasicAuth(user, pass string, validCreds map[string]string) bool {
	credPass, credUserOk := validCreds[user]
	if !credUserOk {
		return false
	}

	// Use constant time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(pass), []byte(credPass)) == 1
}

// requireBasicAuth sends a 401 response with Basic auth challenge.
func requireBasicAuth(w http.ResponseWriter, realm string) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, realm))
	w.WriteHeader(http.StatusUnauthorized)
}

// requireBearerAuth sends a 401 response with Bearer auth challenge.
func requireBearerAuth(w http.ResponseWriter, realm string) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s"`, realm))
	w.WriteHeader(http.StatusUnauthorized)
}

// GetClientIP extracts the client IP address from the request.
// It checks X-Forwarded-For and X-Real-IP headers for proxied requests.
// Port suffixes (e.g. from HAProxy source-port annotation) are stripped so
// that all connections from the same client IP share one rate-limit bucket.
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		raw := xff
		if before, _, ok := strings.Cut(xff, ","); ok {
			raw = before
		}
		return stripPort(strings.TrimSpace(raw))
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return stripPort(strings.TrimSpace(xri))
	}

	// Fall back to RemoteAddr - use net.SplitHostPort for proper IPv6 handling
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Return as-is if parsing fails (e.g., no port present)
		return r.RemoteAddr
	}
	return host
}

// stripPort removes a trailing ":port" from an IP string.
// It handles bare IPv4 ("1.2.3.4:1234"), bracketed IPv6 ("[::1]:1234"),
// and plain addresses without a port.
func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr // no port present; return unchanged
	}
	return host
}
