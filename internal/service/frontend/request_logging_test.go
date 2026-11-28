// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	frontendauth "github.com/dagucloud/dagu/v2/internal/service/frontend/auth"
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

func TestRequestLoggerRedactsCallbackParams(t *testing.T) {
	var output bytes.Buffer
	httpLogger := httplog.NewLogger("test", httplog.Options{JSON: true, Concise: true, Writer: &output})
	handler := sanitizedRequestLogger(httpLogger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "auth-code", r.URL.Query().Get("code"))
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=auth-code&state=sealed-state&scope=email", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Contains(t, output.String(), "scope=email")
	assert.NotContains(t, output.String(), "auth-code")
	assert.NotContains(t, output.String(), "sealed-state")
}

// A pair that the query parser drops, here one joined by a semicolon, is
// left out of the log, so it cannot carry a secret past the redaction.
func TestRequestLoggerDropsMalformedQueryPairs(t *testing.T) {
	var output bytes.Buffer
	httpLogger := httplog.NewLogger("test", httplog.Options{JSON: true, Concise: true, Writer: &output})
	handler := sanitizedRequestLogger(httpLogger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=auth-code;state=sealed-state&scope=email", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Contains(t, output.String(), "scope=email")
	assert.NotContains(t, output.String(), "auth-code")
	assert.NotContains(t, output.String(), "sealed-state")
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

// testInvite is shaped like a worker invite.
var testInvite = "dagu1." + strings.Repeat("A", 43) + "." + strings.Repeat("B", 43)

// An invite pasted into a path or a query value reaches the handler as
// sent and stays out of the access log.
func TestRequestLoggerRedactsInvites(t *testing.T) {
	var output bytes.Buffer
	httpLogger := httplog.NewLogger("test", httplog.Options{JSON: true, Concise: true, Writer: &output})
	handler := sanitizedRequestLogger(httpLogger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/workers/invites/"+testInvite, r.URL.Path)
		assert.Equal(t, testInvite, r.URL.Query().Get("expiresIn"))
		w.WriteHeader(http.StatusNotFound)
	}))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workers/invites/"+testInvite+"?expiresIn="+testInvite+"&remoteNode=local", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Contains(t, output.String(), "/api/v1/workers/invites/")
	assert.Contains(t, output.String(), "remoteNode=local")
	assert.NotContains(t, output.String(), testInvite)
}
