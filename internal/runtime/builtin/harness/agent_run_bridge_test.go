// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agenttools"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bridgeLocalDAGs returns the local DAGs echo and boom. The Dagu tools of the
// tests are entries of these DAGs, so that the catalog of the bridge needs no DAG
// loader. A test lists the tools of the bridge and never calls one, because a
// call runs a child run. The tests of the package agenttools call a tool.
func bridgeLocalDAGs() map[string]*ir.DAG {
	echo := &ir.DAG{Name: "echo", Description: "Echo the text", ParamDefs: []ir.ParamDef{{Name: "text", Type: ir.ParamDefTypeString}}}
	boom := &ir.DAG{Name: "boom", Description: "Always fails"}
	return map[string]*ir.DAG{"echo": echo, "boom": boom}
}

// bridgeEntries returns the entries of the category dags for the local DAGs echo
// and boom.
func bridgeEntries() []ir.ToolEntry {
	return []ir.ToolEntry{
		{Kind: ir.ToolEntryDAG, Name: "echo", DAG: "echo"},
		{Kind: ir.ToolEntryDAG, Name: "boom", DAG: "boom"},
	}
}

func (f *fakeClaudeRun) file(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	require.NoError(t, err)
	return string(data)
}

// holdAttempt makes the fake claude wait for the file go.<n>, so that a test can
// use the bridge while the attempt runs.
func (f *fakeClaudeRun) holdAttempt(t *testing.T, n int) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, fmt.Sprintf("wait.%d", n)), nil, 0o600))
}

func (f *fakeClaudeRun) releaseAttempt(t *testing.T, n int) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(f.dir, fmt.Sprintf("go.%d", n)), nil, 0o600))
}

// bridgeConfig reads the server dagu of the MCP config that the fake claude
// copied.
func (f *fakeClaudeRun) bridgeConfig(t *testing.T, n int) (url, authorization string) {
	t.Helper()
	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal([]byte(f.file(t, fmt.Sprintf("mcp.%d.json", n))), &config))
	server, ok := config.MCPServers["dagu"]
	require.True(t, ok, "the config has the server dagu")
	assert.Equal(t, "http", server.Type)
	return server.URL, server.Headers["Authorization"]
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool { _, err := os.Stat(path); return err == nil }, 20*time.Second, 10*time.Millisecond, path)
}

func streamWith(servers string, calls ...string) string {
	var content []string
	for i, call := range calls {
		content = append(content, fmt.Sprintf(`{"type":"tool_use","name":%q,"id":"t%d","input":{}}`, call, i))
	}
	return `{"type":"system","subtype":"init","model":"sonnet","mcp_servers":[` + servers + `]}` + "\n" +
		`{"type":"assistant","message":{"content":[` + strings.Join(content, ",") + `]}}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"done"}` + "\n"
}

const bridgeConnected = `{"name":"dagu","status":"connected"}`

