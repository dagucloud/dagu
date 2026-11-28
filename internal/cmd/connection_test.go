// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// The authorization code and state that the fake provider redirects with.
// No output may show them.
const (
	fakeCode  = "4/0-fake-code"
	fakeState = "sealed-fake-state"
)

// fakeConnectionAPI serves the connections REST API from canned replies
// and records each request it gets.
type fakeConnectionAPI struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]json.RawMessage
	replies  map[string]any
}

func newFakeConnectionAPI(t *testing.T, replies map[string]any) (*fakeConnectionAPI, *remoteClient) {
	t.Helper()
	f := &fakeConnectionAPI{bodies: map[string]json.RawMessage{}, replies: replies}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, &remoteClient{baseURL: server.URL, client: server.Client()}
}

func (f *fakeConnectionAPI) serve(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, key+"?"+r.URL.RawQuery)
	if len(body) > 0 {
		f.bodies[key] = body
	}
	reply, ok := f.replies[key]
	f.mu.Unlock()
	switch reply := reply.(type) {
	case nil:
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case int:
		w.WriteHeader(reply)
		_, _ = w.Write([]byte(`{"code":"forbidden","message":"needs admin"}`))
	default:
		_ = json.NewEncoder(w).Encode(reply)
	}
}

func (f *fakeConnectionAPI) body(t *testing.T, key string, out any) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Contains(t, f.bodies, key)
	require.NoError(t, json.Unmarshal(f.bodies[key], out))
}

func (f *fakeConnectionAPI) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// testConnectionCLI returns a CLI in workspace ops whose person types
// input, and the output it prints.
func testConnectionCLI(client *remoteClient, input string, browser func(string) error) (*connectionCLI, *bytes.Buffer) {
	out := &bytes.Buffer{}
	t := &connectionTerm{in: bufio.NewReader(strings.NewReader(input)), out: out, openBrowser: browser}
	t.readSecret = t.readLine
	return &connectionCLI{client: client, workspace: "ops", term: t}, out
}

func testApp(clientType api.OAuthClientType) api.OAuthApp {
	return api.OAuthApp{
		Id: "app-1", Name: "google-agent", Provider: api.ConnectionProviderNameGoogle,
		ClientId: "123-abc.apps.googleusercontent.com", ClientType: clientType, Agent: true, Status: api.OAuthAppStatusOk,
	}
}

func testConnection(status api.ConnectionStatus, account string) api.Connection {
	return api.Connection{
		Name: "work", Workspace: "ops", Provider: api.ConnectionProviderNameGoogle, App: "app-1",
		Services: []api.ConnectionService{{Name: "gmail", Access: api.ConnectionAccessRead}},
		Status:   status, Account: &account,
	}
}

var testProviders = api.ConnectionProviderListResponse{
	Providers: []api.ConnectionProvider{{
		Name:       api.ConnectionProviderNameGoogle,
		SetupLinks: &api.ConnectionProviderSetupLinks{AdminTrust: new("https://admin.google.com/ac/owl")},
	}},
}

var testTools = api.ConnectionToolsResponse{Servers: []api.ConnectionToolServer{{
	Server: "gmail",
	Tools:  []api.ConnectionTool{{Name: "search_threads"}, {Name: "get_thread"}},
}}}

