// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	cmnlogger "github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/license"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/dagucloud/dagu/v2/internal/service/trustedproxyprovision"
	"github.com/stretchr/testify/assert"
)

type trustedProxyProvisionStub struct {
	user   *authmodel.User
	isNew  bool
	err    error
	calls  int
	groups []string
}

func (s *trustedProxyProvisionStub) ProcessLogin(_ context.Context, _ string, groups []string) (*authmodel.User, bool, error) {
	s.calls++
	s.groups = append([]string(nil), groups...)
	return s.user, s.isNew, s.err
}

type trustedProxyTokenStub struct {
	result *authservice.TokenResult
	err    error
}

func (s trustedProxyTokenStub) GenerateToken(*authmodel.User) (*authservice.TokenResult, error) {
	return s.result, s.err
}

type trustedProxyLicenseStub struct {
	enabled bool
}

func (s trustedProxyLicenseStub) IsFeatureEnabled(string) bool   { return s.enabled }
func (s trustedProxyLicenseStub) Plan() string                   { return "test" }
func (s trustedProxyLicenseStub) IsGracePeriod() bool            { return false }
func (s trustedProxyLicenseStub) IsCommunity() bool              { return !s.enabled }
func (s trustedProxyLicenseStub) Claims() *license.LicenseClaims { return nil }
func (s trustedProxyLicenseStub) WarningCode() string            { return "" }

func TestTrustedProxyLoginHandlerSuccess(t *testing.T) {
	var logOutput bytes.Buffer
	user := authmodel.NewUser("proxy-user", "", authmodel.RoleDeveloper)
	provision := &trustedProxyProvisionStub{user: user, isNew: true}
	cfg := trustedProxyTestConfig(provision)

	req := httptest.NewRequest(http.MethodGet, "/base/proxy-login", nil)
	req = req.WithContext(cmnlogger.WithLogger(req.Context(), cmnlogger.NewLogger(
		cmnlogger.WithWriter(&logOutput),
		cmnlogger.WithQuiet(),
	)))
	req.Header.Set("X-Proxy-User", "stable-id")
	req.Header.Set("X-Proxy-Groups", "team-a,team-b")
	resp := httptest.NewRecorder()
	TrustedProxyLoginHandler(cfg).ServeHTTP(resp, req)

	assert.Equal(t, http.StatusFound, resp.Code)
	assert.Equal(t, "/base/login?welcome=true#token=token%2Bvalue", resp.Header().Get("Location"))
	assert.Equal(t, []string{"team-a", "team-b"}, provision.groups)
	assert.NotContains(t, logOutput.String(), "stable-id")
	assert.NotContains(t, logOutput.String(), "team-a")
	assert.NotContains(t, logOutput.String(), "team-b")
	assert.NotContains(t, logOutput.String(), "token+value")
	assert.Empty(t, resp.Body.String())
	assertTrustedProxyResponseHeaders(t, resp)
}