// The attempt serves the catalog of the Dagu tools with a bridge. The MCP config
// has the server dagu with the URL and the bearer secret, the tools are
// allowed as mcp__dagu__<name>, the bridge lists them, a request without the
// secret is refused, and everything ends with the attempt: the config file, the
// listener. tool_calls lists the calls with the plain names.
func TestAgentRunCLIServesTheToolBridge(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{
		Prompt: "Use the tools.",
		CLI: &ir.AgentCLIConfig{Tools: append(bridgeEntries(),
			ir.ToolEntry{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}}),
		},
	}, nil, streamWith(bridgeConnected, "mcp__dagu__echo", "Read", "mcp__dagu__boom"))
	run.holdAttempt(t, 1)

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	waitForFile(t, filepath.Join(run.dir, "mcp.1.json"))
	// The fake CLI writes the mode after the copy of the config.
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(run.dir, "mcp.1.mode"))
		return err == nil && len(data) > 0
	}, 20*time.Second, 10*time.Millisecond, "the mode of the config")

	url, authorization := run.bridgeConfig(t, 1)
	secret, ok := strings.CutPrefix(authorization, "Bearer ")
	require.True(t, ok)
	require.Len(t, secret, 43, "32 random bytes")
	assert.True(t, strings.HasPrefix(url, "http://127.0.0.1:"), url)
	assert.True(t, strings.HasSuffix(url, "/mcp"), url)
	mode, err := os.ReadFile(filepath.Join(run.dir, "mcp.1.mode"))
	require.NoError(t, err)
	assert.Equal(t, "-r--------", strings.TrimSpace(string(mode)), "only the owner reads the config")

	// A request without the secret gets 401.
	resp, err := http.Post(url, "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// The bridge lists the tools of the catalog.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "fake", Version: "v0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             url,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: secret}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	require.NoError(t, err)
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{"echo", "boom"}, names)
	_ = session.Close()

	run.releaseAttempt(t, 1)
	require.NoError(t, <-done)

	// The flags: the bridge tools are allowed, and they are not built-in tools.
	args := run.argv(t, 1)
	assert.Equal(t, "Read", args[slicesIndex(args, "--tools")+1])
	allowed := args[slicesIndex(args, "--allowedTools")+1:]
	assert.Equal(t, []string{"Read", "mcp__dagu__echo", "mcp__dagu__boom"}, allowed[:3])
	assert.Equal(t, map[string]any{"tool_calls": []string{"echo", "read", "boom"}}, run.agent.GetOutputs(),
		"the names are normalized: mcp__dagu__<name> gives <name>")

	// The catalog is recorded with the source of each tool.
	definitions := run.agent.GetToolDefinitions()
	require.Len(t, definitions, 2)
	assert.ElementsMatch(t, []string{"echo", "boom"}, []string{definitions[0].Name, definitions[1].Name})
	assert.Equal(t, "Echo the text", definitionOf(definitions, "echo").Description)
	assert.Equal(t, "dag:echo", definitionOf(definitions, "echo").Source)
	assert.NotContains(t, fmt.Sprint(definitions), secret)

	// The attempt ended: no listener, no config file.
	requireNoBridge(t, strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/mcp"), "no listener remains after the attempt")
	_, err = os.Stat(args[slicesIndex(args, "--mcp-config")+1])
	assert.True(t, os.IsNotExist(err), "the MCP config is deleted")
}

// requireNoBridge checks that no tool bridge listens at the address. Another test of
// the package that runs in parallel can reuse the port of a closed bridge, so a
// connection is no proof of a leak. The bridge answers a request without the secret
// with 401 and the header WWW-Authenticate: Bearer realm="dagu".
func requireNoBridge(t *testing.T, hostPort, message string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", hostPort, time.Second)
	if err != nil {
		return
	}
	_ = conn.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post("http://"+hostPort+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	assert.NotEqual(t, `Bearer realm="dagu"`, resp.Header.Get("WWW-Authenticate"), message)
}

func definitionOf(definitions []ir.ToolDefinition, name string) ir.ToolDefinition {
	for _, definition := range definitions {
		if definition.Name == name {
			return definition
		}
	}
	return ir.ToolDefinition{}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(clone)
}

// The secret never enters the stream file nor the stderr of the step, even when
// the CLI prints it.
func TestAgentRunCLIMasksTheBridgeSecret(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{
		Prompt: "Use the tools.",
		CLI:    &ir.AgentCLIConfig{Tools: bridgeEntries()},
	}, nil, "placeholder")
	run.holdAttempt(t, 1)

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	waitForFile(t, filepath.Join(run.dir, "mcp.1.json"))
	_, authorization := run.bridgeConfig(t, 1)
	secret := strings.TrimPrefix(authorization, "Bearer ")

	stream := `{"type":"system","subtype":"init","model":"sonnet","mcp_servers":[` + bridgeConnected + `],"debug":"` + secret + `"}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"done"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "stream.1"), []byte(stream), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "stderr.1"), []byte("token "+secret+" leaked\n"), 0o600))
	run.releaseAttempt(t, 1)
	require.NoError(t, <-done)

	file, err := os.ReadFile(filepath.Join(run.dir, "step.out"+ir.AgentStreamSuffix))
	require.NoError(t, err)
	assert.NotContains(t, string(file), secret)
	assert.Contains(t, string(file), "*******")
	assert.NotContains(t, run.stderr.String(), secret)
	assert.Contains(t, run.stderr.String(), "token ******* leaked")
}

// When the event system/init does not show the server dagu as connected, the
// attempt fails with the kind error and the reason bridge_unavailable. The
// bridge ends with the attempt.
func TestAgentRunCLIBridgeUnavailable(t *testing.T) {
	t.Parallel()

	for name, servers := range map[string]string{
		"failed":  `{"name":"dagu","status":"failed"}`,
		"missing": `{"name":"other","status":"connected"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := newFakeClaudeRun(t, ir.AgentRun{
				Prompt: "Use the tools.",
				CLI:    &ir.AgentCLIConfig{Tools: bridgeEntries()},
			}, nil, streamWith(servers))

			err := run.run()
			agentErr, ok := executor.AsAgentError(err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, ir.AgentKindError, agentErr.Kind)
			assert.Equal(t, "bridge_unavailable", agentErr.Reason)
			assert.Nil(t, run.agent.GetOutputs())
			assert.Equal(t, "agent: attempt ended error\n", run.stderr.String())

			url, _ := run.bridgeConfig(t, 1)
			requireNoBridge(t, strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/mcp"), "the bridge ends with the failed attempt")
		})
	}
}

