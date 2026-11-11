// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	mcpTestToken    = "tok-0123456789abcdefghij-token"
	mcpTestTokenTwo = "tok-ZYXWVUTSRQPONMLKJIHG-token"
	mcpTestHeader   = "hdr-value-0123456789abcdef"
)

func newAIMCPTestAPI(t *testing.T) (*apiv1.API, *aiAuditStore) {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	enc, err := crypto.NewEncryptor("test-key-for-ai-mcp")
	require.NoError(t, err)
	secrets, err := persiststore.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)
	serverStore, err := persiststore.NewAIMCPServerStore(backend.Collection("ai_mcp_servers"))
	require.NoError(t, err)
	service, err := aiendpoint.NewMCPService(serverStore, secrets)
	require.NoError(t, err)
	workspaces, err := persiststore.NewWorkspaceStore(backend.Collection("workspaces"))
	require.NoError(t, err)
	require.NoError(t, workspaces.Create(context.Background(), workspace.NewWorkspace("team", "")))

	auditStore := &aiAuditStore{}
	cfg := &config.Config{}
	cfg.Server.Permissions = map[config.Permission]bool{config.PermissionWriteDAGs: true}
	return apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil,
		apiv1.WithAIMCPServerService(service),
		apiv1.WithWorkspaceStore(workspaces),
		apiv1.WithAuditService(audit.New(auditStore)),
	), auditStore
}

func mcpCreateBody(name, scope string) apigen.CreateAIMCPServerRequest {
	body := apigen.CreateAIMCPServerRequest{
		Name:  apigen.AIMCPServerName(name),
		Url:   "https://docs.example.com/mcp",
		Scope: apigen.AIEndpointScope(scope),
		Allow: apigen.AIMCPServerAllow{Dags: []string{"fix-bug", "review"}},
		Token: new(mcpTestToken),
	}
	if scope == "global" {
		body.Allow.Workspaces = &[]string{"*"}
	}
	return body
}

func TestAIMCPServersAPI_CreateListNeverReturnTheCredentials(t *testing.T) {
	ctx := context.Background()
	api, auditStore := newAIMCPTestAPI(t)

	body := mcpCreateBody("docs", "global")
	body.Token = nil
	body.Headers = &map[string]string{"X-Api-Key": mcpTestHeader}
	resp, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: &body})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateAIMCPServer201JSONResponse)
	require.True(t, ok, "%T", resp)

	assert.Equal(t, "docs", string(created.Name))
	assert.Equal(t, apigen.AIEndpointScope("global"), created.Scope)
	assert.Nil(t, created.Token)
	require.NotNil(t, created.Headers)
	require.Len(t, *created.Headers, 1)
	header := (*created.Headers)[0]
	prefix, fingerprint := aiendpoint.KeyDisplay(mcpTestHeader)
	assert.Equal(t, "x-api-key", header.Name, "the name is in lower case")
	assert.Equal(t, fingerprint, header.KeyFingerprint)
	require.NotNil(t, header.KeyPrefix)
	assert.Equal(t, prefix, *header.KeyPrefix)
	assert.NotContains(t, mustJSON(t, created), mcpTestHeader)

	listResp, err := api.ListAIMCPServers(ctx, apigen.ListAIMCPServersRequestObject{})
	require.NoError(t, err)
	list, ok := listResp.(apigen.ListAIMCPServers200JSONResponse)
	require.True(t, ok)
	require.Len(t, list.Servers, 1)
	assert.NotContains(t, mustJSON(t, list), mcpTestHeader)

	// The audit line has the action, the name, the scope and the fingerprint.
	require.Len(t, auditStore.entries, 1)
	entry := auditStore.entries[0]
	assert.Equal(t, "ai_mcp_server_create", entry.Action)
	assert.Contains(t, entry.Details, `"name":"docs"`)
	assert.Contains(t, entry.Details, fingerprint)
	assert.NotContains(t, entry.Details, mcpTestHeader)
	assert.NotContains(t, entry.Details, prefix+`"`, "the audit line has no prefix of a value")
}

