// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec091_agent_node_test

import (
	"encoding/json"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// The tool bridge: a CLI agent reaches the Dagu tools of an agent.run step
// through an MCP server that Dagu serves for the attempt. The fake claude reads
// the MCP config, speaks MCP over HTTP to the bridge with the bearer secret, and
// records what it saw (testdata/bin/mcp_client.py, plans in testdata/plans).

// requirePython skips a test when the fake MCP client cannot run.
func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 runs the fake MCP client of the CLI")
	}
}

// startBridge runs a fixture with a bridge scenario.
func (p *agentProject) startBridge(scenario, fixture string, extra ...string) {
	p.t.Helper()
	requirePython(p.t)
	p.start(scenario, fixture, extra...).ExpectExitCode(0)
	require.Empty(p.t, p.optionalFile("client_error.1"), "the fake MCP client works")
}

// optionalFile returns the content of a file of the fake CLI, or "" when it does
// not exist.
func (p *agentProject) optionalFile(name string) string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.fake, name))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(p.t, err)
	return string(data)
}

// mcpTool is one tool of the result of tools/list.
type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	} `json:"inputSchema"`
}

// listedTools returns the tools that the fake CLI listed from the bridge.
func (p *agentProject) listedTools() []mcpTool {
	p.t.Helper()
	var listed struct {
		Tools []mcpTool `json:"tools"`
	}
	require.NoError(p.t, json.Unmarshal([]byte(p.fakeFile("tools.1.json")), &listed))
	return listed.Tools
}