func TestTrustedProxyLoginHandlerFailures(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		target        string
		body          string
		chunked       bool
		configure     func(*TrustedProxyLoginConfig, *trustedProxyProvisionStub)
		addIdentity   bool
		duplicateUser bool
		wantStatus    int
		wantBody      string
		wantLocation  string
		wantCalls     int
	}{
		{name: "disabled", method: http.MethodGet, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) { cfg.Enabled = false }, wantStatus: http.StatusNotFound, wantBody: "not found\n"},
		{name: "method", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed, wantBody: "method not allowed\n"},
		{name: "query", method: http.MethodGet, target: "?redirect=/other", wantStatus: http.StatusBadRequest, wantBody: "invalid request\n"},
		{name: "body", method: http.MethodGet, body: "unexpected", wantStatus: http.StatusBadRequest, wantBody: "invalid request\n"},
		{name: "chunked body", method: http.MethodGet, chunked: true, wantStatus: http.StatusBadRequest, wantBody: "invalid request\n"},
		{name: "license", method: http.MethodGet, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) {
			cfg.LicenseChecker = trustedProxyLicenseStub{}
		}, wantStatus: http.StatusForbidden, wantBody: "access denied\n"},
		{name: "setup", method: http.MethodGet, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) {
			cfg.InitialSetupComplete = func(context.Context) (bool, error) { return false, nil }
		}, wantStatus: http.StatusFound, wantLocation: "/base/setup"},
		{name: "setup before license", method: http.MethodGet, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) {
			cfg.InitialSetupComplete = func(context.Context) (bool, error) { return false, nil }
			cfg.LicenseChecker = trustedProxyLicenseStub{}
		}, wantStatus: http.StatusFound, wantLocation: "/base/setup"},
		{name: "setup check error", method: http.MethodGet, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) {
			cfg.InitialSetupComplete = func(context.Context) (bool, error) { return false, errors.New("store unavailable") }
		}, wantStatus: http.StatusInternalServerError, wantBody: "authentication failed\n"},
		{name: "missing user", method: http.MethodGet, wantStatus: http.StatusUnauthorized, wantBody: "proxy identity unavailable\n"},
		{name: "missing required groups", method: http.MethodGet, addIdentity: true, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) { cfg.GroupsRequired = true }, wantStatus: http.StatusUnauthorized, wantBody: "proxy identity unavailable\n"},
		{name: "duplicate user", method: http.MethodGet, addIdentity: true, duplicateUser: true, wantStatus: http.StatusBadRequest, wantBody: "invalid proxy identity\n"},
		{name: "signup disabled", method: http.MethodGet, addIdentity: true, configure: provisionError(trustedproxyprovision.ErrAutoSignupDisabled), wantStatus: http.StatusForbidden, wantBody: "access denied\n", wantCalls: 1},
		{name: "mapping denied", method: http.MethodGet, addIdentity: true, configure: provisionError(trustedproxyprovision.ErrAuthorizationMapping), wantStatus: http.StatusForbidden, wantBody: "access denied\n", wantCalls: 1},
		{name: "disabled user", method: http.MethodGet, addIdentity: true, configure: provisionError(authservice.ErrUserDisabled), wantStatus: http.StatusForbidden, wantBody: "access denied\n", wantCalls: 1},
		{name: "provision error", method: http.MethodGet, addIdentity: true, configure: provisionError(errors.New("store unavailable")), wantStatus: http.StatusInternalServerError, wantBody: "authentication failed\n", wantCalls: 1},
		{name: "missing provisioned user", method: http.MethodGet, addIdentity: true, configure: func(_ *TrustedProxyLoginConfig, provision *trustedProxyProvisionStub) {
			provision.user = nil
		}, wantStatus: http.StatusInternalServerError, wantBody: "authentication failed\n", wantCalls: 1},
		{name: "setup race", method: http.MethodGet, addIdentity: true, configure: provisionError(trustedproxyprovision.ErrInitialSetupRequired), wantStatus: http.StatusFound, wantLocation: "/base/setup", wantCalls: 1},
		{name: "token error", method: http.MethodGet, addIdentity: true, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) {
			cfg.AuthService = trustedProxyTokenStub{err: errors.New("signing failed")}
		}, wantStatus: http.StatusInternalServerError, wantBody: "authentication failed\n", wantCalls: 1},
		{name: "missing token result", method: http.MethodGet, addIdentity: true, configure: func(cfg *TrustedProxyLoginConfig, _ *trustedProxyProvisionStub) {
			cfg.AuthService = trustedProxyTokenStub{}
		}, wantStatus: http.StatusInternalServerError, wantBody: "authentication failed\n", wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provision := &trustedProxyProvisionStub{user: authmodel.NewUser("proxy-user", "", authmodel.RoleViewer)}
			cfg := trustedProxyTestConfig(provision)
			if tt.configure != nil {
				tt.configure(cfg, provision)
			}
			target := "/base/proxy-login" + tt.target
			var body *strings.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(tt.method, target, body)
			if tt.addIdentity {
				req.Header.Add("X-Proxy-User", "stable-id")
			}
			if tt.chunked {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			if tt.duplicateUser {
				req.Header.Add("X-Proxy-User", "other-id")
			}
			resp := httptest.NewRecorder()
			TrustedProxyLoginHandler(cfg).ServeHTTP(resp, req)

			assert.Equal(t, tt.wantStatus, resp.Code)
			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, resp.Body.String())
			}
			assert.Equal(t, tt.wantLocation, resp.Header().Get("Location"))
			if tt.wantStatus == http.StatusMethodNotAllowed {
				assert.Equal(t, http.MethodGet, resp.Header().Get("Allow"))
			}
			assert.Equal(t, tt.wantCalls, provision.calls)
			assertTrustedProxyResponseHeaders(t, resp)
		})
	}
}

func trustedProxyTestConfig(provision TrustedProxyProvisioner) *TrustedProxyLoginConfig {
	return &TrustedProxyLoginConfig{
		Enabled:        true,
		UserHeader:     "X-Proxy-User",
		GroupsHeader:   "X-Proxy-Groups",
		GroupsRequired: false,
		Provision:      provision,
		AuthService: trustedProxyTokenStub{result: &authservice.TokenResult{
			Token:     "token+value",
			ExpiresAt: time.Now().Add(time.Hour),
		}},
		InitialSetupComplete: func(context.Context) (bool, error) { return true, nil },
		LoginBasePath:        "/base",
	}
}

func provisionError(err error) func(*TrustedProxyLoginConfig, *trustedProxyProvisionStub) {
	return func(_ *TrustedProxyLoginConfig, provision *trustedProxyProvisionStub) {
		provision.err = err
	}
}

func assertTrustedProxyResponseHeaders(t *testing.T, resp *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, "no-store", resp.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", resp.Header().Get("Pragma"))
	assert.Equal(t, "no-referrer", resp.Header().Get("Referrer-Policy"))
}
