// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	t.Parallel()

	noop := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		tlsEnabled bool
		wantHSTS   bool
	}{
		{name: "without TLS", tlsEnabled: false, wantHSTS: false},
		{name: "with TLS", tlsEnabled: true, wantHSTS: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := securityHeadersMiddleware(tt.tlsEnabled)(noop)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			h := rec.Result().Header
			assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
			assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
			assert.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"))
			assert.Equal(t, "geolocation=(), camera=(), microphone=(), payment=()", h.Get("Permissions-Policy"))
			assert.Equal(t, "0", h.Get("X-XSS-Protection"))

			hsts := h.Get("Strict-Transport-Security")
			if tt.wantHSTS {
				assert.Equal(t, "max-age=31536000; includeSubDomains", hsts)
			} else {
				assert.Empty(t, hsts)
			}
		})
	}
}

func TestSecurityHeadersMiddlewarePassesThrough(t *testing.T) {
	t.Parallel()

	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	handler := securityHeadersMiddleware(false)(inner)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dags", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusTeapot, rec.Code)
}
