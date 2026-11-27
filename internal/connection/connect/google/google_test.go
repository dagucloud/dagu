// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package google_test

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	"github.com/dagucloud/dagu/v2/internal/connection/connect/google"
	"github.com/dagucloud/dagu/v2/internal/connection/toolcatalog"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

const (
	clientID    = "123-abc.apps.googleusercontent.com"
	publicURL   = "https://dagu.example.com"
	callbackURI = publicURL + "/oauth/callback"
	scopePrefix = "https://www.googleapis.com/auth/"
	// emailAlias is how Google reports the email scope back.
	emailAlias  = scopePrefix + "userinfo.email"
	testTimeout = 30 * time.Second
)

var serverCallback = connect.RedirectOptions{PublicURL: publicURL}

func TestAuthorizationRequest(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true, connection.Service{Name: "gmail", Access: connection.AccessRead})

	auth, err := f.engine.Start(testCtx(t), f.conn, connect.StartOptions{User: "alice", Redirect: serverCallback})
	require.NoError(t, err)
	u, err := url.Parse(auth.URL)
	require.NoError(t, err)
	assert.Equal(t, "accounts.google.com", u.Host)
	q := u.Query()
	assert.Equal(t, "offline", q.Get("access_type"))
	assert.Equal(t, "consent", q.Get("prompt"))
	assert.Equal(t, clientID, q.Get("client_id"))
	assert.Equal(t, callbackURI, q.Get("redirect_uri"))
	assert.Equal(t, "openid email "+scopePrefix+"gmail.readonly", q.Get("scope"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
}

func TestParseClientFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		file     string
		opts     connect.RedirectOptions
		wantType connection.ClientType
		wantErr  string
	}{
		{
			name:     "Web",
			file:     `{"web":{"client_id":"` + clientID + `","client_secret":"s","redirect_uris":["` + callbackURI + `"]}}`,
			opts:     serverCallback,
			wantType: connection.ClientTypeWeb,
		},
		{
			name:    "WebWithoutCallback",
			file:    `{"web":{"client_id":"` + clientID + `","client_secret":"s","redirect_uris":["https://other.example/cb"]}}`,
			opts:    serverCallback,
			wantErr: "does not list the redirect URI " + callbackURI,
		},
		{
			// Without a public URL no redirect URI is known yet.
			name:     "WebWithoutPublicURL",
			file:     `{"web":{"client_id":"` + clientID + `","client_secret":"s"}}`,
			wantType: connection.ClientTypeWeb,
		},
		{
			name:     "Desktop",
			file:     `{"installed":{"client_id":"` + clientID + `","client_secret":"s","redirect_uris":["http://localhost"]}}`,
			opts:     serverCallback,
			wantType: connection.ClientTypeDesktop,
		},
		{name: "NoSecret", file: `{"web":{"client_id":"` + clientID + `"}}`, wantErr: "no client ID or secret"},
		{name: "OtherType", file: `{"service_account":{}}`, wantErr: "neither a web nor a desktop client"},
		{name: "NotJSON", file: `client_secret=s`, wantErr: "not JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, false)
			app, err := f.engine.ParseClientFile(connection.ProviderGoogle, []byte(tt.file), tt.opts)
			if tt.wantErr != "" {
				require.ErrorIs(t, err, connection.ErrInvalid)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, connection.ProviderGoogle, app.Provider)
			assert.Equal(t, clientID, app.ClientID)
			assert.Equal(t, "s", app.ClientSecret)
			assert.Equal(t, tt.wantType, app.ClientType)
		})
	}
}

// With an agent app, each MCP service lists its tools with the consent's
// token. A refusal says what to fix: a disabled API, or else the agent
// flag. Google's email alias counts as the requested email scope.
func TestConsentChecksMCP(t *testing.T) {
	t.Parallel()
	f := newFixture(t, true,
		connection.Service{Name: "gmail", Access: connection.AccessRead},
		connection.Service{Name: "calendar", Access: connection.AccessRead},
		connection.Service{Name: "drive", Access: connection.AccessRead},
	)
	f.google.grant("openid", emailAlias, "gmail.readonly", "calendar.calendarlist.readonly",
		"calendar.events.freebusy", "calendar.events.readonly", "drive.readonly")

	status := f.consent(t, connect.StartOptions{})
	assert.Equal(t, connect.PhaseConfirm, status.Phase)
	assert.Equal(t, "user@example.com", status.Account)
	assert.Equal(t, "sub-1", status.AccountID)
	assert.Contains(t, status.GrantedScopes, "email")
	assert.NotContains(t, status.GrantedScopes, emailAlias)
	assert.True(t, status.RefreshTokenExpiresAt.IsZero())
	assert.Empty(t, status.RefreshTokenFix)

	probes := probeErrors(t, status.Probes, "gmail", "calendar", "drive")
	assert.Empty(t, probes["gmail"])
	assert.Contains(t, probes["calendar"], "agent flag")
	assert.Contains(t, probes["calendar"], "https://console.cloud.google.com/auth/clients/"+clientID)
	// Google's error names no API here, so the fix names the preset's.
	assert.Contains(t, probes["drive"], "Enable drive.googleapis.com and drivemcp.googleapis.com in")
	assert.NotContains(t, probes["drive"], "agent flag")

	catalog, err := f.store.Catalog(testCtx(t), f.conn, "gmail")
	require.NoError(t, err)
	require.Len(t, catalog.Tools, 1)
	assert.Equal(t, "search_threads", catalog.Tools[0].Name)
}

