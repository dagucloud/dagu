// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
)

const mcpToken = "mcp-token-SECRET-0123456789"

// fakeMCPSource is a MCPSource with one entry.
type fakeMCPSource struct {
	mu      sync.Mutex
	server  aiendpoint.MCPServer
	creds   aiendpoint.MCPCredentials
	resolve error
	readErr error
}

func (f *fakeMCPSource) ResolveMCPServer(_ context.Context, name, _, _ string) (aiendpoint.MCPServer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resolve != nil {
		return aiendpoint.MCPServer{}, f.resolve
	}
	if name != f.server.Name {
		return aiendpoint.MCPServer{}, &aiendpoint.Error{Reason: aiendpoint.ReasonMCPUnknown, Name: name, Kind: "MCP server"}
	}
	return f.server, nil
}

func (f *fakeMCPSource) ReadCredentials(context.Context, aiendpoint.MCPServer) (aiendpoint.MCPCredentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creds, f.readErr
}

func newMCPGateway(t *testing.T, src *fakeMCPSource, limits aigateway.Limits, audit aigateway.AuditFunc) *aigateway.Gateway {
	t.Helper()
	g, err := aigateway.New(aigateway.Config{
		Endpoints:  &fakeSource{},
		MCPServers: src,
		Limits:     limits,
		Audit:      audit,
	})
	require.NoError(t, err)
	return g
}

func mcpSourceFor(url string) *fakeMCPSource {
	return &fakeMCPSource{
		server: aiendpoint.MCPServer{
			Name: "docs", URL: url, Scope: aiendpoint.GlobalScope(),
			Token: &aiendpoint.Credential{Header: "authorization", Fingerprint: "0123456789ab"},
		},
		creds: aiendpoint.MCPCredentials{Token: mcpToken},
	}
}

func mcpRequest(method, body string) aigateway.Request {
	return aigateway.Request{
		Subject:       aigateway.Subject{RunID: "run-1", DAGName: "fix-bug", StepName: "code", WorkerID: "w1", AttemptKey: "key-1"},
		Target:        aigateway.MCPTarget("docs"),
		MCPAllow:      []string{"search"},
		Method:        method,
		Path:          "/mcp/docs",
		Header:        http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}},
		ContentLength: int64(len(body)),
		Body:          strings.NewReader(body),
	}
}

