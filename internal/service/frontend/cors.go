// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"fmt"
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

// originWarnings describes allowed-origin entries that allow more, or less,
// than they appear to. A bare "*" is reported by the config loader.
func (p corsPolicy) originWarnings() []string {
	var warnings []string
	for _, entry := range p.allowedOrigins {
		if problem := allowedOriginProblem(entry); problem != "" {
			warnings = append(warnings, fmt.Sprintf("cors_allowed_origins entry %q %s",
				redactOriginUserinfo(entry), problem))
		}
	}
	return warnings
}

// redactOriginUserinfo masks everything between the scheme and the last "@",
// so credentials put in an entry by mistake are not logged. It works on the
// raw string because the entries it reports are often not parseable URLs, and
// it masks generously rather than risk printing part of a secret.
func redactOriginUserinfo(entry string) string {
	start := 0
	if i := strings.Index(entry, "://"); i >= 0 {
		start = i + len("://")
	}
	at := strings.LastIndex(entry[start:], "@")
	if at < 0 {
		return entry
	}
	return entry[:start] + "xxxxx" + entry[start+at:]
}

// allowedOriginProblem explains how allowsOrigin treats entry differently
// than it reads, or returns "" when the entry matches as written.
func allowedOriginProblem(entry string) string {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if entry == "*" {
		return ""
	}
	if prefix, suffix, ok := strings.Cut(entry, "*"); ok {
		if isSubdomainPattern(prefix, suffix) {
			return ""
		}
		return "is matched as a raw prefix and suffix, so it may allow unintended origins or none; use scheme://*.domain[:port] to allow subdomains"
	}
	if canonicalOrigin(entry) == "" {
		return "is not an http(s) origin of the form scheme://host[:port]"
	}
	if parsed, err := url.Parse(entry); err == nil &&
		((parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "") {
		return "includes a path, query, or fragment; only scheme://host[:port] is enforced"
	}
	return ""
}

// isSubdomainPattern reports whether a wildcard entry split at its "*" has the
// form scheme://*.domain[:port], which matches only subdomains of domain.
func isSubdomainPattern(prefix, suffix string) bool {
	if prefix != "http://" && prefix != "https://" {
		return false
	}
	if !strings.HasPrefix(suffix, ".") || strings.Contains(suffix, "*") {
		return false
	}
	// Browsers send canonical origins, so a suffix that canonicalization would
	// change, such as one with a path or default port, never matches.
	sample := prefix + "a" + suffix
	return canonicalOrigin(sample) == sample
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