func TestAIMCPServersAPI_TokenRotationIsAudited(t *testing.T) {
	ctx := context.Background()
	api, auditStore := newAIMCPTestAPI(t)
	body := mcpCreateBody("docs", "global")
	createdResp, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: &body})
	require.NoError(t, err)
	created := createdResp.(apigen.CreateAIMCPServer201JSONResponse)
	require.NotNil(t, created.Token)
	assert.NotContains(t, mustJSON(t, created), mcpTestToken)

	resp, err := api.SetAIMCPServerCredentials(ctx, apigen.SetAIMCPServerCredentialsRequestObject{
		Name: "docs",
		Body: &apigen.SetAIMCPServerCredentialsRequest{Token: new(mcpTestTokenTwo)},
	})
	require.NoError(t, err)
	rotated, ok := resp.(apigen.SetAIMCPServerCredentials200JSONResponse)
	require.True(t, ok, "%T", resp)
	assert.NotEqual(t, created.Token.KeyFingerprint, rotated.Token.KeyFingerprint)
	assert.NotContains(t, mustJSON(t, rotated), mcpTestTokenTwo)

	require.Len(t, auditStore.entries, 2)
	entry := auditStore.entries[1]
	assert.Equal(t, "ai_mcp_server_key_rotate", entry.Action)
	assert.Contains(t, entry.Details, rotated.Token.KeyFingerprint)
	assert.NotContains(t, entry.Details, mcpTestTokenTwo)

	// A token with a header authorization contradicts itself.
	bad, err := api.SetAIMCPServerCredentials(ctx, apigen.SetAIMCPServerCredentialsRequestObject{
		Name: "docs",
		Body: &apigen.SetAIMCPServerCredentialsRequest{
			Token: new(mcpTestTokenTwo), Headers: &map[string]string{"Authorization": "x"},
		},
	})
	require.NoError(t, err)
	badResp, ok := bad.(apigen.SetAIMCPServerCredentials400JSONResponse)
	require.True(t, ok, "%T", bad)
	assert.NotContains(t, badResp.Message, mcpTestTokenTwo)
	missing, err := api.SetAIMCPServerCredentials(ctx, apigen.SetAIMCPServerCredentialsRequestObject{
		Name: "nope", Body: &apigen.SetAIMCPServerCredentialsRequest{Token: new(mcpTestTokenTwo)},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.SetAIMCPServerCredentials404JSONResponse{}, missing)
	assert.Len(t, auditStore.entries, 2, "a failed call writes no audit line")

	// A request with no credential removes them.
	cleared, err := api.SetAIMCPServerCredentials(ctx, apigen.SetAIMCPServerCredentialsRequestObject{
		Name: "docs", Body: &apigen.SetAIMCPServerCredentialsRequest{},
	})
	require.NoError(t, err)
	clearedResp, ok := cleared.(apigen.SetAIMCPServerCredentials200JSONResponse)
	require.True(t, ok)
	assert.Nil(t, clearedResp.Token)
	assert.Nil(t, clearedResp.Headers)
}

