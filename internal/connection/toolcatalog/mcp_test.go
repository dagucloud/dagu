// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package toolcatalog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/toolcatalog"
)

// The lister pages through every tool, sends the connection's token, and
// advertises form elicitation.
func TestMCPListerLists(t *testing.T) {
	t.Parallel()
	srv := newMCPServer(t, serverOptions{})
	source := &fakeSource{}

	tools, err := srv.lister(source).ListTools(testCtx(t), srv.conn(), srv.url("/mcp"))
	require.NoError(t, err)

	require.Len(t, tools, 3)
	assert.Equal(t, []string{"a_read", "b_write", "c_read"}, toolNames(tools))
	assert.Equal(t, "Reads a.", tools[0].Description)
	assert.JSONEq(t, `{"type":"object","properties":{"q":{"type":"string"}}}`, string(tools[0].InputSchema))
	assert.JSONEq(t, `{"idempotentHint":false,"readOnlyHint":true}`, string(tools[0].Annotations))
	assert.Empty(t, tools[1].Annotations)
	assert.Equal(t, []string{"Bearer at-1"}, srv.authorizations())
	assert.True(t, srv.advertisedFormElicitation())
}

// A 401 reports the token to the source once and lists again with the
// token it returns; a second 401 fails the listing.
func TestMCPListerRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		reject  []string
		wantErr bool
	}{
		{name: "Refreshed", reject: []string{"at-1"}},
		{name: "RejectedAgain", reject: []string{"at-1", "at-2"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newMCPServer(t, serverOptions{refuse: func(w http.ResponseWriter, authorization string) bool {
				if slices.Contains(tt.reject, strings.TrimPrefix(authorization, "Bearer ")) {
					w.WriteHeader(http.StatusUnauthorized)
					return true
				}
				return false
			}})
			source := &fakeSource{}

			tools, err := srv.lister(source).ListTools(testCtx(t), srv.conn(), srv.url("/mcp"))
			if tt.wantErr {
				var rejected *toolcatalog.RejectedError
				require.ErrorAs(t, err, &rejected)
				assert.Equal(t, http.StatusUnauthorized, rejected.Status)
			} else {
				require.NoError(t, err)
				assert.Len(t, tools, 3)
			}
			assert.Equal(t, []string{"at-1"}, source.rejectedTokens())
		})
	}
}

// A 403 fails with the challenge's error and scope, without reporting the
// token to the source.
func TestMCPListerInsufficientScope(t *testing.T) {
	t.Parallel()
	const scope = "https://www.googleapis.com/auth/gmail.readonly"
	srv := newMCPServer(t, serverOptions{refuse: func(w http.ResponseWriter, _ string) bool {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+scope+`"`)
		w.WriteHeader(http.StatusForbidden)
		return true
	}})
	source := &fakeSource{}

	_, err := srv.lister(source).ListTools(testCtx(t), srv.conn(), srv.url("/mcp"))
	var rejected *toolcatalog.RejectedError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, toolcatalog.RejectedError{Status: http.StatusForbidden, Code: "insufficient_scope", Scope: scope}, *rejected)
	assert.Empty(t, source.rejectedTokens())
}