func rpcCall(id int, tool string) string {
	return `{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
}

func TestDoMCP_AddsTheCredentialsAndKeepsTheAllowedHeaders(t *testing.T) {
	t.Parallel()
	var got http.Header
	var gotPath, gotQuery, gotMethod, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotPath, gotQuery, gotMethod = r.Header.Clone(), r.URL.Path, r.URL.RawQuery, r.Method
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "session-1")
		w.Header().Set("Set-Cookie", "a=b")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer upstream.Close()

	var records []aigateway.AuditRecord
	g := newMCPGateway(t, mcpSourceFor(upstream.URL+"/server/mcp?workspace=main"), testLimits(),
		func(_ context.Context, r aigateway.AuditRecord) { records = append(records, r) })

	body := rpcCall(1, "search")
	req := mcpRequest(http.MethodPost, body)
	req.Header.Set("Authorization", "Bearer relay-secret-of-the-attempt")
	req.Header.Set("X-Api-Key", "placeholder")
	req.Header.Set("Cookie", "x=y")
	req.Header.Set("X-Evil", "1")
	req.Header.Set("Mcp-Session-Id", "client-session")
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	req.Header.Set("Last-Event-Id", "7")
	sink := &recSink{}
	require.Nil(t, g.Do(context.Background(), req, sink))

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/server/mcp", gotPath, "the call goes to the URL of the entry")
	assert.Equal(t, "workspace=main", gotQuery)
	assert.Equal(t, body, gotBody)
	assert.Equal(t, "Bearer "+mcpToken, got.Get("Authorization"), "the token of the entry replaces the secret of the relay")
	assert.NotContains(t, strings.Join(got.Values("Authorization"), ","), "relay-secret")
	assert.Empty(t, got.Get("X-Api-Key"))
	assert.Empty(t, got.Get("Cookie"))
	assert.Empty(t, got.Get("X-Evil"))
	assert.Equal(t, "identity", got.Get("Accept-Encoding"))
	assert.Equal(t, "client-session", got.Get("Mcp-Session-Id"))
	assert.Equal(t, "2025-06-18", got.Get("Mcp-Protocol-Version"))
	assert.Equal(t, "7", got.Get("Last-Event-Id"))
	assert.Equal(t, "application/json, text/event-stream", got.Get("Accept"))

	assert.Equal(t, http.StatusOK, sink.status)
	assert.Equal(t, "session-1", sink.header.Get("Mcp-Session-Id"), "the session id reaches the client")
	assert.Empty(t, sink.header.Get("Set-Cookie"))

	require.Len(t, records, 1)
	r := records[0]
	assert.Equal(t, "mcp", r.TargetKind)
	assert.Equal(t, "docs", r.Name)
	assert.Equal(t, "mcp", r.Route)
	assert.Equal(t, "tools/call", r.RPCMethod)
	assert.Equal(t, "search", r.Tool)
	assert.Equal(t, "0123456789ab", r.Fingerprint)
	assert.Equal(t, http.StatusOK, r.Status)
	assert.NotContains(t, strings.Join([]string{r.Name, r.Route, r.Method, r.Fingerprint, r.RPCMethod, r.Tool, string(r.Reason)}, " "), mcpToken)
}

func TestDoMCP_HeaderCredentials(t *testing.T) {
	t.Parallel()
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	defer upstream.Close()
	src := mcpSourceFor(upstream.URL)
	src.server.Token = nil
	src.server.Headers = []aiendpoint.Credential{{Header: "x-api-key", Fingerprint: "aaaaaaaaaaaa"}, {Header: "x-team", Fingerprint: "bbbbbbbbbbbb"}}
	src.creds = aiendpoint.MCPCredentials{Headers: map[string]string{"x-api-key": "value-one", "x-team": "value-two"}}
	var records []aigateway.AuditRecord
	g := newMCPGateway(t, src, testLimits(), func(_ context.Context, r aigateway.AuditRecord) { records = append(records, r) })

	req := mcpRequest(http.MethodPost, rpcCall(1, "search"))
	req.Header.Set("X-Api-Key", "placeholder")
	require.Nil(t, g.Do(context.Background(), req, &recSink{}))

	assert.Equal(t, "value-one", got.Get("X-Api-Key"))
	assert.Equal(t, "value-two", got.Get("X-Team"))
	assert.Empty(t, got.Get("Authorization"))
	require.Len(t, records, 1)
	assert.Equal(t, "x-api-key:aaaaaaaaaaaa,x-team:bbbbbbbbbbbb", records[0].Fingerprint)
}

// A tool outside allow gets a JSON-RPC error and no call to the server.
func TestDoMCP_ToolOutsideAllowIsRefused(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	var records []aigateway.AuditRecord
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(),
		func(_ context.Context, r aigateway.AuditRecord) { records = append(records, r) })

	sink := &recSink{}
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, rpcCall(7, "delete_all")), sink))
	assert.Equal(t, int32(0), calls.Load())
	assert.Equal(t, http.StatusOK, sink.status)
	assert.Equal(t, "application/json", sink.header.Get("Content-Type"))
	var answer struct {
		ID    int `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(sink.body()), &answer))
	assert.Equal(t, 7, answer.ID, "the error has the id of the request")
	assert.Contains(t, answer.Error.Message, "mcp_tool_not_allowed")
	assert.Contains(t, answer.Error.Message, `"delete_all"`)
	assert.NotContains(t, sink.body(), mcpToken)

	require.Len(t, records, 1)
	assert.Equal(t, aigateway.ReasonMCPToolNotAllowed, records[0].Reason)
	assert.Equal(t, "delete_all", records[0].Tool)
	assert.Equal(t, http.StatusOK, records[0].Status)

	// An allowed tool goes through, and tools/list is not changed.
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, rpcCall(1, "search")), &recSink{}))
	assert.Equal(t, int32(1), calls.Load())
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, list), &recSink{}))
	assert.Equal(t, int32(2), calls.Load())
	init := `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}}`
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, init), &recSink{}))
	assert.Equal(t, int32(3), calls.Load())

	// A tool name that differs in case or by a prefix is another tool.
	for _, tool := range []string{"Search", "search ", "search2", ""} {
		sink := &recSink{}
		failure := g.Do(context.Background(), mcpRequest(http.MethodPost, rpcCall(1, tool)), sink)
		if tool == "" {
			require.NotNil(t, failure, "a call with no name is invalid")
			assert.Equal(t, aigateway.ReasonMCPRequestInvalid, failure.Reason)
			continue
		}
		require.Nil(t, failure)
		assert.Contains(t, sink.body(), "mcp_tool_not_allowed", tool)
	}
	assert.Equal(t, int32(3), calls.Load())
}

