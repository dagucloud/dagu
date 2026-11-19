// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const relayMCPToken = "mcp-token-THE-REAL-TOKEN-0123456789"

// mcpSeen is what the fake MCP server saw of one request.
type mcpSeen struct {
	method string
	header http.Header
	body   string
}

// startMCP creates the entry docs with its token and the fake MCP server that it
// names.
func (f *relayFixture) startMCP(t *testing.T, opts relayOptions, secrets secret.Store) {
	t.Helper()
	handler := opts.mcpHandler
	if handler == nil {
		handler = func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "session-1")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
		}
	}
	f.mcpUpstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		f.mcpMu.Lock()
		f.mcpRequests = append(f.mcpRequests, mcpSeen{method: r.Method, header: r.Header.Clone(), body: string(data)})
		f.mcpMu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.mcpUpstream.Close)

	servers, err := store.NewAIMCPServerStore(testutil.NewMemoryBackend().Collection("mcp"))
	require.NoError(t, err)
	f.mcp, err = aiendpoint.NewMCPService(servers, secrets)
	require.NoError(t, err)
	allow := opts.mcpAllowDAGs
	if allow == nil {
		allow = []string{"fix-bug"}
	}
	_, err = f.mcp.Create(context.Background(), aiendpoint.MCPCreateInput{
		Name: "docs", URL: f.mcpUpstream.URL + "/mcp", Scope: aiendpoint.GlobalScope(),
		Allow:       aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: allow},
		Credentials: aiendpoint.MCPCredentialsInput{Token: relayMCPToken}, Actor: "test",
	})
	require.NoError(t, err)
	// An entry that the step does not name.
	_, err = f.mcp.Create(context.Background(), aiendpoint.MCPCreateInput{
		Name: "wiki", URL: f.mcpUpstream.URL + "/wiki", Scope: aiendpoint.GlobalScope(),
		Allow: aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"*"}}, Actor: "test",
	})
	require.NoError(t, err)
}

func (f *relayFixture) mcpSeen() []mcpSeen {
	f.mcpMu.Lock()
	defer f.mcpMu.Unlock()
	return append([]mcpSeen(nil), f.mcpRequests...)
}

func mcpPost(body string) aigateway.HTTPRequest {
	return aigateway.HTTPRequest{
		Method: http.MethodPost, Path: "/mcp/docs",
		Header: http.Header{
			"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"},
		},
		ContentLength: int64(len(body)),
	}
}