// A first connect over SSH uses paste mode: it shows the redirect URI and
// client ID before consent, completes with the pasted address, and
// confirms the account after the person agrees. The pasted code and state
// are never printed.
func TestConnectionAddPaste(t *testing.T) {
	t.Parallel()
	pasted := "http://127.0.0.1:1/oauth/callback?code=" + url.QueryEscape(fakeCode) + "&state=" + fakeState
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /oauth-apps":           api.OAuthAppListResponse{Apps: []api.OAuthApp{testApp(api.OAuthClientTypeDesktop)}},
		"POST /connections":         testConnection(api.ConnectionStatusPending, ""),
		"GET /connection-providers": testProviders,
		"POST /connections/work/authorization": api.ConnectionAuthorizationStart{
			Session: "s1", Mode: api.ConnectionRedirectModePaste, AuthorizationUrl: "https://accounts.google.com/o/oauth2/v2/auth?x=1",
			RedirectUri: "http://127.0.0.1:1/oauth/callback", ClientId: "123-abc.apps.googleusercontent.com",
		},
		"POST /connections/work/authorization/complete": nil,
		"GET /connections/work/authorization": api.ConnectionAuthorization{
			Phase: api.ConnectionAuthorizationPhaseConfirm, Account: new("ops@example.com"),
			Probes:     &[]api.ConnectionProbe{{Service: "gmail", Ok: true}},
			Duplicates: &[]string{"work-google"},
		},
		"POST /connections/work/authorization/confirm": testConnection(api.ConnectionStatusConnected, "ops@example.com"),
		"GET /connections/work/tools":                  testTools,
	})
	c, out := testConnectionCLI(client, pasted+"\ny\n", nil)

	err := c.addGoogle(context.Background(), googleAddRequest{
		name: "work", services: []api.ConnectionServiceRequest{{Name: "gmail", Access: api.ConnectionAccessRead}},
		agent: true, appRef: "google-agent", noBrowser: true,
	})

	require.NoError(t, err, out.String())
	var start api.StartConnectionAuthorizationRequest
	fake.body(t, "POST /connections/work/authorization", &start)
	assert.Equal(t, api.ConnectionRedirectModePaste, *start.Mode)
	assert.Nil(t, start.LoopbackRedirectUri)
	var complete api.CompleteConnectionAuthorizationRequest
	fake.body(t, "POST /connections/work/authorization/complete", &complete)
	assert.Equal(t, pasted, *complete.RedirectUrl)
	var confirm api.ConfirmConnectionAuthorizationRequest
	fake.body(t, "POST /connections/work/authorization/confirm", &confirm)
	assert.Equal(t, "s1", confirm.Session)
	assert.Contains(t, fake.sent(), "POST /connections?workspace=ops")

	printed := out.String()
	assert.Contains(t, printed, "Register this redirect URI in the client: http://127.0.0.1:1/oauth/callback")
	assert.Contains(t, printed, "Client ID: 123-abc.apps.googleusercontent.com")
	assert.Contains(t, printed, "https://admin.google.com/ac/owl")
	assert.Contains(t, printed, "Signed in as ops@example.com. Is this the right account? [Y/n]")
	assert.Contains(t, printed, "Warning: work-google in ops already holds ops@example.com on this client.")
	assert.Contains(t, printed, "gmail  ok  (2 MCP tools)")
	assert.Contains(t, printed, "Connection work is ready.")
	assert.NotContains(t, printed, fakeCode)
	assert.NotContains(t, printed, fakeState)
}

// Declining the account cancels the session, and the new connection stays
// pending.
func TestConnectionAddDeclined(t *testing.T) {
	t.Parallel()
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /oauth-apps":           api.OAuthAppListResponse{Apps: []api.OAuthApp{testApp(api.OAuthClientTypeDesktop)}},
		"POST /connections":         testConnection(api.ConnectionStatusPending, ""),
		"GET /connection-providers": testProviders,
		"POST /connections/work/authorization": api.ConnectionAuthorizationStart{
			Session: "s1", Mode: api.ConnectionRedirectModePaste, AuthorizationUrl: "https://accounts.google.com/auth",
		},
		"POST /connections/work/authorization/complete": nil,
		"GET /connections/work/authorization": api.ConnectionAuthorization{
			Phase: api.ConnectionAuthorizationPhaseConfirm, Account: new("someone@example.com"),
		},
		"DELETE /connections/work/authorization": nil,
	})
	c, _ := testConnectionCLI(client, "http://127.0.0.1:1/oauth/callback?code=x&state=y\nn\n", nil)

	err := c.addGoogle(context.Background(), googleAddRequest{
		name: "work", services: []api.ConnectionServiceRequest{{Name: "gmail", Access: api.ConnectionAccessRead}},
		appRef: "app-1", noBrowser: true,
	})

	require.ErrorContains(t, err, "someone@example.com was not confirmed")
	assert.ErrorContains(t, err, "Connection work stays pending")
	assert.Contains(t, fake.sent(), "DELETE /connections/work/authorization?session=s1&workspace=ops")
	assert.NotContains(t, fake.sent(), "POST /connections/work/authorization/confirm?workspace=ops")
}

