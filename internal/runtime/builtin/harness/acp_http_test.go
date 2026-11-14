// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newACPHTTPPeer checks ACP connection/session headers and independent SSE routing.
func newACPHTTPPeer(t *testing.T, mode string) (*httptest.Server, <-chan struct{}, <-chan struct{}, *atomic.Int32) {
	t.Helper()
	connection, session := make(chan any, 10), make(chan any, 10)
	promptIDs := make(chan any, 1)
	prompted, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	deleted := new(atomic.Int32)
	result := func(id, value any) any { return map[string]any{"jsonrpc": "2.0", "id": id, "result": value} }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		if mode == "unauthorized" {
			http.Error(w, "secret diagnostic", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Acp-Connection-Id") == "" {
			assert.Equal(t, http.MethodPost, r.Method)
			http.SetCookie(w, &http.Cookie{Name: "affinity", Value: "node1", Path: "/"})
			if mode != "missing-id" {
				w.Header().Set("Acp-Connection-Id", "connection")
			}
			w.Header().Set("Content-Type", "application/json")
			var request struct{ ID any }
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			_ = json.NewEncoder(w).Encode(result(request.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}))
			return
		}
		assert.Equal(t, "connection", r.Header.Get("Acp-Connection-Id"))
		cookie, err := r.Cookie("affinity")
		if assert.NoError(t, err) {
			assert.Equal(t, "node1", cookie.Value)
		}
		switch r.Method {
		case http.MethodGet:
			assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))
			queue := connection
			if r.Header.Get("Acp-Session-Id") != "" {
				assert.Equal(t, "session", r.Header.Get("Acp-Session-Id"))
				queue = session
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = fmt.Fprint(w, ": ready\n\n")
			w.(http.Flusher).Flush()
			if mode == "drop" && queue == session {
				return
			}
			for {
				select {
				case msg := <-queue:
					data, _ := json.MarshalIndent(msg, "", "  ")
					for line := range bytes.SplitSeq(data, []byte("\n")) {
						_, _ = fmt.Fprintf(w, "data: %s\n", line)
					}
					_, _ = fmt.Fprint(w, "\n")
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		case http.MethodDelete:
			deleted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			var msg struct {
				ID     any
				Method string
				Params struct {
					Cwd       string
					SessionID string `json:"sessionId"`
					Prompt    []struct{ Text string }
				}
				Result json.RawMessage
			}
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&msg)) {
				return
			}
			w.WriteHeader(http.StatusAccepted)
			switch msg.Method {
			case "session/new":
				assert.Equal(t, "/remote/workspace", msg.Params.Cwd)
				connection <- result(msg.ID, map[string]any{"sessionId": "session"})
			case "session/prompt":
				assert.Equal(t, "session", r.Header.Get("Acp-Session-Id"))
				prompted <- struct{}{}
				if mode == "wait" {
					return
				}
				if mode == "permission" {
					promptIDs <- msg.ID
					session <- map[string]any{"jsonrpc": "2.0", "id": "permission", "method": "session/request_permission", "params": map[string]any{
						"sessionId": "session", "toolCall": map[string]any{"toolCallId": "tool"},
						"options": []any{map[string]any{"optionId": "once", "name": "Allow", "kind": "allow_once"}},
					}}
					// The prompt response is emitted after the callback POST below.
					return
				}
				if !assert.Len(t, msg.Params.Prompt, 1) {
					return
				}
				session <- map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": "session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "http:" + msg.Params.Prompt[0].Text}},
				}}
				session <- result(msg.ID, map[string]any{"stopReason": "end_turn"})
			case "session/cancel":
				assert.Equal(t, "session", r.Header.Get("Acp-Session-Id"))
				cancelled <- struct{}{}
			case "":
				assert.Equal(t, "session", r.Header.Get("Acp-Session-Id"))
				assert.True(t, strings.Contains(string(msg.Result), "selected") || strings.Contains(string(msg.Result), "cancelled"))
				session <- result(<-promptIDs, map[string]any{"stopReason": "end_turn"})
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, prompted, cancelled, deleted
}

// TestACPHTTP checks real POST/SSE/DELETE exchanges, cookies, and permission callbacks.
func TestACPHTTP(t *testing.T) {
	for _, mode := range []string{"echo", "permission", "denied", "unauthorized", "missing-id", "drop"} {
		t.Run(mode, func(t *testing.T) {
			peerMode := mode
			if mode == "denied" {
				peerMode = "permission"
			}
			server, _, _, deleted := newACPHTTPPeer(t, peerMode)
			def := &ir.HarnessDefinition{Protocol: "acp", Transport: "http", URL: server.URL, Cwd: "/remote/workspace", Headers: map[string]string{"Authorization": "Bearer ${ACP_TOKEN}"}}
			step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "remote", "acp_permission": "allow"}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
			if mode == "denied" {
				step.ExecutorConfig.Config["acp_permission"] = "deny"
			}
			ctx := newHarnessTestContext(t, &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}, step, "ACP_TOKEN=test-token")
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			exec, err := newHarness(ctx, step)
			require.NoError(t, err)
			var out bytes.Buffer
			exec.SetStdout(&out)
			err = exec.Run(ctx)
			if mode == "unauthorized" {
				require.ErrorContains(t, err, "HTTP 401")
				require.NotContains(t, err.Error(), "secret")
				require.Empty(t, out.String())
				return
			}
			if mode == "missing-id" {
				require.ErrorContains(t, err, "Acp-Connection-Id")
				return
			}
			if mode == "drop" || mode == "denied" {
				require.Error(t, err)
				require.Empty(t, out.String())
				require.EqualValues(t, 1, deleted.Load())
				return
			}
			require.NoError(t, err)
			require.EqualValues(t, 1, deleted.Load())
			if mode == "echo" {
				require.Equal(t, "http:hello", out.String())
			}
		})
	}
}

