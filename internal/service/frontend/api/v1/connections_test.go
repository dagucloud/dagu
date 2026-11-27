// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	"github.com/dagucloud/dagu/v2/internal/connection/connect/google"
	"github.com/dagucloud/dagu/v2/internal/connection/toolcatalog"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	testClientSecret = "client-secret-value"
	testRefreshToken = "refresh-token-value"
	testAccessToken  = "access-token-value"
	testAccount      = "user@example.com"
)

// Every response and audit entry of the apps and connections API reports
// whether a secret is configured and never holds one, also once the
// connection holds tokens. A blank secret in a change keeps the stored one.
func TestConnectionsHideSecrets(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	app := f.createApp(t, admin)
	assert.True(t, app.ClientSecretConfigured)
	conn := f.createConnection(t, admin, "ops", "work", app.Id)
	assert.Equal(t, apigen.ConnectionStatusPending, conn.Status)
	assert.False(t, conn.RefreshTokenConfigured)
	f.connect(t, "ops", "work")

	blank := " "
	appResp, err := f.api.UpdateOAuthApp(admin, apigen.UpdateOAuthAppRequestObject{
		AppId: app.Id,
		Body:  &apigen.UpdateOAuthAppRequest{ClientSecret: &blank},
	})
	require.NoError(t, err)
	stored, err := f.store.GetApp(t.Context(), app.Id)
	require.NoError(t, err)
	assert.Equal(t, testClientSecret, stored.ClientSecret)

	appList, err := f.api.ListOAuthApps(admin, apigen.ListOAuthAppsRequestObject{})
	require.NoError(t, err)
	appGet, err := f.api.GetOAuthApp(admin, apigen.GetOAuthAppRequestObject{AppId: app.Id})
	require.NoError(t, err)
	connList, err := f.api.ListConnections(admin, apigen.ListConnectionsRequestObject{Params: apigen.ListConnectionsParams{Workspace: new("ops")}})
	require.NoError(t, err)
	connGet, err := f.api.GetConnection(admin, apigen.GetConnectionRequestObject{Name: "work", Params: apigen.GetConnectionParams{Workspace: new("ops")}})
	require.NoError(t, err)
	readWrite := []apigen.ConnectionServiceRequest{{Name: "gmail", Access: apigen.ConnectionAccessWrite}}
	connPatch, err := f.api.UpdateConnection(admin, apigen.UpdateConnectionRequestObject{
		Name:   "work",
		Params: apigen.UpdateConnectionParams{Workspace: new("ops")},
		Body:   &apigen.UpdateConnectionRequest{Services: &readWrite},
	})
	require.NoError(t, err)

	got := connGet.(apigen.GetConnection200JSONResponse)
	assert.Equal(t, apigen.ConnectionStatusConnected, got.Status)
	assert.True(t, got.RefreshTokenConfigured)
	assert.Equal(t, "user@example.com", valueOf(got.Account))
	assert.Empty(t, got.Services[0].MissingScopes)
	patched := connPatch.(apigen.UpdateConnection200JSONResponse)
	assert.Equal(t, []string{
		"https://www.googleapis.com/auth/gmail.compose",
		"https://www.googleapis.com/auth/gmail.modify",
	}, patched.Services[0].MissingScopes, "a service changed later lacks its new scopes until a reconnect")
	assert.True(t, appGet.(apigen.GetOAuthApp200JSONResponse).ClientSecretConfigured)

	for _, resp := range []any{app, conn, appResp, appList, appGet, connList, connGet, connPatch, f.audit.entries} {
		payload := mustJSON(t, resp)
		for _, secret := range []string{testClientSecret, testRefreshToken, testAccessToken} {
			assert.NotContains(t, payload, secret)
		}
	}
}

