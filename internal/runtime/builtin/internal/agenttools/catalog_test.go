// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catalogContext builds a run context whose DAG has local DAGs and variables.
func catalogContext(t *testing.T, locals map[string]*ir.DAG, envs ...string) context.Context {
	t.Helper()

	dag := &ir.DAG{Name: "parent", LocalDAGs: locals}
	ctx := runctx.NewContext(context.Background(), dag, "run-1", "/tmp/log", runctx.WithEnvVars(envs...))
	return runtime.WithEnv(ctx, runtime.NewEnv(ctx, ir.Step{Name: "ask"}))
}

func toolDAG(name, description string, params ...string) *ir.DAG {
	dag := &ir.DAG{Name: name, Description: description}
	for _, param := range params {
		dag.ParamDefs = append(dag.ParamDefs, ir.ParamDef{Name: param, Type: ir.ParamDefTypeString})
	}
	return dag
}

func toolNames(registry *ToolRegistry) []string {
	var names []string
	for _, tool := range registry.ToLLMTools() {
		names = append(names, tool.Function.Name)
	}
	return names
}

// dagEntry returns the entry of the category dags that the loader builds.
func dagEntry(name string) ir.ToolEntry {
	return ir.ToolEntry{Kind: ir.ToolEntryDAG, Name: name, DAG: name}
}

func TestBuildToolRegistryKeepsOrderAndSource(t *testing.T) {
	t.Parallel()

	ctx := catalogContext(t, map[string]*ir.DAG{
		"zeta":  toolDAG("zeta", "Last"),
		"alpha": toolDAG("alpha", "First"),
	})
	registry, err := BuildToolRegistry(ctx, &ir.LLMConfig{
		ToolEntries: []ir.ToolEntry{dagEntry("zeta"), dagEntry("alpha")},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"zeta", "alpha"}, toolNames(registry))

	definitions := registry.ToolDefinitions()
	require.Len(t, definitions, 2)
	assert.Equal(t, "dag:zeta", definitions[0].Source)
	assert.Equal(t, "First", definitions[1].Description)
}

// The tool name comes from the name of the entry, not from the name that the
// DAG file gives. A character outside [A-Za-z0-9_-] becomes an underscore.
func TestBuildToolRegistryToolNameComesFromTheEntry(t *testing.T) {
	t.Parallel()

	ctx := catalogContext(t, map[string]*ir.DAG{
		"ops.refund": toolDAG("Some Other Name", "Refund"),
	})
	registry, err := BuildToolRegistry(ctx, &ir.LLMConfig{
		ToolEntries: []ir.ToolEntry{dagEntry("ops.refund")},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"ops_refund"}, toolNames(registry))
	name, ok := registry.GetDAGName("ops_refund")
	require.True(t, ok)
	assert.Equal(t, "ops.refund", name)
}

// An entry of the list form of tools, which is deprecated, keeps the rule of
// main: the tool name is the name of the loaded DAG.
func TestBuildToolRegistryListFormNameComesFromTheDAG(t *testing.T) {
	t.Parallel()

	ctx := catalogContext(t, map[string]*ir.DAG{
		"lookup-file": toolDAG("lookup", "Look up"),
	})
	entry := dagEntry("lookup-file")
	entry.NameFromDAG = true
	registry, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{entry}})
	require.NoError(t, err)
	assert.Equal(t, []string{"lookup"}, toolNames(registry))
	name, ok := registry.GetDAGName("lookup")
	require.True(t, ok)
	assert.Equal(t, "lookup-file", name)
}

// A DAG with no description gets a generated description.
func TestBuildToolRegistryDefaultDescription(t *testing.T) {
	t.Parallel()

	ctx := catalogContext(t, map[string]*ir.DAG{"quiet": toolDAG("quiet", "")})
	registry, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{dagEntry("quiet")}})
	require.NoError(t, err)
	assert.Equal(t, "Run the DAG quiet.", registry.ToolDefinitions()[0].Description)
}

func TestBuildToolRegistryNoToolsGivesNil(t *testing.T) {
	t.Parallel()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{})
	require.NoError(t, err)
	assert.Nil(t, registry)
}

