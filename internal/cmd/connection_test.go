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
// and records each request it gets. A reply that is an int is that error
// status, and a rawReply is sent as it is.
type fakeConnectionAPI struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]json.RawMessage
	replies  map[string]any
}

// rawReply is a reply body that the fake sends byte for byte.
type rawReply []byte

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
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "error", "message": http.StatusText(reply)})
	case rawReply:
		_, _ = w.Write(reply)
	default:
		_ = json.NewEncoder(w).Encode(reply)
	}
}

// set replaces the reply to key.
func (f *fakeConnectionAPI) set(key string, reply any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[key] = reply
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

// syncBuffer is output that a test reads while the CLI writes it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testConnectionCLI returns a CLI in workspace ops whose person types
// input, and the output it prints.
func testConnectionCLI(client *remoteClient, input io.Reader, browser func(string) error) (*connectionCLI, *syncBuffer) {
	out := &syncBuffer{}
	t := &connectionTerm{in: bufio.NewReader(input), out: out, openBrowser: browser}
	t.readSecret = t.readAnswer
	return &connectionCLI{client: client, workspace: "ops", term: t, sessionLifetime: sessionLifetime}, out
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

var (
	testServices  = []api.ConnectionServiceRequest{{Name: "gmail", Access: api.ConnectionAccessRead}}
	testProviders = api.ConnectionProviderListResponse{
		Providers: []api.ConnectionProvider{{
			Name:       api.ConnectionProviderNameGoogle,
			SetupLinks: &api.ConnectionProviderSetupLinks{AdminTrust: new("https://admin.google.com/ac/owl")},
		}},
	}
	testTools = api.ConnectionToolsResponse{Servers: []api.ConnectionToolServer{{
		Server: "gmail",
		Tools:  []api.ConnectionTool{{Name: "search_threads"}, {Name: "get_thread"}},
	}}}
	testPasteStart = api.ConnectionAuthorizationStart{
		Session: "s1", Mode: api.ConnectionRedirectModePaste, AuthorizationUrl: "https://accounts.google.com/o/oauth2/v2/auth?x=1",
		RedirectUri: "http://127.0.0.1:1/oauth/callback", ClientId: "123-abc.apps.googleusercontent.com",
		// The server's clock runs behind this host's, which must not cut the
		// paste wait short.
		ExpiresAt: time.Now().Add(-time.Hour),
	}
)

// pasteReplies are the replies to a paste connect of a new connection
// whose session waits in confirm.
func pasteReplies() map[string]any {
	return map[string]any{
		"GET /oauth-apps":                               api.OAuthAppListResponse{Apps: []api.OAuthApp{testApp(api.OAuthClientTypeDesktop)}},
		"POST /connections":                             testConnection(api.ConnectionStatusPending, ""),
		"GET /connection-providers":                     testProviders,
		"POST /connections/work/authorization":          testPasteStart,
		"POST /connections/work/authorization/complete": nil,
		"GET /connections/work/authorization": api.ConnectionAuthorization{
			Phase: api.ConnectionAuthorizationPhaseConfirm, Account: new("ops@example.com"),
			Probes:     &[]api.ConnectionProbe{{Service: "gmail", Ok: true}},
			Duplicates: &[]string{"work-google"},
		},
		"POST /connections/work/authorization/confirm": testConnection(api.ConnectionStatusConnected, "ops@example.com"),
		"DELETE /connections/work/authorization":       nil,
		"GET /connections/work/tools":                  testTools,
	}
}

// A first connect over SSH uses paste mode: it shows the redirect URI and
// client ID before consent, asks again after an empty paste, completes
// with the pasted address, and confirms the account after the person
// agrees. The pasted code and state are never printed.
func TestConnectionAddPaste(t *testing.T) {
	t.Parallel()
	pasted := "http://127.0.0.1:1/oauth/callback?code=" + url.QueryEscape(fakeCode) + "&state=" + fakeState
	fake, client := newFakeConnectionAPI(t, pasteReplies())
	c, out := testConnectionCLI(client, strings.NewReader("\n"+pasted+"\ny\n"), nil)

	err := c.addGoogle(context.Background(), googleAddRequest{
		name: "work", services: testServices, agent: true, appRef: "google-agent", noBrowser: true,
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
	assert.NotContains(t, fake.sent(), "DELETE /connections/work/authorization?session=s1&workspace=ops")

	printed := out.String()
	assert.Contains(t, printed, "Register this redirect URI in the client: http://127.0.0.1:1/oauth/callback")
	assert.Contains(t, printed, "Client ID: 123-abc.apps.googleusercontent.com")
	assert.Contains(t, printed, "https://admin.google.com/ac/owl")
	assert.Contains(t, printed, "Paste the address of the page the browser landed on")
	assert.Contains(t, printed, "Signed in as ops@example.com. Is this the right account? [Y/n]")
	assert.Contains(t, printed, "Warning: work-google in ops already holds ops@example.com on this client.")
	assert.Contains(t, printed, "gmail  read  ok  (2 MCP tools)")
	assert.Contains(t, printed, "Connection work is ready. Try: dagu connection tools work --workspace ops")
	assert.NotContains(t, printed, fakeCode)
	assert.NotContains(t, printed, fakeState)
}

// Declining the account cancels the session, and the new connection stays
// pending, with commands for the same workspace.
func TestConnectionAddDeclined(t *testing.T) {
	t.Parallel()
	fake, client := newFakeConnectionAPI(t, pasteReplies())
	c, _ := testConnectionCLI(client, strings.NewReader("http://127.0.0.1:1/oauth/callback?code=x&state=y\nn\n"), nil)

	err := c.addGoogle(context.Background(), googleAddRequest{name: "work", services: testServices, appRef: "app-1", noBrowser: true})

	require.ErrorContains(t, err, "ops@example.com was not confirmed")
	assert.ErrorContains(t, err, "Connection work stays pending. Sign in again with 'dagu connection reconnect work --workspace ops'")
	assert.Contains(t, fake.sent(), "DELETE /connections/work/authorization?session=s1&workspace=ops")
	assert.NotContains(t, fake.sent(), "POST /connections/work/authorization/confirm?workspace=ops")
}

// An interrupt while the account question waits cancels the session, so
// the server discards the tokens of an account nobody confirmed.
func TestConnectionInterruptCancels(t *testing.T) {
	t.Parallel()
	fake, client := newFakeConnectionAPI(t, pasteReplies())
	// The answer to the account question never arrives.
	input, unanswered := io.Pipe()
	t.Cleanup(func() { _ = unanswered.Close() })
	go func() { _, _ = io.WriteString(unanswered, "http://127.0.0.1:1/oauth/callback?code=x&state=y\n") }()
	c, out := testConnectionCLI(client, input, nil)
	ctx, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	asked := make(chan bool, 1)
	go func() {
		// The deadline only unblocks the command when the question never
		// shows.
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(out.String(), "Is this the right account?") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		asked <- strings.Contains(out.String(), "Is this the right account?")
		interrupt()
	}()

	err := c.addGoogle(ctx, googleAddRequest{name: "work", services: testServices, appRef: "app-1", noBrowser: true})

	require.True(t, <-asked, out.String())
	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorContains(t, err, "Connection work stays pending")
	assert.Contains(t, fake.sent(), "DELETE /connections/work/authorization?session=s1&workspace=ops")
	assert.NotContains(t, fake.sent(), "POST /connections/work/authorization/confirm?workspace=ops")
}

// A reconnect on this host listens on 127.0.0.1 for the browser's redirect
// and expects the account the connection holds. A session that the
// expected account confirms at once is checked with a test, shows no
// duplicates warning (Spec 107 §Confirmation), and shows the refresh
// token's limit that the connection reports.
func TestConnectionReconnectLoopback(t *testing.T) {
	t.Parallel()
	conn := testConnection(api.ConnectionStatusNeedsReconnect, "ops@example.com")
	conn.RefreshTokenExpiresAt = new(time.Date(2027, 1, 8, 9, 0, 0, 0, time.UTC))
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /connections/work":                         conn,
		"GET /oauth-apps/app-1":                         testApp(api.OAuthClientTypeDesktop),
		"GET /connection-providers":                     testProviders,
		"POST /connections/work/authorization/complete": nil,
		"POST /connections/work/test":                   api.ConnectionTestResponse{Services: []api.ConnectionProbe{{Service: "gmail", Ok: true}}},
		"GET /connections/work/tools":                   testTools,
	})
	// The start reply names the listener the request asked for, as the
	// server does in the authorization URL's redirect_uri, and the session
	// waits until the redirect completes it.
	fake.set("POST /connections/work/authorization", loopbackStart{fake: fake})
	fake.set("GET /connections/work/authorization", completedSession{fake: fake, status: api.ConnectionAuthorization{
		Phase: api.ConnectionAuthorizationPhaseConnected, Duplicates: &[]string{"work-google"},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pages := make(chan string, 1)
	browser := func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		redirect := u.Query().Get("redirect_uri") + "?code=" + url.QueryEscape(fakeCode) + "&state=" + fakeState
		go func() { pages <- getPage(ctx, redirect) }()
		return nil
	}
	c, out := testConnectionCLI(client, strings.NewReader(""), browser)

	err := c.reconnect(ctx, "work", false)

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
	assert.Contains(t, <-pages, "Signed in")

	printed := out.String()
	assert.Contains(t, printed, "Opening the sign-in page in your browser")
	assert.Contains(t, printed, "dagu connection reconnect work --workspace ops --no-browser")
	assert.Contains(t, printed, "Signed in as ops@example.com, the expected account.")
	assert.NotContains(t, printed, "Is this the right account?")
	assert.NotContains(t, printed, "Warning")
	assert.Contains(t, printed, "gmail  read  ok  (2 MCP tools)")
	assert.Contains(t, printed, "The refresh token expires "+formatConnectionTime(conn.RefreshTokenExpiresAt)+". Reconnect before then.")
	assert.NotContains(t, printed, fakeCode)
}

// getPage returns the status and body that a GET of raw answers, or the
// error.
func getPage(ctx context.Context, raw string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return strconv.Itoa(resp.StatusCode) + " " + string(body)
}

// The loopback listener hands on only the redirect that carries the
// session's state, and only once: a stray request gets 400 and a repeated
// one 409, while the first still gets its result.
func TestLoopbackListenerTakesOneRedirect(t *testing.T) {
	t.Parallel()
	l, err := listenLoopback()
	require.NoError(t, err)
	t.Cleanup(l.close)
	l.expect(fakeState)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	redirect := l.uri + "?code=x&state=" + fakeState

	assert.True(t, strings.HasPrefix(getPage(ctx, l.uri+"?code=x&state=other"), "400 "))
	first := make(chan string, 1)
	go func() { first <- getPage(ctx, redirect) }()
	var r consentRedirect
	select {
	case r = <-l.redirects:
	case <-ctx.Done():
		t.Fatal("the redirect never reached the connect flow")
	}
	assert.Equal(t, redirect, r.url)
	assert.True(t, strings.HasPrefix(getPage(ctx, redirect), "409 "))
	r.done(nil)
	assert.Contains(t, <-first, "200 Signed in")
}

// When a sign-in fails after the redirect, the CLI says that the previous
// tokens stay only when the cancel shows the session was still open. A
// session the server already ended may have been confirmed, and a cancel
// that does not arrive leaves the tokens on the server until a reconnect
// or remove drops them. Failed reads are retried until the session
// expires, which the CLI says once.
func TestConnectionUnsettledOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cancel  any
		want    string
		notWant string
	}{
		{name: "Cancelled", cancel: nil, want: "Connection work stays pending"},
		{name: "Ended", cancel: http.StatusConflict, want: "The sign-in may have completed", notWant: "stays pending"},
		{
			name: "Unreachable", cancel: http.StatusBadGateway,
			want:    "To drop them, run 'dagu connection reconnect work --workspace ops', which replaces the session, or 'dagu connection remove work --workspace ops'",
			notWant: "stays pending",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			replies := pasteReplies()
			replies["GET /connections/work/authorization"] = http.StatusInternalServerError
			replies["DELETE /connections/work/authorization"] = tt.cancel
			fake, client := newFakeConnectionAPI(t, replies)
			c, out := testConnectionCLI(client, strings.NewReader("http://127.0.0.1:1/oauth/callback?code=x&state=y\n"), nil)
			c.sessionLifetime = 2 * time.Second

			err := c.addGoogle(context.Background(), googleAddRequest{name: "work", services: testServices, appRef: "app-1", noBrowser: true})

			require.ErrorContains(t, err, tt.want)
			if tt.notWant != "" {
				assert.NotContains(t, err.Error(), tt.notWant)
			}
			assert.Equal(t, 1, strings.Count(out.String(), "Could not reach the server"), out.String())
			assert.Contains(t, fake.sent(), "DELETE /connections/work/authorization?session=s1&workspace=ops")
		})
	}
}

// A web client's session takes the mode the server picks.
func TestConnectionWebClientServerMode(t *testing.T) {
	t.Parallel()
	replies := pasteReplies()
	replies["GET /oauth-apps"] = api.OAuthAppListResponse{Apps: []api.OAuthApp{testApp(api.OAuthClientTypeWeb)}}
	replies["POST /connections/work/authorization"] = api.ConnectionAuthorizationStart{
		Session: "s1", Mode: api.ConnectionRedirectModeServerCallback, AuthorizationUrl: "https://accounts.google.com/auth",
		RedirectUri: "https://dagu.example.com/oauth/callback", ExpiresAt: time.Now().Add(time.Hour),
	}
	fake, client := newFakeConnectionAPI(t, replies)
	c, out := testConnectionCLI(client, strings.NewReader("y\n"), nil)

	err := c.addGoogle(context.Background(), googleAddRequest{name: "work", services: testServices, appRef: "app-1", noBrowser: true})

	require.NoError(t, err, out.String())
	var start api.StartConnectionAuthorizationRequest
	fake.body(t, "POST /connections/work/authorization", &start)
	assert.Nil(t, start.Mode)
	assert.Contains(t, out.String(), "Register this redirect URI in the client: https://dagu.example.com/oauth/callback")
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
		AuthorizationUrl: "https://accounts.google.com/auth?state=" + fakeState + "&redirect_uri=" + url.QueryEscape(*req.LoopbackRedirectUri),
		RedirectUri:      *req.LoopbackRedirectUri, ClientId: "123-abc.apps.googleusercontent.com",
		ExpiresAt: time.Now().Add(time.Hour),
	})
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