func TestDoMCP_BatchIsParsedItemByItem(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var gotBody atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		data, _ := io.ReadAll(r.Body)
		gotBody.Store(string(data))
		_, _ = w.Write([]byte(`[]`))
	}))
	defer upstream.Close()
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(), nil)

	// A batch with one refused call is refused as a whole. Each request gets an error.
	batch := "[" + rpcCall(1, "search") + "," + rpcCall(2, "drop") + `,{"jsonrpc":"2.0","method":"notifications/initialized"}]`
	sink := &recSink{}
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, batch), sink))
	assert.Equal(t, int32(0), calls.Load())
	var answers []struct {
		ID    int `json:"id"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(sink.body()), &answers))
	require.Len(t, answers, 2, "one error for each request of the batch")
	assert.Equal(t, 1, answers[0].ID)
	assert.Equal(t, 2, answers[1].ID)
	assert.Contains(t, answers[1].Error.Message, `"drop"`)
	assert.Contains(t, answers[0].Error.Message, "no item ran")

	// A batch of allowed calls goes through as it is.
	ok := "[" + rpcCall(1, "search") + "," + rpcCall(2, "search") + "]"
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, ok), &recSink{}))
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, ok, gotBody.Load())

	// A refused call that is a notification has no answer: the status is 202.
	notification := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"drop"}}`
	sink = &recSink{}
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, notification), sink))
	assert.Equal(t, http.StatusAccepted, sink.status)
	assert.Empty(t, sink.body())
	assert.Equal(t, int32(1), calls.Load())
}

func TestDoMCP_InvalidBodiesAreRefused(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(), nil)

	for name, body := range map[string]string{
		"empty":               "",
		"not json":            "hello",
		"truncated":           `{"jsonrpc":"2.0","id":1,`,
		"trailing data":       `{"jsonrpc":"2.0","id":1,"method":"ping"} {}`,
		"no version":          `{"id":1,"method":"ping"}`,
		"wrong version":       `{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		"no method":           `{"jsonrpc":"2.0","id":1}`,
		"method and result":   `{"jsonrpc":"2.0","id":1,"method":"ping","result":{}}`,
		"empty batch":         `[]`,
		"batch with a scalar": `[1]`,
		"batch not valid":     `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"id":2}]`,
		"call with no params": `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
		"call name not text":  `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":5}}`,
		"a json string":       `"tools/call"`,
	} {
		failure := g.Do(context.Background(), mcpRequest(http.MethodPost, body), &recSink{})
		require.NotNil(t, failure, name)
		assert.Equal(t, aigateway.ReasonMCPRequestInvalid, failure.Reason, name)
	}
	assert.Equal(t, int32(0), calls.Load(), "an invalid body never reaches the server")

	// A response of the client to a request of the server is valid.
	reply := `{"jsonrpc":"2.0","id":"s1","result":{}}`
	require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, reply), &recSink{}))
	assert.Equal(t, int32(1), calls.Load())
}

