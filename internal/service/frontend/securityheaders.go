// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import "net/http"

// securityHeadersMiddleware adds defensive HTTP response headers to every response.
// Pass tlsEnabled=true to also emit Strict-Transport-Security (HSTS).
func securityHeadersMiddleware(tlsEnabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), payment=()")
			// Disable the legacy XSS auditor; modern browsers removed it and it
			// caused false-positive page blocking. CSP is the correct mitigation.
			h.Set("X-XSS-Protection", "0")
			if tlsEnabled {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