// A manager whose client file the server refuses learns what to ask an
// admin for.
func TestConnectionAddClientFileNeedsAdmin(t *testing.T) {
	t.Parallel()
	file := writeClientFile(t)
	_, client := newFakeConnectionAPI(t, map[string]any{
		"POST /oauth-apps": http.StatusForbidden,
		"GET /connection-providers": api.ConnectionProviderListResponse{Redirects: []api.ConnectionRedirect{{
			Mode: api.ConnectionRedirectModeServerCallback, ClientType: api.OAuthClientTypeWeb,
			RedirectUri: new("https://dagu.example.com/oauth/callback"),
		}}},
	})
	c, out := testConnectionCLI(client, strings.NewReader(""), nil)

	err := c.addGoogle(context.Background(), googleAddRequest{name: "work", services: testServices, agent: true, clientFile: file})

	require.ErrorContains(t, err, "Registering a client file needs admin")
	assert.ErrorContains(t, err, "a web client with the redirect URI https://dagu.example.com/oauth/callback")
	assert.ErrorContains(t, err, "the agent flag")
	assert.NotContains(t, err.Error()+out.String(), "GOCSPX-secret")
}

// An app that a client file registered for a connection that cannot be
// created is deleted again, so no app keeps the secret without a use.
func TestConnectionAddDeletesUnusedApp(t *testing.T) {
	t.Parallel()
	file := writeClientFile(t)
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"POST /oauth-apps":          testApp(api.OAuthClientTypeDesktop),
		"POST /connections":         http.StatusConflict,
		"DELETE /oauth-apps/app-1":  nil,
		"GET /connection-providers": testProviders,
	})
	c, _ := testConnectionCLI(client, strings.NewReader(""), nil)

	err := c.addGoogle(context.Background(), googleAddRequest{name: "work", services: testServices, clientFile: file})

	require.ErrorContains(t, err, http.StatusText(http.StatusConflict))
	assert.Contains(t, fake.sent(), "DELETE /oauth-apps/app-1?")
}

