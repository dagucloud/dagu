// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec059_chat_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// lastStepStdout reads the exact bytes the last step in the run wrote to
// stdout, by locating that step's captured-output log file from dagu
// start's own tree render (the last "stdout:" line in a multi-step run)
// and reading it directly, since the tree render re-wraps long lines with
// its own indentation, which would corrupt a strict content match.
func lastStepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	matches := stdoutLogPattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.NotEmptyf(t, matches, "expected a stdout log path in output:\n%s", daguStartOutput)
	path := strings.TrimSpace(matches[len(matches)-1][1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// mockMessage is the OpenAI-compatible message shape a chat.completion
// request sends, decoded for test assertions.
type mockMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// mockRequest is the subset of an OpenAI-compatible chat completion request
// this mock server captures for test assertions.
type mockRequest struct {
	Model       string        `json:"model"`
	Messages    []mockMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature *float64      `json:"temperature"`
	MaxTokens   *int          `json:"max_tokens"`
	TopP        *float64      `json:"top_p"`
	Tools       []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

// mockToolCall describes a tool call a mockResponse asks the server to
// request from the client.
type mockToolCall struct {
	ID        string
	Name      string
	Arguments string // a JSON-encoded arguments object
}

// mockResponse describes how the mock server should answer one request.
type mockResponse struct {
	StatusCode int // 0 means 200
	Content    string
	ToolCalls  []mockToolCall
}

// mockLLMServer is a minimal OpenAI-compatible chat completions server
// (matching internal/llm/providers/local's wire format) driven by a
// per-request respond function, so each test controls exactly what the
// "model" answers.
type mockLLMServer struct {
	*httptest.Server

	mu       sync.Mutex
	requests []mockRequest
}

// newMockLLMServer starts a server that calls respond for every request it
// receives (in order) to decide how to answer, and records every decoded
// request for later assertions via Requests().
func newMockLLMServer(t *testing.T, respond func(n int, req mockRequest) mockResponse) *mockLLMServer {
	t.Helper()

	srv := &mockLLMServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req mockRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		srv.mu.Lock()
		n := len(srv.requests)
		srv.requests = append(srv.requests, req)
		srv.mu.Unlock()

		resp := respond(n, req)

		if resp.StatusCode != 0 && resp.StatusCode != http.StatusOK {
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write([]byte(`{"error":"mock error"}`))
			return
		}

		if req.Stream {
			writeMockStream(t, w, resp)
			return
		}
		writeMockJSON(t, w, resp)
	})

	srv.Server = httptest.NewServer(mux)
	t.Cleanup(srv.Server.Close)
	return srv
}

// Requests returns every request this server has decoded so far, in order.
func (s *mockLLMServer) Requests() []mockRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mockRequest(nil), s.requests...)
}

// BaseURL returns the "PROVIDER=local base_url" env assignment fixtures
// resolve via ${LLM_BASE_URL}.
func (s *mockLLMServer) BaseURLEnv() string {
	return "LLM_BASE_URL=" + s.Server.URL
}

func writeMockJSON(t *testing.T, w http.ResponseWriter, resp mockResponse) {
	t.Helper()

	message := map[string]any{"role": "assistant", "content": resp.Content}
	if len(resp.ToolCalls) > 0 {
		message["content"] = ""
		message["tool_calls"] = toolCallsJSON(resp.ToolCalls)
	}

	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{
			{"message": message, "finish_reason": "stop"},
		},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	}))
}

// writeMockStream writes resp.Content as a single streamed delta. Tool
// calling always uses a non-streaming request (see runWithToolsForModel),
// so a streamed response never carries tool calls.
func writeMockStream(t *testing.T, w http.ResponseWriter, resp mockResponse) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	require.True(t, ok, "response writer must support flushing for a streamed mock response")

	chunk, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{"delta": map[string]any{"content": resp.Content}}},
	})
	require.NoError(t, err)

	_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
	flusher.Flush()
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func toolCallsJSON(calls []mockToolCall) []map[string]any {
	result := make([]map[string]any, len(calls))
	for i, c := range calls {
		result[i] = map[string]any{
			"id":   c.ID,
			"type": "function",
			"function": map[string]any{
				"name":      c.Name,
				"arguments": c.Arguments,
			},
		}
	}
	return result
}