func callBody(id int, tool string) string {
	return `{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
}

// The token reaches the MCP server and no other place. The allow list comes from
// the DAG that the coordinator holds.
func TestAIRelayMCP_RelaysWithTheRealToken(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{withMCP: true})
	client := f.good()
	docs := aigateway.MCPTarget("docs")

	_, err := client.Check(context.Background(), relayStep, docs, "")
	require.NoError(t, err)

	sink := newMemSink()
	body := callBody(1, "search")
	req := mcpPost(body)
	req.Header.Set("Authorization", "Bearer relay-secret")
	require.NoError(t, client.Relay(context.Background(), relayStep, docs, req, strings.NewReader(body), sink))
	assert.Equal(t, http.StatusOK, sink.status)
	assert.Equal(t, "session-1", sink.header.Get("Mcp-Session-Id"))
	assert.NotContains(t, sink.text(), relayMCPToken)

	seen := f.mcpSeen()
	require.Len(t, seen, 1)
	assert.Equal(t, "Bearer "+relayMCPToken, seen[0].header.Get("Authorization"))
	assert.NotContains(t, seen[0].header.Get("Authorization"), "relay-secret")
	assert.Equal(t, body, seen[0].body)

	// The audit line names the target and the tool. It has no token.
	records := f.audit.all()
	require.Len(t, records, 1)
	assert.Equal(t, "mcp", records[0].TargetKind)
	assert.Equal(t, "docs", records[0].Name)
	assert.Equal(t, "search", records[0].Tool)
	assert.Equal(t, "worker-1", records[0].WorkerID)
	assert.NotContains(t, records[0].Fingerprint, relayMCPToken)
	for _, header := range f.upstreamHeaders() {
		assert.Empty(t, header, "no model request was made")
	}
}

func TestAIRelayMCP_ToolOutsideAllowIsRefusedByTheCoordinator(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{withMCP: true})
	client := f.good()

	sink := newMemSink()
	body := callBody(5, "drop_tables")
	require.NoError(t, client.Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), mcpPost(body), strings.NewReader(body), sink))
	assert.Equal(t, http.StatusOK, sink.status)
	assert.Contains(t, sink.text(), "mcp_tool_not_allowed")
	assert.Contains(t, sink.text(), `"id":5`)
	assert.Empty(t, f.mcpSeen(), "the refused call never reaches the server")

	// A batch with one refused item is refused as a whole.
	batch := "[" + callBody(1, "search") + "," + callBody(2, "drop_tables") + "]"
	sink = newMemSink()
	require.NoError(t, client.Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), mcpPost(batch), strings.NewReader(batch), sink))
	assert.Contains(t, sink.text(), "mcp_tool_not_allowed")
	assert.Empty(t, f.mcpSeen())

	// A body that is not JSON-RPC is refused.
	sink = newMemSink()
	err := client.Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), mcpPost("hello"), strings.NewReader("hello"), sink)
	assert.Equal(t, aigateway.ReasonMCPRequestInvalid, asFailure(t, err).Reason)
	assert.Empty(t, f.mcpSeen())
}

// A worker cannot widen the allow list: the stream carries no list, and the step
// of the DAG fixes it.
func TestAIRelayMCP_TheWorkerCannotNameAnotherServerOrTarget(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{withMCP: true})
	client := f.good()
	body := callBody(1, "search")

	// The step does not name wiki.
	err := client.Relay(context.Background(), relayStep, aigateway.MCPTarget("wiki"), mcpPost(body), strings.NewReader(body), newMemSink())
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, asFailure(t, err).Reason)
	_, err = client.Check(context.Background(), relayStep, aigateway.MCPTarget("wiki"), "")
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, asFailure(t, err).Reason)
	// The step names ghost, but no entry exists.
	_, err = client.Check(context.Background(), relayStep, aigateway.MCPTarget("ghost"), "")
	assert.Equal(t, aigateway.ReasonMCPServerUnknown, asFailure(t, err).Reason)
	// The step "plain" names no MCP server, and an inline entry is no target.
	_, err = client.Check(context.Background(), aigateway.StepRef{DAGName: "fix-bug", StepName: "plain"}, aigateway.MCPTarget("docs"), "")
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, asFailure(t, err).Reason)
	_, err = client.Check(context.Background(), relayStep, aigateway.MCPTarget("inline"), "")
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, asFailure(t, err).Reason)
	// The worker of another attempt has no access.
	other := f.relayClient("worker-cert", "worker-2", f.attemptK, f.attemptI)
	_, err = other.Check(context.Background(), relayStep, aigateway.MCPTarget("docs"), "")
	assert.Equal(t, aigateway.ReasonEndpointForbidden, asFailure(t, err).Reason)
	assert.Empty(t, f.mcpSeen())
}

func TestAIRelayMCP_ForbiddenDAGAndDeletedEntry(t *testing.T) {
	t.Parallel()
	f := startRelayFixture(t, relayOptions{withMCP: true, mcpAllowDAGs: []string{"another-dag"}})
	client := f.good()
	body := callBody(1, "search")

	_, err := client.Check(context.Background(), relayStep, aigateway.MCPTarget("docs"), "")
	failure := asFailure(t, err)
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, failure.Reason)
	assert.Contains(t, failure.Message, "not in the allow list")
	err = client.Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), mcpPost(body), strings.NewReader(body), newMemSink())
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, asFailure(t, err).Reason)
	assert.Empty(t, f.mcpSeen())

	// A new use rule applies to the next request, and so does a delete.
	allow := aiendpoint.Allow{Workspaces: []string{"*"}, DAGs: []string{"fix-bug"}}
	_, err = f.mcp.Update(context.Background(), aiendpoint.GlobalScope(), "docs", aiendpoint.MCPUpdateInput{Allow: &allow})
	require.NoError(t, err)
	require.NoError(t, client.Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), mcpPost(body), strings.NewReader(body), newMemSink()))
	require.NoError(t, f.mcp.Delete(context.Background(), aiendpoint.GlobalScope(), "docs"))
	err = client.Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), mcpPost(body), strings.NewReader(body), newMemSink())
	assert.Equal(t, aigateway.ReasonMCPServerUnknown, asFailure(t, err).Reason)
	assert.Len(t, f.mcpSeen(), 1)
}

func TestAIRelayMCP_SSEArrivesChunkByChunkAndCancels(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	ended := make(chan struct{})
	f := startRelayFixture(t, relayOptions{withMCP: true, mcpHandler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"one\"}\n\n"))
		flusher.Flush()
		select {
		case <-release:
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"two\"}\n\n"))
			flusher.Flush()
		case <-r.Context().Done():
			close(ended)
		}
	}})
	client := f.good()
	req := aigateway.HTTPRequest{Method: http.MethodGet, Path: "/mcp/docs", Header: http.Header{"Accept": {"text/event-stream"}}}

	// The first event arrives before the second leaves the server.
	sink := newMemSink()
	done := make(chan error, 1)
	go func() {
		done <- client.Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), req, strings.NewReader(""), sink)
	}()
	select {
	case <-sink.first:
	case <-time.After(5 * time.Second):
		t.Fatal("the first event did not arrive before the end of the stream")
	}
	assert.Contains(t, sink.text(), `"method":"one"`)
	assert.NotContains(t, sink.text(), `"method":"two"`)
	close(release)
	require.NoError(t, <-done)
	assert.Contains(t, sink.text(), `"method":"two"`)

	// The stop of the client cancels the call to the server.
	f2 := startRelayFixture(t, relayOptions{withMCP: true, mcpHandler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	}})
	ctx, cancel := context.WithCancel(context.Background())
	sink = newMemSink()
	done = make(chan error, 1)
	go func() {
		done <- f2.good().Relay(ctx, relayStep, aigateway.MCPTarget("docs"), req, strings.NewReader(""), sink)
	}()
	<-sink.first
	cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the call to the MCP server stayed open after the cancel")
	}
	assert.Error(t, <-done)
}

// A lost lease cancels the open MCP streams, as it cancels the model streams.
func TestAIRelayMCP_LostLeaseCancelsTheStream(t *testing.T) {
	t.Parallel()
	ended := make(chan struct{})
	f := startRelayFixture(t, relayOptions{withMCP: true, recheck: 100 * time.Millisecond, mcpHandler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	}})
	req := aigateway.HTTPRequest{Method: http.MethodGet, Path: "/mcp/docs", Header: http.Header{"Accept": {"text/event-stream"}}}
	sink := newMemSink()
	done := make(chan error, 1)
	go func() {
		done <- f.good().Relay(context.Background(), relayStep, aigateway.MCPTarget("docs"), req, strings.NewReader(""), sink)
	}()
	<-sink.first
	require.NoError(t, f.leases.Delete(context.Background(), f.attemptK))
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the lost lease did not cancel the stream")
	}
	failure := asFailure(t, <-done)
	assert.Equal(t, aigateway.ReasonRelayUnavailable, failure.Reason)
	assert.Contains(t, failure.Message, "lease")
}

func TestAIRelayMCP_StepWithNoMCPEntryHasNoMCPTarget(t *testing.T) {
	t.Parallel()
	// The step code of this fixture has no named MCP entry.
	f := startRelayFixture(t, relayOptions{})
	client := f.good()
	_, err := client.Check(context.Background(), relayStep, aigateway.MCPTarget("docs"), "")
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, asFailure(t, err).Reason, "the step names no MCP server")
}
