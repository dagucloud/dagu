// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// EndpointName is the name of the AI endpoint that SeedTestEndpoint creates.
// An agent.run step of the provider claude MUST name an endpoint (Spec 091), so a
// fixture of a test that does not study the endpoint names this one.
const EndpointName = "test-endpoint"

// seedServerConfig is the configuration of the server that creates the endpoint.
// The server never calls a model API: the automatic refresh of the models list is
// off, and the first read goes to a proxy that refuses it.
const seedServerConfig = `ai:
  models_refresh_hours: 0
  upstream:
    proxy_url: http://127.0.0.1:9
`

// SeedTestEndpoint adds the AI endpoint EndpointName to the data directory home.
// The endpoint is global, it has the protocol anthropic, and each DAG and each
// workspace may use it. Its base URL is a stub that answers 503, and the fake CLI of
// the tests never calls it. A run with this data directory passes the start check
// of the endpoint. A coordinator that starts with this data directory serves it.
//
// The helper starts a short-lived `dagu server`, calls the REST API of the AI page
// and stops the server, so the endpoint is in the store before the test starts the
// process under test.
func SeedTestEndpoint(t *testing.T, r *Runner, home string) {
	t.Helper()

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the fake model API of the tests", http.StatusServiceUnavailable)
	}))
	t.Cleanup(stub.Close)

	configPath := filepath.Join(t.TempDir(), "seed-config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(seedServerConfig), 0o600))
	port := FreePort(t)
	server := r.StartWithEnv([]string{"DAGU_HOME=" + home},
		"server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port), "--config", configPath)
	defer server.Stop()

	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)
	client := &http.Client{Timeout: 15 * time.Second}
	deadline := time.Now().Add(WaitTimeout(t))
	for {
		select {
		case <-server.Done():
			require.FailNowf(t, "the server of the endpoint seed exited at start", "%s", server.FailureOutput())
		default:
		}
		resp, err := client.Get(base + "/health") //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			require.FailNow(t, "the server of the endpoint seed did not answer", "%s", server.Output())
		}
		time.Sleep(50 * time.Millisecond)
	}

	body, err := json.Marshal(map[string]any{
		"name": EndpointName, "preset": "custom", "protocol": "anthropic", "baseUrl": stub.URL,
		"auth": "x-api-key", "scope": "global",
		"allow": map[string]any{"workspaces": []string{"*"}, "dags": []string{"*"}},
		"key":   "sk-ant-test-endpoint-key-0000000000",
	})
	require.NoError(t, err)
	resp, err := client.Post(base+"/ai/endpoints", "application/json", bytes.NewReader(body)) //nolint:noctx
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "the endpoint seed: %s", server.Output())
}