func TestBuildToolRegistryParams(t *testing.T) {
	t.Parallel()

	ctx := catalogContext(t, map[string]*ir.DAG{
		"refund": toolDAG("refund", "Refund", "customer_id", "amount", "note"),
	}, "CUSTOMER=c-42")
	entry := dagEntry("refund")
	entry.Params = map[string]ir.ToolParam{
		"customer_id": {Value: "${CUSTOMER}"},
		"amount":      {FromAI: true, Description: "Amount in euros"},
	}
	registry, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{entry}})
	require.NoError(t, err)

	tool := registry.ToLLMTools()[0]
	properties, _ := tool.Function.Parameters["properties"].(map[string]any)
	assert.NotContains(t, properties, "customer_id")
	assert.Contains(t, properties, "note")
	amount, _ := properties["amount"].(map[string]any)
	assert.Equal(t, "Amount in euros", amount["description"])
	assert.Equal(t, map[string]string{"customer_id": "c-42"}, registry.tools["refund"].Fixed)
}

func TestApplyFixedArgsWinOverTheModel(t *testing.T) {
	t.Parallel()

	args := map[string]any{"customer_id": "evil", "note": "hi"}
	merged := applyFixedArgs(args, map[string]string{"customer_id": "c-42"})
	assert.Equal(t, map[string]any{"customer_id": "c-42", "note": "hi"}, merged)
	assert.Equal(t, "evil", args["customer_id"], "the caller's map stays unchanged")
	assert.Equal(t, args, applyFixedArgs(args, nil))
	assert.Equal(t, map[string]any{"a": "1"}, applyFixedArgs(nil, map[string]string{"a": "1"}))
}

func TestBuildToolRegistryUnknownParamFails(t *testing.T) {
	t.Parallel()

	ctx := catalogContext(t, map[string]*ir.DAG{"refund": toolDAG("refund", "Refund", "amount")})
	entry := dagEntry("refund")
	entry.Params = map[string]ir.ToolParam{"bogus": {Value: "x"}}
	_, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{entry}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm.dags[0].params.bogus")

	// The options name the keys of an agent.run step.
	_, err = BuildToolRegistryWith(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{entry}},
		BuildOptions{DAGsPath: "with.dags", ToolsPath: "with.tools"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with.dags[0].params.bogus")
}

// The path of an entry names the key that holds it: dags for a DAG entry, tools
// for the list form and for the category mcp.
func TestEntryPath(t *testing.T) {
	t.Parallel()

	listed := dagEntry("lookup")
	listed.NameFromDAG = true
	mcp := ir.ToolEntry{Kind: ir.ToolEntryMCP, Name: "docs"}
	assert.Equal(t, "with.dags[1]", EntryPath(dagEntry("lookup"), "with.dags", "with.tools", 1))
	assert.Equal(t, "with.tools[1]", EntryPath(listed, "with.dags", "with.tools", 1))
	assert.Equal(t, "with.tools.mcp[0]", EntryPath(mcp, "with.dags", "with.tools", 0))
}

func TestBuildToolRegistryLimitAndNames(t *testing.T) {
	t.Parallel()

	locals := map[string]*ir.DAG{
		"a": toolDAG("a", "A"),
		"b": toolDAG("b", "B"),
		"c": toolDAG("c", "C"),
	}
	two := 2
	one := 1
	entries := []ir.ToolEntry{dagEntry("a"), dagEntry("b"), dagEntry("c")}

	registry, err := BuildToolRegistry(catalogContext(t, locals), &ir.LLMConfig{ToolEntries: entries})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, toolNames(registry))

	_, err = BuildToolRegistry(catalogContext(t, locals), &ir.LLMConfig{ToolEntries: entries, MaxTools: &two})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "3 tools, above max_tools 2")

	_, err = BuildToolRegistry(catalogContext(t, locals), &ir.LLMConfig{ToolEntries: entries[:1], MaxTools: &one})
	require.NoError(t, err)
}