func toolNamesOf(tools []mcpTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func propertyNames(tool mcpTool) []string {
	names := make([]string, 0, len(tool.InputSchema.Properties))
	for name := range tool.InputSchema.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (p *agentProject) toolNamed(name string) mcpTool {
	p.t.Helper()
	for _, tool := range p.listedTools() {
		if tool.Name == name {
			return tool
		}
	}
	p.t.Fatalf("the bridge did not list the tool %q", name)
	return mcpTool{}
}

// bridgeCall is the result of one call that the fake CLI made.
type bridgeCall struct {
	IsError bool `json:"isError"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Error string `json:"error"`
}

func (c bridgeCall) text() string {
	parts := make([]string, 0, len(c.Content))
	for _, content := range c.Content {
		parts = append(parts, content.Text)
	}
	return strings.Join(parts, "\n")
}

func (p *agentProject) bridgeCall(i int) bridgeCall {
	p.t.Helper()
	var call bridgeCall
	require.NoError(p.t, json.Unmarshal([]byte(p.fakeFile("call.1."+string(rune('0'+i))+".json")), &call))
	require.Empty(p.t, call.Error)
	return call
}

// mcpConfig is the MCP config that Dagu wrote, as the fake CLI copied it.
type mcpConfig struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	} `json:"mcpServers"`
}

func (p *agentProject) bridgeConfig() mcpConfig {
	p.t.Helper()
	var config mcpConfig
	require.NoError(p.t, json.Unmarshal([]byte(p.fakeFile("mcp.1.json")), &config))
	return config
}

// bridgeSecret returns the secret of the first attempt, which only the MCP config
// holds.
func (p *agentProject) bridgeSecret() string {
	p.t.Helper()
	server, ok := p.bridgeConfig().MCPServers["dagu"]
	require.True(p.t, ok)
	secret, ok := strings.CutPrefix(server.Headers["Authorization"], "Bearer ")
	require.True(p.t, ok)
	return secret
}

// requireBridgeClosed checks that nothing listens at the address of the bridge
// of the first attempt.
func (p *agentProject) requireBridgeClosed() {
	p.t.Helper()
	address := strings.TrimSuffix(strings.TrimPrefix(p.fakeFile("url.1"), "http://"), "/mcp")
	_, err := net.DialTimeout("tcp", address, 2*time.Second)
	require.Error(p.t, err, "no listener remains at %s after the step", address)
}

// requireNoSecretUnder checks that the secret is in no file under dir.
func requireNoSecretUnder(t *testing.T, dir, secret string) {
	t.Helper()
	require.NotEmpty(t, secret)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(path) // #nosec G304 -- the path is under the temp directory of the test.
		if err != nil {
			return err
		}
		require.NotContains(t, string(data), secret, "the secret is in %s", path)
		return nil
	})
	require.NoError(t, err)
}

// The catalog of the Dagu tools reaches the CLI. The MCP config has one server named
// dagu with its URL and a bearer secret, the tools are allowed as
// mcp__dagu__<name>, tools/list returns the names and the schemas of the tools,
// and a call runs the child run and returns its outputs. The bridge refuses a
// request without the secret, and it does not outlive the step.
func TestBridgeOffersTheCatalogAndRunsTheCalls(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.startBridge("bridge_calls", "bridge_catalog.yaml")

	// The flags: the tools of the bridge are allowed as mcp__dagu__<name>, they are
	// not built-in tools, and the config is passed with the strict flag.
	argv := project.argv(1)
	mcpPath := flagValue(t, argv, "--mcp-config")
	require.Equal(t, []string{
		"-p", "Help the customer c-42.",
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", "dontAsk",
		"--model", "claude-test",
		"--tools", "Read",
		"--allowedTools", "Read", "mcp__dagu__lookup", "mcp__dagu__refund", "mcp__dagu__invoice",
		"--strict-mcp-config", "--mcp-config", mcpPath,
	}, argv)

	// The config: one server named dagu, the URL on the loopback address, and a
	// secret of 32 random bytes (43 characters). The file is mode 0400 and is
	// deleted after the attempt. The URL has no secret.
	config := project.bridgeConfig()
	require.Len(t, config.MCPServers, 1)
	server := config.MCPServers["dagu"]
	require.Equal(t, "http", server.Type)
	require.Regexp(t, regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]+/mcp$`), server.URL)
	require.Regexp(t, regexp.MustCompile(`^Bearer [A-Za-z0-9_-]{43}$`), server.Headers["Authorization"])
	require.Len(t, server.Headers, 1)
	require.NotContains(t, server.URL, project.bridgeSecret())
	require.Equal(t, "-r--------", strings.TrimSpace(project.fakeFile("mcp.1.mode")))
	_, err := os.Stat(mcpPath)
	require.True(t, os.IsNotExist(err), "the MCP config is deleted after the attempt")

	// tools/list: the names, the descriptions and the schemas. The input fixed by
	// the workflow is not in the schema of refund, and the description of the
	// input that the model fills is the one of the author.
	require.Equal(t, []string{"invoice", "lookup", "refund"}, toolNamesOf(project.listedTools()))
	require.Equal(t, "Look up a customer", project.toolNamed("lookup").Description)
	require.Equal(t, "Create an invoice", project.toolNamed("invoice").Description)
	refund := project.toolNamed("refund")
	require.Equal(t, "object", refund.InputSchema.Type)
	require.Equal(t, []string{"AMOUNT"}, propertyNames(refund))
	require.Equal(t, "Amount to refund, in euros", refund.InputSchema.Properties["AMOUNT"].Description)
	require.Equal(t, []string{"CUSTOMER_ID"}, propertyNames(project.toolNamed("lookup")))

	// A call runs the child run, and the result is the JSON text of its outputs.
	lookup := project.bridgeCall(0)
	require.False(t, lookup.IsError)
	require.JSONEq(t, `{"INFO":"customer=c-9"}`, lookup.text())
	// The fixed input is hidden from the model, and Dagu applies it.
	refunded := project.bridgeCall(1)
	require.False(t, refunded.IsError)
	require.JSONEq(t, `{"RESULT":"refund 12 to c-42"}`, refunded.text())
	// An argument that names an input that the tool does not show, such as a fixed
	// input, is an error result, and no child run starts.
	overridden := project.bridgeCall(2)
	require.True(t, overridden.IsError)
	require.Equal(t, `tool "refund" has no input "CUSTOMER_ID"`, overridden.text())
	invoiced := project.bridgeCall(3)
	require.JSONEq(t, `{"RESULT":"invoice for c-3"}`, invoiced.text())
	// A name outside the catalog is an error result, and the step does not fail.
	unknown := project.bridgeCall(4)
	require.True(t, unknown.IsError)
	require.Equal(t, `tool "nothing" not found`, unknown.text())

	// The bridge refuses every request that has no secret or another value.
	require.JSONEq(t, `{"no_secret":401,"wrong_secret":401,"no_scheme":401}`, project.fakeFile("refused.1.json"))

	// tool_calls lists each call of the stream with the plain name.
	project.dagu.ExpectTextFileContent("show.out", `calls=["lookup","refund","refund","invoice","nothing"]`+"\n")
	project.dagu.ExpectTextFileContent("review.out", "done\n")

	// The bridge ends with the attempt.
	project.requireBridgeClosed()

	// The record: the catalog with the source of each tool, no fixed value, the
	// child runs of the calls, and no secret in the persisted state.
	node := project.savedNode("review")
	definitions := map[string]toolDefinition{}
	for _, definition := range node.ToolDefinitions {
		definitions[definition.Name] = definition
	}
	require.Len(t, definitions, 3)
	require.Equal(t, "dag:lookup", definitions["lookup"].Source)
	require.Equal(t, "dag:refund", definitions["refund"].Source)
	require.Equal(t, "dag:invoice", definitions["invoice"].Source)
	require.Equal(t, "Look up a customer", definitions["lookup"].Description)
	require.NotContains(t, definitions["refund"].Parameters["properties"], "CUSTOMER_ID")
	raw, err := json.Marshal(node.ToolDefinitions)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "c-42", "the record has no fixed input value")
	var childDAGs []string
	for _, child := range node.Children {
		childDAGs = append(childDAGs, child.DAGName)
	}
	require.Equal(t, []string{"lookup", "refund", "invoice"}, childDAGs)
	requireNoSecretUnder(t, project.home, project.bridgeSecret())
	requireNoSecretUnder(t, project.dagu.ProjectPath("."), project.bridgeSecret())
}

