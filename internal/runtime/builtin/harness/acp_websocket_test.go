// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newACPWebSocketPeer serves independent JSON-RPC messages, including batched frames.
func newACPWebSocketPeer(t *testing.T, tls bool) (*httptest.Server, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	prompted, cancelled := make(chan struct{}, 10), make(chan struct{}, 10)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("authenticate") {
			assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
			assert.Equal(t, "test-value", r.Header.Get("X-Agent"))
			assert.NotContains(t, r.Header.Get("Sec-WebSocket-Protocol"), "bearer.test-token")
		}
		socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"acp.v1"}})
		if !assert.NoError(t, err) {
			return
		}
		defer func() { _ = socket.CloseNow() }()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		var promptID any
		send := func(messages ...any) {
			var frame bytes.Buffer
			for _, message := range messages {
				_ = json.NewEncoder(&frame).Encode(message)
			}
			assert.NoError(t, socket.Write(ctx, websocket.MessageText, bytes.TrimSuffix(frame.Bytes(), []byte("\n"))))
		}
		result := func(id, value any) any {
			return map[string]any{"jsonrpc": "2.0", "id": id, "result": value}
		}
		for {
			kind, data, err := socket.Read(ctx)
			if err != nil {
				return
			}
			assert.Equal(t, websocket.MessageText, kind)
			assert.True(t, bytes.HasSuffix(data, []byte("\n")), "stdio bridges need the NDJSON delimiter in outgoing frames")
			var msg struct {
				ID     any
				Method string
				Params struct {
					Cwd    string
					Prompt []struct{ Text string }
				}
				Result json.RawMessage
			}
			if !assert.NoError(t, json.Unmarshal(data, &msg)) {
				return
			}
			switch msg.Method {
			case "initialize":
				send(result(msg.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}))
			case "session/new":
				assert.Equal(t, "/remote/workspace", msg.Params.Cwd)
				send(result(msg.ID, map[string]any{"sessionId": "remote"}))
			case "session/prompt":
				prompted <- struct{}{}
				promptID = msg.ID
				switch r.URL.Path {
				case "/wait", "/cancel":
					continue
				case "/drop":
					return
				case "/binary":
					_ = socket.Write(ctx, websocket.MessageBinary, []byte(`{}`))
					continue
				case "/permission":
					send(map[string]any{"jsonrpc": "2.0", "id": "permission", "method": "session/request_permission", "params": map[string]any{
						"sessionId": "remote", "toolCall": map[string]any{"toolCallId": "tool"},
						"options": []any{map[string]any{"optionId": "once", "name": "Allow", "kind": "allow_once"}},
					}})
					continue
				}
				if len(msg.Params.Prompt) == 0 {
					t.Error("missing prompt")
					return
				}
				update := func(text string) any {
					return map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
						"sessionId": "remote", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}},
					}}
				}
				send(update("remote:"), update(msg.Params.Prompt[0].Text), result(msg.ID, map[string]any{"stopReason": "end_turn"}))
			case "session/cancel":
				cancelled <- struct{}{}
				if r.URL.Path == "/cancel" {
					send(result(promptID, map[string]any{"stopReason": "cancelled"}))
				}
			default:
				if msg.ID == "permission" {
					assert.True(t, strings.Contains(string(msg.Result), "selected") || strings.Contains(string(msg.Result), "cancelled"))
					send(result(promptID, map[string]any{"stopReason": "end_turn"}))
				}
			}
		}
	})
	var server *httptest.Server
	if tls {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)
	return server, prompted, cancelled
}

// Heartbeats must be WebSocket control frames, not invented ACP methods.
func TestACPHeartbeat(t *testing.T) {
	pinged, text := make(chan struct{}, 1), make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OnPingReceived: func(context.Context, []byte) bool {
			select {
			case pinged <- struct{}{}:
			default:
			}
			return true
		}})
		if !assert.NoError(t, err) {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		if _, _, err := conn.Read(r.Context()); err == nil {
			text <- struct{}{}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), nil)
	require.NoError(t, err)
	defer func() { _ = conn.CloseNow() }()
	readerDone := make(chan struct{})
	go func() { defer close(readerDone); _, _, _ = conn.Read(ctx) }()
	beatDone := make(chan struct{})
	go func() { defer close(beatDone); keepACPWebSocketAlive(ctx, conn, 10*time.Millisecond) }()
	defer func() { cancel(); _ = conn.CloseNow(); <-readerDone; <-beatDone }()
	select {
	case <-pinged:
	case <-text:
		t.Fatal("heartbeat sent an ACP application message")
	case <-ctx.Done():
		t.Fatal("no WebSocket ping received")
	}
}

