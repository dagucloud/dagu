// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	frontendauth "github.com/dagucloud/dagu/internal/service/frontend/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httplog/v2"
	"github.com/stretchr/testify/assert"
)

func TestRequestLoggerRedactsTrustedProxyHeaders(t *testing.T) {
	var output bytes.Buffer
	httpLogger := httplog.NewLogger("test", httplog.Options{
		LogLevel:       slog.LevelDebug,
		JSON:           true,
		Concise:        true,
		RequestHeaders: true,
		HideRequestHeaders: trustedProxyRequestHeaders(config.AuthTrustedProxy{
			Enabled: true,
			Headers: config.TrustedProxyHeaders{
				User:   "X-Proxy-User",
				Groups: "X-Proxy-Groups",
			},
		}),
		Writer: &output,
	})
	handler := sanitizedRequestLogger(httpLogger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "canonical-user", r.Header.Get("X-Proxy-User"))
		assert.Equal(t, "group-secret", r.Header.Get("X-Proxy-Groups"))
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/proxy-login", nil)
	req.Header.Set("X-Proxy-User", "canonical-user")
	req.Header.Set("X-Proxy-Groups", "group-secret")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.NotContains(t, output.String(), "canonical-user")
	assert.NotContains(t, output.String(), "group-secret")
}

func TestDisabledTrustedProxyRoutePrecedesSPAFallback(t *testing.T) {
	router := chi.NewRouter()
	srv := &Server{trustedProxyCfg: &frontendauth.TrustedProxyLoginConfig{}}
	srv.setupTrustedProxyRoute(router, "/base")
	router.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/base/proxy-login", nil))

	assert.Equal(t, http.StatusNotFound, resp.Code)
	assert.Equal(t, "not found\n", resp.Body.String())
}
