// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
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

func TestForbiddenIsAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	_, err := run(t.Context(), t, map[string]any{"url": srv.URL, "tool": "echo"})
	require.Error(t, err)
	assert.Equal(t, kindAuth, errKind(t, err))
	assert.Contains(t, err.Error(), "403")
}

// An unavailable server is a transport failure, not a protocol mismatch, so
// it can be retried like a lost connection.
func TestUnavailableIsTransport(t *testing.T) {
	t.Parallel()
	for name, code := range map[string]int{"503": http.StatusServiceUnavailable, "502": http.StatusBadGateway} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)

			_, err := run(t.Context(), t, map[string]any{"url": srv.URL, "tool": "echo"})
			require.Error(t, err)
			assert.Equal(t, kindTransport, errKind(t, err))
			assert.Contains(t, err.Error(), name)
		})
	}
}

// A rate limited call fails with the provider's message and Retry-After, so
// a user who hits a quota sees a quota error.
func TestRateLimited(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded for quota metric 'Queries'"}}`))
	}))
	t.Cleanup(srv.Close)

	out, err := run(t.Context(), t, map[string]any{"url": srv.URL, "tool": "echo"})
	require.Error(t, err)
	assert.Equal(t, kindRateLimited, errKind(t, err))
	assert.Equal(t, map[string]any{
		"kind":        kindRateLimited,
		"message":     "server responded with HTTP 429 Too Many Requests: Quota exceeded for quota metric 'Queries'; retry after 30",
		"retry_after": "30",
	}, decode(t, out)["error"])
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

// A graceful dagu stop reaches a running step through Kill, which may also
// arrive before Run starts.
func TestKillStopsCall(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	start := func(t *testing.T) (executor.Executor, *bytes.Buffer) {
		exec, err := newCaller(t.Context(), ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mcp", Config: f.cfg("slow", nil)}})
		require.NoError(t, err)
		var out bytes.Buffer
		exec.SetStdout(&out)
		return exec, &out
	}

	t.Run("during call", func(t *testing.T) {
		t.Parallel()
		exec, out := start(t)
		time.AfterFunc(300*time.Millisecond, func() { _ = exec.Kill(os.Interrupt) })
		begin := time.Now()
		err := exec.Run(t.Context())
		require.Error(t, err)
		assert.Equal(t, kindCanceled, errKind(t, err), out.String())
		assert.Less(t, time.Since(begin), 8*time.Second)
	})
	t.Run("before run", func(t *testing.T) {
		t.Parallel()
		exec, out := start(t)
		require.NoError(t, exec.Kill(os.Interrupt))
		err := exec.Run(t.Context())
		require.Error(t, err)
		assert.Equal(t, kindCanceled, errKind(t, err), out.String())
	})
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

// A connection that breaks after the response headers is a network failure,
// not an invalid response.
func TestDroppedStreamIsTransport(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	inner := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("tools/call")) {
			r.Body = io.NopCloser(bytes.NewReader(body))
			inner.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})

	out, err := run(t.Context(), t, f.cfg("echo", nil))
	require.Error(t, err)
	assert.Equal(t, kindTransport, errKind(t, err), out)
}

// A server may close a call's SSE stream and expect the client to resume it
// with Last-Event-ID. Resuming replays stored events, so the tool runs once.
func TestStreamResumesAfterServerClose(t *testing.T) {
	t.Parallel()
	var invoked, resumed atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "resume", Version: "1"}, nil)
	server.AddTool(tool("poll", func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		invoked.Add(1)
		req.Extra.CloseSSEStream(sdk.CloseSSEStreamArgs{RetryAfter: 10 * time.Millisecond})
		return &sdk.CallToolResult{Content: text("done")}, nil
	}))
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{EventStore: sdk.NewMemoryEventStore(nil)})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.Header.Get("Last-Event-ID") != "" {
			resumed.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	out, err := run(t.Context(), t, map[string]any{"url": srv.URL, "tool": "poll"})
	require.NoError(t, err, out)
	assert.Contains(t, out, "done")
	assert.EqualValues(t, 1, invoked.Load())
	assert.Positive(t, resumed.Load())
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

// A url reference resolves before the step runs, so load-time validation
// defers the URL checks to the resolved value.
func TestURLReference(t *testing.T) {
	t.Parallel()
	step := func(u string) ir.Step {
		return ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "mcp", Config: map[string]any{"url": u, "tool": "t"}}}
	}
	for _, u := range []string{"${MCP_URL}", "https://${MCP_HOST}/mcp", "http://localhost:${PORT}/mcp"} {
		assert.NoError(t, validateStep(step(u)), u)
	}
	assert.Error(t, validateStep(step("stdio://x")))
	_, err := newCaller(t.Context(), step("${MCP_URL}"))
	assert.Error(t, err, "an unresolved url must fail when the step runs")
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
	headers := map[string]string{"X-Api-Key": "acme", "Authorization": "Bearer acme_live_0123456789abcdef"}
	for range 50 {
		r := newRedactor(headers, "https://h/mcp?api_key=acme_live")
		got := r.apply("a acme_live_0123456789abcdef b acme c Bearer acme_live_0123456789abcdef")
		assert.Equal(t, "a *** b *** c ***", got)
	}
}

// Only credential headers and query parameters are masked; an ordinary value
// must not rewrite matching text in the tool result.
func TestPlainValuesLeaveOutputIntact(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := f.cfg("echo", map[string]any{
		"url":       f.srv.URL + "?format=json",
		"headers":   map[string]any{"Authorization": "Bearer " + token, "X-Region": "us"},
		"arguments": map[string]any{"q": "status of business users as json"},
	})

	out, err := run(t.Context(), t, cfg)
	require.NoError(t, err)
	assert.Contains(t, out, "status of business users as json")
}

// net/http reports the URL as written, so the percent-encoded form of a
// query secret must be masked as well as the decoded one.
func TestEncodedQuerySecretNotInError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := f.cfg("echo", map[string]any{"url": f.srv.URL + "/mcp?api_key=q1w2%2Be3r4%3D&token=x9y8+z7w6"})
	f.srv.Close()

	out, err := run(t.Context(), t, cfg)
	require.Error(t, err)
	assert.NotContains(t, err.Error()+out, "q1w2")
	assert.NotContains(t, err.Error()+out, "x9y8")
}
