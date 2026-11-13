// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// Spec 095, "The MCP relay". These tests run a chat step with a named MCP entry
// through the real relay and the real gateway of the local mode, and a real MCP
// server. The server records the headers that it received.

const mcpRealToken = "mcp-token-THE-REAL-TOKEN-0123456789"

// mcpSource is the MCP store of the gateway: one entry and its token.
type mcpSource struct {
	url       string
	forbidden bool
	mu        sync.Mutex
	resolves  int
}

func (s *mcpSource) ResolveMCPServer(_ context.Context, name, dagName, _ string) (aiendpoint.MCPServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolves++
	if name != "docs" {
		return aiendpoint.MCPServer{}, &aiendpoint.Error{Reason: aiendpoint.ReasonMCPUnknown, Name: name, Kind: "MCP server", Detail: "no entry of this name"}
	}
	if s.forbidden {
		return aiendpoint.MCPServer{}, &aiendpoint.Error{Reason: aiendpoint.ReasonMCPForbidden, Name: name, Kind: "MCP server",
			Detail: "the DAG " + dagName + " is not in the allow list"}
	}
	return aiendpoint.MCPServer{Name: "docs", URL: s.url, Scope: aiendpoint.GlobalScope(),
		Token: &aiendpoint.Credential{Header: "authorization", Fingerprint: "0123456789ab"}}, nil
}

func (s *mcpSource) ReadCredentials(context.Context, aiendpoint.MCPServer) (aiendpoint.MCPCredentials, error) {
	return aiendpoint.MCPCredentials{Token: mcpRealToken}, nil
}

// mcpServerSeen records what a MCP server received.
type mcpServerSeen struct {
	mu      sync.Mutex
	headers []http.Header
	calls   []string
}

func (s *mcpServerSeen) record(h http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.headers = append(s.headers, h.Clone())
}

func (s *mcpServerSeen) called(tool string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, tool)
}

func (s *mcpServerSeen) snapshot() ([]http.Header, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.headers...), append([]string(nil), s.calls...)
}

type echoInput struct {
	Text string `json:"text"`
}

// startMCPServer starts a MCP server over streamable HTTP with the tools search
// and hidden.
func startMCPServer(t *testing.T) (*httptest.Server, *mcpServerSeen) {
	t.Helper()
	seen := &mcpServerSeen{}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "docs", Version: "v0"}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "search", Description: "Search the docs"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, in echoInput) (*mcpsdk.CallToolResult, any, error) {
			seen.called("search")
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "found: " + in.Text}}}, nil, nil
		})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "hidden", Description: "Not in allow"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ echoInput) (*mcpsdk.CallToolResult, any, error) {
			seen.called("hidden")
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "hidden result"}}}, nil, nil
		})
	handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r.Header)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	return httpServer, seen
}

// newMCPChatRun is newChatRun for a step that the DAG holds, so that the local
// mode reads its key allow from the DAG, as a real run does.
func newMCPChatRun(t *testing.T, step ir.Step, endpoints *endpointSource, mcp *mcpSource) (*chatRun, error) {
	t.Helper()
	gateway, err := aigateway.New(aigateway.Config{Endpoints: endpoints, MCPServers: mcp, Limits: aigateway.Limits{
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, MaxHeaderBytes: 32768,
		MaxStreamsPerRun: 4, MaxStreams: 16, ConnectTimeout: 2 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, TotalTimeout: 30 * time.Second,
	}})
	require.NoError(t, err)
	dag := &ir.DAG{Name: "test", Steps: []ir.Step{step}}
	ctx := runctx.NewContext(context.Background(), dag, "run-1", "/tmp/log")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, ir.Step{Name: "test"}))
	ctx = aigateway.WithClient(ctx, aigateway.Local{Gateway: gateway})
	exec, err := newChatExecutor(ctx, step)
	if err != nil {
		return nil, err
	}
	run := &chatRun{t: t, executor: exec.(*Executor), ctx: ctx}
	run.executor.SetStdout(&run.stdout)
	run.executor.SetStderr(&run.stderr)
	t.Cleanup(func() { _ = run.executor.Close() })
	return run, nil
}

