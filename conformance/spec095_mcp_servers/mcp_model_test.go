// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec095_mcp_servers_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// mcpChatModel stands for the model API of a chat step with a named MCP server. The
// first request gets a call of the tool docs_search, and the next one gets the
// answer. It records the names of the tools and the body of each request.
type mcpChatModel struct {
	server *httptest.Server

	mu   sync.Mutex
	recs []mcpModelRecord
}

type mcpModelRecord struct {
	tools []string
	body  string
}

func startMCPChatModel(t *testing.T) *mcpChatModel {
	t.Helper()
	m := &mcpChatModel{}
	// The coordinator reaches the fake on the loopback address of the host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	m.server = httptest.NewUnstartedServer(http.HandlerFunc(m.serve))
	m.server.Listener = listener
	m.server.Start()
	t.Cleanup(m.server.Close)
	return m
}

func (m *mcpChatModel) URL() string { return m.server.URL }

func (m *mcpChatModel) records() []mcpModelRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mcpModelRecord(nil), m.recs...)
}

func (m *mcpChatModel) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(raw, &body)
	record := mcpModelRecord{body: string(raw)}
	for _, tool := range body.Tools {
		record.tools = append(record.tools, tool.Name)
	}
	m.mu.Lock()
	m.recs = append(m.recs, record)
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/v1/messages" {
		http.Error(w, "unexpected path", http.StatusNotFound)
		return
	}
	if strings.Contains(string(raw), "tool_result") {
		_, _ = io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"text","text":"The docs say: found."}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		return
	}
	_, _ = io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"tu-1","name":"docs_search","input":{"query":"dagu"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`)
}