// The inspector and the MCP server must read the same body. A body that two JSON
// parsers can read in two ways is refused: a key that differs only by case from a key
// that the inspector reads, a duplicate key at any depth, a method that is not a
// plain ASCII name, and a body that is too deep or not UTF-8. The server never gets
// such a body (Spec 095, "The key allow").
func TestDoMCP_AmbiguousBodiesAreRefused(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(), nil)

	call := func(extra string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call",` + extra + `"params":{"name":"forbidden"}}`
	}
	deep := strings.Repeat("[", 300) + strings.Repeat("]", 300)
	tests := map[string]string{
		// Case variants of the keys that the inspector reads. Go matches them, a server does not.
		"method upper":       `{"jsonrpc":"2.0","id":1,"method":"tools/call","METHOD":"ping","params":{"name":"forbidden"}}`,
		"method mixed first": `{"jsonrpc":"2.0","id":1,"Method":"ping","method":"tools/call","params":{"name":"forbidden"}}`,
		"params upper":       `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"forbidden"},"PARAMS":{"name":"search"}}`,
		"name upper":         `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"forbidden","NAME":"search"}}`,
		"name title":         `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","Name":"forbidden"}}`,
		"arguments upper":    `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"forbidden","ARGUMENTS":{}}}`,
		"id upper":           `{"jsonrpc":"2.0","id":1,"ID":null,"method":"tools/call","params":{"name":"forbidden"}}`,
		"jsonrpc upper":      `{"jsonrpc":"2.0","JSONRPC":"2.0","id":1,"method":"ping"}`,
		"result upper":       `{"jsonrpc":"2.0","id":1,"result":{},"RESULT":{}}`,
		"error upper":        `{"jsonrpc":"2.0","id":1,"error":{},"Error":{}}`,
		"long s folds to s":  `{"jsonrpc":"2.0","id":1,"method":"tools/call","paramſ":{"name":"search"},"params":{"name":"forbidden"}}`,
		"notification":       `{"jsonrpc":"2.0","method":"tools/call","METHOD":"ping","params":{"name":"forbidden"}}`,
		// Duplicate keys: Go keeps the last one, a server can keep the first one.
		"method twice":         `{"jsonrpc":"2.0","id":1,"method":"ping","method":"tools/call","params":{"name":"forbidden"}}`,
		"method twice escaped": `{"jsonrpc":"2.0","id":1,"method":"tools/call","method":"ping","params":{"name":"forbidden"}}`,
		"params twice":         `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"forbidden"},"params":{"name":"search"}}`,
		"name twice":           `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"forbidden","name":"search"}}`,
		"name twice escaped":   `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"forbidden","name":"search"}}`,
		"id twice":             `{"jsonrpc":"2.0","id":1,"id":2,"method":"ping"}`,
		"jsonrpc twice":        `{"jsonrpc":"2.0","jsonrpc":"2.0","id":1,"method":"ping"}`,
		"nested twice":         `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"a":{"b":1,"b":2}}}}`,
		"nested in array":      `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"a":[{"b":1,"b":2}]}}}`,
		// The same cases in the items of a batch.
		"batch case variant": `[` + rpcCall(1, "search") + `,` + call(`"METHOD":"ping",`) + `]`,
		"batch duplicate":    `[` + rpcCall(1, "search") + `,{"jsonrpc":"2.0","id":2,"method":"ping","method":"tools/call","params":{"name":"forbidden"}}]`,
		"batch nested dup":   `[{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"a":1,"a":1}}}]`,
		// A method that is not a plain ASCII name can be read as tools/call by a lenient server.
		"method with space":        `{"jsonrpc":"2.0","id":1,"method":"tools/call ","params":{"name":"forbidden"}}`,
		"method with lead space":   `{"jsonrpc":"2.0","id":1,"method":" tools/call","params":{"name":"forbidden"}}`,
		"method with tab":          `{"jsonrpc":"2.0","id":1,"method":"tools/call\t","params":{"name":"forbidden"}}`,
		"method with zero width":   `{"jsonrpc":"2.0","id":1,"method":"tools/call\u200b","params":{"name":"forbidden"}}`,
		"method with fullwidth":    `{"jsonrpc":"2.0","id":1,"method":"tools／call","params":{"name":"forbidden"}}`,
		"method with nul":          `{"jsonrpc":"2.0","id":1,"method":"tools/call\u0000","params":{"name":"forbidden"}}`,
		"method upper tools/call":  `{"jsonrpc":"2.0","id":1,"method":"TOOLS/CALL","params":{"name":"forbidden"}}`,
		"method title tools/call":  `{"jsonrpc":"2.0","id":1,"method":"Tools/Call","params":{"name":"forbidden"}}`,
		"method is not text":       `{"jsonrpc":"2.0","id":1,"method":["tools/call"],"params":{"name":"forbidden"}}`,
		"too deep":                 `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":` + deep + `}}`,
		"invalid UTF-8 in a value": "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\",\"params\":{\"x\":\"\xff\"}}",
		"invalid UTF-8 in a key":   "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\",\"\xff\":1}",
	}
	for name, body := range tests {
		failure := g.Do(context.Background(), mcpRequest(http.MethodPost, body), &recSink{})
		require.NotNil(t, failure, name)
		assert.Equal(t, aigateway.ReasonMCPRequestInvalid, failure.Reason, name)
	}
	assert.Equal(t, int32(0), calls.Load(), "an ambiguous body never reaches the server")
}