// Each role can do what RFC 025's permissions list for apps and
// connections, and nothing more. A user who cannot see a workspace finds
// no connection in it.
func TestConnectionsRoles(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	app := f.createApp(t, admin)
	f.createConnection(t, admin, "ops", "work", app.Id)
	f.createConnection(t, admin, "global", "work", app.Id)
	opsRole := func(role auth.Role) context.Context {
		return asConnectionUser(auth.RoleViewer, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: role}}})
	}
	viewer, developer, manager := opsRole(auth.RoleViewer), opsRole(auth.RoleDeveloper), opsRole(auth.RoleManager)
	globalDeveloper := asConnectionUser(auth.RoleDeveloper, auth.AllWorkspaceAccess())

	create := func(ctx context.Context, ws, name string, export bool) error {
		resp, err := f.api.CreateConnection(ctx, apigen.CreateConnectionRequestObject{
			Params: apigen.CreateConnectionParams{Workspace: &ws},
			Body: &apigen.CreateConnectionRequest{
				Name: name, App: app.Id, AllowTokenExport: &export,
				Services: []apigen.ConnectionServiceRequest{{Name: "gmail", Access: apigen.ConnectionAccessRead}},
			},
		})
		return expectResponse[apigen.CreateConnection201JSONResponse](resp, err)
	}
	get := func(ctx context.Context, ws string) error {
		resp, err := f.api.GetConnection(ctx, apigen.GetConnectionRequestObject{Name: "work", Params: apigen.GetConnectionParams{Workspace: &ws}})
		return expectResponse[apigen.GetConnection200JSONResponse](resp, err)
	}
	listApps := func(ctx context.Context) error {
		resp, err := f.api.ListOAuthApps(ctx, apigen.ListOAuthAppsRequestObject{})
		return expectResponse[apigen.ListOAuthApps200JSONResponse](resp, err)
	}
	createApp := func(ctx context.Context) error {
		resp, err := f.api.CreateOAuthApp(ctx, apigen.CreateOAuthAppRequestObject{Body: googleAppRequest()})
		return expectResponse[apigen.CreateOAuthApp201JSONResponse](resp, err)
	}

	require.NoError(t, get(viewer, "ops"))
	requireForbidden(t, create(viewer, "ops", "viewer", false))
	requireForbidden(t, create(developer, "ops", "developer", false))
	require.NoError(t, create(manager, "ops", "manager", false))
	requireForbidden(t, create(manager, "ops", "export", true), "token export needs admin")
	require.NoError(t, create(admin, "ops", "export", true))
	requireStatus(t, http.StatusNotFound, create(manager, "billing", "other", false))
	requireStatus(t, http.StatusNotFound, get(viewerIn("billing"), "ops"))
	// No grant names the global workspace, so a user scoped to workspaces
	// never reaches its connections.
	requireStatus(t, http.StatusNotFound, get(manager, "global"))
	requireStatus(t, http.StatusNotFound, create(manager, "global", "scoped", false))
	require.NoError(t, get(globalDeveloper, "global"))

	requireForbidden(t, createApp(manager))
	require.NoError(t, createApp(admin))
	require.NoError(t, listApps(manager), "a manager in a workspace picks an app")
	require.NoError(t, listApps(globalDeveloper), "a developer may change the notification settings")
	requireForbidden(t, listApps(viewer))

	export := false
	resp, err := f.api.UpdateConnection(manager, apigen.UpdateConnectionRequestObject{
		Name:   "export",
		Params: apigen.UpdateConnectionParams{Workspace: new("ops")},
		Body:   &apigen.UpdateConnectionRequest{AllowTokenExport: &export},
	})
	requireForbidden(t, expectResponse[apigen.UpdateConnection200JSONResponse](resp, err))
	delResp, err := f.api.DeleteConnection(viewer, apigen.DeleteConnectionRequestObject{Name: "manager", Params: apigen.DeleteConnectionParams{Workspace: new("ops")}})
	requireForbidden(t, expectResponse[apigen.DeleteConnection204Response](delResp, err))
	delResp, err = f.api.DeleteConnection(manager, apigen.DeleteConnectionRequestObject{Name: "manager", Params: apigen.DeleteConnectionParams{Workspace: new("ops")}})
	require.NoError(t, expectResponse[apigen.DeleteConnection204Response](delResp, err))
}

// A connection whose token state is missing, as a create that stopped
// halfway leaves it, or cannot be decoded is shown as broken, not hidden, so
// it can be removed and added again.
func TestConnectionsShowBrokenTokenState(t *testing.T) {
	for name, damage := range map[string]func(path string) error{
		"Missing": os.Remove,
		"Corrupt": func(path string) error { return os.WriteFile(path, []byte("{"), 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newConnectionsFixture(t)
			admin := f.admin()
			app := f.createApp(t, admin)
			f.createConnection(t, admin, "ops", "work", app.Id)
			files := f.tokenFiles(t)
			require.Len(t, files, 1)
			require.NoError(t, damage(files[0]))

			resp, err := f.api.ListConnections(admin, apigen.ListConnectionsRequestObject{Params: apigen.ListConnectionsParams{Workspace: new("ops")}})
			require.NoError(t, err)
			listed := resp.(apigen.ListConnections200JSONResponse).Connections
			require.Len(t, listed, 1)
			assert.Equal(t, apigen.ConnectionStatusBroken, listed[0].Status)
			assert.Contains(t, valueOf(listed[0].StatusReason), "Remove the connection")

			delResp, err := f.api.DeleteConnection(admin, apigen.DeleteConnectionRequestObject{Name: "work", Params: apigen.DeleteConnectionParams{Workspace: new("ops")}})
			require.NoError(t, expectResponse[apigen.DeleteConnection204Response](delResp, err))
		})
	}
}

