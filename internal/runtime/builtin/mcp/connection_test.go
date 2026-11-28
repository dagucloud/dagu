// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// TestMain makes every call trust the certificate that all httptest TLS
// servers share.
func TestMain(m *testing.M) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	roots := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	srv.Close()
	newBaseTransport = func() *http.Transport {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		return t
	}
	os.Exit(m.Run())
}

// tokenSource hands out at-1, and at-2 once at-1 is reported, recording
// each report.
type tokenSource struct {
	conn *connection.Connection
	// err fails every token request.
	err error

	mu       sync.Mutex
	rejected []string
}

func (s *tokenSource) Connection(context.Context, string) (*connection.Connection, error) {
	return s.conn, nil
}

func (s *tokenSource) Account(context.Context, *connection.Connection) (string, error) {
	return "", nil
}

func (s *tokenSource) Token(context.Context, *connection.Connection, string, time.Duration) (string, time.Duration, error) {
	if s.err != nil {
		return "", 0, s.err
	}
	return "at-1", time.Hour, nil
}

func (s *tokenSource) Rejected(_ context.Context, _ *connection.Connection, _, token string, _ time.Duration) (string, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = append(s.rejected, token)
	return "at-2", time.Hour, nil
}

func (s *tokenSource) reports() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rejected)
}

// connServer is an MCP server over HTTPS that accepts the tokens in valid
// and records each Authorization header it receives.
type connServer struct {
	srv *httptest.Server
	// refuse answers a request in place of the MCP server when it returns
	// true.
	refuse func(w http.ResponseWriter, authorization string) bool

	mu   sync.Mutex
	auth []string
}

func newConnServer(t *testing.T, refuse func(http.ResponseWriter, string) bool) *connServer {
	t.Helper()
	s := &connServer{refuse: refuse}
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(tool("leak", func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: text("token at-1 and at-2")}, nil
	}))
	server.AddTool(tool("send", func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		res, err := req.Session.Elicit(ctx, &sdk.ElicitParams{
			Message:         "Send the draft to ops@example.com?",
			RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		})
		if err != nil {
			return nil, err
		}
		return &sdk.CallToolResult{Content: text("confirmation " + res.Action)}, nil
	}))
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		s.mu.Lock()
		s.auth = append(s.auth, authorization)
		s.mu.Unlock()
		if s.refuse != nil && s.refuse(w, authorization) {
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// conn returns an oauth2 connection bound to the server's host.
func (s *connServer) conn() *connection.Connection {
	u, _ := url.Parse(s.srv.URL)
	return &connection.Connection{Name: "work", Provider: connection.ProviderOAuth2, BoundHosts: []string{u.Host}}
}

func (s *connServer) authorizations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.auth)
}

// runConnected runs a call of toolName through source's connection, with
// the step's masker recording what it is given.
func runConnected(t *testing.T, source *tokenSource, cfg map[string]any) (string, []string, error) {
	t.Helper()
	ctx := runtime.NewContext(t.Context(), &ir.DAG{Name: "mcp-test"}, "run-1", "", runtime.WithConnections(source))
	exec, err := newCaller(ctx, mcpStep(cfg))
	require.NoError(t, err)
	var masked []string
	exec.(*caller).SetSecretMasker(func(values ...string) { masked = append(masked, values...) })
	var out strings.Builder
	exec.SetStdout(&out)
	err = exec.Run(ctx)
	return out.String(), masked, err
}

func (s *connServer) cfg(toolName string) map[string]any {
	return map[string]any{"connection": "work", "url": s.srv.URL, "tool": toolName}
}

// A call through a connection sends its token, which the step masks and
// the output redacts.
func TestConnectionCall(t *testing.T) {
	t.Parallel()
	srv := newConnServer(t, nil)
	source := &tokenSource{conn: srv.conn()}

	out, masked, err := runConnected(t, source, srv.cfg("leak"))
	require.NoError(t, err)
	assert.Contains(t, srv.authorizations(), "Bearer at-1")
	assert.Equal(t, []string{"at-1"}, masked)
	assert.NotContains(t, out, "at-1")
	assert.Contains(t, out, "***")
}

