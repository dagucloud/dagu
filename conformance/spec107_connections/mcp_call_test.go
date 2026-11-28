// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec107_connections_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

const (
	firstToken  = "conformance-access-token-1"
	secondToken = "conformance-access-token-2"
)

// apiConnection stores work-api in ops: an oauth2 connection bound to host,
// on an app that refreshes at tokenURL. A connected one holds firstToken,
// valid for an hour; any other is pending.
func apiConnection(t *testing.T, host, tokenURL string, connected bool) apiHome {
	t.Helper()

	ctx := context.Background()
	home, env, store := newHome(t)
	app, err := store.CreateApp(ctx, &connection.App{
		Name: "api", Provider: connection.ProviderOAuth2, Kind: connection.AppKindBYO,
		ClientID: "client", ClientSecret: "secret", ClientType: connection.ClientTypeWeb,
		Endpoints: &connection.Endpoints{Token: tokenURL},
	})
	require.NoError(t, err)
	conn, err := store.Create(ctx, &connection.Connection{
		Workspace: "ops", Name: "work-api", Provider: connection.ProviderOAuth2,
		App: app.ID, Grant: connection.GrantAuthorizationCode, BoundHosts: []string{host},
	})
	require.NoError(t, err)
	if connected {
		_, err = store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
			now := time.Now()
			st.Status = connection.TokenStatusConnected
			st.Generation = 1
			st.RefreshToken = "conformance-refresh-token"
			st.AccessTokens = map[string]connection.AccessToken{
				"": {Token: firstToken, IssuedAt: now, ExpiresAt: now.Add(time.Hour)},
			}
			return nil
		})
		require.NoError(t, err)
	}
	return apiHome{dir: home, env: env, store: store, conn: conn}
}

// apiHome is a dagu home that holds work-api.
type apiHome struct {
	dir string
	// env points dagu at the home.
	env   []string
	store connection.Store
	conn  *connection.Connection
}

// newMCPServer serves MCP over TLS. It refuses firstToken, as a server does
// a revoked token, and accepts secondToken. Its tool echoes both tokens, so
// redaction is observable. The returned env makes dagu trust its
// certificate.
func newMCPServer(t *testing.T) (*httptest.Server, []string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("trusting the test certificate needs SSL_CERT_FILE, which only Linux honors")
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "conformance", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "whoami", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "tokens " + firstToken + " " + secondToken}}}, nil
		})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secondToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600))
	return srv, []string{"MCP_URL=" + srv.URL + "/mcp", "SSL_CERT_FILE=" + ca}
}

// newTokenEndpoint grants secondToken to each refresh, counting them.
func newTokenEndpoint(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": secondToken, "token_type": "Bearer", "expires_in": 3600})
	}))
	t.Cleanup(srv.Close)
	return srv, &refreshes
}

// A step calls an MCP server with its connection's token. A 401 to the
// stored token refreshes it once and retries, and no token reaches the
// step's output, logs, or run status.
func TestMCPCallWithConnection(t *testing.T) {
	t.Parallel()

	mcp, mcpEnv := newMCPServer(t)
	tokens, refreshes := newTokenEndpoint(t)
	u, err := url.Parse(mcp.URL)
	require.NoError(t, err)
	home := apiConnection(t, u.Host, tokens.URL, true)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(append(home.env, mcpEnv...), "start", "mcp_call.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContains("whoami.out", `"is_error":false`, "tokens *** ***")
	require.EqualValues(t, 1, refreshes.Load())

	state, err := home.store.TokenState(context.Background(), home.conn)
	require.NoError(t, err)
	require.Equal(t, secondToken, state.AccessTokens[""].Token)

	for _, dir := range []string{home.dir, dagu.ProjectPath(".")} {
		expectNoToken(t, dir, firstToken, secondToken)
	}
}

// A step whose connection was never connected fails with
// reconnect_required, naming the connection, and the run's other steps run.
func TestMCPCallReconnectRequired(t *testing.T) {
	t.Parallel()

	home := apiConnection(t, "127.0.0.1:9", "https://auth.example.com/token", false)
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(home.env, "start", "mcp_call_pending.yaml")
	result.ExpectNonZeroExitCode()
	dagu.ExpectFileContains("unread.out", `"kind":"auth"`, `"connection":"work-api"`, `"detail":"reconnect_required"`, "never connected")
	dagu.ExpectFileContains("executed.txt", "executed")
	// The run status records the step's connection and why it got no token.
	expectRecorded(t, `"connection":{"name":"work-api","failure":"reconnect_required"}`, home.dir, dagu.ProjectPath("."))
}

// expectRecorded fails unless a file under one of dirs holds part.
func expectRecorded(t *testing.T, part string, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		found := false
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || found {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = strings.Contains(string(data), part)
			return nil
		})
		require.NoError(t, err)
		if found {
			return
		}
	}
	t.Errorf("no file under %v holds %s", dirs, part)
}

// Static headers may not set Authorization beside a connection.
func TestMCPCallAuthorizationHeader(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "mcp_call_authorization.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("with.headers must not set Authorization with with.connection")
}

// expectNoToken fails when a file under dir holds one of tokens.
func expectNoToken(t *testing.T, dir string, tokens ...string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if strings.Contains(string(data), token) {
				t.Errorf("%s holds a token", path)
			}
		}
		return nil
	})
	require.NoError(t, err)
}