func TestAIMCPServersAPI_ScopeAndErrors(t *testing.T) {
	ctx := context.Background()
	api, auditStore := newAIMCPTestAPI(t)

	_, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: new(mcpCreateBody("docs", "global"))})
	require.NoError(t, err)
	resp, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: new(mcpCreateBody("docs", "workspace:team"))})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateAIMCPServer201JSONResponse)
	require.True(t, ok, "%T", resp)
	assert.Nil(t, created.Allow.Workspaces)

	dup, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: new(mcpCreateBody("docs", "workspace:team"))})
	require.NoError(t, err)
	assert.IsType(t, apigen.CreateAIMCPServer409JSONResponse{}, dup)
	unknown, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: new(mcpCreateBody("other", "workspace:none"))})
	require.NoError(t, err)
	assert.IsType(t, apigen.CreateAIMCPServer404JSONResponse{}, unknown)

	// Invalid values give 400. The message never holds a credential value.
	for name, mutate := range map[string]func(*apigen.CreateAIMCPServerRequest){
		"scope":            func(b *apigen.CreateAIMCPServerRequest) { b.Scope = "team" },
		"url":              func(b *apigen.CreateAIMCPServerRequest) { b.Url = "ftp://x" },
		"user information": func(b *apigen.CreateAIMCPServerRequest) { b.Url = "https://user:pw@x.example.com/mcp" },
		"fragment":         func(b *apigen.CreateAIMCPServerRequest) { b.Url = "https://x.example.com/mcp#f" },
		"name dagu":        func(b *apigen.CreateAIMCPServerRequest) { b.Name = "dagu" },
		"name case":        func(b *apigen.CreateAIMCPServerRequest) { b.Name = "Docs" },
		"empty allow":      func(b *apigen.CreateAIMCPServerRequest) { b.Allow.Dags = nil },
		"token and header": func(b *apigen.CreateAIMCPServerRequest) {
			b.Headers = &map[string]string{"Authorization": "x"}
		},
		"reserved header": func(b *apigen.CreateAIMCPServerRequest) {
			b.Token = nil
			b.Headers = &map[string]string{"Content-Type": "x"}
		},
		"empty value": func(b *apigen.CreateAIMCPServerRequest) {
			b.Token = nil
			b.Headers = &map[string]string{"X-Key": ""}
		},
		"control character": func(b *apigen.CreateAIMCPServerRequest) { b.Token = new("bad\ntoken") },
	} {
		body := mcpCreateBody("fresh", "global")
		mutate(&body)
		resp, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: &body})
		require.NoError(t, err, name)
		bad, ok := resp.(apigen.CreateAIMCPServer400JSONResponse)
		require.True(t, ok, "%s: %T", name, resp)
		assert.NotContains(t, bad.Message, mcpTestToken, name)
		assert.NotContains(t, bad.Message, "pw@", name)
	}
	tooMany := mcpCreateBody("fresh", "global")
	tooMany.Token = nil
	headers := map[string]string{}
	for i := range aiendpoint.MaxMCPHeaders + 1 {
		headers["X-H"+string(rune('a'+i))] = "value"
	}
	tooMany.Headers = &headers
	tooManyResp, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: &tooMany})
	require.NoError(t, err)
	assert.IsType(t, apigen.CreateAIMCPServer400JSONResponse{}, tooManyResp)
	assert.Len(t, auditStore.entries, 2, "a failed call writes no audit line")

	// A change, then a delete. The scope comes from the query workspace.
	team := "team"
	upd, err := api.UpdateAIMCPServer(ctx, apigen.UpdateAIMCPServerRequestObject{
		Name: "docs", Params: apigen.UpdateAIMCPServerParams{Workspace: &team},
		Body: &apigen.UpdateAIMCPServerRequest{Url: new("https://docs2.example.com/mcp")},
	})
	require.NoError(t, err)
	updated, ok := upd.(apigen.UpdateAIMCPServer200JSONResponse)
	require.True(t, ok, "%T", upd)
	assert.Equal(t, "https://docs2.example.com/mcp", updated.Url)
	assert.Equal(t, apigen.AIEndpointScope("workspace:team"), updated.Scope)
	assert.Equal(t, "ai_mcp_server_update", auditStore.entries[len(auditStore.entries)-1].Action)

	del, err := api.DeleteAIMCPServer(ctx, apigen.DeleteAIMCPServerRequestObject{
		Name: "docs", Params: apigen.DeleteAIMCPServerParams{Workspace: &team},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteAIMCPServer204Response{}, del)
	assert.Equal(t, "ai_mcp_server_delete", auditStore.entries[len(auditStore.entries)-1].Action)
	again, err := api.DeleteAIMCPServer(ctx, apigen.DeleteAIMCPServerRequestObject{
		Name: "docs", Params: apigen.DeleteAIMCPServerParams{Workspace: &team},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteAIMCPServer404JSONResponse{}, again)
}

func TestAIMCPServersAPI_ChoicesFollowTheUseRule(t *testing.T) {
	ctx := context.Background()
	api, _ := newAIMCPTestAPI(t)
	for _, body := range []apigen.CreateAIMCPServerRequest{
		mcpCreateBody("docs", "global"),
		mcpCreateBody("wiki", "workspace:team"),
	} {
		_, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: &body})
		require.NoError(t, err)
	}
	other := mcpCreateBody("secret-docs", "global")
	other.Allow.Dags = []string{"review"}
	_, err := api.CreateAIMCPServer(ctx, apigen.CreateAIMCPServerRequestObject{Body: &other})
	require.NoError(t, err)

	choicesFor := func(dag string, workspaceName *string) []apigen.AIMCPServerChoice {
		resp, err := api.ListAIMCPServerChoices(ctx, apigen.ListAIMCPServerChoicesRequestObject{
			Params: apigen.ListAIMCPServerChoicesParams{Dag: dag, Workspace: workspaceName},
		})
		require.NoError(t, err)
		list, ok := resp.(apigen.ListAIMCPServerChoices200JSONResponse)
		require.True(t, ok, "%T", resp)
		assert.NotContains(t, mustJSON(t, list), "keyFingerprint")
		assert.NotContains(t, mustJSON(t, list), "docs.example.com", "the choices have no URL")
		return list.Choices
	}
	team := "team"
	assert.Len(t, choicesFor("fix-bug", nil), 1)
	withTeam := choicesFor("fix-bug", &team)
	require.Len(t, withTeam, 2)
	assert.Equal(t, apigen.AIEndpointScope("workspace:team"), withTeam[1].Scope)
	assert.Len(t, choicesFor("unknown-dag", nil), 0)
}