// An error of the login or of the rate limit keeps its kind when the bridge is
// not connected too, because it says what the author must do.
func TestAgentRunCLIBridgeUnavailableKeepsAuthAndRateKinds(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		assistantError string
		kind           ir.AgentFailureKind
	}{
		"auth": {"authentication_failed", ir.AgentKindAuthRequired},
		"rate": {"rate_limit", ir.AgentKindRateLimited},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := newFakeClaudeRun(t, ir.AgentRun{
				Prompt: "x",
				CLI:    &ir.AgentCLIConfig{Tools: bridgeEntries()},
			}, nil,
				`{"type":"system","subtype":"init","mcp_servers":[{"name":"dagu","status":"failed"}]}`+"\n"+
					`{"type":"assistant","error":"`+tc.assistantError+`","message":{"content":[]}}`+"\n"+
					`{"type":"result","subtype":"success","is_error":true,"result":"stop"}`+"\n")
			require.NoError(t, os.WriteFile(filepath.Join(run.dir, "exit.1"), []byte("1"), 0o600))

			agentErr, ok := executor.AsAgentError(run.run())
			require.True(t, ok)
			assert.Equal(t, tc.kind, agentErr.Kind)
		})
	}
}

// A step without a Dagu tool starts no bridge: the config has no server dagu.
func TestAgentRunCLIWithoutDaguToolsStartsNoBridge(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{
		Prompt: "x",
		CLI: &ir.AgentCLIConfig{Tools: []ir.ToolEntry{
			{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}},
		}},
	}, nil, streamWith(""))
	require.NoError(t, run.run())
	assert.JSONEq(t, `{"mcpServers":{}}`, run.file(t, "mcp.1.json"))
	assert.Empty(t, run.agent.GetToolDefinitions())
}

// Each attempt has its own bridge, port and secret. A retry of the step is a new
// execution, so the test runs the step two times: a rate limit, then a success.
func TestAgentRunCLIEachAttemptHasItsOwnBridge(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{
		Prompt: "x",
		CLI:    &ir.AgentCLIConfig{Tools: bridgeEntries()},
	}, nil,
		`{"type":"system","subtype":"init","mcp_servers":[`+bridgeConnected+`]}`+"\n"+
			`{"type":"assistant","error":"rate_limit","message":{"content":[]}}`+"\n"+
			`{"type":"result","subtype":"success","is_error":true,"result":"limit"}`+"\n",
		streamWith(bridgeConnected, "mcp__dagu__echo"))
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "exit.1"), []byte("1"), 0o600))

	agentErr, ok := executor.AsAgentError(run.run())
	require.True(t, ok)
	assert.Equal(t, ir.AgentKindRateLimited, agentErr.Kind)
	require.NoError(t, run.run())
	firstURL, firstAuth := run.bridgeConfig(t, 1)
	secondURL, secondAuth := run.bridgeConfig(t, 2)
	assert.NotEqual(t, firstAuth, secondAuth)
	assert.NotEqual(t, firstURL, secondURL)
	for _, url := range []string{firstURL, secondURL} {
		requireNoBridge(t, strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/mcp"), "the bridge ends with its attempt")
	}
	assert.Equal(t, map[string]any{"tool_calls": []string{"echo"}}, run.agent.GetOutputs())
}

// A stop request while a call runs stops the bridge: the child run of the call
// gets the signal and the attempt ends.
func TestAgentRunCLIStopStopsTheBridge(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{
		Prompt: "x",
		CLI:    &ir.AgentCLIConfig{Tools: bridgeEntries()},
	}, nil, streamWith(bridgeConnected))
	run.holdAttempt(t, 1)

	done := make(chan error, 1)
	go func() { done <- run.run() }()
	waitForFile(t, filepath.Join(run.dir, "mcp.1.json"))
	url, _ := run.bridgeConfig(t, 1)

	require.NoError(t, run.agent.Kill(os.Interrupt))
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("the attempt did not end after the stop request")
	}
	requireNoBridge(t, strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/mcp"), "no listener remains")
}