func namedMCPStep(allow ...string) ir.Step {
	stream := false
	step := userStep(&ir.LLMConfig{
		Model: "model-x", Endpoint: "cle1", Stream: &stream,
		ToolEntries: []ir.ToolEntry{{Kind: ir.ToolEntryMCP, Name: "docs", MCP: &ir.MCPToolSource{Allow: allow}}},
	})
	step.ExecutorConfig.Type = ir.ExecutorTypeChat
	return step
}

// A tool of a named MCP entry runs through the relay. The server receives the
// token of the entry, and never the secret of the relay. The model receives the
// result.
func TestChatNamedMCPCallsTheToolThroughTheRelay(t *testing.T) {
	t.Parallel()

	mcpServer, seen := startMCPServer(t)
	provider := newFakeProvider(t, func(n int, w http.ResponseWriter, _ map[string]any) {
		if n == 1 {
			writeJSON(w, openaiWire.toolCall("call-1", "docs_search", `{"text":"dagu"}`))
			return
		}
		writeJSON(w, openaiWire.text("all done"))
	})
	endpoints := &endpointSource{}
	endpoints.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)

	run, err := newMCPChatRun(t, namedMCPStep("search"), endpoints, &mcpSource{url: mcpServer.URL + "/mcp"})
	require.NoError(t, err)
	require.NoError(t, run.run())
	assert.Equal(t, "all done\n", run.stdout.String())

	headers, calls := seen.snapshot()
	assert.Equal(t, []string{"search"}, calls)
	require.NotEmpty(t, headers, "the server received requests: the listing, the initialization and the call")
	for _, header := range headers {
		assert.Equal(t, "Bearer "+mcpRealToken, header.Get("Authorization"), "each request carries the token of the entry")
	}

	// The model got the result of the call as the tool message.
	requests := provider.all()
	require.Len(t, requests, 2)
	assert.Contains(t, requests[1].Body, "found: dagu")
	assert.NotContains(t, requests[0].Body+requests[1].Body, mcpRealToken, "the token never goes to the model")
	assert.NotContains(t, run.stdout.String()+run.stderr.String(), mcpRealToken)
}

