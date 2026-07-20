// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestBuiltinOIDCHandlersRequireInitialSetup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		handler func(*BuiltinOIDCConfig) http.HandlerFunc
	}{
		{name: "login", path: "/dagu/oidc-login", handler: BuiltinOIDCLoginHandler},
		{name: "callback", path: "/dagu/oidc-callback", handler: BuiltinOIDCCallbackHandler},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &BuiltinOIDCConfig{
				LoginBasePath: "/dagu",
				InitialSetupComplete: func(context.Context) (bool, error) {
					return false, nil
				},
			}
			recorder := httptest.NewRecorder()

			tt.handler(cfg).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))

			assert.Equal(t, http.StatusFound, recorder.Code)
			assert.Equal(t, "/dagu/setup", recorder.Header().Get("Location"))
		})
	}
}

func TestBuiltinOIDCLoginHandlerFailsClosedWhenSetupCheckFails(t *testing.T) {
	t.Parallel()

	cfg := &BuiltinOIDCConfig{
		LoginBasePath: "/dagu",
		InitialSetupComplete: func(context.Context) (bool, error) {
			return false, errors.New("storage unavailable")
		},
	}
	recorder := httptest.NewRecorder()

	BuiltinOIDCLoginHandler(cfg).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/dagu/oidc-login", nil),
	)

	assert.Equal(t, http.StatusFound, recorder.Code)
	assert.Equal(t, "/dagu/setup", recorder.Header().Get("Location"))
}

func TestBuiltinOIDCLoginHandlerAllowsCompletedSetup(t *testing.T) {
	t.Parallel()

	cfg := &BuiltinOIDCConfig{
		OAuth2Config: &oauth2.Config{
			Endpoint: oauth2.Endpoint{AuthURL: "https://idp.example.com/authorize"},
		},
		LoginBasePath: "/dagu",
		InitialSetupComplete: func(context.Context) (bool, error) {
			return true, nil
		},
	}
	recorder := httptest.NewRecorder()

	BuiltinOIDCLoginHandler(cfg).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/dagu/oidc-login", nil),
	)

	assert.Equal(t, http.StatusFound, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Location"), "https://idp.example.com/authorize?")
}

func TestBuiltinOIDCCallbackHandlerAllowsCompletedSetup(t *testing.T) {
	t.Parallel()

	cfg := &BuiltinOIDCConfig{
		LoginBasePath: "/dagu",
		InitialSetupComplete: func(context.Context) (bool, error) {
			return true, nil
		},
	}
	recorder := httptest.NewRecorder()

	BuiltinOIDCCallbackHandler(cfg).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/dagu/oidc-callback", nil),
	)

	assert.Equal(t, http.StatusFound, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Location"), "/dagu/login?error=Authentication+failed")
}