// TestACPHTTPStop checks cancellation routing, DELETE cleanup, and bounded shutdown.
func TestACPHTTPStop(t *testing.T) {
	server, prompted, cancelled, deleted := newACPHTTPPeer(t, "wait")
	def := &ir.HarnessDefinition{Protocol: "acp", Transport: "http", URL: server.URL, Cwd: "/remote/workspace", Headers: map[string]string{"Authorization": "Bearer test-token"}}
	step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "remote"}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
	ctx := newHarnessTestContext(t, &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- exec.Run(ctx) }()
	select {
	case <-prompted:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP agent did not receive prompt")
	}
	require.NoError(t, exec.(*harnessExecutor).Stop(cmdutil.ForceTermination()))
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP agent did not stop")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("HTTP agent did not receive cancellation")
	}
	require.EqualValues(t, 1, deleted.Load())
}

// TestACPWSS verifies a full executor turn over TLS using an explicit private CA.
func TestACPWSS(t *testing.T) {
	server, _, _ := newACPWebSocketPeer(t, true)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}), 0o600))
	def := &ir.HarnessDefinition{Protocol: "acp", Transport: "websocket", URL: strings.Replace(server.URL, "https", "wss", 1), Cwd: "/remote/workspace", CAFile: caFile}
	step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "remote"}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
	ctx := newHarnessTestContext(t, &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)
	var out bytes.Buffer
	exec.SetStdout(&out)
	require.NoError(t, exec.Run(ctx))
	require.Equal(t, "remote:hello", out.String())
}

// TestACPHTTPS verifies TLS verification and HTTP/2 multiplexing of scoped SSE streams.
func TestACPHTTPS(t *testing.T) {
	peer, _, _, deleted := newACPHTTPPeer(t, "echo")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, 2, r.ProtoMajor)
		peer.Config.Handler.ServeHTTP(w, r)
	}))
	server.EnableHTTP2 = true
	var connections atomic.Int32
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
		if state == http.StateClosed {
			connections.Add(-1)
		}
	}
	server.StartTLS()
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}), 0o600))
	def := &ir.HarnessDefinition{Protocol: "acp", Transport: "http", URL: server.URL, Cwd: "/remote/workspace", Headers: map[string]string{"Authorization": "Bearer test-token"}, CAFile: caFile}
	step := ir.Step{ExecutorConfig: ir.ExecutorConfig{Config: map[string]any{"provider": "remote"}}, Commands: []ir.CommandEntry{{Command: "hello"}}}
	ctx := newHarnessTestContext(t, &ir.DAG{Harnesses: ir.HarnessDefinitions{"remote": def}}, step)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)
	var out bytes.Buffer
	exec.SetStdout(&out)
	require.NoError(t, exec.Run(ctx))
	require.Equal(t, "http:hello", out.String())
	require.EqualValues(t, 1, deleted.Load())
	require.Eventually(t, func() bool { return connections.Load() == 0 }, time.Second, 10*time.Millisecond, "private CA transport left an idle connection open")
}

// TestACPRemoteClientReleaseClosesBusyConnections verifies that releasing a
// private CA client closes a connection still carrying a request, which
// closing idle connections leaves open.
func TestACPRemoteClientReleaseClosesBusyConnections(t *testing.T) {
	streaming, stop := make(chan struct{}), make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(streaming)
		// The stream lasts until the client goes, or the test ends, so a
		// failure does not hold the server open.
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	server.EnableHTTP2 = true
	var open atomic.Int32
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			open.Add(1)
		case http.StateClosed, http.StateHijacked:
			open.Add(-1)
		}
	}
	server.StartTLS()
	defer server.Close()
	defer close(stop)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}), 0o600))
	client, release, err := acpRemoteHTTPClient(caFile, t.TempDir())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		response, err := client.Get(server.URL)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-streaming:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not reach the server")
	}
	release()
	select {
	case err := <-done:
		require.Error(t, err, "the busy connection was closed under the request")
	case <-time.After(5 * time.Second):
		t.Fatal("release left the busy connection open")
	}
	require.Eventually(t, func() bool { return open.Load() == 0 }, 5*time.Second, 10*time.Millisecond)

	_, err = client.Get(server.URL)
	require.Error(t, err, "a released client dials nothing new")
}