// A body with no ambiguity goes to the server byte for byte, even when it has keys
// that look like the inspected keys at a place that the inspector does not read.
func TestDoMCP_UnambiguousBodiesAreForwardedAsTheyAre(t *testing.T) {
	t.Parallel()
	var got atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		got.Store(string(data))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(), nil)

	for name, body := range map[string]string{
		"arguments with the same names": " \n" + `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"Name":"x","METHOD":"y","ID":1,"method":"z"}}}` + "\n",
		"escaped tool name":             `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search"}}`,
		"result with its own keys":      `{"jsonrpc":"2.0","id":"s1","result":{"Method":"a","method":"b"}}`,
		"custom method":                 `{"jsonrpc":"2.0","id":1,"method":"notifications/x-y_z.1"}`,
		"batch":                         `[{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"ID":1}}},{"jsonrpc":"2.0","method":"notifications/initialized"}]`,
	} {
		require.Nil(t, g.Do(context.Background(), mcpRequest(http.MethodPost, body), &recSink{}), name)
		assert.Equal(t, body, got.Load(), name)
	}
}

func TestDoMCP_GetAndDelete(t *testing.T) {
	t.Parallel()
	var methods []string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method+" auth="+r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(), nil)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := mcpRequest(method, "")
		req.Header.Set("Mcp-Session-Id", "s1")
		sink := &recSink{}
		require.Nil(t, g.Do(context.Background(), req, sink), method)
		assert.Equal(t, http.StatusNoContent, sink.status)
	}
	assert.Equal(t, []string{"GET auth=Bearer " + mcpToken, "DELETE auth=Bearer " + mcpToken}, methods)
}