// Removing a connection revokes its refresh token, and the audit log
// records the removal and the revocation without the token.
func TestDeleteConnectionRevokes(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	app := f.createApp(t, admin)
	f.createConnection(t, admin, "ops", "work", app.Id)
	f.connect(t, "ops", "work")

	resp, err := f.api.DeleteConnection(admin, apigen.DeleteConnectionRequestObject{Name: "work", Params: apigen.DeleteConnectionParams{Workspace: new("ops")}})
	require.NoError(t, expectResponse[apigen.DeleteConnection204Response](resp, err))

	_, err = f.store.Get(t.Context(), connection.Key{Workspace: "ops", Name: "work"})
	require.ErrorIs(t, err, connection.ErrNotFound)
	assert.Equal(t, []string{testRefreshToken}, f.provider.revoked)
	actions := auditActions(f.audit)
	assert.Contains(t, actions, "connection_delete")
	assert.Contains(t, actions, "connection_token_revoke")
	assert.NotContains(t, mustJSON(t, f.audit.entries), testRefreshToken)
}

// An app that a connection uses cannot be deleted, nor change the client
// type its connections were created for. The refusal names the connection
// with its workspace.
func TestOAuthAppInUse(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	app := f.createApp(t, admin)
	f.createConnection(t, admin, "ops", "work", app.Id)
	changeType := func() error {
		resp, err := f.api.UpdateOAuthApp(admin, apigen.UpdateOAuthAppRequestObject{
			AppId: app.Id,
			Body:  &apigen.UpdateOAuthAppRequest{ClientType: new(apigen.OAuthClientTypeDesktop)},
		})
		return expectResponse[apigen.UpdateOAuthApp200JSONResponse](resp, err)
	}
	deleteApp := func() error {
		resp, err := f.api.DeleteOAuthApp(admin, apigen.DeleteOAuthAppRequestObject{AppId: app.Id})
		return expectResponse[apigen.DeleteOAuthApp204Response](resp, err)
	}

	for _, err := range []error{changeType(), deleteApp()} {
		requireStatus(t, http.StatusConflict, err)
		assert.ErrorContains(t, err, "ops/work")
	}

	delResp, err := f.api.DeleteConnection(admin, apigen.DeleteConnectionRequestObject{Name: "work", Params: apigen.DeleteConnectionParams{Workspace: new("ops")}})
	require.NoError(t, expectResponse[apigen.DeleteConnection204Response](delResp, err))
	require.NoError(t, changeType())
	require.NoError(t, deleteApp())
}

// A Google client JSON registers an app as downloaded. A web client must
// list the server callback that the server would register, and the file
// takes the place of the client fields.
func TestCreateOAuthAppFromClientFile(t *testing.T) {
	f := newConnectionsFixture(t, apiv1.WithPublicURLResolver(func() string { return "https://dagu.example.com" }))
	create := func(file string, clientID *string) (apigen.OAuthApp, error) {
		resp, err := f.api.CreateOAuthApp(f.admin(), apigen.CreateOAuthAppRequestObject{Body: &apigen.CreateOAuthAppRequest{
			Name: "Google", Provider: apigen.CreateOAuthAppRequestProviderGoogle, ClientFile: &file, ClientId: clientID,
		}})
		if err := expectResponse[apigen.CreateOAuthApp201JSONResponse](resp, err); err != nil {
			return apigen.OAuthApp{}, err
		}
		return apigen.OAuthApp(resp.(apigen.CreateOAuthApp201JSONResponse)), nil
	}
	clientFile := func(kind, redirect string) string {
		return `{"` + kind + `": {"client_id": "client-1.apps.googleusercontent.com", "client_secret": "` + testClientSecret + `", "redirect_uris": ["` + redirect + `"]}}`
	}

	web, err := create(clientFile("web", "https://dagu.example.com/oauth/callback"), nil)
	require.NoError(t, err)
	assert.Equal(t, apigen.OAuthClientTypeWeb, web.ClientType)
	assert.Equal(t, "client-1.apps.googleusercontent.com", web.ClientId)
	assert.True(t, web.ClientSecretConfigured)
	assert.NotContains(t, mustJSON(t, web), testClientSecret)
	stored, err := f.store.GetApp(t.Context(), web.Id)
	require.NoError(t, err)
	assert.Equal(t, testClientSecret, stored.ClientSecret)

	desktop, err := create(clientFile("installed", "http://localhost"), nil)
	require.NoError(t, err)
	assert.Equal(t, apigen.OAuthClientTypeDesktop, desktop.ClientType)

	_, err = create(clientFile("web", "https://other.example.com/oauth/callback"), nil)
	requireStatus(t, http.StatusBadRequest, err)
	assert.ErrorContains(t, err, "https://dagu.example.com/oauth/callback")
	_, err = create(clientFile("web", "https://dagu.example.com/oauth/callback"), new("client-2"))
	requireStatus(t, http.StatusBadRequest, err)
}