// Without the agent flag, each service reads its REST profile, and a
// service whose scopes Google did not grant names them without a call. A
// read of an absent resource passes only on the API's own Not Found.
func TestConsentChecksREST(t *testing.T) {
	t.Parallel()
	f := newFixture(t, false,
		connection.Service{Name: "gmail", Access: connection.AccessRead},
		connection.Service{Name: "docs", Access: connection.AccessRead},
		connection.Service{Name: "sheets", Access: connection.AccessRead},
		connection.Service{Name: "slides", Access: connection.AccessRead},
		connection.Service{Name: "calendar", Access: connection.AccessRead},
		connection.Service{Name: "search", Access: connection.AccessRead},
	)
	f.google.grant("openid", emailAlias, "gmail.readonly", "drive.readonly", "documents.readonly",
		"spreadsheets.readonly", "presentations.readonly", "calendar.calendarlist.readonly",
		"calendar.events.freebusy", "calendar.readonly", "chat.messages.readonly", "chat.spaces.readonly",
		"chat.memberships.readonly")

	status := f.consent(t, connect.StartOptions{})
	probes := probeErrors(t, status.Probes, "gmail", "docs", "sheets", "slides", "calendar", "search")
	assert.Empty(t, probes["gmail"])
	assert.Empty(t, probes["docs"])
	// Google's error names the disabled API, and the REST read needs no
	// MCP API.
	assert.Contains(t, probes["sheets"], "Enable sheets.googleapis.com in")
	assert.Contains(t, probes["slides"], "slides answered HTTP 404")
	assert.Contains(t, probes["calendar"], "Google did not grant "+scopePrefix+"calendar.events.readonly")
	assert.Contains(t, probes["search"], "no REST API")
	assert.Equal(t, []string{
		"/gmail/v1/users/me/profile",
		"/v1/documents/dagu-connection-check",
		"/v1/presentations/dagu-connection-check",
		"/v4/spreadsheets/dagu-connection-check",
	}, f.google.restReads())
}

// Google reports a refresh token's end for time-based access. Only an end
// within seven days may be the limit of an app in Testing status, so only
// then does the fix name it.
func TestRefreshTokenFix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		expiresIn   time.Duration
		wantTesting bool
	}{
		{name: "WithinSevenDays", expiresIn: 7*24*time.Hour - time.Second, wantTesting: true},
		{name: "Longer", expiresIn: 30 * 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, false)
			f.google.refreshExpiresIn = int(tt.expiresIn.Seconds())

			status := f.consent(t, connect.StartOptions{})
			assert.False(t, status.RefreshTokenExpiresAt.IsZero())
			assert.Contains(t, status.RefreshTokenFix, "limited time")
			if tt.wantTesting {
				assert.Contains(t, status.RefreshTokenFix, "Publishing the app to Production")
			} else {
				assert.NotContains(t, status.RefreshTokenFix, "Testing")
			}
		})
	}
}

// An expected account confirms at once only by an email that Google
// reports as verified.
func TestVerifiedEmailConfirmsAtOnce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		verified  any
		wantPhase connect.Phase
	}{
		{name: "Verified", verified: true, wantPhase: connect.PhaseConnected},
		// Google's ID token example writes the flag as a string.
		{name: "VerifiedString", verified: "true", wantPhase: connect.PhaseConnected},
		{name: "Unverified", verified: false, wantPhase: connect.PhaseConfirm},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, false)
			f.google.emailVerified = tt.verified

			status := f.consent(t, connect.StartOptions{ExpectedAccount: "User@Example.com"})
			assert.Equal(t, tt.wantPhase, status.Phase)
		})
	}
}