// A reconnect on this host listens on 127.0.0.1 for the browser's redirect
// and expects the account the connection holds. A session that account
// confirms at once is checked with a test, and shows no duplicates
// warning (Spec 107 §Confirmation).
func TestConnectionReconnectLoopback(t *testing.T) {
	t.Parallel()
	var browserPage string
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /connections/work":                         testConnection(api.ConnectionStatusNeedsReconnect, "ops@example.com"),
		"GET /oauth-apps/app-1":                         testApp(api.OAuthClientTypeDesktop),
		"GET /connection-providers":                     testProviders,
		"POST /connections/work/authorization/complete": nil,
		"POST /connections/work/test":                   api.ConnectionTestResponse{Services: []api.ConnectionProbe{{Service: "gmail", Ok: true}}},
		"GET /connections/work/tools":                   testTools,
	})
	// The start reply names the listener the request asked for, as the
	// server does in the authorization URL's redirect_uri, and the session
	// waits until the redirect completes it.
	fake.mu.Lock()
	fake.replies["POST /connections/work/authorization"] = loopbackStart{fake: fake}
	fake.replies["GET /connections/work/authorization"] = completedSession{fake: fake, status: api.ConnectionAuthorization{
		Phase: api.ConnectionAuthorizationPhaseConnected, Duplicates: &[]string{"work-google"},
	}}
	fake.mu.Unlock()
	browser := func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		redirect := u.Query().Get("redirect_uri") + "?code=" + url.QueryEscape(fakeCode) + "&state=" + fakeState
		go func() {
			resp, err := http.Get(redirect) //nolint:gosec,noctx // the CLI's own listener
			if err == nil {
				page, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				fake.mu.Lock()
				browserPage = string(page)
				fake.mu.Unlock()
			}
		}()
		return nil
	}
	c, out := testConnectionCLI(client, "", browser)

	conn, err := c.client.connection(context.Background(), c.workspace, "work")
	require.NoError(t, err)
	app, err := c.client.oauthApp(context.Background(), conn.App)
	require.NoError(t, err)
	err = c.connect(context.Background(), conn, app, connectOptions{expectedAccount: derefString(conn.Account)})

	require.NoError(t, err, out.String())
	var start api.StartConnectionAuthorizationRequest
	fake.body(t, "POST /connections/work/authorization", &start)
	assert.Equal(t, api.ConnectionRedirectModeLoopback, *start.Mode)
	assert.Equal(t, "ops@example.com", *start.ExpectedAccount)
	listener, err := url.Parse(*start.LoopbackRedirectUri)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(listener.Host)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	var complete api.CompleteConnectionAuthorizationRequest
	fake.body(t, "POST /connections/work/authorization/complete", &complete)
	assert.Contains(t, *complete.RedirectUrl, "code="+url.QueryEscape(fakeCode))
	assert.Contains(t, fake.sent(), "POST /connections/work/test?workspace=ops")
	assert.Eventually(t, func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return strings.Contains(browserPage, "Signed in")
	}, 5*time.Second, 10*time.Millisecond)

	printed := out.String()
	assert.Contains(t, printed, "Opening the sign-in page in your browser")
	assert.Contains(t, printed, "Signed in as ops@example.com, the expected account.")
	assert.NotContains(t, printed, "Is this the right account?")
	assert.NotContains(t, printed, "Warning")
	assert.Contains(t, printed, "gmail  ok  (2 MCP tools)")
	assert.NotContains(t, printed, fakeCode)
}

// loopbackStart replies to a start with an authorization URL whose
// redirect_uri is the request's loopback listener.
type loopbackStart struct{ fake *fakeConnectionAPI }

func (l loopbackStart) MarshalJSON() ([]byte, error) {
	l.fake.mu.Lock()
	var req api.StartConnectionAuthorizationRequest
	err := json.Unmarshal(l.fake.bodies["POST /connections/work/authorization"], &req)
	l.fake.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return json.Marshal(api.ConnectionAuthorizationStart{
		Session: "s2", Mode: api.ConnectionRedirectModeLoopback,
		AuthorizationUrl: "https://accounts.google.com/auth?redirect_uri=" + url.QueryEscape(*req.LoopbackRedirectUri),
		RedirectUri:      *req.LoopbackRedirectUri, ClientId: "123-abc.apps.googleusercontent.com",
	})
}