func TestDoMCP_RefusalsNeverCallTheServer(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	good := mcpSourceFor(upstream.URL)

	tests := []struct {
		name   string
		src    *fakeMCPSource
		mutate func(*aigateway.Request)
		want   aigateway.Reason
	}{
		{"unknown", &fakeMCPSource{resolve: &aiendpoint.Error{Reason: aiendpoint.ReasonMCPUnknown, Name: "docs", Kind: "MCP server"}}, nil, aigateway.ReasonMCPServerUnknown},
		{"forbidden", &fakeMCPSource{resolve: &aiendpoint.Error{Reason: aiendpoint.ReasonMCPForbidden, Name: "docs", Kind: "MCP server"}}, nil, aigateway.ReasonMCPServerForbidden},
		{"store failed", &fakeMCPSource{resolve: &aiendpoint.Error{Reason: aiendpoint.ReasonUnavailable, Name: "docs"}}, nil, aigateway.ReasonEndpointUnavailable},
		{"credentials unreadable", func() *fakeMCPSource {
			src := mcpSourceFor(upstream.URL)
			src.readErr = &aiendpoint.Error{Reason: aiendpoint.ReasonUnavailable, Name: "docs"}
			return src
		}(), nil, aigateway.ReasonEndpointUnavailable},
		{"other path", good, func(r *aigateway.Request) { r.Path = "/mcp/docs/extra" }, aigateway.ReasonPathNotAllowed},
		{"model path", good, func(r *aigateway.Request) { r.Path = "/v1/messages" }, aigateway.ReasonPathNotAllowed},
		{"another server in the path", good, func(r *aigateway.Request) { r.Path = "/mcp/other" }, aigateway.ReasonMCPServerForbidden},
		{"the bridge name", good, func(r *aigateway.Request) { r.Path = "/mcp/dagu" }, aigateway.ReasonPathNotAllowed},
		{"query", good, func(r *aigateway.Request) { r.RawQuery = "a=b" }, aigateway.ReasonPathNotAllowed},
		{"method", good, func(r *aigateway.Request) { r.Method = http.MethodPut }, aigateway.ReasonMethodNotAllowed},
		{"content encoding", good, func(r *aigateway.Request) { r.Header.Set("Content-Encoding", "gzip") }, aigateway.ReasonHostNotAllowed},
		{"declared length", good, func(r *aigateway.Request) { r.ContentLength = 1 << 30 }, aigateway.ReasonRequestTooLarge},
	}
	for _, tc := range tests {
		req := mcpRequest(http.MethodPost, rpcCall(1, "search"))
		if tc.mutate != nil {
			tc.mutate(&req)
		}
		failure := newMCPGateway(t, tc.src, testLimits(), nil).Do(context.Background(), req, &recSink{})
		require.NotNil(t, failure, tc.name)
		assert.Equal(t, tc.want, failure.Reason, tc.name)
		assert.NotContains(t, failure.Error(), mcpToken, tc.name)
	}
	assert.Equal(t, int32(0), calls.Load())

	// A process with no MCP store has no MCP gateway.
	g, err := aigateway.New(aigateway.Config{Endpoints: &fakeSource{}, Limits: testLimits()})
	require.NoError(t, err)
	failure := g.Do(context.Background(), mcpRequest(http.MethodPost, rpcCall(1, "search")), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointUnavailable, failure.Reason)
	_, failure = g.Check(context.Background(), aigateway.Subject{DAGName: "fix-bug"}, aigateway.MCPTarget("docs"), "")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonEndpointUnavailable, failure.Reason)
}

