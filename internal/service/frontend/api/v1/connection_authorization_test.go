// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/connection"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

const (
	testCode      = "authorization-code-value"
	testPublicURL = "https://dagu.example.com"
)

// A paste connect runs over the API: start, the pasted redirect, the
// account to confirm, and a confirm that only the starter may send. A
// reconnect leaves the live tokens until it is confirmed, so its cancel
// keeps them. No response or audit entry holds a token, code, or secret.
func TestConnectFlow(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	app := f.createDesktopApp(t, admin)
	f.createConnection(t, admin, "ops", "work", app.Id)
	starter, other := managerIn("ops", "starter"), managerIn("ops", "other")

	start := f.startAuthorization(t, starter, nil)
	assert.Equal(t, apigen.ConnectionRedirectModePaste, start.Mode)
	assert.Equal(t, "http://127.0.0.1:1/oauth/callback", start.RedirectUri)
	assert.Equal(t, "desktop-client-id", start.ClientId)
	assert.Equal(t, apigen.ConnectionAuthorizationPhaseWaiting, f.session(t, viewerIn("ops"), start.Session).Phase)

	authURL, err := url.Parse(start.AuthorizationUrl)
	require.NoError(t, err)
	state := authURL.Query().Get("state")
	pasted := start.RedirectUri + "?" + url.Values{"code": {testCode}, "state": {state}}.Encode()
	complete, err := f.api.CompleteConnectionAuthorization(starter, apigen.CompleteConnectionAuthorizationRequestObject{
		Name:   "work",
		Params: apigen.CompleteConnectionAuthorizationParams{Workspace: new("ops")},
		Body:   &apigen.CompleteConnectionAuthorizationRequest{RedirectUrl: &pasted},
	})
	require.NoError(t, expectResponse[apigen.CompleteConnectionAuthorization204Response](complete, err))

	pending := f.session(t, starter, start.Session)
	assert.Equal(t, apigen.ConnectionAuthorizationPhaseConfirm, pending.Phase)
	assert.Equal(t, testAccount, valueOf(pending.Account))
	assert.Equal(t, []apigen.ConnectionProbe{{Service: "gmail", Ok: true}}, valueOf(pending.Probes))

	confirm := func(ctx context.Context) (apigen.ConfirmConnectionAuthorizationResponseObject, error) {
		return f.api.ConfirmConnectionAuthorization(ctx, apigen.ConfirmConnectionAuthorizationRequestObject{
			Name:   "work",
			Params: apigen.ConfirmConnectionAuthorizationParams{Workspace: new("ops")},
			Body:   &apigen.ConfirmConnectionAuthorizationRequest{Session: start.Session},
		})
	}
	_, err = confirm(other)
	requireForbidden(t, err, "only the starter confirms")
	assert.Equal(t, apigen.ConnectionAuthorizationPhaseConfirm, f.session(t, starter, start.Session).Phase)
	confirmed, err := confirm(starter)
	require.NoError(t, err)
	conn := confirmed.(apigen.ConfirmConnectionAuthorization200JSONResponse)
	assert.Equal(t, apigen.ConnectionStatusConnected, conn.Status)
	assert.Equal(t, testAccount, valueOf(conn.Account))
	assert.Equal(t, apigen.ConnectionAuthorizationPhaseConnected, f.session(t, starter, start.Session).Phase)

	again := f.startAuthorization(t, starter, nil)
	cancel, err := f.api.CancelConnectionAuthorization(starter, apigen.CancelConnectionAuthorizationRequestObject{
		Name:   "work",
		Params: apigen.CancelConnectionAuthorizationParams{Workspace: new("ops"), Session: again.Session},
	})
	require.NoError(t, expectResponse[apigen.CancelConnectionAuthorization204Response](cancel, err))
	cancelled := f.session(t, starter, again.Session)
	assert.Equal(t, apigen.ConnectionAuthorizationPhaseFailed, cancelled.Phase)
	assert.Equal(t, "cancelled", valueOf(cancelled.Reason))
	live, err := f.api.GetConnection(starter, apigen.GetConnectionRequestObject{Name: "work", Params: apigen.GetConnectionParams{Workspace: new("ops")}})
	require.NoError(t, err)
	assert.Equal(t, apigen.ConnectionStatusConnected, live.(apigen.GetConnection200JSONResponse).Status)

	assert.Equal(t, []string{
		"oauth_app_create", "connection_create", "connection_connect", "connection_connect_confirm",
		"connection_reconnect", "connection_connect_cancel",
	}, auditActions(f.audit))
	for _, resp := range []any{start, pending, confirmed, again, cancelled, live, f.audit.entries} {
		payload := mustJSON(t, resp)
		for _, secret := range []string{testClientSecret, testRefreshToken, testAccessToken, testCode} {
			assert.NotContains(t, payload, secret)
		}
	}
	assert.NotContains(t, mustJSON(t, f.audit.entries), state)
}

