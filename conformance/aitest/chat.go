// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aitest

import (
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// ChatUpstream stands for a model API. It serves the Anthropic form on
// /v1/messages and the OpenAI form on /chat/completions. It records each request.
// The model bad-model gets a 400. A request that offers the tool respond gets a
// call of that tool.
type ChatUpstream struct {
	// Server is the HTTP server of the fake. A suite that wraps the fake sets it.
	Server *httptest.Server

	mu       sync.Mutex
	requests []ChatRecord
}

// ChatRecord is what the fake saw of one request.
type ChatRecord struct {
	Path   string
	Header http.Header
	Model  string
	Stream bool
}

// StartChatUpstream starts the fake on the loopback address of the host.
func StartChatUpstream(t *testing.T) *ChatUpstream {
	t.Helper()
	u := &ChatUpstream{}
	// The coordinator reaches the fake on the loopback address of the host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	u.Server = httptest.NewUnstartedServer(http.HandlerFunc(u.Serve))
	u.Server.Listener = listener
	u.Server.Start()
	t.Cleanup(u.Server.Close)
	return u
}

// URL is the base URL of the fake.
func (u *ChatUpstream) URL() string { return u.Server.URL }

// All returns the requests that the fake saw.
func (u *ChatUpstream) All() []ChatRecord {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]ChatRecord(nil), u.requests...)
}

// Serve answers one request.
func (u *ChatUpstream) Serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
		Tools  []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(raw, &body)
	u.mu.Lock()
	u.requests = append(u.requests, ChatRecord{Path: r.URL.Path, Header: r.Header.Clone(), Model: body.Model, Stream: body.Stream})
	u.mu.Unlock()

	if body.Model == "bad-model" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"model unavailable"}}`)
		return
	}
	switch r.URL.Path {
	case "/v1/messages":
		u.serveMessages(w, body.Stream, body.Tools)
	case "/chat/completions":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"Hello from the old method"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	default:
		http.Error(w, "unexpected path", http.StatusNotFound)
	}
}

func (u *ChatUpstream) serveMessages(w http.ResponseWriter, stream bool, tools []struct {
	Name string `json:"name"`
}) {
	for _, tool := range tools {
		if tool.Name == "respond" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"respond","input":{"category":"refund","amount":12.5}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
	}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"world\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"text","text":"Hello world"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
}

// ReadResult returns the file result.out of the project, which the step wrote, and removes it.
func ReadResult(t *testing.T, runner *harness.Runner) string {
	t.Helper()
	path := runner.ProjectPath("result.out")
	data, err := os.ReadFile(path) // #nosec G304 -- the path is in the project of the test.
	require.NoError(t, err)
	// The step appends to its stdout file, so the next run starts with no file.
	require.NoError(t, os.Remove(path))
	return string(data)
}

// StepLogs returns the text of each log file of a step in the home of the coordinator.
func StepLogs(t *testing.T, home string) string {
	t.Helper()
	var text strings.Builder
	require.NoError(t, filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(filepath.Base(path), "stdout") {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 G122 -- the path is in the home of the test.
		if readErr == nil {
			text.Write(data)
		}
		return nil
	}))
	return text.String()
}
