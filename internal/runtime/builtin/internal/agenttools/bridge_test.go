// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bridgeRegistry offers the tools echo and fail of an MCP test server, with the
// label docs: the tool names are docs_echo and docs_fail. A call of the bridge
// reaches that server, so no DAG loader is needed.
func bridgeRegistry(t *testing.T, serverURL string, allow ...string) *ToolRegistry {
	t.Helper()

	registry, err := BuildToolRegistry(catalogContext(t, nil), &ir.LLMConfig{ToolEntries: []ir.ToolEntry{{
		Kind: ir.ToolEntryMCP,
		Name: "docs",
		MCP:  &ir.MCPToolSource{URL: serverURL, Allow: allow},
	}}})
	require.NoError(t, err)
	return registry
}

func startBridge(t *testing.T, registry *ToolRegistry, mutate func(*BridgeOptions)) *Bridge {
	t.Helper()

	opts := BridgeOptions{Registry: registry, DAGRunID: "run-1", Step: "ask"}
	if mutate != nil {
		mutate(&opts)
	}
	bridge, err := StartBridge(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = bridge.Close() })
	return bridge
}

// withBridgeSession connects to the bridge as a CLI agent does.
func withBridgeSession(t *testing.T, bridge *Bridge, fn func(context.Context, *mcpsdk.ClientSession)) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	headers := map[string]string{"Authorization": "Bearer " + bridge.Secret()}
	err := withMCPSession(ctx, bridge.URL(), headers, func(ctx context.Context, session *mcpsdk.ClientSession) error {
		fn(ctx, session)
		return nil
	})
	require.NoError(t, err)
}

func callBridge(t *testing.T, bridge *Bridge, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()

	var result *mcpsdk.CallToolResult
	withBridgeSession(t, bridge, func(ctx context.Context, session *mcpsdk.ClientSession) {
		var err error
		result, err = session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
	})
	return result
}

func resultText(result *mcpsdk.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcpsdk.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// The bridge answers on the loopback address only, at the path /mcp, and the URL
// carries no secret.
func TestBridgeAddress(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), nil)

	host, port, err := net.SplitHostPort(strings.TrimSuffix(strings.TrimPrefix(bridge.URL(), "http://"), BridgePath))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	assert.NotEqual(t, "0", port)
	assert.True(t, strings.HasSuffix(bridge.URL(), "/mcp"))
	assert.NotContains(t, bridge.URL(), bridge.Secret())
}

// Each bridge has its own secret of 32 random bytes.
func TestBridgeSecretIsRandom(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	registry := bridgeRegistry(t, server.URL, "echo")
	first := startBridge(t, registry, nil)
	second := startBridge(t, registry, nil)

	assert.NotEqual(t, first.Secret(), second.Secret())
	assert.Len(t, first.Secret(), 43, "32 bytes in base64url without padding")
}

// A request without the secret, or with another value, gets the status 401. The
// secret opens the bridge.
func TestBridgeRefusesRequestsWithoutTheSecret(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

	post := func(authorization string) int {
		req, err := http.NewRequest(http.MethodPost, bridge.URL(), strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusUnauthorized, post(""))
	assert.Equal(t, http.StatusUnauthorized, post("Bearer wrong"))
	assert.Equal(t, http.StatusUnauthorized, post(bridge.Secret()), "the scheme Bearer is required")
	assert.Equal(t, http.StatusUnauthorized, post("Bearer "+bridge.Secret()+"x"))
	assert.Equal(t, http.StatusOK, post("Bearer "+bridge.Secret()))
}

// tools/list returns the effective catalog: the name, the description and the
// input schema of each tool. The MCP server lists the tools sorted by name.
func TestBridgeListsTheCatalog(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "fail", "echo"), nil)

	withBridgeSession(t, bridge, func(ctx context.Context, session *mcpsdk.ClientSession) {
		listed, err := session.ListTools(ctx, nil)
		require.NoError(t, err)
		require.Len(t, listed.Tools, 2)
		assert.Equal(t, "docs_echo", listed.Tools[0].Name)
		assert.Equal(t, "docs_fail", listed.Tools[1].Name)
		assert.Equal(t, "Echo the text", listed.Tools[0].Description)
		schema, ok := listed.Tools[0].InputSchema.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "object", schema["type"])
		assert.Contains(t, schema["properties"], "text")
	})
	assert.Equal(t, []string{"docs_fail", "docs_echo"}, bridge.ToolNames(), "ToolNames keeps the order of the catalog")
}

// A call runs the tool and returns its text. A tool that fails gives a result
// with isError true, and the bridge keeps serving.
func TestBridgeCallsTheTool(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo", "fail"), nil)

	ok := callBridge(t, bridge, "docs_echo", map[string]any{"text": "hi"})
	assert.False(t, ok.IsError)
	assert.Equal(t, "echo: hi", resultText(ok))

	failed := callBridge(t, bridge, "docs_fail", map[string]any{"text": "x"})
	assert.True(t, failed.IsError)
	assert.Equal(t, "boom", resultText(failed))
}

// A name outside the catalog gives a result with isError true.
func TestBridgeUnknownToolIsAnErrorResult(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), nil)

	result := callBridge(t, bridge, "hidden", nil)
	assert.True(t, result.IsError)
	assert.Equal(t, `tool "hidden" not found`, resultText(result))
}

