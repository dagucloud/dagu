// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/cors"
)

type corsPolicy struct {
	allowedOrigins []string
	publicURL      string
	setupPath      string
}

func (p corsPolicy) middleware(next http.Handler) http.Handler {
	corsConfigured := len(p.allowedOrigins) > 0
	wrapped := next
	if corsConfigured {
		allowAllOrigins := p.allowsAllOrigins()
		options := cors.Options{
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Content-Type", "Authorization", "Content-Encoding", "Accept", "MCP-Protocol-Version", "Mcp-Method", "Mcp-Name", "Mcp-Session-Id", "Last-Event-ID"},
			ExposedHeaders:   []string{"Mcp-Session-Id"},
			AllowCredentials: !allowAllOrigins,
			MaxAge:           300,
		}
		if allowAllOrigins {
			options.AllowedOrigins = []string{"*"}
		} else {
			options.AllowOriginFunc = func(_ *http.Request, origin string) bool {
				return p.allowsOrigin(origin)
			}
		}
		wrapped = cors.Handler(options)(next)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !corsConfigured {
			w.Header().Add("Vary", "Origin")
		}
		origin := r.Header.Get("Origin")
		if origin != "" && p.isCrossOrigin(r, origin) {
			if p.isSetupPath(r.URL.Path) || !p.allowsOrigin(origin) {
				if corsConfigured {
					w.Header().Add("Vary", "Origin")
				}
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
	if sourceOrigin == requestOrigin(r) || sourceOrigin == canonicalOrigin(p.publicURL) {
		return false
	}

	// Fetch Metadata preserves same-origin classification through reverse proxies
	// that do not expose the public scheme and host to the application.
	return !strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return canonicalOrigin(scheme + "://" + r.Host)
}

func (p corsPolicy) allowsOrigin(origin string) bool {
	origin = strings.ToLower(strings.TrimSpace(origin))
	canonicalRequestOrigin := canonicalOrigin(origin)
	for _, candidate := range p.allowedOrigins {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "*" {
			return true
		}
		if prefix, suffix, ok := strings.Cut(candidate, "*"); ok {
			if len(origin) >= len(prefix)+len(suffix) &&
				strings.HasPrefix(origin, prefix) && strings.HasSuffix(origin, suffix) {
				return true
			}
			continue
		}
		if candidate == origin ||
			(canonicalRequestOrigin != "" && canonicalOrigin(candidate) == canonicalRequestOrigin) {
			return true
		}
	}
	return false
}

func (p corsPolicy) allowsAllOrigins() bool {
	for _, origin := range p.allowedOrigins {
		if strings.TrimSpace(origin) == "*" {
			return true
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

	scheme := strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return ""
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return scheme + "://" + host
}