// Consent errors, from the redirect or from the code exchange, fail the
// session with the fix of each.
func TestConsentErrorFixes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		redirectError string
		exchangeError string
		want          string
	}{
		{name: "AccessDenied", redirectError: "access_denied", want: "add the account under Test users"},
		{name: "AdminPolicy", redirectError: "admin_policy_enforced", want: "trust client ID " + clientID},
		{name: "RedirectMismatch", redirectError: "redirect_uri_mismatch", want: "Register the redirect URI " + callbackURI},
		{name: "InvalidClient", exchangeError: "invalid_client", want: "Download the client JSON again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, false)
			f.google.exchangeError = tt.exchangeError
			auth, err := f.engine.Start(testCtx(t), f.conn, connect.StartOptions{User: "alice", Redirect: serverCallback})
			require.NoError(t, err)
			params := callback(t, auth)
			if tt.redirectError != "" {
				params.Code, params.Error, params.ErrorDescription = "", tt.redirectError, "Google's description"
			}

			require.ErrorIs(t, f.engine.Callback(testCtx(t), params), connect.ErrConsentFailed)
			status, err := f.engine.Session(testCtx(t), f.conn, auth.SessionID)
			require.NoError(t, err)
			assert.Equal(t, connect.PhaseFailed, status.Phase)
			assert.True(t, strings.HasPrefix(status.Reason, cmp.Or(tt.redirectError, tt.exchangeError)+": "), status.Reason)
			assert.Contains(t, status.Reason, tt.want)
		})
	}
}

type fixture struct {
	store  connection.Store
	conn   *connection.Connection
	engine *connect.Engine
	google *fakeGoogle
}

// newFixture returns a connection of services through a web client, with
// the agent flag when agent is set, and an engine whose Google provider
// calls a fake Google.
func newFixture(t *testing.T, agent bool, services ...connection.Service) *fixture {
	t.Helper()
	ctx := context.Background()
	stores := file.NewServerStores(&config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}})
	s, err := stores.ConnectionStore()
	require.NoError(t, err)
	enc, err := stores.Encryptor()
	require.NoError(t, err)
	app, err := s.CreateApp(ctx, &connection.App{
		Name:         "Workspace",
		Provider:     connection.ProviderGoogle,
		Kind:         connection.AppKindBYO,
		ClientID:     clientID,
		ClientSecret: "client-secret",
		ClientType:   connection.ClientTypeWeb,
		Agent:        agent,
	})
	require.NoError(t, err)
	conn, err := s.Create(ctx, &connection.Connection{
		Workspace: "ops",
		Name:      "work",
		Provider:  connection.ProviderGoogle,
		App:       app.ID,
		Grant:     connection.GrantAuthorizationCode,
		Services:  services,
	})
	require.NoError(t, err)

	fake := newFakeGoogle(t)
	tokens, err := broker.New(broker.Config{Store: s, Locks: stores.ConnectionLocks()})
	require.NoError(t, err)
	catalog, err := toolcatalog.New(toolcatalog.Config{
		Store:  s,
		Locks:  stores.ConnectionCatalogLocks(),
		Lister: toolcatalog.NewMCPLister(tokens, fake.transport()),
	})
	require.NoError(t, err)
	cfg := google.Config{Catalog: catalog}
	google.SetTransport(&cfg, fake.transport())
	provider, err := google.New(cfg)
	require.NoError(t, err)
	engine, err := connect.New(connect.Config{
		Store:     s,
		Providers: map[connection.Provider]connect.Provider{connection.ProviderGoogle: provider},
		Encryptor: enc,
	})
	require.NoError(t, err)
	return &fixture{store: s, conn: conn, engine: engine, google: fake}
}

// consent runs a server callback session through consent and returns its
// status.
func (f *fixture) consent(t *testing.T, opts connect.StartOptions) connect.SessionStatus {
	t.Helper()
	opts.User, opts.Redirect = "alice", serverCallback
	auth, err := f.engine.Start(testCtx(t), f.conn, opts)
	require.NoError(t, err)
	require.NoError(t, f.engine.Callback(testCtx(t), callback(t, auth)))
	status, err := f.engine.Session(testCtx(t), f.conn, auth.SessionID)
	require.NoError(t, err)
	return status
}

// callback returns Google's redirect after a consent on the page of auth.
func callback(t *testing.T, auth *connect.Authorization) connect.CallbackParams {
	t.Helper()
	u, err := url.Parse(auth.URL)
	require.NoError(t, err)
	return connect.CallbackParams{State: u.Query().Get("state"), Code: fakeCode}
}

const (
	fakeCode   = "code-1"
	fakeAccess = "at-1"
)