// The tool that is not in allow is not in the catalog, and the coordinator refuses
// a call of it also when a client sends it through the relay.
func TestChatNamedMCPAllowListHoldsAtTheGateway(t *testing.T) {
	t.Parallel()

	mcpServer, seen := startMCPServer(t)
	provider := newFakeProvider(t, func(_ int, w http.ResponseWriter, _ map[string]any) {
		writeJSON(w, openaiWire.text("ok"))
	})
	endpoints := &endpointSource{}
	endpoints.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, provider.server.URL+"/v1", theRealKey)

	run, err := newMCPChatRun(t, namedMCPStep("search"), endpoints, &mcpSource{url: mcpServer.URL + "/mcp"})
	require.NoError(t, err)
	names := run.executor.toolRegistry.Names()
	assert.Equal(t, []string{"docs_search"}, names, "the catalog has the tool of allow only")

	// A client that calls the hidden tool through the relay gets a JSON-RPC error.
	relay := run.executor.mcpRelay
	require.NotNil(t, relay)
	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"hidden","arguments":{"text":"x"}}}`
	req, err := http.NewRequest(http.MethodPost, relay.URL()+"/mcp/docs", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+relay.Secret())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	var buf strings.Builder
	_, _ = io.Copy(&buf, resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, buf.String(), "mcp_tool_not_allowed")
	_, calls := seen.snapshot()
	assert.NotContains(t, calls, "hidden")
}

// A start check that fails stops the step before any request: the error names the
// MCP server and the reason.
func TestChatNamedMCPStartCheck(t *testing.T) {
	t.Parallel()

	endpoints := &endpointSource{}
	endpoints.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "http://127.0.0.1:1/v1", theRealKey)

	_, err := newMCPChatRun(t, namedMCPStep("search"), endpoints, &mcpSource{url: "http://127.0.0.1:1/mcp", forbidden: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "ask": MCP server "docs": mcp_server_forbidden`)

	step := namedMCPStep("search")
	step.LLM.ToolEntries[0].Name = "ghost"
	_, err = newMCPChatRun(t, step, endpoints, &mcpSource{url: "http://127.0.0.1:1/mcp"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `MCP server "ghost": mcp_server_unknown`)

	// A process with no gateway fails with the reason relay_unavailable.
	dag := &ir.DAG{Name: "test", Steps: []ir.Step{namedMCPStep("search")}}
	ctx := runctx.NewContext(context.Background(), dag, "run-1", "/tmp/log")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, ir.Step{Name: "test"}))
	_, err = newChatExecutor(ctx, namedMCPStep("search"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relay_unavailable")
}

// The inline form with a url needs no gateway: the step connects to the server.
func TestChatInlineMCPNeedsNoGateway(t *testing.T) {
	t.Parallel()

	mcpServer, seen := startMCPServer(t)
	stream := false
	step := userStep(&ir.LLMConfig{
		Model: "model-x", Provider: "local", BaseURL: "http://127.0.0.1:1", Stream: &stream,
		ToolEntries: []ir.ToolEntry{{Kind: ir.ToolEntryMCP, Name: "docs", MCP: &ir.MCPToolSource{
			URL: mcpServer.URL + "/mcp", Headers: map[string]string{"X-Team": "t1"}, Allow: []string{"search"},
		}}},
	})
	dag := &ir.DAG{Name: "test", Steps: []ir.Step{step}}
	ctx := runctx.NewContext(context.Background(), dag, "run-1", "/tmp/log")
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, ir.Step{Name: "test"}))
	exec, err := newChatExecutor(ctx, step)
	require.NoError(t, err)
	chat := exec.(*Executor)
	assert.Nil(t, chat.mcpRelay, "an inline entry starts no relay")
	assert.NoError(t, chat.Close())

	headers, _ := seen.snapshot()
	require.NotEmpty(t, headers)
	assert.Equal(t, "t1", headers[0].Get("X-Team"))
	assert.Empty(t, headers[0].Get("Authorization"))
}

// The relay of the step ends when the step closes.
func TestChatNamedMCPRelayEndsWithTheStep(t *testing.T) {
	t.Parallel()

	mcpServer, _ := startMCPServer(t)
	endpoints := &endpointSource{}
	endpoints.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "http://127.0.0.1:1/v1", theRealKey)
	run, err := newMCPChatRun(t, namedMCPStep("search"), endpoints, &mcpSource{url: mcpServer.URL + "/mcp"})
	require.NoError(t, err)

	url := run.executor.mcpRelay.URL()
	require.NoError(t, run.executor.Close())
	require.NoError(t, run.executor.Close(), "Close can run twice")
	_, err = (&http.Client{Timeout: time.Second}).Post(url+"/mcp/docs", "application/json", strings.NewReader("{}"))
	assert.Error(t, err)
}

// A tool in allow that the server does not list fails the step when it starts, as for an
// inline entry (Spec 090). The error names the entry, not the address of the relay.
func TestChatNamedMCPChecksAllowAgainstTheListOfTheServer(t *testing.T) {
	t.Parallel()

	mcpServer, _ := startMCPServer(t)
	endpoints := &endpointSource{}
	endpoints.add("cle1", "custom", aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer, "http://127.0.0.1:1/v1", theRealKey)
	_, err := newMCPChatRun(t, namedMCPStep("search", "missing"), endpoints, &mcpSource{url: mcpServer.URL + "/mcp"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `server entry "docs" of the AI page does not list tool "missing"`)
	assert.NotContains(t, err.Error(), "127.0.0.1")
}
