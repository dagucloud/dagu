// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec067_harness_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessACPHTTP verifies ACP POST/SSE/DELETE through the real Dagu binary.
func TestHarnessACPHTTP(t *testing.T) {
	connection, session := make(chan any, 4), make(chan any, 4)
	var deleted atomic.Bool
	response := func(id, result any) any { return map[string]any{"jsonrpc": "2.0", "id": id, "result": result} }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		if r.Method == http.MethodGet {
			queue := connection
			if r.Header.Get("Acp-Session-Id") != "" {
				assert.Equal(t, "session", r.Header.Get("Acp-Session-Id"))
				queue = session
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			for {
				select {
				case msg := <-queue:
					data, _ := json.Marshal(msg)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
		if r.Method == http.MethodDelete {
			deleted.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var msg struct {
			ID     any
			Method string
			Params struct {
				Cwd    string
				Prompt []struct{ Text string }
			}
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&msg)) {
			return
		}
		if msg.Method == "initialize" {
			w.Header().Set("Acp-Connection-Id", "connection")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response(msg.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}))
			return
		}
		assert.Equal(t, "connection", r.Header.Get("Acp-Connection-Id"))
		w.WriteHeader(http.StatusAccepted)
		if msg.Method == "session/new" {
			assert.Equal(t, "/srv/project", msg.Params.Cwd)
			connection <- response(msg.ID, map[string]any{"sessionId": "session"})
		} else if msg.Method == "session/prompt" && assert.Len(t, msg.Params.Prompt, 1) {
			assert.Equal(t, "session", r.Header.Get("Acp-Session-Id"))
			session <- map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": "session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "http:" + msg.Params.Prompt[0].Text}},
			}}
			session <- response(msg.ID, map[string]any{"stopReason": "end_turn"})
		}
	}))
	defer server.Close()
	dagu := harness.NewRunner(t)
	dagu.WriteFile("http.yaml", fmt.Sprintf(`harnesses:
  remote:
    protocol: acp
    transport: http
    url: %q
    cwd: /srv/project
    headers:
      Authorization: Bearer test-token
steps:
  - action: harness.run
    with:
      provider: remote
      prompt: hello
`, server.URL))
	dagu.Run("validate", "http.yaml").ExpectExitCode(0)
	result := dagu.Run("start", "http.yaml")
	result.ExpectExitCode(0)
	require.Equal(t, "http:hello", stepStdout(t, result.Stdout(), 0))
	require.True(t, deleted.Load())
}