// Apps of oauth2 and mcp are refused, since no request sets the endpoints
// they need, nor the resource URL of an mcp connection. The schema leaves
// them out, and the handler refuses them also without request validation.
func TestCreateOAuthAppProviders(t *testing.T) {
	f := newConnectionsFixture(t)
	for _, provider := range []apigen.ConnectionProviderName{apigen.ConnectionProviderNameOauth2, apigen.ConnectionProviderNameMcp} {
		body := googleAppRequest()
		body.Provider = apigen.CreateOAuthAppRequestProvider(provider)
		resp, err := f.api.CreateOAuthApp(f.admin(), apigen.CreateOAuthAppRequestObject{Body: body})
		requireStatus(t, http.StatusBadRequest, expectResponse[apigen.CreateOAuthApp201JSONResponse](resp, err))
	}
}

// A workspace that owns connections cannot be deleted or renamed, since a
// workspace created later under its name would inherit them.
func TestWorkspaceWithConnections(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	ws, err := f.workspaces.GetByName(t.Context(), "ops")
	require.NoError(t, err)
	app := f.createApp(t, admin)
	f.createConnection(t, admin, "ops", "work", app.Id)

	rename := "ops2"
	updResp, err := f.api.UpdateWorkspace(admin, apigen.UpdateWorkspaceRequestObject{WorkspaceId: ws.ID, Body: &apigen.UpdateWorkspaceJSONRequestBody{Name: &rename}})
	err = expectResponse[apigen.UpdateWorkspace200JSONResponse](updResp, err)
	requireStatus(t, http.StatusConflict, err)
	assert.ErrorContains(t, err, "work")
	description := "kept"
	updResp, err = f.api.UpdateWorkspace(admin, apigen.UpdateWorkspaceRequestObject{WorkspaceId: ws.ID, Body: &apigen.UpdateWorkspaceJSONRequestBody{Description: &description}})
	require.NoError(t, expectResponse[apigen.UpdateWorkspace200JSONResponse](updResp, err))
	delResp, err := f.api.DeleteWorkspace(admin, apigen.DeleteWorkspaceRequestObject{WorkspaceId: ws.ID})
	requireStatus(t, http.StatusConflict, expectResponse[apigen.DeleteWorkspace204Response](delResp, err))

	connResp, err := f.api.DeleteConnection(admin, apigen.DeleteConnectionRequestObject{Name: "work", Params: apigen.DeleteConnectionParams{Workspace: new("ops")}})
	require.NoError(t, expectResponse[apigen.DeleteConnection204Response](connResp, err))
	delResp, err = f.api.DeleteWorkspace(admin, apigen.DeleteWorkspaceRequestObject{WorkspaceId: ws.ID})
	require.NoError(t, expectResponse[apigen.DeleteWorkspace204Response](delResp, err))
}