func TestACPBearerSubprotocol(t *testing.T) {
	for _, token := range []string{"test.jwt.signature", "base64-secret==", "slash/secret"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", token, enabled), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					assert.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
					if enabled {
						assert.Contains(t, r.Header.Get("Sec-WebSocket-Protocol"), "bearer."+token)
					} else {
						assert.NotContains(t, r.Header.Get("Sec-WebSocket-Protocol"), token)
					}
					conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"acp.v1"}})
					if assert.NoError(t, err) {
						_ = conn.CloseNow()
					}
				}))
				defer server.Close()
				def := &ir.HarnessDefinition{URL: strings.Replace(server.URL, "http", "ws", 1), BearerSubprotocol: enabled, Headers: map[string]string{"Authorization": "Bearer " + token}}
				conn, err := dialACPWebSocket(t.Context(), def, acpRemoteDefaultHTTPClient)
				if enabled && token != "test.jwt.signature" {
					require.Error(t, err)
					require.NotContains(t, err.Error(), token)
					require.Zero(t, requests.Load())
					return
				}
				require.NoError(t, err)
				_ = conn.CloseNow()
			})
		}
	}
}

// TestACPWebSocket checks remote framing, authentication, permissions, and failures.
func TestACPWebSocket(t *testing.T) {
	server, _, cancelled := newACPWebSocketPeer(t, false)
	for _, tc := range []struct {
		path, policy, wantError string
		container               bool
	}{
		{path: "/echo?authenticate=1"},
		// A DAG-level container applies to local steps, not to remote agents.
		{path: "/echo?container=1", container: true},
		{path: "/permission", policy: "allow"},
		{path: "/permission", wantError: "permission request denied"},
		{path: "/drop", wantError: "remote ACP"},
		{path: "/binary", wantError: "remote ACP"},
		{path: "/wait", wantError: "deadline exceeded"},
	} {
		t.Run(tc.path+tc.policy, func(t *testing.T) {
			def := &ir.HarnessDefinition{Protocol: "acp", Transport: "websocket", URL: strings.Replace(server.URL, "http", "ws", 1) + tc.path, Cwd: "/remote/workspace",
				Headers: map[string]string{"Authorization": "Bearer ${ACP_TOKEN}", "X-Agent": "test-value"}}
			cfg := map[string]any{"provider": "remote"}
			if tc.policy != "" {
				cfg["acp_permission"] = tc.policy
			}
			step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: cfg}, Commands: []ir.CommandEntry{{Command: "hello"}}, Script: "context"}
			dag := &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}
			if tc.container {
				dag.Container = &ir.Container{Image: "alpine"}
			}
			ctx := newHarnessTestContext(t, dag, step, "ACP_TOKEN=test-token")
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			exec, err := newHarness(ctx, step)
			require.NoError(t, err)
			var out, stderr bytes.Buffer
			exec.SetStdout(&out)
			exec.SetStderr(&stderr)
			err = exec.Run(ctx)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Empty(t, out.String())
			} else {
				require.NoError(t, err)
				if strings.HasPrefix(tc.path, "/echo") {
					require.Equal(t, "remote:hello\n\ncontext", out.String())
				}
			}
			require.Equal(t, "Bearer ${ACP_TOKEN}", def.Headers["Authorization"])
			require.NotContains(t, stderr.String(), "unencrypted", "loopback endpoints need no cleartext warning")
			if tc.path == "/wait" {
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("agent did not receive session/cancel")
				}
			}
		})
	}
}

// TestACPWebSocketTLS checks certificate verification and trusted wss connections.
func TestACPWebSocketTLS(t *testing.T) {
	server, _, _ := newACPWebSocketPeer(t, true)
	def := &ir.HarnessDefinition{Protocol: "acp", Transport: "websocket", URL: strings.Replace(server.URL, "https", "wss", 1), Cwd: "/remote/workspace"}
	_, err := dialACPWebSocket(t.Context(), def, acpRemoteDefaultHTTPClient)
	require.ErrorContains(t, err, "connection failed")
	require.ErrorContains(t, err, "x509", "the certificate failure should be reported")
	socket, err := dialACPWebSocket(t.Context(), def, server.Client())
	require.NoError(t, err)
	defer func() { _ = socket.CloseNow() }()
	var out bytes.Buffer
	stream := &acpWebSocketStream{ctx: t.Context(), conn: socket}
	client := &acpHarnessClient{out: &out}
	conn := acp.NewClientSideConnection(client, stream, stream)
	defer func() { _ = socket.CloseNow(); <-conn.Done() }()
	e := &harnessExecutor{prompt: "hello"}
	require.NoError(t, e.acpTurn(t.Context(), conn, client, def.Cwd))
	require.Equal(t, "remote:hello", out.String())
}