// The redirect mode follows the app's client type and the server's public
// URL. A mode that does not work starts no session.
func TestStartConnectionRedirect(t *testing.T) {
	start := func(f *connectionsFixture, mode *apigen.ConnectionRedirectMode) (apigen.StartConnectionAuthorizationResponseObject, error) {
		return f.api.StartConnectionAuthorization(f.admin(), apigen.StartConnectionAuthorizationRequestObject{
			Name:   "work",
			Params: apigen.StartConnectionAuthorizationParams{Workspace: new("ops")},
			Body:   &apigen.StartConnectionAuthorizationRequest{Mode: mode},
		})
	}
	private := newConnectionsFixture(t)
	private.createConnection(t, private.admin(), "ops", "work", private.createApp(t, private.admin()).Id)
	// A web client needs a public URL.
	_, err := start(private, nil)
	requireStatus(t, http.StatusBadRequest, err)

	public := newConnectionsFixture(t, apiv1.WithPublicURLResolver(func() string { return testPublicURL }))
	public.createConnection(t, public.admin(), "ops", "work", public.createApp(t, public.admin()).Id)
	resp, err := start(public, nil)
	require.NoError(t, err)
	started := resp.(apigen.StartConnectionAuthorization200JSONResponse)
	assert.Equal(t, apigen.ConnectionRedirectModeServerCallback, started.Mode)
	assert.Equal(t, testPublicURL+"/oauth/callback", started.RedirectUri)
	assert.Equal(t, "client-id", started.ClientId)
	// Paste needs a desktop client.
	_, err = start(public, new(apigen.ConnectionRedirectModePaste))
	requireStatus(t, http.StatusBadRequest, err)
}

// The providers list each service with its scopes and setup pages, and
// the redirect modes, with the URI to register, follow the server's public
// URL.
func TestListConnectionProviders(t *testing.T) {
	list := func(f *connectionsFixture) apigen.ListConnectionProviders200JSONResponse {
		t.Helper()
		resp, err := f.api.ListConnectionProviders(viewerIn("ops"), apigen.ListConnectionProvidersRequestObject{})
		require.NoError(t, err)
		return resp.(apigen.ListConnectionProviders200JSONResponse)
	}
	paste := apigen.ConnectionRedirect{Mode: apigen.ConnectionRedirectModePaste, ClientType: apigen.OAuthClientTypeDesktop, RedirectUri: new("http://127.0.0.1:1/oauth/callback")}
	loopback := apigen.ConnectionRedirect{Mode: apigen.ConnectionRedirectModeLoopback, ClientType: apigen.OAuthClientTypeDesktop}

	private := list(newConnectionsFixture(t))
	assert.Nil(t, private.PublicUrl)
	assert.Equal(t, []apigen.ConnectionRedirect{loopback, paste}, private.Redirects)

	public := list(newConnectionsFixture(t, apiv1.WithPublicURLResolver(func() string { return testPublicURL })))
	assert.Equal(t, []apigen.ConnectionRedirect{
		{Mode: apigen.ConnectionRedirectModeServerCallback, ClientType: apigen.OAuthClientTypeWeb, RedirectUri: new(testPublicURL + "/oauth/callback")},
		loopback, paste,
	}, public.Redirects)

	require.Len(t, public.Providers, 1)
	google := public.Providers[0]
	assert.Equal(t, apigen.ConnectionProviderNameGoogle, google.Name)
	assert.Equal(t, []string{"openid", "email"}, google.UserScopes)
	require.NotNil(t, google.SetupLinks)
	assert.NotEmpty(t, valueOf(google.SetupLinks.Clients))
	gmail := google.Services[0]
	assert.Equal(t, "gmail", gmail.Name)
	assert.Equal(t, "https://gmailmcp.googleapis.com/mcp/v1", valueOf(gmail.McpUrl))
	assert.Equal(t, []string{"https://www.googleapis.com/auth/gmail.readonly"}, gmail.ReadScopes)
	assert.Contains(t, valueOf(gmail.EnableApisUrl), "gmail.googleapis.com,gmailmcp.googleapis.com")
}