// A result above the limit is cut, and stays valid UTF-8.
func TestBridgeCutsTheResult(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), func(o *BridgeOptions) { o.ResultMaxBytes = 60 })

	result := callBridge(t, bridge, "docs_echo", map[string]any{"text": strings.Repeat("é", 100)})
	text := resultText(result)
	assert.LessOrEqual(t, len(text), 60)
	assert.Contains(t, text, "[truncated:")
	assert.True(t, strings.ToValidUTF8(text, "") == text)
}

// A call over max_tool_calls gets an error result. The limit counts the calls of
// one attempt.
func TestBridgeMaxToolCalls(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), func(o *BridgeOptions) { o.MaxToolCalls = 2 })

	for range 2 {
		assert.False(t, callBridge(t, bridge, "docs_echo", map[string]any{"text": "a"}).IsError)
	}
	over := callBridge(t, bridge, "docs_echo", map[string]any{"text": "a"})
	assert.True(t, over.IsError)
	assert.Contains(t, resultText(over), "limit of 2 tool calls")
	assert.Equal(t, 2, bridge.Calls(), "a refused call does not run")
}

// The default of max_tool_calls is 100.
func TestBridgeDefaultMaxToolCalls(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), nil)
	assert.Equal(t, 100, bridge.MaxToolCalls())
	assert.Equal(t, DefaultMaxToolCalls, bridge.MaxToolCalls())
}

// The bridge runs one call at a time, also when the agent sends many at once.
func TestBridgeRunsOneCallAtATime(t *testing.T) {
	t.Parallel()

	var running, peak, total atomic.Int32
	upstream := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "slow", Version: "v0"}, nil)
	type in struct {
		Text string `json:"text"`
	}
	mcpsdk.AddTool(upstream, &mcpsdk.Tool{Name: "slow", Description: "Slow tool"},
		func(_ context.Context, _ *mcpsdk.CallToolRequest, _ in) (*mcpsdk.CallToolResult, any, error) {
			now := running.Add(1)
			for {
				seen := peak.Load()
				if now <= seen || peak.CompareAndSwap(seen, now) {
					break
				}
			}
			time.Sleep(60 * time.Millisecond)
			running.Add(-1)
			total.Add(1)
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "done"}}}, nil, nil
		})
	handler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return upstream }, nil)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	bridge := startBridge(t, bridgeRegistry(t, httpServer.URL, "slow"), nil)
	withBridgeSession(t, bridge, func(ctx context.Context, session *mcpsdk.ClientSession) {
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "docs_slow", Arguments: map[string]any{"text": "x"}})
				assert.NoError(t, err)
				assert.False(t, result.IsError)
			})
		}
		wg.Wait()
	})
	assert.Equal(t, int32(4), total.Load())
	assert.Equal(t, int32(1), peak.Load())
}

// After Close the listener is gone: a new connection fails, also with the
// secret. Close can run twice.
func TestBridgeCloseStopsTheListener(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), nil)
	address := strings.TrimSuffix(strings.TrimPrefix(bridge.URL(), "http://"), BridgePath)

	conn, err := net.Dial("tcp", address)
	require.NoError(t, err)
	_ = conn.Close()

	require.NoError(t, bridge.Close())
	require.NoError(t, bridge.Close())

	_, err = net.DialTimeout("tcp", address, time.Second)
	require.Error(t, err, "no listener remains")
}

// A call that runs when the bridge closes ends with the bridge.
func TestBridgeCloseEndsAnOpenCall(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	upstream := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "hang", Version: "v0"}, nil)
	type in struct{}
	mcpsdk.AddTool(upstream, &mcpsdk.Tool{Name: "hang", Description: "Waits"},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ in) (*mcpsdk.CallToolResult, any, error) {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "late"}}}, nil, nil
		})
	httpServer := httptest.NewServer(mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return upstream }, nil))
	t.Cleanup(func() { close(release); httpServer.Close() })

	bridge := startBridge(t, bridgeRegistry(t, httpServer.URL, "hang"), nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		headers := map[string]string{"Authorization": "Bearer " + bridge.Secret()}
		_ = withMCPSession(ctx, bridge.URL(), headers, func(ctx context.Context, session *mcpsdk.ClientSession) error {
			_, _ = session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "docs_hang"})
			return nil
		})
	}()
	<-started

	closed := make(chan error, 1)
	go func() { closed <- bridge.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return while a call was open")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the client call did not end")
	}
}

// A bridge needs a catalog with a tool.
func TestStartBridgeNeedsATool(t *testing.T) {
	t.Parallel()

	_, err := StartBridge(context.Background(), BridgeOptions{})
	require.Error(t, err)
	_, err = StartBridge(context.Background(), BridgeOptions{Registry: &ToolRegistry{}})
	require.Error(t, err)
}

// A tool whose input schema is not an object cannot join the MCP server. The
// bridge does not start, and no listener stays open.
func TestStartBridgeFailsForASchemaThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	registry := NewRegistryWithSchema("odd", map[string]any{"type": "string"})
	bridge, err := StartBridge(context.Background(), BridgeOptions{Registry: registry})
	require.Error(t, err)
	assert.Nil(t, bridge)
	assert.Contains(t, err.Error(), `the tool "odd" cannot join the tool bridge`)
}

// The secret never appears in an error of the bridge.
func TestBridgeErrorsDoNotCarryTheSecret(t *testing.T) {
	t.Parallel()

	server, _ := mcpTestServer(t)
	bridge := startBridge(t, bridgeRegistry(t, server.URL, "echo"), nil)
	result := callBridge(t, bridge, "missing", nil)
	assert.NotContains(t, resultText(result), bridge.Secret())
}
