// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const token = "s3cr3t-token"

type fixture struct {
	srv     *httptest.Server
	mu      sync.Mutex
	headers []http.Header
	calls   atomic.Int32
}

func tool(name string, h sdk.ToolHandler) (*sdk.Tool, sdk.ToolHandler) {
	return &sdk.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, h
}

func text(s string) []sdk.Content { return []sdk.Content{&sdk.TextContent{Text: s}} }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(tool("echo", func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: text(string(req.Params.Arguments))}, nil
	}))
	server.AddTool(tool("structured", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: text("ok"), StructuredContent: map[string]any{"n": 7}}, nil
	}))
	server.AddTool(tool("fail", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		f.calls.Add(1)
		return &sdk.CallToolResult{IsError: true, Content: text("boom " + token)}, nil
	}))
	server.AddTool(tool("leak", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{
			Content:           text("echo " + token),
			StructuredContent: map[string]any{"auth": "Bearer " + token},
		}, nil
	}))
	server.AddTool(tool("slow", func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
		return &sdk.CallToolResult{Content: text("late")}, nil
	}))
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)

	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) cfg(toolName string, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"url":     f.srv.URL,
		"tool":    toolName,
		"headers": map[string]any{"Authorization": "Bearer " + token},
	}
	maps.Copy(cfg, extra)
	return cfg
}

func run(ctx context.Context, t *testing.T, cfg map[string]any) (string, error) {
	t.Helper()
	exec, err := newCaller(ctx, ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mcp", Config: cfg}})
	require.NoError(t, err)
	var out bytes.Buffer
	exec.SetStdout(&out)
	err = exec.Run(ctx)
	return out.String(), err
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m), s)
	return m
}

func errKind(t *testing.T, err error) string {
	t.Helper()
	var ce *callError
	require.ErrorAs(t, err, &ce)
	return ce.kind
}

func TestCallSuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("echo", map[string]any{
		"arguments": map[string]any{"url": "https://example.com", "n": 2},
	}))
	require.NoError(t, err)
	res := decode(t, out)
	assert.Equal(t, false, res["is_error"])
	assert.Nil(t, res["structured_content"])
	content := res["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "text", content["type"])
	assert.JSONEq(t, `{"url":"https://example.com","n":2}`, content["text"].(string))
	assert.NotContains(t, res, "raw")
}

func TestCallStructuredAndRaw(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("structured", map[string]any{"include_raw": true}))
	require.NoError(t, err)
	res := decode(t, out)
	assert.Equal(t, map[string]any{"n": float64(7)}, res["structured_content"])
	assert.Contains(t, res["raw"], "structuredContent")
}

func TestCallSendsHeadersOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := run(t.Context(), t, f.cfg("echo", nil))
	require.NoError(t, err)
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.headers)
	for _, h := range f.headers {
		assert.Equal(t, "Bearer "+token, h.Get("Authorization"))
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	for name, cfg := range map[string]map[string]any{
		"missing tool": {"url": "http://x"},
		"missing url":  {"tool": "t"},
		"bad scheme":   {"url": "stdio://x", "tool": "t"},
		"userinfo":     {"url": "https://u:p@host/mcp", "tool": "t"},
		"unknown key":  {"url": "http://x", "tool": "t", "retries": 3},
	} {
		_, err := newCaller(t.Context(), ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mcp", Config: cfg}})
		assert.Error(t, err, name)
	}
}

func TestCallRedactsToolOutput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("leak", map[string]any{"include_raw": true}))
	require.NoError(t, err)
	assert.NotContains(t, out, token)
	assert.Contains(t, out, "***")
}

func TestRedactorCoversQueryValues(t *testing.T) {
	t.Parallel()
	r := newRedactor(nil, "https://h/mcp?api_key=qsecret&x=1")
	assert.NotContains(t, r.apply("failed: qsecret"), "qsecret")
}

func TestRedirectsNotFollowed(t *testing.T) {
	t.Parallel()
	for name, code := range map[string]int{"307": http.StatusTemporaryRedirect, "302": http.StatusFound} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var other atomic.Int32
			second := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { other.Add(1) }))
			t.Cleanup(second.Close)
			var first atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if bytes.Contains(body, []byte("tools/call")) {
					first.Add(1)
				}
				http.Redirect(w, r, second.URL, code)
			}))
			t.Cleanup(origin.Close)

			out, err := run(t.Context(), t, map[string]any{
				"url":     origin.URL,
				"tool":    "echo",
				"headers": map[string]any{"Authorization": "Bearer " + token},
			})
			require.Error(t, err)
			assert.Equal(t, kindProtocol, errKind(t, err))
			assert.Contains(t, err.Error(), "redirects are not followed")
			assert.NotContains(t, err.Error()+out, token)
			assert.Zero(t, other.Load())
			assert.Zero(t, first.Load(), "tools/call must never be sent")
		})
	}
}
