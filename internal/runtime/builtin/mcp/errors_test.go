// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
)

func TestToolError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("fail", nil))
	require.Error(t, err)
	assert.Equal(t, kindTool, errKind(t, err))
	assert.Equal(t, true, decode(t, out)["is_error"])
	assert.NotContains(t, err.Error(), token)
	assert.Contains(t, err.Error(), "***")
}

func TestProtocolError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	out, err := run(t.Context(), t, f.cfg("no_such_tool", nil))
	require.Error(t, err)
	assert.Equal(t, kindProtocol, errKind(t, err))
	assert.Equal(t, kindProtocol, decode(t, out)["error"].(map[string]any)["kind"])
}

func TestTransportError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := f.cfg("echo", nil)
	f.srv.Close()

	_, err := run(t.Context(), t, cfg)
	require.Error(t, err)
	assert.Equal(t, kindTransport, errKind(t, err))
	assert.NotContains(t, err.Error(), token)
}

func TestAuthFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := f.cfg("echo", map[string]any{"headers": map[string]any{"Authorization": "Bearer wrong-token"}})

	out, err := run(t.Context(), t, cfg)
	require.Error(t, err)
	assert.Equal(t, kindAuth, errKind(t, err))
	assert.Contains(t, out, `"kind":"auth"`)
	assert.NotContains(t, err.Error()+out, "wrong-token")
}

func TestNoImplicitRetry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := run(t.Context(), t, f.cfg("fail", nil))
	require.Error(t, err)
	assert.EqualValues(t, 1, f.calls.Load())
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := run(ctx, t, f.cfg("slow", nil))
	require.Error(t, err)
	assert.Equal(t, kindTimeout, errKind(t, err))
	assert.Less(t, time.Since(start), 8*time.Second)
}

func TestCancellation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(300*time.Millisecond, cancel)

	_, err := run(ctx, t, f.cfg("slow", nil))
	require.Error(t, err)
	assert.Equal(t, kindCanceled, errKind(t, err))
}

func TestRedactor(t *testing.T) {
	t.Parallel()
	r := newRedactor(map[string]string{"Authorization": "Bearer abc123", "X-Key": "k-999"}, "")
	got := r.apply("failed with Bearer abc123 / abc123 / k-999")
	assert.Equal(t, "failed with *** / *** / ***", got)
}

func TestRedactorPreservesJSONSyntax(t *testing.T) {
	t.Parallel()
	r := newRedactor(map[string]string{"X-Key": `q"u\ote`}, "https://h/mcp?v=1&api_key=longsecret")
	var buf bytes.Buffer
	val := map[string]any{"n": 1, "ok": true, "s": `has q"u\ote and longsecret`}
	require.NoError(t, writeRedacted(&buf, val, r))
	got := decode(t, buf.String())
	assert.Equal(t, float64(1), got["n"])
	assert.Equal(t, true, got["ok"])
	assert.Equal(t, "has *** and ***", got["s"])
}

func TestNoRetryWhenResponseDropped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var calls atomic.Int32
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("tools/call")) {
			r.Body = io.NopCloser(bytes.NewReader(body))
			inner.ServeHTTP(w, r)
			return
		}
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})

	_, err := run(t.Context(), t, f.cfg("echo", nil))
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load())
}

func TestCloseDoesNotHang(t *testing.T) {
	old := closeTimeout
	closeTimeout = 300 * time.Millisecond
	t.Cleanup(func() { closeTimeout = old })
	f := newFixture(t)
	var canceled atomic.Bool
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			inner.ServeHTTP(w, r)
			return
		}
		select {
		case <-r.Context().Done():
			canceled.Store(true)
		case <-time.After(10 * time.Second):
		}
	})

	start := time.Now()
	_, err := run(t.Context(), t, f.cfg("echo", nil))
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Eventually(t, canceled.Load, 2*time.Second, 20*time.Millisecond, "DELETE request must be aborted")
}

func TestArgumentsReference(t *testing.T) {
	t.Parallel()
	step := func(args any) ir.Step {
		return ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mcp", Config: map[string]any{
			"url": "http://x", "tool": "t", "arguments": args,
		}}}
	}
	require.NoError(t, validateStep(step("${params.args}")))
	assert.Error(t, validateStep(step("plain")))
	cfg, err := decodeConfig(step(`{"a":1}`), false)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"a": json.Number("1")}, cfg.Arguments)
	_, err = decodeConfig(step("not json"), false)
	assert.Error(t, err)
}

func TestShortHeaderSecretsRedacted(t *testing.T) {
	t.Parallel()
	server := sdk.NewServer(&sdk.Implementation{Name: "short", Version: "1"}, nil)
	server.AddTool(tool("ok", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: text("value xq9")}, nil
	}))
	server.AddTool(tool("bad", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{IsError: true, Content: text("denied xq9")}, nil
	}))
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := func(name string) map[string]any {
		return map[string]any{"url": srv.URL, "tool": name, "headers": map[string]any{"Authorization": "Bearer xq9"}}
	}

	out, err := run(t.Context(), t, cfg("ok"))
	require.NoError(t, err)
	assert.NotContains(t, out, "xq9")
	assert.Contains(t, out, "value ***")

	out, err = run(t.Context(), t, cfg("bad"))
	require.Error(t, err)
	assert.NotContains(t, err.Error()+out, "xq9")
	assert.Contains(t, err.Error(), "***")
}

func TestLargeIntegerArgumentsKeepPrecision(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const big = "9007199254740993"

	out, err := run(t.Context(), t, f.cfg("echo", map[string]any{"arguments": `{"id": ` + big + `}`}))
	require.NoError(t, err)
	assert.Contains(t, out, big)

	_, err = newCaller(t.Context(), ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mcp", Config: f.cfg("echo", map[string]any{"arguments": `{"a":1} {"b":2}`})}})
	assert.Error(t, err)
}

func TestRedactorOverlappingSecrets(t *testing.T) {
	t.Parallel()
	headers := map[string]string{"X-Tenant": "acme", "Authorization": "Bearer acme_live_0123456789abcdef"}
	for range 50 {
		r := newRedactor(headers, "https://h/mcp?k=acme_live")
		got := r.apply("a acme_live_0123456789abcdef b acme c Bearer acme_live_0123456789abcdef")
		assert.Equal(t, "a *** b *** c ***", got)
	}
}