// A connection request is refused when no run could use the connection or
// the store would hold a host it must not: a workspace name with uppercase
// letters, a workspace that does not exist and whose later namesake would
// inherit it, a service the provider lacks, or a bound host with a scheme.
// Omitting the workspace selects the connections of DAGs without a label.
func TestCreateConnectionValidation(t *testing.T) {
	f := newConnectionsFixture(t)
	admin := f.admin()
	app := f.createApp(t, admin)
	create := func(ws *string, service string, hosts []string) error {
		resp, err := f.api.CreateConnection(admin, apigen.CreateConnectionRequestObject{
			Params: apigen.CreateConnectionParams{Workspace: ws},
			Body: &apigen.CreateConnectionRequest{
				Name: "work", App: app.Id, BoundHosts: &hosts,
				Services: []apigen.ConnectionServiceRequest{{Name: service, Access: apigen.ConnectionAccessRead}},
			},
		})
		return expectResponse[apigen.CreateConnection201JSONResponse](resp, err)
	}

	requireStatus(t, http.StatusBadRequest, create(new("Ops"), "gmail", nil))
	requireStatus(t, http.StatusBadRequest, create(new("default"), "gmail", nil))
	requireStatus(t, http.StatusNotFound, create(new("gone"), "gmail", nil))
	requireStatus(t, http.StatusBadRequest, create(nil, "tasks", nil))
	requireStatus(t, http.StatusBadRequest, create(nil, "gmail", []string{"https://api.example.com"}))
	requireStatus(t, http.StatusBadRequest, create(nil, "gmail", []string{"*.example.com"}))
	require.NoError(t, create(nil, "gmail", []string{"api.example.com:8443"}))

	resp, err := f.api.GetConnection(admin, apigen.GetConnectionRequestObject{Name: "work", Params: apigen.GetConnectionParams{Workspace: new("global")}})
	require.NoError(t, err)
	assert.Equal(t, "global", resp.(apigen.GetConnection200JSONResponse).Workspace)
	requireStatus(t, http.StatusConflict, create(nil, "gmail", nil))
}

type connectionsFixture struct {
	api        *apiv1.API
	store      connection.Store
	workspaces workspace.Store
	provider   *fakeGoogle
	audit      *recordingAuditStore
	dataDir    string
}

func newConnectionsFixture(t *testing.T, opts ...apiv1.APIOption) *connectionsFixture {
	t.Helper()
	dataDir := t.TempDir()
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: dataDir, WorkspacesDir: filepath.Join(dataDir, "workspaces")}}
	stores := persisfile.NewServerStores(cfg)
	store, err := stores.ConnectionStore()
	require.NoError(t, err)
	workspaces, err := stores.WorkspaceStore()
	require.NoError(t, err)
	enc, err := stores.Encryptor()
	require.NoError(t, err)

	tokens, err := broker.New(broker.Config{Store: store, Locks: stores.ConnectionLocks()})
	require.NoError(t, err)
	catalog, err := toolcatalog.New(toolcatalog.Config{Store: store, Locks: stores.ConnectionCatalogLocks(), Lister: toolcatalog.NewMCPLister(tokens, nil)})
	require.NoError(t, err)
	googleProvider, err := google.New(google.Config{Catalog: catalog})
	require.NoError(t, err)

	f := &connectionsFixture{store: store, workspaces: workspaces, provider: &fakeGoogle{Provider: googleProvider}, audit: &recordingAuditStore{}, dataDir: dataDir}
	engine, err := connect.New(connect.Config{
		Store:     store,
		Locks:     stores.ConnectionLocks(),
		Encryptor: enc,
		Tokens:    tokens,
		Providers: map[connection.Provider]connect.Provider{connection.ProviderGoogle: f.provider},
		OnRevoke:  func(ctx context.Context, r connect.Revocation) { f.api.AuditRevocation(ctx, r) },
	})
	require.NoError(t, err)
	opts = append([]apiv1.APIOption{
		apiv1.WithConnections(store, engine, catalog),
		apiv1.WithWorkspaceStore(workspaces),
		apiv1.WithAuditService(audit.New(f.audit)),
	}, opts...)
	f.api = apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil, opts...)
	require.NoError(t, workspaces.Create(t.Context(), workspace.NewWorkspace("ops", "")))
	return f
}

func (f *connectionsFixture) admin() context.Context {
	return asConnectionUser(auth.RoleAdmin, auth.AllWorkspaceAccess())
}

func (f *connectionsFixture) createApp(t *testing.T, ctx context.Context) apigen.OAuthApp {
	t.Helper()
	resp, err := f.api.CreateOAuthApp(ctx, apigen.CreateOAuthAppRequestObject{Body: googleAppRequest()})
	require.NoError(t, err)
	return apigen.OAuthApp(resp.(apigen.CreateOAuthApp201JSONResponse))
}

