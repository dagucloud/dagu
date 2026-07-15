// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/cors"
)

type corsPolicy struct {
	allowedOrigins []string
	publicURL      string
	setupPath      string
}

func (p corsPolicy) middleware(next http.Handler) http.Handler {
	wrapped := next
	if len(p.allowedOrigins) > 0 {
		wrapped = cors.Handler(cors.Options{
			AllowedOrigins:   p.allowedOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Content-Type", "Authorization", "Content-Encoding", "Accept", "MCP-Protocol-Version", "Mcp-Session-Id", "Last-Event-ID"},
			ExposedHeaders:   []string{"Mcp-Session-Id"},
			AllowCredentials: !slices.Contains(p.allowedOrigins, "*"),
			MaxAge:           300,
		})(next)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && p.isCrossOrigin(r, origin) {
			if p.isSetupPath(r.URL.Path) || !p.allowsOrigin(origin) {
				http.Error(w, "cross-origin request denied", http.StatusForbidden)
				return
			}
		}
		wrapped.ServeHTTP(w, r)
	})
}

func (p corsPolicy) isCrossOrigin(r *http.Request, origin string) bool {
	sourceOrigin := canonicalOrigin(origin)
	if sourceOrigin == "" {
		return true
	}
	if sourceOrigin == p.targetOrigin(r) {
		return false
	}

	// Fetch Metadata preserves same-origin classification through reverse proxies
	// that do not expose the public scheme and host to the application.
	return !strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
}

func (p corsPolicy) targetOrigin(r *http.Request) string {
	if origin := canonicalOrigin(p.publicURL); origin != "" {
		return origin
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return canonicalOrigin(scheme + "://" + r.Host)
}

func (p corsPolicy) allowsOrigin(origin string) bool {
	origin = strings.ToLower(strings.TrimSpace(origin))
	for _, candidate := range p.allowedOrigins {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "*" || candidate == origin {
			return true
		}
		if wildcardIndex := strings.IndexByte(candidate, '*'); wildcardIndex >= 0 {
			prefix := candidate[:wildcardIndex]
			suffix := candidate[wildcardIndex+1:]
			if strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}
	return false
}

func (p corsPolicy) isSetupPath(requestPath string) bool {
	return strings.TrimRight(requestPath, "/") == strings.TrimRight(p.setupPath, "/")
}

func canonicalOrigin(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}