// A manager whose client file the server refuses learns what to ask an
// admin for.
func TestConnectionAddClientFileNeedsAdmin(t *testing.T) {
	t.Parallel()
	file := t.TempDir() + "/client.json"
	require.NoError(t, os.WriteFile(file, []byte(`{"installed":{"client_id":"x","client_secret":"GOCSPX-secret"}}`), 0o600))
	_, client := newFakeConnectionAPI(t, map[string]any{
		"POST /oauth-apps": http.StatusForbidden,
		"GET /connection-providers": api.ConnectionProviderListResponse{Redirects: []api.ConnectionRedirect{{
			Mode: api.ConnectionRedirectModeServerCallback, ClientType: api.OAuthClientTypeWeb,
			RedirectUri: new("https://dagu.example.com/oauth/callback"),
		}}},
	})
	c, out := testConnectionCLI(client, "", nil)

	err := c.addGoogle(context.Background(), googleAddRequest{
		name: "work", services: []api.ConnectionServiceRequest{{Name: "gmail", Access: api.ConnectionAccessRead}},
		agent: true, clientFile: file,
	})

	require.ErrorContains(t, err, "Registering a client file needs admin")
	assert.ErrorContains(t, err, "a web client with the redirect URI https://dagu.example.com/oauth/callback")
	assert.ErrorContains(t, err, "the agent flag")
	assert.NotContains(t, err.Error()+out.String(), "GOCSPX-secret")
}

// Without a terminal, remove needs --yes, and removes nothing without it.
func TestConnectionRemoveNeedsYes(t *testing.T) {
	t.Parallel()
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /connections/work":    testConnection(api.ConnectionStatusConnected, "ops@example.com"),
		"DELETE /connections/work": nil,
	})
	c, out := testConnectionCLI(client, "", nil)

	require.ErrorContains(t, c.remove(context.Background(), "work", false), "pass --yes")
	assert.NotContains(t, fake.sent(), "DELETE /connections/work?workspace=ops")
	assert.Contains(t, out.String(), "Connections on other servers that use a client of the same Google Cloud project may lose access.")

	require.NoError(t, c.remove(context.Background(), "work", true))
	assert.Contains(t, fake.sent(), "DELETE /connections/work?workspace=ops")
}

// The local context calls the server that the configuration describes,
// with its basic auth credentials, and says what to do when no server
// answers.
func TestLocalServerClient(t *testing.T) {
	t.Parallel()
	var gotPath, gotUser string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser, _, _ = r.BasicAuth()
		_ = json.NewEncoder(w).Encode(api.OAuthAppListResponse{})
	}))
	t.Cleanup(server.Close)
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Server.Host = host
	cfg.Server.Port, err = strconv.Atoi(port)
	require.NoError(t, err)
	cfg.Server.BasePath = "/dagu"
	cfg.Server.Auth.Mode = config.AuthModeBasic
	cfg.Server.Auth.Basic = config.AuthBasic{Username: "admin", Password: "pw"}

	client, base, err := localServerClient(context.Background(), cfg)
	require.NoError(t, err)
	_, err = client.oauthApps(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "/dagu/api/v1/oauth-apps", gotPath)
	assert.Equal(t, "admin", gotUser)

	server.Close()
	c := &connectionCLI{client: client, localServer: base}
	_, err = client.oauthApps(context.Background())
	assert.ErrorContains(t, c.explain(err), "No Dagu server answers at http://"+cfg.Server.Host+":"+port+"/dagu/api/v1")
}

// completedSession reads waiting until the session's redirect completed
// it, and status afterwards.
type completedSession struct {
	fake   *fakeConnectionAPI
	status api.ConnectionAuthorization
}

func (s completedSession) MarshalJSON() ([]byte, error) {
	s.fake.mu.Lock()
	_, completed := s.fake.bodies["POST /connections/work/authorization/complete"]
	s.fake.mu.Unlock()
	if !completed {
		return json.Marshal(api.ConnectionAuthorization{Phase: api.ConnectionAuthorizationPhaseWaiting})
	}
	return json.Marshal(s.status)
}