// A tool name that another tool has fails the build and names both sources. The
// loader rejects two DAG entries with one name, so the runtime check covers the
// names that only the catalog knows: an MCP tool and the tool load_skill.
func TestBuildToolRegistryDuplicateToolNameFails(t *testing.T) {
	t.Parallel()

	locals := map[string]*ir.DAG{"a": toolDAG("a", "A"), "docs_echo": toolDAG("docs_echo", "Own")}
	httpServer, _ := mcpTestServer(t)
	ctx := catalogContext(t, locals, "MCP_URL="+httpServer.URL)

	t.Run("mcp tool and dag", func(t *testing.T) {
		t.Parallel()
		_, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{
			dagEntry("docs_echo"),
			{Kind: ir.ToolEntryMCP, Name: "docs", MCP: &ir.MCPToolSource{URL: "${MCP_URL}", Allow: []string{"echo"}}},
		}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `tool name "docs_echo" of mcp:docs is also the tool name of dag:docs_echo`)
		assert.Contains(t, err.Error(), "llm.tools.mcp[0]")
	})
	t.Run("dag named load_skill with a skill", func(t *testing.T) {
		t.Parallel()
		ctx := catalogContext(t, map[string]*ir.DAG{"load_skill": toolDAG("load_skill", "Mine")})
		_, err := BuildToolRegistry(ctx, &ir.LLMConfig{
			ToolEntries: []ir.ToolEntry{dagEntry("load_skill")},
			Skills:      []ir.SkillEntry{skillEntry("refund", "Body")},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `tool name "load_skill" of skills is also the tool name of dag:load_skill`)
	})
}

func TestBuildToolRegistryNameAbove64CharactersFails(t *testing.T) {
	t.Parallel()

	name := strings.Repeat("a", 65)
	ctx := catalogContext(t, map[string]*ir.DAG{name: toolDAG(name, "Long")})
	_, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{dagEntry(name)}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "above the limit of 64")
}

func TestTruncateToolText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "short", truncateToolText("short", 100))
	assert.Equal(t, "whole", truncateToolText("whole", 0), "zero means no limit")

	long := strings.Repeat("a", 200)
	out := truncateToolText(long, 100)
	assert.LessOrEqual(t, len(out), 100)
	assert.Contains(t, out, "[truncated: ")

	multi := strings.Repeat("é", 100)
	out = truncateToolText(multi, 51)
	assert.True(t, strings.ToValidUTF8(out, "") == out, "the cut stays on a character boundary")
	assert.LessOrEqual(t, len(out), 51)

	assert.Equal(t, "aaaa", truncateToolText(long, 4), "a limit below the marker keeps the head")
}

// mcpTestServer starts an MCP server over HTTP with an echo tool and a failing
// tool. It records the Authorization header of each request.
func mcpTestServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "v0"}, nil)
	type echoIn struct {
		Text string `json:"text" jsonschema:"text to echo"`
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "echo", Description: "Echo the text"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoIn) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "fail", Description: "Always fails"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ echoIn) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "boom"}},
			}, nil, nil
		})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "hidden", Description: "Not allowed"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ echoIn) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{}, nil, nil
		})

	var (
		mu      sync.Mutex
		headers []string
	)
	handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	return httpServer, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), headers...)
	}
}

