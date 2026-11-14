// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSetupPath = "/api/v1/auth/setup"

// A DNS-rebinding page sends its own domain as Host. Over plain HTTP a
// same-origin GET carries neither Origin nor Fetch Metadata, so the name is
// the only thing that tells it apart from the real UI.
func TestHostPolicyRejectsPublicDomainsWithoutAuth(t *testing.T) {
	t.Parallel()

	handler := newTestHostPolicy(t, config.AuthModeNone, nil, nil)

	resp := serveHost(handler, httptest.NewRequest(http.MethodGet, "http://rebind.example.com:8080/api/v1/dags", nil))

	assert.Equal(t, http.StatusBadRequest, resp.Code)
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	assert.Contains(t, body.Message, `"rebind.example.com"`)
	assert.Contains(t, body.Message, "allowed_hosts")
}

// Single labels and names under domains reserved so they are never delegated
// resolve only through local DNS, so containers, clusters, and home networks
// keep working without configuration.
func TestHostPolicyAllowsAddressesAndPrivateNames(t *testing.T) {
	t.Parallel()

	handler := newTestHostPolicy(t, config.AuthModeNone, nil, nil)

	for _, host := range []string{
		"127.0.0.1:8080",
		"192.0.2.10",
		"[::1]:8080",
		"localhost:8080",
		"app.localhost",
		"dagu",
		"app",
		"dev",
		"dagu-ui.dagu.svc.cluster.local",
		"printer.local",
		"build.internal.",
		"ci.test",
		"dagu.home.arpa",
		"dagu.default.service.arpa",
	} {
		resp := serveHost(handler, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/dags", nil))
		assert.Equal(t, http.StatusNoContent, resp.Code, host)
	}

	// Any other name may sit under a domain delegated now or later, including
	// dynamic DNS suffixes and top-level domains no list knows yet, so it
	// needs configuration.
	for _, host := range []string{
		"rebind.duckdns.org",
		"rebind.example.co.ck",
		"rebind.example.web",
		"rebind.example.unlisted",
		"nas.lan",
		"nothome.arpa",
	} {
		resp := serveHost(handler, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/dags", nil))
		assert.Equal(t, http.StatusBadRequest, resp.Code, host)
	}
}

func TestHostPolicyAllowsConfiguredHosts(t *testing.T) {
	t.Parallel()

	handler := newTestHostPolicy(t, config.AuthModeNone, func(cfg *config.Config) {
		cfg.Server.Host = "dagu.example.org"
		cfg.Server.PublicURL = "https://dagu.example.com/ui"
		cfg.Server.AllowedHosts = []string{"Ops.Example.NET:8080", ".corp.example.com", "bücher.de", ".例え.jp"}
	}, func() []string { return []string{"dagu.tail1234.ts.net"} })

	// Browsers send internationalized names in their Punycode form.
	for _, host := range []string{
		"dagu.example.org:8080",
		"dagu.example.com",
		"ops.example.net",
		"corp.example.com",
		"build.corp.example.com.",
		"xn--bcher-kva.de",
		"dagu.xn--r8jz45g.jp",
		"dagu.tail1234.ts.net",
	} {
		resp := serveHost(handler, httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/dags", nil))
		assert.Equal(t, http.StatusNoContent, resp.Code, host)
	}

	resp := serveHost(handler, httptest.NewRequest(http.MethodGet, "http://notcorp.example.com/api/v1/dags", nil))
	assert.Equal(t, http.StatusBadRequest, resp.Code, "a suffix entry matches whole labels only")
}

func TestHostPolicyAllowsMachineHostname(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Server: config.Server{Auth: config.Auth{Mode: config.AuthModeNone}}}
	handler := newHostPolicy(cfg, testSetupPath, "build-box.example.com", nil).middleware(noContentHandler())

	resp := serveHost(handler, httptest.NewRequest(http.MethodGet, "http://build-box.example.com:8080/", nil))
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

func TestHostPolicyWildcardAllowsEveryHost(t *testing.T) {
	t.Parallel()

	handler := newTestHostPolicy(t, config.AuthModeNone, func(cfg *config.Config) {
		cfg.Server.AllowedHosts = []string{"*"}
	}, nil)

	resp := serveHost(handler, httptest.NewRequest(http.MethodPost, "http://rebind.example.com/api/v1/dag-runs", nil))
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

// With authentication, a rebinding page holds no credentials for the server,
// so only the endpoint that creates the first admin needs the host check.
func TestHostPolicyGuardsOnlySetupWithAuth(t *testing.T) {
	t.Parallel()

	handler := newTestHostPolicy(t, config.AuthModeBuiltin, nil, nil)

	resp := serveHost(handler, httptest.NewRequest(http.MethodGet, "http://rebind.example.com/api/v1/dags", nil))
	assert.Equal(t, http.StatusNoContent, resp.Code)

	for _, host := range []string{"rebind.example.com", "rebind.example.co.ck"} {
		resp = serveHost(handler, httptest.NewRequest(http.MethodPost, "http://"+host+testSetupPath+"/", nil))
		assert.Equal(t, http.StatusBadRequest, resp.Code, host)
	}

	resp = serveHost(handler, httptest.NewRequest(http.MethodPost, "http://localhost:8080"+testSetupPath, nil))
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

func newTestHostPolicy(t *testing.T, mode config.AuthMode, configure func(*config.Config), runtimeHosts func() []string) http.Handler {
	t.Helper()
	cfg := &config.Config{Server: config.Server{Auth: config.Auth{Mode: mode}}}
	if configure != nil {
		configure(cfg)
	}
	return newHostPolicy(cfg, testSetupPath, "", runtimeHosts).middleware(noContentHandler())
}

func noContentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func serveHost(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}