// A test probes each service with the connection's live token, and the
// tools come from the stored catalog. A developer may do both and a viewer
// neither. A connection that never connected cannot be tested.
func TestTestConnectionAndTools(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	f.createConnection(t, admin, "ops", "work", f.createApp(t, admin).Id)
	developer := asConnectionUser(auth.RoleViewer, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleDeveloper}}})
	viewer := viewerIn("ops")
	test := func(ctx context.Context) (apigen.TestConnectionResponseObject, error) {
		return f.api.TestConnection(ctx, apigen.TestConnectionRequestObject{Name: "work", Params: apigen.TestConnectionParams{Workspace: new("ops")}})
	}
	tools := func(ctx context.Context, server *string) (apigen.ListConnectionToolsResponseObject, error) {
		return f.api.ListConnectionTools(ctx, apigen.ListConnectionToolsRequestObject{Name: "work", Params: apigen.ListConnectionToolsParams{Workspace: new("ops"), Server: server}})
	}

	// A pending connection has no token to test.
	_, err := test(developer)
	requireStatus(t, http.StatusConflict, err)
	f.connect(t, "ops", "work")
	tested, err := test(developer)
	require.NoError(t, err)
	assert.Equal(t, []apigen.ConnectionProbe{{Service: "gmail", Ok: true}}, tested.(apigen.TestConnection200JSONResponse).Services)
	assert.Equal(t, []string{testAccessToken}, f.provider.probed, "the probe uses the live token")
	_, err = test(viewer)
	requireForbidden(t, err)

	conn, err := f.store.Get(t.Context(), connection.Key{Workspace: "ops", Name: "work"})
	require.NoError(t, err)
	_, err = f.store.SaveCatalog(t.Context(), conn, &connection.Catalog{
		Service:  "gmail",
		ListedAt: time.Now(),
		Tools: []connection.Tool{{
			Name: "search_threads", Description: "Search mail",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Annotations: json.RawMessage(`{"readOnlyHint":true}`),
		}},
	})
	require.NoError(t, err)
	listed, err := tools(developer, nil)
	require.NoError(t, err)
	servers := listed.(apigen.ListConnectionTools200JSONResponse).Servers
	require.Len(t, servers, 1)
	assert.Equal(t, "gmail", servers[0].Server)
	require.Len(t, servers[0].Tools, 1)
	tool := servers[0].Tools[0]
	assert.Equal(t, "search_threads", tool.Name)
	assert.Equal(t, map[string]any{"type": "object"}, tool.InputSchema)
	assert.Equal(t, map[string]any{"readOnlyHint": true}, valueOf(tool.Annotations))
	// calendar is not a service of the connection.
	_, err = tools(developer, new("calendar"))
	requireStatus(t, http.StatusBadRequest, err)
	_, err = tools(viewer, nil)
	requireForbidden(t, err)

	for _, resp := range []any{tested, listed} {
		payload := mustJSON(t, resp)
		for _, secret := range []string{testClientSecret, testRefreshToken, testAccessToken} {
			assert.NotContains(t, payload, secret)
		}
	}
}

func (f *connectionsFixture) createDesktopApp(t *testing.T, ctx context.Context) apigen.OAuthApp {
	t.Helper()
	body := googleAppRequest()
	body.Name, body.ClientId, body.ClientType = "Google desktop", new("desktop-client-id"), new(apigen.OAuthClientTypeDesktop)
	resp, err := f.api.CreateOAuthApp(ctx, apigen.CreateOAuthAppRequestObject{Body: body})
	require.NoError(t, err)
	return apigen.OAuthApp(resp.(apigen.CreateOAuthApp201JSONResponse))
}

func (f *connectionsFixture) startAuthorization(t *testing.T, ctx context.Context, body *apigen.StartConnectionAuthorizationRequest) apigen.StartConnectionAuthorization200JSONResponse {
	t.Helper()
	resp, err := f.api.StartConnectionAuthorization(ctx, apigen.StartConnectionAuthorizationRequestObject{
		Name:   "work",
		Params: apigen.StartConnectionAuthorizationParams{Workspace: new("ops")},
		Body:   body,
	})
	require.NoError(t, err)
	return resp.(apigen.StartConnectionAuthorization200JSONResponse)
}

func (f *connectionsFixture) session(t *testing.T, ctx context.Context, session string) apigen.GetConnectionAuthorization200JSONResponse {
	t.Helper()
	resp, err := f.api.GetConnectionAuthorization(ctx, apigen.GetConnectionAuthorizationRequestObject{
		Name:   "work",
		Params: apigen.GetConnectionAuthorizationParams{Workspace: new("ops"), Session: session},
	})
	require.NoError(t, err)
	return resp.(apigen.GetConnectionAuthorization200JSONResponse)
}

// managerIn returns a user named id who manages ws alone.
func managerIn(ws, id string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{
		ID: id, Username: id, Role: auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: ws, Role: auth.RoleManager}}},
		AuthProvider:    auth.AuthProviderCloud,
	})
}
