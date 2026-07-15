// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCORS_DefaultAndExplicitWildcard(t *testing.T) {
	t.Run("default denies cross-origin API preflight", func(t *testing.T) {
		server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeNone
			cfg.Server.CORSAllowedOrigins = nil
		}))

		resp := sendPreflight(t, server, "/api/v1/dag-runs", "https://evil.example")
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	})

	t.Run("explicit wildcard allows API but not setup", func(t *testing.T) {
		server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Auth.Mode = config.AuthModeNone
			cfg.Server.CORSAllowedOrigins = []string{"*"}
		}))

		apiResp := sendPreflight(t, server, "/api/v1/dag-runs", "https://app.example")
		defer func() { _ = apiResp.Body.Close() }()
		require.Equal(t, http.StatusOK, apiResp.StatusCode)
		assert.Equal(t, "*", apiResp.Header.Get("Access-Control-Allow-Origin"))
		assert.Empty(t, apiResp.Header.Get("Access-Control-Allow-Credentials"))

		setupResp := sendPreflight(t, server, "/api/v1/auth/setup", "https://app.example")
		defer func() { _ = setupResp.Body.Close() }()
		assert.Equal(t, http.StatusForbidden, setupResp.StatusCode)
		assert.Empty(t, setupResp.Header.Get("Access-Control-Allow-Origin"))
	})
}

func sendPreflight(t *testing.T, server test.Server, requestPath, origin string) *http.Response {
	t.Helper()

	requestURL := fmt.Sprintf(
		"http://%s:%d%s",
		server.Config.Server.Host,
		server.Config.Server.Port,
		requestPath,
	)
	req, err := http.NewRequest(http.MethodOptions, requestURL, nil)
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}