func (f *connectionsFixture) createConnection(t *testing.T, ctx context.Context, ws, name, app string) apigen.Connection {
	t.Helper()
	resp, err := f.api.CreateConnection(ctx, apigen.CreateConnectionRequestObject{
		Params: apigen.CreateConnectionParams{Workspace: &ws},
		Body: &apigen.CreateConnectionRequest{
			Name: name, App: app,
			Services: []apigen.ConnectionServiceRequest{{Name: "gmail", Access: apigen.ConnectionAccessRead}},
		},
	})
	require.NoError(t, err)
	return apigen.Connection(resp.(apigen.CreateConnection201JSONResponse))
}

// connect gives the connection the tokens a confirmed consent leaves.
func (f *connectionsFixture) connect(t *testing.T, ws, name string) {
	t.Helper()
	conn, err := f.store.Get(t.Context(), connection.Key{Workspace: ws, Name: name})
	require.NoError(t, err)
	_, err = f.store.UpdateTokenState(t.Context(), conn, func(st *connection.TokenState) error {
		st.Status = connection.TokenStatusConnected
		st.Tokens = connection.Tokens{
			Account:       "user@example.com",
			AccountID:     "sub-1",
			RefreshToken:  testRefreshToken,
			AccessTokens:  map[string]connection.AccessToken{"": {Token: testAccessToken}},
			GrantedScopes: conn.RequestedScopes(),
		}
		return nil
	})
	require.NoError(t, err)
}

func (f *connectionsFixture) tokenFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(f.dataDir, "connection-tokens"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".json") {
			files = append(files, path)
		}
		return err
	})
	require.NoError(t, err)
	return files
}

func googleAppRequest() *apigen.CreateOAuthAppRequest {
	return &apigen.CreateOAuthAppRequest{
		Name:         "Google",
		Provider:     apigen.CreateOAuthAppRequestProviderGoogle,
		ClientId:     new("client-id"),
		ClientSecret: new(testClientSecret),
		ClientType:   new(apigen.OAuthClientTypeWeb),
		Agent:        new(true),
	}
}

func asConnectionUser(role auth.Role, access *auth.WorkspaceAccess) context.Context {
	return auth.WithUser(context.Background(), &auth.User{
		ID: "cloud:" + string(role), Username: string(role), Role: role,
		WorkspaceAccess: access, AuthProvider: auth.AuthProviderCloud,
	})
}

func viewerIn(ws string) context.Context {
	return asConnectionUser(auth.RoleViewer, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: ws, Role: auth.RoleViewer}}})
}

func requireStatus(t *testing.T, status int, err error) {
	t.Helper()
	var apiErr *apiv1.Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, status, apiErr.HTTPStatus)
}

// fakeGoogle is the Google provider with a consent, an identity, probes,
// and a revocation that never call Google. Each consent returns a new
// refresh token, and each probe and revocation is recorded.
type fakeGoogle struct {
	*google.Provider
	revoked  []string
	consents int
	// probed holds the access token of each probe.
	probed []string
}

func (p *fakeGoogle) RevokeScope() connect.RevokeScope { return connect.RevokesToken }

func (p *fakeGoogle) Revoke(_ context.Context, _ *connection.App, refreshToken string) error {
	p.revoked = append(p.revoked, refreshToken)
	return nil
}

func (p *fakeGoogle) Exchange(_ context.Context, cfg *oauth2.Config, _ *connection.Connection, _ string, _ ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	p.consents++
	token := &oauth2.Token{
		AccessToken:  testAccessToken,
		RefreshToken: fmt.Sprintf("%s-%d", testRefreshToken, p.consents),
		Expiry:       time.Now().Add(time.Hour),
	}
	return token.WithExtra(map[string]any{"scope": strings.Join(cfg.Scopes, " ")}), nil
}

func (p *fakeGoogle) Identity(context.Context, *connection.App, *oauth2.Token) (connect.Identity, error) {
	return connect.Identity{Account: testAccount, AccountID: "sub-1", VouchedEmail: testAccount}, nil
}

func (p *fakeGoogle) Probe(_ context.Context, _ *connection.App, conn *connection.Connection, token *oauth2.Token) []connection.Probe {
	p.probed = append(p.probed, token.AccessToken)
	probes := make([]connection.Probe, len(conn.Services))
	for i, svc := range conn.Services {
		probes[i] = connection.Probe{Service: svc.Name}
	}
	return probes
}

func auditActions(store *recordingAuditStore) []string {
	var actions []string
	for _, entry := range store.entries {
		actions = append(actions, entry.Action)
	}
	return actions
}