func TestBuildBridgeRegistry(t *testing.T) {
	t.Parallel()

	ctx := newHarnessTestContext(t, &ir.DAG{Name: "parent", LocalDAGs: bridgeLocalDAGs()}, ir.Step{Name: "ask"})

	none, err := buildBridgeRegistry(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, none)

	none, err = buildBridgeRegistry(ctx, &ir.AgentCLIConfig{Tools: []ir.ToolEntry{
		{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}},
		{Kind: ir.ToolEntryMCP, Name: "docs", MCP: &ir.MCPToolSource{URL: "http://127.0.0.1:1", Allow: []string{"x"}}},
	}})
	require.NoError(t, err)
	assert.Nil(t, none, "a builtin entry and an mcp entry give no bridge")

	// The key agent_tools.max_tools of the Dagu configuration limits the catalog of
	// the bridge. The step has no key for it.
	limited := config.WithConfig(ctx, &config.Config{AgentTools: config.AgentToolsConfig{MaxTools: 1, ToolResultMaxBytes: 10}})
	_, err = buildBridgeRegistry(limited, &ir.AgentCLIConfig{Tools: bridgeEntries()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "above max_tools 1")

	registry, err := buildBridgeRegistry(ctx, &ir.AgentCLIConfig{Tools: append(
		[]ir.ToolEntry{{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}}},
		bridgeEntries()...,
	)})
	require.NoError(t, err)
	assert.True(t, registry.HasTools())
	assert.Len(t, registry.ToolDefinitions(), 2)
}

// The CLI accepts a tool name of 64 characters at most, and it names a tool of
// the bridge mcp__dagu__<name>. Dagu does not cut a longer name.
func TestCheckBridgeToolNames(t *testing.T) {
	t.Parallel()

	fits := strings.Repeat("a", 64-len("mcp__dagu__"))
	require.NoError(t, checkBridgeToolNames(agenttools.NewRegistryOfNames(map[string]string{fits: "d"})))

	err := checkBridgeToolNames(agenttools.NewRegistryOfNames(map[string]string{fits + "b": "d"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too long for the tool bridge")
	assert.Contains(t, err.Error(), "use a shorter name")
}

// The mcp entries of an agent.run step give one server each, named by the label of
// the entry. Dagu resolves the URL and the header values. An error names the
// path of the entry.
func TestResolveMCPServers(t *testing.T) {
	t.Parallel()

	ctx := newHarnessTestContext(t, nil, ir.Step{Name: "ask"}, "DOCS_URL=http://127.0.0.1:9/mcp", "TOKEN=abc")
	entries := []ir.ToolEntry{
		{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}},
		{Kind: ir.ToolEntryMCP, Name: "docs", MCP: &ir.MCPToolSource{
			URL: "${DOCS_URL}", Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}, Allow: []string{"search"},
		}},
	}

	servers, err := resolveMCPServers(ctx, entries)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "docs", servers[0].name)
	assert.Equal(t, "http://127.0.0.1:9/mcp", servers[0].url)
	assert.Equal(t, map[string]string{"Authorization": "Bearer abc"}, servers[0].headers)
	assert.Equal(t, []string{"search"}, servers[0].allow)

	entries[1].MCP.URL = "ftp://x"
	_, err = resolveMCPServers(ctx, entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with.tools.mcp[0].url must resolve to an http or https URL")
}

// A tool of the agent can print a secret of the run, and the raw stream of the
// CLI is a file that the UI shows. The stream masks the secrets of the run, also
// in the JSON form that a value with a quote has in a line of the stream.
func TestStreamMaskerHidesTheSecretsOfTheRun(t *testing.T) {
	t.Parallel()
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "d"}, "run-1", "d.log",
		runctx.WithSecrets([]string{`TOKEN=tok"en-value-1234`}))
	masker := secretMasker(streamSecrets(ctx, []string{"attempt-secret-5678"}))
	require.NotNil(t, masker)
	line, err := json.Marshal(map[string]string{"text": `tok"en-value-1234 and attempt-secret-5678`})
	require.NoError(t, err)
	masked := string(masker.MaskBytes(line))
	assert.NotContains(t, masked, "en-value-1234")
	assert.NotContains(t, masked, "attempt-secret-5678")
}