// A 401 reports the token once and retries with the token the source
// returns; a second 401 fails as auth, naming the connection.
func TestConnectionRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		reject  []string
		wantErr bool
	}{
		{name: "Refreshed", reject: []string{"Bearer at-1"}},
		{name: "RejectedAgain", reject: []string{"Bearer at-1", "Bearer at-2"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newConnServer(t, func(w http.ResponseWriter, authorization string) bool {
				if slices.Contains(tt.reject, authorization) {
					w.WriteHeader(http.StatusUnauthorized)
					return true
				}
				return false
			})
			source := &tokenSource{conn: srv.conn()}

			out, masked, err := runConnected(t, source, srv.cfg("leak"))
			assert.Equal(t, []string{"at-1"}, source.reports())
			assert.Equal(t, []string{"at-1", "at-2"}, masked)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, kindAuth, errKind(t, err))
			body := decode(t, out)["error"].(map[string]any)
			assert.Equal(t, "work", body["connection"])
			assert.Contains(t, body["message"], "HTTP 401")
		})
	}
}

// A 403 with insufficient_scope fails as auth with the missing scope, and
// the token is not reported.
func TestConnectionInsufficientScope(t *testing.T) {
	t.Parallel()
	const scope = "https://www.googleapis.com/auth/gmail.compose"
	srv := newConnServer(t, func(w http.ResponseWriter, _ string) bool {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+scope+`"`)
		w.WriteHeader(http.StatusForbidden)
		return true
	})
	source := &tokenSource{conn: srv.conn()}

	out, _, err := runConnected(t, source, srv.cfg("leak"))
	require.Error(t, err)
	assert.Equal(t, kindAuth, errKind(t, err))
	assert.Equal(t, map[string]any{
		"kind":       kindAuth,
		"message":    `connection "work": the server rejected the connection's token (HTTP 403, insufficient_scope, scope ` + scope + `): Forbidden`,
		"connection": "work",
		"detail":     detailInsufficientScope,
		"scope":      scope,
	}, decode(t, out)["error"])
	assert.Empty(t, source.reports())
}

// A connection that cannot hand out a token, or whose binding does not
// cover the URL, fails as auth, naming the connection, before any request.
func TestConnectionUnusable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		url        string
		wantDetail string
		wantMsg    string
	}{
		{
			name:       "ReconnectRequired",
			err:        fmt.Errorf("%w: connection %q: the connection was never connected", broker.ErrReconnectRequired, "work"),
			wantDetail: detailReconnectRequired,
			wantMsg:    "the connection was never connected",
		},
		{
			name:    "OldCoordinator",
			err:     errors.New(`connection "work": the coordinator must be upgraded to hand out connection tokens to workers`),
			wantMsg: "the coordinator must be upgraded",
		},
		{name: "UnboundHost", url: "https://elsewhere.example.com/mcp", wantMsg: "bound hosts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newConnServer(t, nil)
			cfg := srv.cfg("leak")
			if tt.url != "" {
				cfg["url"] = tt.url
			}

			out, _, err := runConnected(t, &tokenSource{conn: srv.conn(), err: tt.err}, cfg)
			require.Error(t, err)
			assert.Equal(t, kindAuth, errKind(t, err))
			body := decode(t, out)["error"].(map[string]any)
			assert.Equal(t, "work", body["connection"])
			detail, _ := body["detail"].(string)
			assert.Equal(t, tt.wantDetail, detail)
			assert.Contains(t, body["message"], tt.wantMsg)
			assert.Empty(t, srv.authorizations())
		})
	}
}

// A confirmation request is declined at once, and the call fails, naming
// the ways to let the action through.
func TestConnectionConfirmationRequired(t *testing.T) {
	t.Parallel()
	srv := newConnServer(t, nil)

	out, _, err := runConnected(t, &tokenSource{conn: srv.conn()}, srv.cfg("send"))
	require.Error(t, err)
	assert.Equal(t, kindConfirmationRequired, errKind(t, err))
	msg := decode(t, out)["error"].(map[string]any)["message"]
	assert.Contains(t, msg, "Send the draft to ops@example.com?")
	assert.Contains(t, msg, "Workspace admin")
	assert.Contains(t, msg, "provider: connection")
}