// TestACPWebSocketStop verifies explicit cancellation without remote fallback.
func TestACPWebSocketStop(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		intent     cmdutil.TerminationIntent
		exitCode   int
	}{
		// The /cancel peer ends the turn itself after session/cancel.
		{name: "graceful", path: "/cancel", intent: cmdutil.TerminationFromSignal(syscall.SIGTERM), exitCode: 143},
		{name: "force", path: "/wait", intent: cmdutil.ForceTermination(), exitCode: 137},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, prompted, cancelled := newACPWebSocketPeer(t, false)
			def := &ir.HarnessDefinition{Protocol: "acp", Transport: "websocket", URL: strings.Replace(server.URL, "http", "ws", 1) + tc.path, Cwd: "/remote/workspace"}
			step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "remote", "fallback": []any{map[string]any{"provider": "remote"}}}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
			ctx := newHarnessTestContext(t, &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}, step)
			exec, err := newHarness(ctx, step)
			require.NoError(t, err)
			e := exec.(*harnessExecutor)
			done := make(chan error, 1)
			go func() { done <- e.Run(ctx) }()
			select {
			case <-prompted:
			case <-time.After(5 * time.Second):
				t.Fatal("agent did not receive prompt")
			}
			require.NoError(t, e.Stop(tc.intent))
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, tc.exitCode, e.ExitCode())
			case <-time.After(5 * time.Second):
				t.Fatal("remote ACP did not stop")
			}
			require.Empty(t, prompted)
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("agent did not receive session/cancel")
			}
		})
	}
}

// TestACPRemoteInheritedOptions covers a provider chosen by a variable that
// resolves to a remote agent: DAG-level options it inherited are dropped, while
// options set on the step itself are still rejected.
func TestACPRemoteInheritedOptions(t *testing.T) {
	server, _, _ := newACPWebSocketPeer(t, false)
	def := &ir.HarnessDefinition{Protocol: "acp", Transport: "websocket", URL: strings.Replace(server.URL, "http", "ws", 1) + "/echo", Cwd: "/remote/workspace"}
	for _, tc := range []struct {
		name      string
		inherited any
		wantError string
	}{
		{name: "inherited", inherited: []string{"model"}},
		{name: "persisted", inherited: []any{"model"}},
		{name: "explicit", wantError: `does not accept CLI option "model"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "remote", "model": "sonnet"}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
			if tc.inherited != nil {
				step.ExecutorConfig.Metadata = map[string]any{ir.HarnessInheritedOptionsKey: tc.inherited}
			}
			ctx := newHarnessTestContext(t, &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}, step)
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			exec, err := newHarness(ctx, step)
			require.NoError(t, err)
			var out bytes.Buffer
			exec.SetStdout(&out)
			err = exec.Run(ctx)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "remote:hello", out.String())
		})
	}
}

// TestACPCleartextWarning flags credentials sent unencrypted beyond the local host.
func TestACPCleartextWarning(t *testing.T) {
	for _, tc := range []struct {
		url  string
		warn bool
	}{
		{url: "ws://192.0.2.1:9/acp", warn: true},
		{url: "wss://192.0.2.1:9/acp"},
		{url: "ws://127.0.0.1:9/acp"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			def := &ir.HarnessDefinition{Protocol: "acp", Transport: "websocket", URL: tc.url, Cwd: "/remote/workspace", Headers: map[string]string{"Authorization": "Bearer token"}}
			step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "remote"}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
			ctx := newHarnessTestContext(t, &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}, step)
			ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			exec, err := newHarness(ctx, step)
			require.NoError(t, err)
			var stderr bytes.Buffer
			exec.SetStderr(&stderr)
			require.Error(t, exec.Run(ctx))
			require.Equal(t, tc.warn, strings.Contains(stderr.String(), "unencrypted"), stderr.String())
		})
	}
}

// TestACPWebSocketDialFailure protects credentials and rejects handshake redirects.
func TestACPWebSocketDialFailure(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	def := &ir.HarnessDefinition{URL: strings.Replace(server.URL, "http", "ws", 1) + "?token=secret", Headers: map[string]string{"Authorization": "Bearer secret"}}
	_, err := dialACPWebSocket(t.Context(), def, acpRemoteDefaultHTTPClient)
	require.ErrorContains(t, err, "HTTP 302")
	require.NotContains(t, err.Error(), "secret")
	require.False(t, redirected.Load())
}

// TestACPWebSocketFallback verifies that a disconnected remote can use another provider.
func TestACPWebSocketFallback(t *testing.T) {
	server, _, _ := newACPWebSocketPeer(t, false)
	dag := &ir.DAG{Harnesses: ir.HarnessDefinitions{}}
	for _, name := range []string{"drop", "echo"} {
		dag.Harnesses[name] = &ir.HarnessDefinition{Protocol: "acp", Transport: "websocket", URL: strings.Replace(server.URL, "http", "ws", 1) + "/" + name, Cwd: "/remote/workspace"}
	}
	step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "drop", "fallback": []any{map[string]any{"provider": "echo"}}}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
	ctx := newHarnessTestContext(t, dag, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)
	var out, stderr bytes.Buffer
	exec.SetStdout(&out)
	exec.SetStderr(&stderr)
	require.NoError(t, exec.Run(ctx))
	require.Equal(t, "remote:hello", out.String())
	require.Contains(t, stderr.String(), "trying fallback")
}