func TestBuildToolRegistryMCP(t *testing.T) {
	t.Parallel()

	httpServer, seenHeaders := mcpTestServer(t)
	ctx := catalogContext(t, nil, "MCP_URL="+httpServer.URL, "TOKEN=secret-token")
	registry, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{{
		Kind: ir.ToolEntryMCP,
		Name: "docs",
		MCP: &ir.MCPToolSource{
			URL:     "${MCP_URL}",
			Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"},
			Allow:   []string{"echo", "fail"},
		},
	}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"docs_echo", "docs_fail"}, toolNames(registry))
	assert.NotContains(t, toolNames(registry), "docs_hidden")
	assert.Contains(t, seenHeaders(), "Bearer secret-token")

	definitions := registry.ToolDefinitions()
	assert.Equal(t, "mcp:docs", definitions[0].Source, "the source is the label, not the host")
	assert.Equal(t, "Echo the text", definitions[0].Description)
	properties, _ := definitions[0].Parameters["properties"].(map[string]any)
	assert.Contains(t, properties, "text")

	exec := NewToolExecutor(registry, "")
	arguments, err := json.Marshal(map[string]any{"text": "hello"})
	require.NoError(t, err)
	results := exec.ExecuteToolCalls(ctx, []llmpkg.ToolCall{
		{ID: "1", Type: "function", Function: llmpkg.ToolCallFunction{Name: "docs_echo", Arguments: string(arguments)}},
		{ID: "2", Type: "function", Function: llmpkg.ToolCallFunction{Name: "docs_fail", Arguments: string(arguments)}},
	})
	require.Len(t, results, 2)
	assert.Equal(t, "echo: hello", results[0].Result.Content)
	assert.Empty(t, results[0].Result.Error)
	assert.Equal(t, "boom", results[1].Result.Error)
	assert.Empty(t, results[1].SubRun.DAGRunID, "an MCP call starts no sub-run")
}

// A tool name of an MCP server above 64 characters fails the build.
func TestBuildToolRegistryMCPToolNameAbove64CharactersFails(t *testing.T) {
	t.Parallel()

	httpServer, _ := mcpTestServer(t)
	ctx := catalogContext(t, nil, "MCP_URL="+httpServer.URL)
	_, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{{
		Kind: ir.ToolEntryMCP,
		Name: strings.Repeat("a", 62),
		MCP:  &ir.MCPToolSource{URL: "${MCP_URL}", Allow: []string{"echo"}},
	}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "above the limit of 64")
}

func TestBuildToolRegistryMCPErrors(t *testing.T) {
	t.Parallel()

	httpServer, _ := mcpTestServer(t)
	ctx := catalogContext(t, nil, "MCP_URL="+httpServer.URL, "BAD_URL=ftp://x")

	t.Run("a tool the server does not list", func(t *testing.T) {
		t.Parallel()
		_, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{{
			Kind: ir.ToolEntryMCP,
			Name: "docs",
			MCP:  &ir.MCPToolSource{URL: "${MCP_URL}", Allow: []string{"missing"}},
		}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `does not list tool "missing"`)
	})
	t.Run("a url that is no http url", func(t *testing.T) {
		t.Parallel()
		_, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{{
			Kind: ir.ToolEntryMCP,
			Name: "docs",
			MCP:  &ir.MCPToolSource{URL: "${BAD_URL}", Allow: []string{"echo"}},
		}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "http or https")
	})
	t.Run("a server that does not answer", func(t *testing.T) {
		t.Parallel()
		dead := httptest.NewServer(http.NotFoundHandler())
		dead.Close()
		_, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{{
			Kind: ir.ToolEntryMCP,
			Name: "docs",
			MCP:  &ir.MCPToolSource{URL: dead.URL, Allow: []string{"echo"}},
		}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "llm.tools.mcp[0]")
	})
}

func TestToolExecutorLimitsTheResult(t *testing.T) {
	t.Parallel()

	httpServer, _ := mcpTestServer(t)
	ctx := catalogContext(t, nil, "MCP_URL="+httpServer.URL)
	registry, err := BuildToolRegistry(ctx, &ir.LLMConfig{ToolEntries: []ir.ToolEntry{{
		Kind: ir.ToolEntryMCP,
		Name: "docs",
		MCP:  &ir.MCPToolSource{URL: "${MCP_URL}", Allow: []string{"echo"}},
	}}})
	require.NoError(t, err)

	exec := NewToolExecutor(registry, "")
	exec.SetResultLimit(60)
	arguments, err := json.Marshal(map[string]any{"text": strings.Repeat("x", 500)})
	require.NoError(t, err)
	results := exec.ExecuteToolCalls(ctx, []llmpkg.ToolCall{
		{ID: "1", Type: "function", Function: llmpkg.ToolCallFunction{Name: "docs_echo", Arguments: string(arguments)}},
	})
	assert.LessOrEqual(t, len(results[0].Result.Content), 60)
	assert.Contains(t, results[0].Result.Content, "[truncated")
}