// fakeGoogle serves Google's token, userinfo, MCP, and REST endpoints on
// one TLS server, telling them apart by host. Its MCP and REST services
// answer as their probes expect: gmailmcp lists a tool, calendarmcp
// rejects the client as Google does without the agent flag, drivemcp and
// the sheets API are disabled, and slides answers a bare 404.
type fakeGoogle struct {
	srv *httptest.Server
	// Tests set these before the consent.
	scope            string
	refreshExpiresIn int
	emailVerified    any
	exchangeError    string

	mu    sync.Mutex
	reads []string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{emailVerified: true}
	server := sdk.NewServer(&sdk.Implementation{Name: "gmail", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "search_threads", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{}, nil
		})
	// The fake listens on loopback but answers for googleapis.com hosts.
	mcp := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{DisableLocalhostProtection: true})

	g.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "oauth2.googleapis.com" && r.Header.Get("Authorization") != "Bearer "+fakeAccess {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Host {
		case "oauth2.googleapis.com":
			g.token(w, r)
		case "openidconnect.googleapis.com":
			writeJSON(w, http.StatusOK, map[string]any{"sub": "sub-1", "email": "user@example.com", "email_verified": g.emailVerified})
		case "gmailmcp.googleapis.com":
			mcp.ServeHTTP(w, r)
		case "calendarmcp.googleapis.com":
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "drivemcp.googleapis.com":
			writeJSON(w, http.StatusForbidden, serviceDisabled(""))
		case "sheets.googleapis.com":
			g.read(r)
			writeJSON(w, http.StatusForbidden, serviceDisabled(r.Host))
		case "docs.googleapis.com":
			g.read(r)
			writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
				"code": 404, "message": "Requested entity was not found.", "status": "NOT_FOUND",
			}})
		case "slides.googleapis.com":
			g.read(r)
			http.NotFound(w, r)
		default:
			g.read(r)
			writeJSON(w, http.StatusOK, map[string]any{})
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// grant makes the token endpoint grant scopes, each under Google's scope
// prefix unless it is openid or a URL.
func (g *fakeGoogle) grant(scopes ...string) {
	for i, s := range scopes {
		if s != "openid" && !strings.HasPrefix(s, "https://") {
			scopes[i] = scopePrefix + s
		}
	}
	g.scope = strings.Join(scopes, " ")
}

func (g *fakeGoogle) token(w http.ResponseWriter, r *http.Request) {
	switch {
	case g.exchangeError != "":
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": g.exchangeError, "error_description": "Unauthorized"})
	case r.FormValue("code") != fakeCode || r.FormValue("redirect_uri") != callbackURI || r.FormValue("code_verifier") == "":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
	default:
		body := map[string]any{
			"access_token":  fakeAccess,
			"refresh_token": "rt-1",
			"expires_in":    3599,
			"token_type":    "Bearer",
			"scope":         g.scope,
		}
		if g.refreshExpiresIn > 0 {
			body["refresh_token_expires_in"] = g.refreshExpiresIn
		}
		writeJSON(w, http.StatusOK, body)
	}
}

// read records a REST read.
func (g *fakeGoogle) read(r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads = append(g.reads, r.URL.Path)
}

// restReads returns the path of each REST read, sorted, since services
// are checked together.
func (g *fakeGoogle) restReads() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Sorted(slices.Values(g.reads))
}

// transport sends every request to the fake, keeping its host.
func (g *fakeGoogle) transport() http.RoundTripper {
	base := g.srv.Client().Transport
	addr := g.srv.Listener.Addr().String()
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Host = r.URL.Host
		r.URL.Host = addr
		return base.RoundTrip(r)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// serviceDisabled is Google's error for an API that the project has not
// enabled. Its ErrorInfo names api, unless api is empty.
func serviceDisabled(api string) map[string]any {
	info := map[string]any{
		"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
		"reason": "SERVICE_DISABLED",
		"domain": "googleapis.com",
	}
	if api != "" {
		info["metadata"] = map[string]string{"service": api, "consumer": "projects/123"}
	}
	return map[string]any{"error": map[string]any{
		"code":    403,
		"message": "The API has not been used in project 123 before or it is disabled.",
		"status":  "PERMISSION_DENIED",
		"details": []map[string]any{info},
	}}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// probeErrors returns each probe's error by service, once the probes are
// exactly one per service of services.
func probeErrors(t *testing.T, probes []connection.Probe, services ...string) map[string]string {
	t.Helper()
	errs := make(map[string]string, len(probes))
	got := make([]string, len(probes))
	for i, p := range probes {
		errs[p.Service] = p.Error
		got[i] = p.Service
	}
	require.ElementsMatch(t, services, got)
	return errs
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}
