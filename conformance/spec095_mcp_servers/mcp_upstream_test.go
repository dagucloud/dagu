// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec095_mcp_servers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mcpToken is the token of the MCP server entry docs. Only the coordinator and the
// fake MCP server know it. It must reach the server, and no other place.
const mcpToken = "mcp-token-REAL-0123456789abcdefghijklmnop"

// fakeMCP stands for a MCP server over the streamable HTTP transport. It records each
// request: the method, the path, the headers and the body. It answers the JSON-RPC
// methods that a client uses, with no session. The path of the request selects the
// server: /docs is the server of the entry docs and /inline is a server that a step
// names with a url.
//
// A GET on /docs answers with a stream of events. The mode sse sends one event, waits
// for the test to call release, and sends a second one. The mode block sends one event
// and keeps the call open until the client goes away.
type fakeMCP struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	requests []mcpRequest
	mode     string

	releaseOnce sync.Once
	release     chan struct{}
	cancelled   chan struct{} // closed when a call of the mode block ends before the end
	cancelOnce  sync.Once
	started     chan struct{} // closed at the first GET of the mode sse or block
	startOnce   sync.Once
}

// mcpRequest is what the fake saw of one request.
type mcpRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

func startFakeMCP(t *testing.T, mode string) *fakeMCP {
	t.Helper()
	m := &fakeMCP{
		t:         t,
		mode:      mode,
		release:   make(chan struct{}),
		cancelled: make(chan struct{}),
		started:   make(chan struct{}),
	}
	// The coordinator reaches the fake on the loopback address of the host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	m.server = httptest.NewUnstartedServer(http.HandlerFunc(m.serve))
	m.server.Listener = listener
	m.server.Start()
	t.Cleanup(func() {
		m.releaseNow()
		m.server.Close()
	})
	return m
}

// URL is the address of the fake, without a path.
func (m *fakeMCP) URL() string { return m.server.URL }

func (m *fakeMCP) releaseNow() { m.releaseOnce.Do(func() { close(m.release) }) }

func (m *fakeMCP) all() []mcpRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mcpRequest(nil), m.requests...)
}

// onPath returns the requests that reached a path of the fake.
func (m *fakeMCP) onPath(path string) []mcpRequest {
	var out []mcpRequest
	for _, request := range m.all() {
		if request.Path == path {
			out = append(out, request)
		}
	}
	return out
}

// tools returns the names of the tools that the requests called.
func toolsCalled(requests []mcpRequest) []string {
	var names []string
	for _, request := range requests {
		for _, item := range rpcItems(request.Body) {
			if item.Method != "tools/call" {
				continue
			}
			var params struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(item.Params, &params)
			names = append(names, params.Name)
		}
	}
	return names
}

type rpcItem struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// rpcItems reads the items of a JSON-RPC body: one message, or a batch.
func rpcItems(body string) []rpcItem {
	trimmed := bytes.TrimSpace([]byte(body))
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		var items []rpcItem
		_ = json.Unmarshal(trimmed, &items)
		return items
	}
	var item rpcItem
	if json.Unmarshal(trimmed, &item) != nil {
		return nil
	}
	return []rpcItem{item}
}

func (m *fakeMCP) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.requests = append(m.requests, mcpRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body),
	})
	mode := m.mode
	m.mu.Unlock()

	switch r.Method {
	case http.MethodPost:
		m.serveRPC(w, body)
	case http.MethodGet:
		m.serveStream(w, r, mode)
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// answer returns the result of one JSON-RPC item, or nil for a notification.
func answer(item rpcItem) map[string]any {
	if len(item.ID) == 0 || string(item.ID) == "null" {
		return nil
	}
	response := map[string]any{"jsonrpc": "2.0", "id": item.ID}
	switch item.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(item.Params, &params)
		if params.ProtocolVersion == "" {
			params.ProtocolVersion = "2025-06-18"
		}
		response["result"] = map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fake-mcp", "version": "1"},
		}
	case "tools/list":
		response["result"] = map[string]any{"tools": []any{
			map[string]any{
				"name": "search", "description": "Search the docs",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}},
			},
			map[string]any{"name": "drop_tables", "description": "Not in allow", "inputSchema": map[string]any{"type": "object"}},
		}}
	case "tools/call":
		var params struct {
			Name      string `json:"name"`
			Arguments struct {
				Query string `json:"query"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(item.Params, &params)
		response["result"] = map[string]any{"content": []any{
			map[string]any{"type": "text", "text": fmt.Sprintf("%s result for %s", params.Name, params.Arguments.Query)},
		}}
	default:
		response["result"] = map[string]any{}
	}
	return response
}

func (m *fakeMCP) serveRPC(w http.ResponseWriter, body []byte) {
	items := rpcItems(string(body))
	if len(items) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var answers []map[string]any
	for _, item := range items {
		if response := answer(item); response != nil {
			answers = append(answers, response)
		}
	}
	if len(answers) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		_ = json.NewEncoder(w).Encode(answers)
		return
	}
	_ = json.NewEncoder(w).Encode(answers[0])
}

func (m *fakeMCP) serveStream(w http.ResponseWriter, r *http.Request, mode string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/one\"}\n\n")
	flusher.Flush()
	m.startOnce.Do(func() { close(m.started) })
	switch mode {
	case "block":
		select {
		case <-r.Context().Done():
			m.cancelOnce.Do(func() { close(m.cancelled) })
		case <-time.After(90 * time.Second):
		}
	default:
		select {
		case <-m.release:
		case <-r.Context().Done():
			return
		case <-time.After(60 * time.Second):
		}
		_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/two\"}\n\n")
	}
}
