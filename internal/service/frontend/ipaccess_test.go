// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

func TestIPAccessPolicyDirectPeer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		allowedIPs []string
		remoteAddr string
		wantStatus int
	}{
		{name: "Disabled", remoteAddr: "invalid", wantStatus: http.StatusNoContent},
		{name: "ExactIPv4", allowedIPs: []string{"203.0.113.10"}, remoteAddr: "203.0.113.10:1234", wantStatus: http.StatusNoContent},
		{name: "IPv4CIDR", allowedIPs: []string{"203.0.113.0/24"}, remoteAddr: "203.0.113.10:1234", wantStatus: http.StatusNoContent},
		{name: "IPv6CIDR", allowedIPs: []string{"2001:db8::/32"}, remoteAddr: "[2001:db8::10]:1234", wantStatus: http.StatusNoContent},
		{name: "IPv4MappedIPv6", allowedIPs: []string{"203.0.113.10"}, remoteAddr: "[::ffff:203.0.113.10]:1234", wantStatus: http.StatusNoContent},
		{name: "Denied", allowedIPs: []string{"203.0.113.10"}, remoteAddr: "198.51.100.7:1234", wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			policy, err := newIPAccessPolicy(config.IPAccessConfig{AllowedIPs: tt.allowedIPs})
			require.NoError(t, err)

			called := false
			handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			assert.Equal(t, tt.wantStatus, recorder.Code)
			assert.Equal(t, tt.wantStatus == http.StatusNoContent, called)
			if tt.wantStatus == http.StatusForbidden {
				assert.Equal(t, "access denied\n", recorder.Body.String())
			}
		})
	}
}

func TestIPAccessPolicyTrustedProxy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remoteAddr string
		headers    http.Header
		wantStatus int
	}{
		{
			name:       "ForwardedClient",
			remoteAddr: "10.0.0.2:1234",
			headers:    http.Header{"X-Forwarded-For": []string{"198.51.100.25, 10.0.0.1"}},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ForwardedClientWithPort",
			remoteAddr: "10.0.0.2:1234",
			headers:    http.Header{"X-Forwarded-For": []string{"198.51.100.25:4567"}},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "RealIPFallback",
			remoteAddr: "10.0.0.2:1234",
			headers:    http.Header{"X-Real-Ip": []string{"198.51.100.25"}},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "SpoofedLeftmostForwardedAddress",
			remoteAddr: "10.0.0.2:1234",
			headers:    http.Header{"X-Forwarded-For": []string{"198.51.100.25, 203.0.113.7, 10.0.0.1"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "MalformedForwardedAddress",
			remoteAddr: "10.0.0.2:1234",
			headers:    http.Header{"X-Forwarded-For": []string{"198.51.100.25, invalid"}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "TrustedPeerWithoutHeaders",
			remoteAddr: "10.0.0.2:1234",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			policy, err := newIPAccessPolicy(config.IPAccessConfig{
				AllowedIPs:     []string{"198.51.100.25"},
				TrustedProxies: []string{"10.0.0.0/8"},
			})
			require.NoError(t, err)

			handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			req.Header = tt.headers
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			assert.Equal(t, tt.wantStatus, recorder.Code)
		})
	}
}

func TestIPAccessPolicyIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	t.Parallel()

	policy, err := newIPAccessPolicy(config.IPAccessConfig{
		AllowedIPs:     []string{"198.51.100.25"},
		TrustedProxies: []string{"10.0.0.0/8"},
	})
	require.NoError(t, err)
	handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.25")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestNewIPAccessPolicyRejectsInvalidNetworks(t *testing.T) {
	t.Parallel()

	_, err := newIPAccessPolicy(config.IPAccessConfig{AllowedIPs: []string{"invalid"}})

	require.ErrorContains(t, err, `allowed IP "invalid"`)
}
