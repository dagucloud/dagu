// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
)

func startMCPRelay(t *testing.T, client aigateway.Client, endpoint string, servers ...string) *aigateway.Relay {
	t.Helper()
	relay, err := aigateway.StartRelay(context.Background(), aigateway.RelayOptions{
		Client: client, Step: aigateway.StepRef{RunID: "run-1", DAGName: "fix-bug", StepName: "code"},
		Endpoint: endpoint, Protocol: aiendpoint.ProtocolAnthropic, MCPServers: servers,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = relay.Close() })
	return relay
}

func bearer(relay *aigateway.Relay) http.Header {
	return http.Header{"Authorization": {"Bearer " + relay.Secret()}, "Content-Type": {"application/json"}}
}

func TestRelayMCP_ForwardsTheThreeMethods(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startMCPRelay(t, client, "", "docs", "wiki")

	header := bearer(relay)
	header.Set("X-Evil", "1")
	header.Set("Mcp-Session-Id", "s1")
	header.Set("Mcp-Protocol-Version", "2025-06-18")
	header.Set("Accept", "application/json, text/event-stream")
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		body := ""
		if method == http.MethodPost {
			body = `{"jsonrpc":"2.0","id":1,"method":"ping"}`
		}
		resp := do(t, relay, method, "/mcp/docs", body, header)
		_ = resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode, method)
	}
	require.Equal(t, 3, client.count())
	for i, target := range client.targets {
		assert.Equal(t, aigateway.MCPTarget("docs"), target, i)
		assert.Equal(t, "/mcp/docs", client.requests[i].Path)
		assert.Empty(t, client.requests[i].RawQuery)
		assert.Empty(t, client.requests[i].Header.Get("Authorization"), "the relay secret never leaves the worker")
		assert.Empty(t, client.requests[i].Header.Get("X-Evil"))
		assert.Equal(t, "s1", client.requests[i].Header.Get("Mcp-Session-Id"))
		assert.Equal(t, "2025-06-18", client.requests[i].Header.Get("Mcp-Protocol-Version"))
	}
	assert.Equal(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, client.bodies[0])

	// The second name of the step has its own target.
	resp := do(t, relay, http.MethodPost, "/mcp/wiki", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, header)
	_ = resp.Body.Close()
	assert.Equal(t, aigateway.MCPTarget("wiki"), client.targets[3])
}

func TestRelayMCP_RefusesWhatTheListDoesNotAllow(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startMCPRelay(t, client, "", "docs")

	encoded := bearer(relay)
	encoded.Set("Content-Encoding", "gzip")
	tests := []struct {
		name   string
		method string
		path   string
		header http.Header
		status int
		reason aigateway.Reason
	}{
		{"no secret", http.MethodPost, "/mcp/docs", http.Header{}, 401, aigateway.ReasonRelayUnauthorized},
		{"wrong secret", http.MethodPost, "/mcp/docs", http.Header{"Authorization": {"Bearer nope"}}, 401, aigateway.ReasonRelayUnauthorized},
		{"a server that the step does not name", http.MethodPost, "/mcp/other", bearer(relay), 403, aigateway.ReasonMCPServerForbidden},
		{"a path after the name", http.MethodPost, "/mcp/docs/extra", bearer(relay), 404, aigateway.ReasonPathNotAllowed},
		{"a query", http.MethodPost, "/mcp/docs?a=b", bearer(relay), 404, aigateway.ReasonPathNotAllowed},
		{"the root of the family", http.MethodPost, "/mcp", bearer(relay), 404, aigateway.ReasonPathNotAllowed},
		{"the name of the bridge", http.MethodPost, "/mcp/dagu", bearer(relay), 404, aigateway.ReasonPathNotAllowed},
		{"a bad name", http.MethodPost, "/mcp/Docs", bearer(relay), 404, aigateway.ReasonPathNotAllowed},
		{"the method PUT", http.MethodPut, "/mcp/docs", bearer(relay), 405, aigateway.ReasonMethodNotAllowed},
		{"the method PATCH", http.MethodPatch, "/mcp/docs", bearer(relay), 405, aigateway.ReasonMethodNotAllowed},
		{"a model path with no endpoint", http.MethodPost, "/v1/messages", bearer(relay), 404, aigateway.ReasonPathNotAllowed},
		{"content encoding", http.MethodPost, "/mcp/docs", encoded, 403, aigateway.ReasonHostNotAllowed},
	}
	for _, tc := range tests {
		resp := do(t, relay, tc.method, tc.path, `{}`, tc.header)
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, tc.status, resp.StatusCode, tc.name)
		if strings.HasPrefix(tc.path, "/mcp") {
			// The error of a MCP path is a JSON-RPC error.
			var body struct {
				JSONRPC string `json:"jsonrpc"`
				Error   struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(data, &body), tc.name)
			assert.Equal(t, "2.0", body.JSONRPC)
			assert.Contains(t, body.Error.Message, string(tc.reason), tc.name)
		}
		assert.NotContains(t, string(data), relay.Secret(), tc.name)
	}
	assert.Equal(t, 0, client.count(), "no refused request reaches the coordinator")
	require.NotNil(t, relay.LastRefusal())
}

func TestRelayMCP_HostAndFormAreChecked(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startMCPRelay(t, client, "", "docs")

	req, err := http.NewRequest(http.MethodPost, relay.URL()+"/mcp/docs", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Host = "evil.example.com"
	req.Header = bearer(relay)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)
	assert.Equal(t, 0, client.count())
}

// A step with an endpoint and MCP servers has one relay for both.
func TestRelayMCP_ServesTheEndpointAndTheServers(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	relay := startMCPRelay(t, client, "cle1", "docs")

	resp := do(t, relay, http.MethodPost, "/v1/messages", `{}`, bearer(relay))
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	resp = do(t, relay, http.MethodPost, "/mcp/docs", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, bearer(relay))
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	require.Equal(t, 2, client.count())
	assert.Equal(t, aigateway.EndpointTarget("cle1"), client.targets[0])
	assert.Equal(t, aigateway.MCPTarget("docs"), client.targets[1])

	// A model error keeps the error format of the protocol.
	resp = do(t, relay, http.MethodPost, "/v1/other", `{}`, bearer(relay))
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode)
	assert.Contains(t, string(data), `"type":"error"`)
}

// The failure of the coordinator reaches the MCP client as a JSON-RPC error with
// the status of the reason.
func TestRelayMCP_CoordinatorFailureIsAJSONRPCError(t *testing.T) {
	t.Parallel()
	client := &fakeClient{handler: func(context.Context, io.Reader, aigateway.ResponseSink) error {
		return aigateway.Fail(aigateway.ReasonMCPServerForbidden, "the DAG is not in the allow list")
	}}
	relay := startMCPRelay(t, client, "", "docs")
	resp := do(t, relay, http.MethodPost, "/mcp/docs", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, bearer(relay))
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)
	assert.Contains(t, string(data), `"jsonrpc":"2.0"`)
	assert.Contains(t, string(data), "mcp_server_forbidden")
	refusal := relay.LastRefusal()
	require.NotNil(t, refusal)
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, refusal.Reason)
}