// A listing that pages without end, or lists a tool beyond the size
// bound, fails instead of keeping part of the tools.
func TestMCPListerBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    serverOptions
		wantErr string
	}{
		{name: "EndlessPages", opts: serverOptions{endlessPages: true}, wantErr: "more than 1000 pages"},
		{
			name: "LargeTool",
			opts: serverOptions{tools: []*sdk.Tool{{
				Name:        "d_large",
				Description: strings.Repeat("x", 64<<10),
				InputSchema: map[string]any{"type": "object"},
			}}},
			wantErr: `tool "d_large" is larger than 64 KiB`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newMCPServer(t, tt.opts)

			_, err := srv.lister(&fakeSource{}).ListTools(testCtx(t), srv.conn(), srv.url("/mcp"))
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// A redirect is followed only to HTTPS on the same host and port, and the
// target of any other redirect receives no request.
func TestMCPListerRedirects(t *testing.T) {
	t.Parallel()
	srv := newMCPServer(t, serverOptions{})
	other := newMCPServer(t, serverOptions{})
	srv.mux.Handle("/moved", http.RedirectHandler(srv.url("/mcp"), http.StatusPermanentRedirect))
	srv.mux.Handle("/away", http.RedirectHandler(other.url("/mcp"), http.StatusPermanentRedirect))
	lister := srv.lister(&fakeSource{})

	tools, err := lister.ListTools(testCtx(t), srv.conn(), srv.url("/moved"))
	require.NoError(t, err)
	assert.Len(t, tools, 3)

	_, err = lister.ListTools(testCtx(t), srv.conn(), srv.url("/away"))
	require.ErrorContains(t, err, "refused a redirect")
	assert.Empty(t, other.authorizations())
}

// A server that is not on the connection's bound hosts receives no request.
func TestMCPListerUnboundHost(t *testing.T) {
	t.Parallel()
	srv := newMCPServer(t, serverOptions{})
	conn := &connection.Connection{
		Name:     "work",
		Provider: connection.ProviderGoogle,
		Services: []connection.Service{{Name: "gmail", Access: connection.AccessRead}},
	}

	_, err := srv.lister(&fakeSource{}).ListTools(testCtx(t), conn, srv.url("/mcp"))
	require.ErrorContains(t, err, "not HTTPS on a bound host")
	assert.Empty(t, srv.authorizations())
}

// mcpServer is an MCP server over HTTPS that lists three tools, two per
// page.
type mcpServer struct {
	srv *httptest.Server
	mux *http.ServeMux

	mu           sync.Mutex
	auth         []string
	capabilities *sdk.ClientCapabilities
}

type serverOptions struct {
	// refuse answers a request in place of the MCP server when it returns
	// true.
	refuse func(w http.ResponseWriter, authorization string) bool
	// tools are listed after the three tools.
	tools []*sdk.Tool
	// endlessPages answers each tools/list with no tools and a next cursor.
	endlessPages bool
}

func newMCPServer(t *testing.T, opts serverOptions) *mcpServer {
	t.Helper()
	m := &mcpServer{mux: http.NewServeMux()}
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{PageSize: 2})
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if params, ok := req.GetParams().(*sdk.InitializeParams); ok {
				m.mu.Lock()
				m.capabilities = params.Capabilities
				m.mu.Unlock()
			}
			if opts.endlessPages && method == "tools/list" {
				return &sdk.ListToolsResult{Tools: []*sdk.Tool{}, NextCursor: "next"}, nil
			}
			return next(ctx, method, req)
		}
	})
	handler := func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	}
	server.AddTool(&sdk.Tool{
		Name:        "a_read",
		Description: "Reads a.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, handler)
	server.AddTool(&sdk.Tool{Name: "b_write", InputSchema: map[string]any{"type": "object"}}, handler)
	server.AddTool(&sdk.Tool{Name: "c_read", InputSchema: map[string]any{"type": "object"}}, handler)
	for _, tool := range opts.tools {
		server.AddTool(tool, handler)
	}
	mcp := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)

	m.mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		m.mu.Lock()
		if !slices.Contains(m.auth, authorization) {
			m.auth = append(m.auth, authorization)
		}
		m.mu.Unlock()
		if opts.refuse != nil && opts.refuse(w, authorization) {
			return
		}
		mcp.ServeHTTP(w, r)
	})
	m.srv = httptest.NewTLSServer(m.mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mcpServer) url(path string) string { return m.srv.URL + path }

// conn returns an mcp connection whose resource is this server.
func (m *mcpServer) conn() *connection.Connection {
	return &connection.Connection{Name: "work", Provider: connection.ProviderMCP, Resource: m.url("/mcp")}
}

func (m *mcpServer) lister(source *fakeSource) *toolcatalog.MCPLister {
	return toolcatalog.NewMCPLister(source, m.srv.Client().Transport)
}

// authorizations returns each distinct Authorization header received.
func (m *mcpServer) authorizations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.auth)
}

func (m *mcpServer) advertisedFormElicitation() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capabilities != nil && m.capabilities.Elicitation != nil && m.capabilities.Elicitation.Form != nil
}

// fakeSource hands out at-1, and at-2 after a rejection.
type fakeSource struct {
	mu       sync.Mutex
	rejected []string
}

func (s *fakeSource) Token(context.Context, *connection.Connection, string, time.Duration) (string, time.Duration, error) {
	return "at-1", time.Hour, nil
}

func (s *fakeSource) Rejected(_ context.Context, _ *connection.Connection, _, token string, _ time.Duration) (string, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = append(s.rejected, token)
	return "at-2", time.Hour, nil
}

func (s *fakeSource) rejectedTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rejected)
}

func toolNames(tools []connection.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}