// The routes need the role admin. The route of choices needs a role that edits DAGs.
func TestAIMCPServersAPI_RolesOverHTTP(t *testing.T) {
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(t, server, adminToken, "mcp-manager", "managerpass1", apigen.UserRoleManager)
	viewerToken := createRuntimeProfileUserToken(t, server, adminToken, "mcp-viewer", "viewerpass1", apigen.UserRoleViewer)
	developerToken := createRuntimeProfileUserToken(t, server, adminToken, "mcp-developer", "developerpass1", apigen.UserRoleDeveloper)

	body := mcpCreateBody("docs", "global")

	for _, token := range []string{managerToken, developerToken, viewerToken} {
		server.Client().Post("/api/v1/ai/mcp-servers", body).WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Get("/api/v1/ai/mcp-servers").WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Patch("/api/v1/ai/mcp-servers/docs", apigen.UpdateAIMCPServerRequest{}).WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Put("/api/v1/ai/mcp-servers/docs/credentials", apigen.SetAIMCPServerCredentialsRequest{Token: new(mcpTestTokenTwo)}).WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Delete("/api/v1/ai/mcp-servers/docs").WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
	}
	server.Client().Get("/api/v1/ai/mcp-servers").ExpectStatus(http.StatusUnauthorized).Send(t)
	server.Client().Get("/api/v1/ai/mcp-server-choices?dag=fix-bug").ExpectStatus(http.StatusUnauthorized).Send(t)

	// The admin creates the entry. The raw response never holds the token.
	created := server.Client().Post("/api/v1/ai/mcp-servers", body).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	assert.NotContains(t, created.Body, mcpTestToken)
	assert.Contains(t, created.Body, `"keyFingerprint"`)
	listed := server.Client().Get("/api/v1/ai/mcp-servers").WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	assert.NotContains(t, listed.Body, mcpTestToken)
	rotated := server.Client().Put("/api/v1/ai/mcp-servers/docs/credentials", apigen.SetAIMCPServerCredentialsRequest{Token: new(mcpTestTokenTwo)}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	assert.NotContains(t, rotated.Body, mcpTestTokenTwo)

	choices := server.Client().Get("/api/v1/ai/mcp-server-choices?dag=fix-bug").WithBearerToken(developerToken).ExpectStatus(http.StatusOK).Send(t)
	assert.Contains(t, choices.Body, `"docs"`)
	assert.NotContains(t, choices.Body, "docs.example.com")
	server.Client().Get("/api/v1/ai/mcp-server-choices?dag=fix-bug").WithBearerToken(viewerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Delete("/api/v1/ai/mcp-servers/docs").WithBearerToken(adminToken).ExpectStatus(http.StatusNoContent).Send(t)
	server.Client().Delete("/api/v1/ai/mcp-servers/docs").WithBearerToken(adminToken).ExpectStatus(http.StatusNotFound).Send(t)
}

// A credential is a managed secret, and only an administrator changes it. A
// manager can use the secret routes for other secrets, not for the refs
// ai/mcp/<name>/<header>.
func TestAIMCPServersAPI_SecretRoutesProtectTheCredentialSecrets(t *testing.T) {
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(t, server, adminToken, "mcp-secret-manager", "managerpass1", apigen.UserRoleManager)

	created := server.Client().Post("/api/v1/ai/mcp-servers", mcpCreateBody("docs", "global")).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	assert.NotContains(t, created.Body, mcpTestToken)

	var secrets apigen.ListSecrets200JSONResponse
	server.Client().Get("/api/v1/secrets").WithBearerToken(managerToken).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &secrets)
	var secretID string
	for _, sec := range secrets.Secrets {
		if sec.Ref == "ai/mcp/docs/authorization" {
			secretID = sec.Id
		}
	}
	require.NotEmpty(t, secretID, "the secret routes list the metadata of the credential secret")

	value := "replacement"
	server.Client().Post("/api/v1/secrets", apigen.CreateSecretRequest{
		Ref: "ai/mcp/other/token", ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged, Value: &value,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Post("/api/v1/secrets/"+secretID+"/versions", apigen.WriteSecretVersionRequest{Value: &value}).
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Delete("/api/v1/secrets/" + secretID).
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	listed := server.Client().Get("/api/v1/ai/mcp-servers").WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	_, fingerprint := aiendpoint.KeyDisplay(mcpTestToken)
	assert.Contains(t, listed.Body, fingerprint)
}
