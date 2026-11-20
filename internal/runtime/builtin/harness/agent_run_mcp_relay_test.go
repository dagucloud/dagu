// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentWithNamedMCP is a step with a named MCP server entry and an inline entry.
func agentWithNamedMCP() ir.AgentRun {
	return ir.AgentRun{
		Prompt: "x",
		CLI: &ir.AgentCLIConfig{Tools: []ir.ToolEntry{
			{Kind: ir.ToolEntryMCP, Name: "docs", MCP: &ir.MCPToolSource{Allow: []string{"search", "read"}}},
			{Kind: ir.ToolEntryMCP, Name: "plain", MCP: &ir.MCPToolSource{
				URL: "http://127.0.0.1:9/mcp", Headers: map[string]string{"X-Team": "t1"}, Allow: []string{"ping"},
			}},
		}},
	}
}

// waitMCPConfig waits until the fake CLI has copied its MCP config and its
// environment, then returns both.
func waitMCPConfig(t *testing.T, run *fakeClaudeRun) (config struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"mcpServers"`
}, env map[string]string) {
	t.Helper()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(run.dir, "mcp.1.json"))
		if err != nil || json.Unmarshal(data, &config) != nil {
			return false
		}
		envData, err := os.ReadFile(filepath.Join(run.dir, "env.1"))
		if err != nil || !strings.HasSuffix(string(envData), "\n") {
			return false
		}
		env = map[string]string{}
		for line := range strings.SplitSeq(string(envData), "\n") {
			if key, value, ok := strings.Cut(line, "="); ok {
				env[key] = value
			}
		}
		_, ok := env["NO_PROXY"]
		return ok
	}, 10*time.Second, 20*time.Millisecond)
	return config, env
}

// A named MCP entry goes to the relay of the attempt: the config of the CLI has
// the address of the relay and the relay secret, and no URL or credential of the
// entry. The inline entry stays as it is.
func TestAgentRunNamedMCPUsesTheRelay(t *testing.T) {
	t.Parallel()

	gateway := &fakeGateway{}
	run := newFakeClaudeRun(t, agentWithNamedMCP(), nil, streamSuccess)
	run.wrap = withGateway(gateway)
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "wait.1"), nil, 0o600))

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	config, env := waitMCPConfig(t, run)

	docs := config.MCPServers["docs"]
	assert.Equal(t, "http", docs.Type)
	assert.True(t, strings.HasPrefix(docs.URL, "http://127.0.0.1:"), docs.URL)
	assert.True(t, strings.HasSuffix(docs.URL, "/mcp/docs"), docs.URL)
	secret := strings.TrimPrefix(docs.Headers["Authorization"], "Bearer ")
	assert.GreaterOrEqual(t, len(secret), 43, "the relay secret is 32 random bytes")
	assert.Len(t, docs.Headers, 1)

	plain := config.MCPServers["plain"]
	assert.Equal(t, "http://127.0.0.1:9/mcp", plain.URL, "the inline entry connects to its server")
	assert.Equal(t, map[string]string{"X-Team": "t1"}, plain.Headers)

	// The rules of the key allow reach the CLI for both entries.
	args := run.argv(t, 1)
	allowed := args[slicesIndex(args, "--allowedTools")+1:]
	for _, rule := range []string{"mcp__docs__search", "mcp__docs__read", "mcp__plain__ping"} {
		assert.Contains(t, allowed, rule)
	}

	// A step with no endpoint keeps its model variables. The loopback joins NO_PROXY.
	assert.Empty(t, env["ANTHROPIC_BASE_URL"])
	assert.Empty(t, env["ANTHROPIC_API_KEY"])
	assert.Contains(t, strings.Split(env["NO_PROXY"], ","), "127.0.0.1")
	assert.Contains(t, strings.Split(env["no_proxy"], ","), "localhost")

	// The CLI calls the relay as it would. The request reaches the gateway with the
	// target of the entry, and the secret does not leave the worker.
	req, err := http.NewRequest(http.MethodPost, docs.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", docs.Headers["Authorization"])
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.JSONEq(t, `{"ok":true}`, string(body))
	require.Len(t, gateway.requests, 1)
	assert.Equal(t, aigateway.MCPTarget("docs"), gateway.relayTargets[0])
	assert.Empty(t, gateway.requests[0].Header.Get("Authorization"))

	// The relay does not serve a server that the step does not name, and no model request.
	for _, path := range []string{"/mcp/other", "/mcp/plain", "/v1/messages"} {
		other, _ := http.NewRequest(http.MethodPost, strings.TrimSuffix(docs.URL, "/mcp/docs")+path, strings.NewReader(`{}`))
		other.Header.Set("Authorization", docs.Headers["Authorization"])
		otherResp, err := (&http.Client{Timeout: 5 * time.Second}).Do(other)
		require.NoError(t, err)
		_ = otherResp.Body.Close()
		assert.GreaterOrEqual(t, otherResp.StatusCode, 400, path)
	}
	assert.Len(t, gateway.requests, 1)

	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "go.1"), nil, 0o600))
	require.NoError(t, <-done)

	// The start check ran for the named entry only. The inline entry is no target.
	assert.Equal(t, []aigateway.Target{aigateway.MCPTarget("docs")}, gateway.targets)
	assert.NotContains(t, run.stdout.String()+run.stderr.String(), secret, "the relay secret is masked in the logs")
	_, err = (&http.Client{Timeout: time.Second}).Post(docs.URL, "application/json", strings.NewReader("{}"))
	assert.Error(t, err, "the relay stops at the end of the attempt")
}

// A step with an endpoint and a named MCP entry has one relay for both.
func TestAgentRunEndpointAndNamedMCPShareTheRelay(t *testing.T) {
	t.Parallel()

	gateway := &fakeGateway{}
	step := agentWithNamedMCP()
	step.Endpoint = "cle1"
	run := newFakeClaudeRun(t, step, nil, streamSuccess)
	run.wrap = withGateway(gateway)
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "wait.1"), nil, 0o600))

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	config, env := waitMCPConfig(t, run)

	base := env["ANTHROPIC_BASE_URL"]
	require.NotEmpty(t, base)
	assert.Equal(t, base+"/mcp/docs", config.MCPServers["docs"].URL, "one relay serves the model and the MCP server")
	assert.Equal(t, env["ANTHROPIC_API_KEY"], strings.TrimPrefix(config.MCPServers["docs"].Headers["Authorization"], "Bearer "))
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "go.1"), nil, 0o600))
	require.NoError(t, <-done)

	assert.Equal(t, []aigateway.Target{aigateway.EndpointTarget("cle1"), aigateway.MCPTarget("docs")}, gateway.targets,
		"the endpoint is checked first, then each MCP server")
}

// The start check fails the step with the reason and the name of the MCP server,
// before the CLI starts.
func TestAgentRunNamedMCPStartCheckFailures(t *testing.T) {
	t.Parallel()

	for _, reason := range []aigateway.Reason{aigateway.ReasonMCPServerUnknown, aigateway.ReasonMCPServerForbidden} {
		t.Run(string(reason), func(t *testing.T) {
			t.Parallel()
			gateway := &fakeGateway{checkErr: aigateway.Fail(reason, `the DAG "fix-bug" is not in the allow list`)}
			run := newFakeClaudeRun(t, agentWithNamedMCP(), nil, streamSuccess)
			run.wrap = withGateway(gateway)
			err := run.run()
			require.Error(t, err)
			assert.Contains(t, err.Error(), `step "review": MCP server "docs": `+string(reason)+`: the DAG "fix-bug" is not in the allow list`)
			var failure *executor.AgentError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, ir.AgentKindError, failure.Kind)
			assert.Equal(t, string(reason), failure.Reason)
			assert.Equal(t, 1, run.agent.ExitCode())
			_, statErr := os.Stat(filepath.Join(run.dir, "argv.1"))
			assert.True(t, os.IsNotExist(statErr), "the CLI did not start")
			assert.Empty(t, gateway.requests)
		})
	}
}

// A process with no gateway fails the step with the reason relay_unavailable.
func TestAgentRunNamedMCPNeedsAGateway(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, agentWithNamedMCP(), nil, streamSuccess)
	err := run.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `MCP server "docs"`)
	var failure *executor.AgentError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "relay_unavailable", failure.Reason)
	_, statErr := os.Stat(filepath.Join(run.dir, "argv.1"))
	assert.True(t, os.IsNotExist(statErr), "the CLI did not start")
}

// The inline form needs no gateway and no relay: the CLI connects to the server.
func TestAgentRunInlineMCPNeedsNoRelay(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{
		Prompt: "x",
		CLI: &ir.AgentCLIConfig{Tools: []ir.ToolEntry{
			{Kind: ir.ToolEntryMCP, Name: "plain", MCP: &ir.MCPToolSource{URL: "http://127.0.0.1:9/mcp", Allow: []string{"ping"}}},
		}},
	}, nil, streamSuccess)
	require.NoError(t, run.run())
	assert.JSONEq(t, `{"mcpServers":{"plain":{"type":"http","url":"http://127.0.0.1:9/mcp"}}}`, run.file(t, "mcp.1.json"))
}

// A refusal of the relay gives the reason of the refusal to a failed attempt.
func TestAgentRunNamedMCPRefusalGivesTheReason(t *testing.T) {
	t.Parallel()

	gateway := &fakeGateway{relayErr: aigateway.Fail(aigateway.ReasonMCPServerForbidden, "the DAG is not in the allow list")}
	run := newFakeClaudeRun(t, agentWithNamedMCP(), nil, `{"type":"result","subtype":"error_during_execution","is_error":true}`+"\n")
	run.wrap = withGateway(gateway)
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "exit.1"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "wait.1"), nil, 0o600))

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	config, _ := waitMCPConfig(t, run)
	req, _ := http.NewRequest(http.MethodPost, config.MCPServers["docs"].URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Authorization", config.MCPServers["docs"].Headers["Authorization"])
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)
	assert.Contains(t, string(body), "mcp_server_forbidden")
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "go.1"), nil, 0o600))

	err = <-done
	require.Error(t, err)
	var failure *executor.AgentError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "mcp_server_forbidden", failure.Reason)
	assert.Contains(t, err.Error(), "the AI relay refused a request: mcp_server_forbidden")
	assert.Contains(t, run.stderr.String(), "ai relay: refuse POST /mcp/docs: mcp_server_forbidden")
}