func writeClientFile(t *testing.T) string {
	t.Helper()
	file := t.TempDir() + "/client.json"
	require.NoError(t, os.WriteFile(file, []byte(`{"installed":{"client_id":"x","client_secret":"GOCSPX-secret"}}`), 0o600))
	return file
}

// Without a terminal, remove needs --yes, and removes nothing without it.
func TestConnectionRemoveNeedsYes(t *testing.T) {
	t.Parallel()
	fake, client := newFakeConnectionAPI(t, map[string]any{
		"GET /connections/work":    testConnection(api.ConnectionStatusConnected, "ops@example.com"),
		"DELETE /connections/work": nil,
	})
	c, out := testConnectionCLI(client, strings.NewReader(""), nil)

	require.ErrorContains(t, c.remove(context.Background(), "work", false), "pass --yes")
	assert.NotContains(t, fake.sent(), "DELETE /connections/work?workspace=ops")
	assert.Contains(t, out.String(), "Connections on other servers that use any OAuth client in the same Google Cloud project may lose access.")

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

// A server that binds every address is reached at localhost, which the
// server takes as its public URL.
func TestLocalServerClientEveryAddress(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"0.0.0.0", "::", ""} {
		cfg := &config.Config{}
		cfg.Server.Host, cfg.Server.Port = host, 8080

		_, base, err := localServerClient(context.Background(), cfg)

		require.NoError(t, err)
		assert.Equal(t, "http://localhost:8080/api/v1", base, host)
	}
}
