// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const token = "s3cr3t-token"

var (
	pngData = []byte("\x89PNG fake image")
	pdfData = []byte("%PDF fake document")
)

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
	server.AddTool(tool("media", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "chart"},
			&sdk.ImageContent{Data: pngData, MIMEType: "image/png"},
			&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///report.pdf", MIMEType: "application/pdf", Blob: pdfData}},
		}}, nil
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
	exec, err := newCaller(ctx, mcpStep(cfg))
	require.NoError(t, err)
	var out bytes.Buffer
	exec.SetStdout(&out)
	err = exec.Run(ctx)
	return out.String(), err
}

func mcpStep(cfg map[string]any) ir.Step {
	return ir.Step{Name: "call", ExecutorConfig: ir.ExecutorConfig{Type: "mcp", Config: cfg}}
}

// withArtifacts returns a context whose run stores artifacts in dir.
func withArtifacts(t *testing.T, dir string) context.Context {
	t.Helper()
	ctx := runtime.NewContext(t.Context(), &ir.DAG{Name: "mcp-test"}, "run-1", "")
	env := runtime.NewEnv(ctx, ir.Step{})
	env.Scope = value.NewEnvScope(nil, false).WithEntry(runenv.EnvKeyDAGRunArtifactsDir, dir, value.EnvSourceDAGEnv)
	return runtime.WithEnv(ctx, env)
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
	require.Contains(t, res, "structured_content")
	assert.Nil(t, res["structured_content"])
	content := res["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "text", content["type"])
	assert.JSONEq(t, `{"url":"https://example.com","n":2}`, content["text"].(string))
	assert.Equal(t, map[string]any{"url": "https://example.com", "n": float64(2)}, res["json"])
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

func TestOutputJSON(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		res  *sdk.CallToolResult
		want any
	}{
		"object":         {&sdk.CallToolResult{Content: text(`{"title":"t"}`)}, map[string]any{"title": "t"}},
		"array":          {&sdk.CallToolResult{Content: text(` [1] `)}, []any{json.Number("1")}},
		"plain text":     {&sdk.CallToolResult{Content: text("hello")}, nil},
		"scalar":         {&sdk.CallToolResult{Content: text("42")}, nil},
		"trailing text":  {&sdk.CallToolResult{Content: text(`{"a":1} and more`)}, nil},
		"two text items": {&sdk.CallToolResult{Content: append(text(`{"a":1}`), text(`{"b":2}`)...)}, nil},
		"structured":     {&sdk.CallToolResult{Content: text(`{"a":1}`), StructuredContent: map[string]any{"a": 1}}, nil},
		"no content":     {&sdk.CallToolResult{}, nil},
	} {
		out, err := newOutput(tc.res, false, nil)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, out.JSON, name)
	}
}

// Large integers in JSON text keep every digit, unlike structured_content.
func TestCallJSONKeepsLargeIntegers(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("echo", map[string]any{
		"arguments": map[string]any{"id": json.Number("9007199254740993")},
	}))
	require.NoError(t, err)
	assert.Contains(t, out, `"json":{"id":9007199254740993}`)
}

func TestCallSaveBinary(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir := t.TempDir()

	out, err := run(withArtifacts(t, dir), t, f.cfg("media", map[string]any{"save_binary": true}))
	require.NoError(t, err)
	content := decode(t, out)["content"].([]any)
	require.Len(t, content, 3)
	assert.Equal(t, map[string]any{"type": "text", "text": "chart"}, content[0])

	image := content[1].(map[string]any)
	assert.Regexp(t, `^mcp/call/[^/]+/content-1\.png$`, image["path"])
	assert.Equal(t, "image/png", image["mimeType"])
	assert.NotContains(t, image, "data")
	assertArtifact(t, dir, image["path"], pngData)

	doc := content[2].(map[string]any)
	assert.Regexp(t, `^mcp/call/[^/]+/content-2\.pdf$`, doc["path"])
	assert.Equal(t, map[string]any{"uri": "file:///report.pdf", "mimeType": "application/pdf"}, doc["resource"])
	assertArtifact(t, dir, doc["path"], pdfData)
}

// Foreach items and retries run the same step in one run, so each call must
// keep its own files.
func TestCallSaveBinaryKeepsCallsApart(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir := t.TempDir()
	ctx := withArtifacts(t, dir)

	paths := make([]any, 2)
	for i := range paths {
		out, err := run(ctx, t, f.cfg("media", map[string]any{"save_binary": true}))
		require.NoError(t, err)
		paths[i] = decode(t, out)["content"].([]any)[1].(map[string]any)["path"]
	}
	assert.NotEqual(t, paths[0], paths[1])
	for _, path := range paths {
		assertArtifact(t, dir, path, pngData)
	}
}

func TestCallSaveBinaryFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// A regular file where the artifacts directory should be makes every
	// write fail.
	blocked := filepath.Join(t.TempDir(), "artifacts")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))

	_, err := run(withArtifacts(t, blocked), t, f.cfg("media", map[string]any{"save_binary": true}))
	require.ErrorContains(t, err, "mcp.call: saving binary content:")
}

func assertArtifact(t *testing.T, dir string, path any, want []byte) {
	t.Helper()
	rel, ok := path.(string)
	require.True(t, ok, "path is %v", path)
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestCallInlinesBinaryByDefault(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("media", nil))
	require.NoError(t, err)
	image := decode(t, out)["content"].([]any)[1].(map[string]any)
	assert.Equal(t, base64.StdEncoding.EncodeToString(pngData), image["data"])
	assert.NotContains(t, image, "path")
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
		// A call never runs without the connection's token.
		"connection":                {"connection": "work-google", "server": "gmail", "tool": "t"},
		"server without connection": {"url": "http://x", "tool": "t", "server": "gmail"},
		// Artifacts disabled explicitly leave no directory for the files.
		"save_binary without artifacts": {"url": "http://x", "tool": "t", "save_binary": true},
	} {
		_, err := newCaller(t.Context(), mcpStep(cfg))
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