// max_tool_calls limits the calls of one attempt. A call over the limit gets an
// error result and does not run. The default is 100.
func TestBridgeMaxToolCalls(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.startBridge("bridge_limit", "bridge_limit.yaml")

	require.False(t, project.bridgeCall(0).IsError)
	require.False(t, project.bridgeCall(1).IsError)
	over := project.bridgeCall(2)
	require.True(t, over.IsError)
	require.Contains(t, over.text(), "limit of 2 tool calls")

	// The child run of the third call never started.
	require.Equal(t, "c-1\nc-2\n", project.fakeFile("lookup.calls"))
	project.dagu.ExpectTextFileContent("show.out", `calls=["lookup","lookup","lookup"]`+"\n")
	require.Len(t, project.savedNode("review").Children, 2)
}

// A child run that fails gives a result with isError true, and the step does not
// fail.
func TestBridgeFailedChildRunIsAnErrorResult(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.startBridge("bridge_broken", "bridge_failing_tool.yaml")

	call := project.bridgeCall(0)
	require.True(t, call.IsError)
	require.Contains(t, call.text(), "execution failed")
	project.dagu.ExpectTextFileContent("review.out", "done\n")
}

// A CLI that does not report the server dagu as connected fails the attempt with
// the kind error and the reason bridge_unavailable. The step publishes no output
// and the bridge ends.
func TestBridgeUnavailableFailsTheAttempt(t *testing.T) {
	t.Parallel()

	requirePython(t)
	project := newProject(t, nil)
	result := project.start("bridge_unavailable", "bridge_unavailable.yaml")
	result.ExpectNonZeroExitCode()

	require.Contains(t, summary(result), "bridge_unavailable")
	project.requireNoStdout("review.out")
	project.dagu.ExpectFileContains("review.err", "agent: attempt ended error")
	project.requireBridgeClosed()
}

// The secret never enters a log file: not the raw stream, not the stderr of the
// step, even when the CLI prints it.
func TestBridgeSecretStaysOutOfTheLogs(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.startBridge("bridge_secret", "bridge_secret.yaml")
	secret := project.bridgeSecret()

	stream, err := os.ReadFile(filepath.Clean(project.savedNode("review").Stdout + ir.AgentStreamSuffix))
	require.NoError(t, err)
	require.NotContains(t, string(stream), secret)
	require.Contains(t, string(stream), "*******", "the stream file keeps the line, with the secret masked")
	project.dagu.ExpectFileContains("review.err", "the secret is *******")
	project.dagu.ExpectFileNotContains("review.err", secret)
	requireNoSecretUnder(t, project.home, secret)
	requireNoSecretUnder(t, project.dagu.ProjectPath("."), secret)
}

// One step with the three categories: the bridge serves the two DAGs, the mcp
// entry goes to the MCP config of the CLI as its own server, and the builtin
// entry gives the built-in tools. Dagu applies a fixed param of the DAG, and the
// param from_ai is the argument of the model.
func TestBridgeWithTheThreeCategories(t *testing.T) {
	t.Parallel()

	project := newProject(t, nil)
	project.startBridge("bridge_all_kinds", "cli_all_tool_kinds.yaml")

	argv := project.argv(1)
	require.Equal(t, "Read,Grep,Glob,Bash", flagValue(t, argv, "--tools"))
	allowed := argv[slices.Index(argv, "--allowedTools")+1 : slices.Index(argv, "--strict-mcp-config")]
	require.Equal(t, []string{
		"Read", "Grep", "Glob", "Bash(git diff *)",
		"mcp__dagu__lookup-ticket", "mcp__dagu__ops_notify", "mcp__docs__search",
	}, allowed)

	config := project.bridgeConfig()
	require.Len(t, config.MCPServers, 2)
	require.Equal(t, "http://127.0.0.1:1/mcp", config.MCPServers["docs"].URL)
	require.Equal(t, map[string]string{"Authorization": "Bearer abc"}, config.MCPServers["docs"].Headers)

	require.Equal(t, []string{"lookup-ticket", "ops_notify"}, toolNamesOf(project.listedTools()))
	require.Equal(t, []string{"QUESTION"}, propertyNames(project.toolNamed("lookup-ticket")))
	require.Equal(t, "The question of the customer",
		project.toolNamed("lookup-ticket").InputSchema.Properties["QUESTION"].Description)
	require.Equal(t, []string{"text"}, propertyNames(project.toolNamed("ops_notify")))

	lookup := project.bridgeCall(0)
	require.False(t, lookup.IsError, lookup.text())
	require.JSONEq(t, `{"TICKET":"ticket of c-42 about Where is my refund?"}`, lookup.text())
	notify := project.bridgeCall(1)
	require.False(t, notify.IsError, notify.text())

	project.dagu.ExpectTextFileContent("show.out", `calls=["lookup-ticket","ops_notify","docs_search","read"]`+"\n")

	node := project.savedNode("review")
	sources := map[string]string{}
	for _, definition := range node.ToolDefinitions {
		sources[definition.Name] = definition.Source
	}
	require.Equal(t, map[string]string{"lookup-ticket": "dag:lookup-ticket", "ops_notify": "dag:ops_notify"}, sources)
	require.Len(t, node.Children, 2, "each call of the bridge is a child run")
	project.requireBridgeClosed()
}