func TestDoMCP_BodyLimitsAndRedirect(t *testing.T) {
	t.Parallel()
	redirects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://evil.example.com/", http.StatusFound)
	}))
	defer redirects.Close()
	g := newMCPGateway(t, mcpSourceFor(redirects.URL), testLimits(), nil)
	failure := g.Do(context.Background(), mcpRequest(http.MethodPost, rpcCall(1, "search")), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonUpstreamError, failure.Reason, "a redirect is an upstream_error")

	limits := testLimits()
	limits.MaxRequestBytes = 64
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"q":"` + strings.Repeat("a", 200) + `"}}}`
	req := mcpRequest(http.MethodPost, big)
	req.ContentLength = -1
	failure = newMCPGateway(t, mcpSourceFor(redirects.URL), limits, nil).Do(context.Background(), req, &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonRequestTooLarge, failure.Reason)

	big20 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer big20.Close()
	limits = testLimits()
	limits.MaxResponseBytes = 1024
	failure = newMCPGateway(t, mcpSourceFor(big20.URL), limits, nil).Do(context.Background(), mcpRequest(http.MethodPost, rpcCall(1, "search")), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonResponseTooLarge, failure.Reason)
}

func TestDoMCP_SSEIsRelayedChunkByChunk(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"one\"}\n\n"))
		flusher.Flush()
		<-release
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"two\"}\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(), nil)

	first := make(chan struct{})
	sink := &recSink{}
	var once sync.Once
	sink.onChnk = func(int) { once.Do(func() { close(first) }) }
	done := make(chan *aigateway.Failure, 1)
	go func() { done <- g.Do(context.Background(), mcpRequest(http.MethodGet, ""), sink) }()

	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("the first event did not reach the client before the end of the stream")
	}
	select {
	case <-done:
		t.Fatal("the call ended before the server sent the second event")
	default:
	}
	close(release)
	require.Nil(t, <-done)
	assert.Contains(t, sink.body(), `"method":"two"`)
	assert.GreaterOrEqual(t, len(sink.chunks), 2)
}

// The stop of the client ends the call to the server.
func TestDoMCP_CancelStopsTheServerCall(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	ended := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(ended)
	}))
	defer upstream.Close()
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), testLimits(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *aigateway.Failure, 1)
	go func() { done <- g.Do(ctx, mcpRequest(http.MethodGet, ""), &recSink{}) }()
	<-started
	cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the call to the server stayed open after the cancel")
	}
	failure := <-done
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonCanceled, failure.Reason)
	assert.Equal(t, 0, g.OpenStreams())
}

func TestDoMCP_IdleStreamEnds(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	limits := testLimits()
	limits.IdleTimeout = 200 * time.Millisecond
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), limits, nil)
	failure := g.Do(context.Background(), mcpRequest(http.MethodGet, ""), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonUpstreamTimeout, failure.Reason)
}

func TestDoMCP_StreamLimitsAreShared(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	started := make(chan struct{}, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-block
	}))
	defer upstream.Close()
	limits := testLimits()
	limits.MaxStreamsPerRun = 1
	g := newMCPGateway(t, mcpSourceFor(upstream.URL), limits, nil)

	done := make(chan *aigateway.Failure, 1)
	go func() { done <- g.Do(context.Background(), mcpRequest(http.MethodGet, ""), &recSink{}) }()
	<-started
	failure := g.Do(context.Background(), mcpRequest(http.MethodGet, ""), &recSink{})
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonTooManyStreams, failure.Reason)
	close(block)
	require.Nil(t, <-done)
}

func TestCheckMCP(t *testing.T) {
	t.Parallel()
	good := mcpSourceFor("https://docs.example.com/mcp")
	g := newMCPGateway(t, good, testLimits(), nil)
	subject := aigateway.Subject{DAGName: "fix-bug"}

	result, failure := g.Check(context.Background(), subject, aigateway.MCPTarget("docs"), "")
	require.Nil(t, failure)
	assert.Empty(t, result.Protocol)

	_, failure = g.Check(context.Background(), subject, aigateway.MCPTarget("other"), "")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonMCPServerUnknown, failure.Reason)

	forbidden := mcpSourceFor("https://docs.example.com/mcp")
	forbidden.resolve = &aiendpoint.Error{Reason: aiendpoint.ReasonMCPForbidden, Name: "docs", Kind: "MCP server", Detail: "the DAG is not in the allow list"}
	_, failure = newMCPGateway(t, forbidden, testLimits(), nil).Check(context.Background(), subject, aigateway.MCPTarget("docs"), "")
	require.NotNil(t, failure)
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, failure.Reason)
}

// The status of a reason of the MCP relay is the status of the table of Spec 094.
func TestMCPReasonStatuses(t *testing.T) {
	t.Parallel()
	for reason, want := range map[aigateway.Reason]int{
		aigateway.ReasonMCPServerUnknown:   http.StatusNotFound,
		aigateway.ReasonMCPServerForbidden: http.StatusForbidden,
		aigateway.ReasonMCPToolNotAllowed:  http.StatusOK,
		aigateway.ReasonMCPRequestInvalid:  http.StatusBadRequest,
	} {
		assert.True(t, reason.Known(), reason)
		assert.Equal(t, want, reason.Status(), reason)
	}
	// The failure of the gRPC message keeps the reason.
	failure, ok := aigateway.ParseFailure("mcp_server_forbidden: the DAG is not in the allow list")
	require.True(t, ok)
	assert.Equal(t, aigateway.ReasonMCPServerForbidden, failure.Reason)
}
