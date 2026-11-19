// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"sync"
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
	aiTestKey    = "sk-ant-api03-0123456789abcdefghij"
	aiTestKeyTwo = "sk-ant-api03-ZYXWVUTSRQPONMLKJIHG"
)

type aiAuditStore struct {
	mu      sync.Mutex
	entries []*audit.Entry
}

func (s *aiAuditStore) Append(_ context.Context, entry *audit.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
	return nil
}

func (s *aiAuditStore) Query(_ context.Context, _ audit.QueryFilter) (*audit.QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &audit.QueryResult{Entries: s.entries, Total: len(s.entries)}, nil
}

func newAIEndpointsTestAPI(t *testing.T) (*apiv1.API, *aiAuditStore) {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	enc, err := crypto.NewEncryptor("test-key-for-ai-endpoints")
	require.NoError(t, err)
	secrets, err := persiststore.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)
	endpointStore, err := persiststore.NewAIEndpointStore(backend.Collection("ai_endpoints"))
	require.NoError(t, err)
	service, err := aiendpoint.NewService(endpointStore, secrets)
	require.NoError(t, err)
	workspaces, err := persiststore.NewWorkspaceStore(backend.Collection("workspaces"))
	require.NoError(t, err)
	require.NoError(t, workspaces.Create(context.Background(), workspace.NewWorkspace("team", "")))

	auditStore := &aiAuditStore{}
	cfg := &config.Config{}
	cfg.Server.Permissions = map[config.Permission]bool{config.PermissionWriteDAGs: true}
	return apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil,
		apiv1.WithAIEndpointService(service),
		apiv1.WithWorkspaceStore(workspaces),
		apiv1.WithAuditService(audit.New(auditStore)),
	), auditStore
}

func aiCreateBody(name, scope string) apigen.CreateAIEndpointRequest {
	body := apigen.CreateAIEndpointRequest{
		Name:     apigen.AIEndpointName(name),
		Preset:   "anthropic",
		Protocol: apigen.AIEndpointProtocolAnthropic,
		Scope:    apigen.AIEndpointScope(scope),
		Allow:    apigen.AIEndpointAllow{Dags: []string{"fix-bug", "review"}},
		Key:      new(aiTestKey),
	}
	if scope == "global" {
		body.Allow.Workspaces = &[]string{"*"}
	}
	return body
}

func TestAIEndpointsAPI_CreateListNeverReturnTheKey(t *testing.T) {
	ctx := context.Background()
	api, auditStore := newAIEndpointsTestAPI(t)

	resp, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: new(aiCreateBody("cle1", "global"))})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateAIEndpoint201JSONResponse)
	require.True(t, ok, "%T", resp)

	assert.Equal(t, "cle1", string(created.Name))
	assert.Equal(t, "https://api.anthropic.com", created.BaseUrl)
	assert.Equal(t, apigen.AIEndpointAuthXApiKey, created.Auth)
	assert.Equal(t, apigen.AIEndpointScope("global"), created.Scope)
	prefix, fingerprint := aiendpoint.KeyDisplay(aiTestKey)
	require.NotNil(t, created.KeyPrefix)
	assert.Equal(t, prefix, *created.KeyPrefix)
	require.NotNil(t, created.KeyFingerprint)
	assert.Equal(t, fingerprint, *created.KeyFingerprint)
	require.NotNil(t, created.KeyUpdatedAt)
	assert.NotContains(t, mustJSON(t, created), aiTestKey)

	listResp, err := api.ListAIEndpoints(ctx, apigen.ListAIEndpointsRequestObject{})
	require.NoError(t, err)
	list, ok := listResp.(apigen.ListAIEndpoints200JSONResponse)
	require.True(t, ok)
	require.Len(t, list.Endpoints, 1)
	assert.NotContains(t, mustJSON(t, list), aiTestKey)
	assert.Equal(t, fingerprint, *list.Endpoints[0].KeyFingerprint)

	// The audit line has the action, the name, the scope and the fingerprint.
	require.Len(t, auditStore.entries, 1)
	entry := auditStore.entries[0]
	assert.Equal(t, "ai_endpoint_create", entry.Action)
	assert.Contains(t, entry.Details, `"name":"cle1"`)
	assert.Contains(t, entry.Details, `"scope":"global"`)
	assert.Contains(t, entry.Details, fingerprint)
	assert.NotContains(t, entry.Details, aiTestKey)
	assert.NotContains(t, entry.Details, prefix+`"`, "the audit line has no prefix of the key")
}

func TestAIEndpointsAPI_RotateChangesTheFingerprintAndIsAudited(t *testing.T) {
	ctx := context.Background()
	api, auditStore := newAIEndpointsTestAPI(t)
	createdResp, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: new(aiCreateBody("cle1", "global"))})
	require.NoError(t, err)
	created := createdResp.(apigen.CreateAIEndpoint201JSONResponse)

	resp, err := api.SetAIEndpointKey(ctx, apigen.SetAIEndpointKeyRequestObject{
		Name: "cle1",
		Body: &apigen.SetAIEndpointKeyRequest{Key: new(aiTestKeyTwo)},
	})
	require.NoError(t, err)
	rotated, ok := resp.(apigen.SetAIEndpointKey200JSONResponse)
	require.True(t, ok, "%T", resp)
	assert.NotEqual(t, *created.KeyFingerprint, *rotated.KeyFingerprint)
	assert.NotContains(t, mustJSON(t, rotated), aiTestKeyTwo)

	require.Len(t, auditStore.entries, 2)
	entry := auditStore.entries[1]
	assert.Equal(t, "ai_endpoint_key_rotate", entry.Action)
	assert.Contains(t, entry.Details, *rotated.KeyFingerprint)
	assert.NotContains(t, entry.Details, aiTestKeyTwo)

	// An invalid key and an unknown endpoint.
	bad, err := api.SetAIEndpointKey(ctx, apigen.SetAIEndpointKeyRequestObject{
		Name: "cle1", Body: &apigen.SetAIEndpointKeyRequest{Key: new("bad\nkey")},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.SetAIEndpointKey400JSONResponse{}, bad)
	missing, err := api.SetAIEndpointKey(ctx, apigen.SetAIEndpointKeyRequestObject{
		Name: "nope", Body: &apigen.SetAIEndpointKeyRequest{Key: new(aiTestKeyTwo)},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.SetAIEndpointKey404JSONResponse{}, missing)
	noKey, err := api.SetAIEndpointKey(ctx, apigen.SetAIEndpointKeyRequestObject{
		Name: "cle1", Body: &apigen.SetAIEndpointKeyRequest{},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.SetAIEndpointKey400JSONResponse{}, noKey)
	assert.Len(t, auditStore.entries, 2, "a failed call writes no audit line")
}

func TestAIEndpointsAPI_ScopeAndErrors(t *testing.T) {
	ctx := context.Background()
	api, auditStore := newAIEndpointsTestAPI(t)

	// A workspace endpoint, with the same name as a global one.
	_, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: new(aiCreateBody("cle1", "global"))})
	require.NoError(t, err)
	resp, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: new(aiCreateBody("cle1", "workspace:team"))})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateAIEndpoint201JSONResponse)
	require.True(t, ok, "%T", resp)
	assert.Equal(t, apigen.AIEndpointScope("workspace:team"), created.Scope)
	assert.Nil(t, created.Allow.Workspaces)

	// A duplicate in the same scope.
	dup, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: new(aiCreateBody("cle1", "workspace:team"))})
	require.NoError(t, err)
	assert.IsType(t, apigen.CreateAIEndpoint409JSONResponse{}, dup)

	// An unknown workspace.
	unknown, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: new(aiCreateBody("cle2", "workspace:other"))})
	require.NoError(t, err)
	assert.IsType(t, apigen.CreateAIEndpoint404JSONResponse{}, unknown)

	// Invalid values give 400 and the message names the field. It never holds the key.
	for name, mutate := range map[string]func(*apigen.CreateAIEndpointRequest){
		"scope":    func(b *apigen.CreateAIEndpointRequest) { b.Scope = "team" },
		"base url": func(b *apigen.CreateAIEndpointRequest) { b.BaseUrl = new("http://api.example.com") },
		"preset":   func(b *apigen.CreateAIEndpointRequest) { b.Preset = "nope" },
		"key":      func(b *apigen.CreateAIEndpointRequest) { b.Key = new("a\tb") },
		"allow":    func(b *apigen.CreateAIEndpointRequest) { b.Allow.Dags = nil },
	} {
		body := aiCreateBody("cle3", "global")
		mutate(&body)
		bad, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: &body})
		require.NoError(t, err, name)
		badResp, ok := bad.(apigen.CreateAIEndpoint400JSONResponse)
		require.True(t, ok, "%s: %T", name, bad)
		assert.NotContains(t, badResp.Message, aiTestKey, name)
	}

	// The routes with a name read the scope from the query workspace.
	team := "team"
	deleted, err := api.DeleteAIEndpoint(ctx, apigen.DeleteAIEndpointRequestObject{
		Name: "cle1", Params: apigen.DeleteAIEndpointParams{Workspace: &team},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteAIEndpoint204Response{}, deleted)
	again, err := api.DeleteAIEndpoint(ctx, apigen.DeleteAIEndpointRequestObject{
		Name: "cle1", Params: apigen.DeleteAIEndpointParams{Workspace: &team},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteAIEndpoint404JSONResponse{}, again)
	badScope := "a/b"
	invalid, err := api.DeleteAIEndpoint(ctx, apigen.DeleteAIEndpointRequestObject{
		Name: "cle1", Params: apigen.DeleteAIEndpointParams{Workspace: &badScope},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteAIEndpoint400JSONResponse{}, invalid)

	// The global endpoint stays, and the delete is audited with its scope.
	listResp, err := api.ListAIEndpoints(ctx, apigen.ListAIEndpointsRequestObject{})
	require.NoError(t, err)
	list := listResp.(apigen.ListAIEndpoints200JSONResponse)
	require.Len(t, list.Endpoints, 1)
	assert.Equal(t, apigen.AIEndpointScope("global"), list.Endpoints[0].Scope)
	last := auditStore.entries[len(auditStore.entries)-1]
	assert.Equal(t, "ai_endpoint_delete", last.Action)
	assert.Contains(t, last.Details, `"scope":"workspace:team"`)
	for _, entry := range auditStore.entries {
		assert.NotContains(t, entry.Details, aiTestKey)
	}
}

func TestAIEndpointsAPI_UpdateChangesFieldsAndKeepsTheKey(t *testing.T) {
	ctx := context.Background()
	api, auditStore := newAIEndpointsTestAPI(t)
	createdResp, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: new(aiCreateBody("cle1", "global"))})
	require.NoError(t, err)
	created := createdResp.(apigen.CreateAIEndpoint201JSONResponse)

	resp, err := api.UpdateAIEndpoint(ctx, apigen.UpdateAIEndpointRequestObject{
		Name: "cle1",
		Body: &apigen.UpdateAIEndpointRequest{
			Description: new("Main key"),
			Allow:       &apigen.AIEndpointAllow{Dags: []string{"*"}, Workspaces: &[]string{"team"}},
		},
	})
	require.NoError(t, err)
	updated, ok := resp.(apigen.UpdateAIEndpoint200JSONResponse)
	require.True(t, ok, "%T", resp)
	assert.Equal(t, "Main key", *updated.Description)
	assert.Equal(t, []string{"*"}, updated.Allow.Dags)
	assert.Equal(t, *created.KeyFingerprint, *updated.KeyFingerprint)
	assert.Equal(t, "ai_endpoint_update", auditStore.entries[len(auditStore.entries)-1].Action)

	bad, err := api.UpdateAIEndpoint(ctx, apigen.UpdateAIEndpointRequestObject{
		Name: "cle1", Body: &apigen.UpdateAIEndpointRequest{BaseUrl: new("ftp://x.example.com")},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.UpdateAIEndpoint400JSONResponse{}, bad)
	missing, err := api.UpdateAIEndpoint(ctx, apigen.UpdateAIEndpointRequestObject{
		Name: "nope", Body: &apigen.UpdateAIEndpointRequest{},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.UpdateAIEndpoint404JSONResponse{}, missing)
}

func TestAIEndpointsAPI_ChoicesFollowTheUseRule(t *testing.T) {
	ctx := context.Background()
	api, _ := newAIEndpointsTestAPI(t)
	for _, body := range []apigen.CreateAIEndpointRequest{
		aiCreateBody("cle1", "global"),
		aiCreateBody("cle2", "workspace:team"),
	} {
		_, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: &body})
		require.NoError(t, err)
	}
	other := aiCreateBody("cle3", "global")
	other.Allow.Dags = []string{"review"}
	_, err := api.CreateAIEndpoint(ctx, apigen.CreateAIEndpointRequestObject{Body: &other})
	require.NoError(t, err)

	choicesFor := func(dag string, workspaceName *string) []apigen.AIEndpointChoice {
		resp, err := api.ListAIEndpointChoices(ctx, apigen.ListAIEndpointChoicesRequestObject{
			Params: apigen.ListAIEndpointChoicesParams{Dag: dag, Workspace: workspaceName},
		})
		require.NoError(t, err)
		list, ok := resp.(apigen.ListAIEndpointChoices200JSONResponse)
		require.True(t, ok, "%T", resp)
		assert.NotContains(t, mustJSON(t, list), "baseUrl")
		assert.NotContains(t, mustJSON(t, list), "keyFingerprint")
		return list.Choices
	}
	team := "team"
	assert.Len(t, choicesFor("fix-bug", nil), 1)
	withTeam := choicesFor("fix-bug", &team)
	require.Len(t, withTeam, 2)
	assert.Equal(t, apigen.AIEndpointScope("workspace:team"), withTeam[1].Scope)
	assert.Equal(t, apigen.AIEndpointProtocolAnthropic, withTeam[1].Protocol)
	assert.Equal(t, "anthropic", withTeam[1].Preset)
	assert.Len(t, choicesFor("unknown-dag", nil), 0)
}

// The routes need the role admin. The route of choices needs a role that edits DAGs.
func TestAIEndpointsAPI_RolesOverHTTP(t *testing.T) {
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(t, server, adminToken, "ai-manager", "managerpass1", apigen.UserRoleManager)
	viewerToken := createRuntimeProfileUserToken(t, server, adminToken, "ai-viewer", "viewerpass1", apigen.UserRoleViewer)
	developerToken := createRuntimeProfileUserToken(t, server, adminToken, "ai-developer", "developerpass1", apigen.UserRoleDeveloper)

	body := aiNoListBody("cle1")

	// A user with another role gets 403 on each admin route, and no endpoint is created.
	for _, token := range []string{managerToken, developerToken, viewerToken} {
		server.Client().Post("/api/v1/ai/endpoints", body).WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Get("/api/v1/ai/endpoints").WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Get("/api/v1/ai/endpoint-presets").WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Patch("/api/v1/ai/endpoints/cle1", apigen.UpdateAIEndpointRequest{}).WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Put("/api/v1/ai/endpoints/cle1/key", apigen.SetAIEndpointKeyRequest{Key: new(aiTestKeyTwo)}).WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Delete("/api/v1/ai/endpoints/cle1").WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
	}
	server.Client().Get("/api/v1/ai/endpoints").ExpectStatus(http.StatusUnauthorized).Send(t)
	server.Client().Get("/api/v1/ai/endpoint-presets").ExpectStatus(http.StatusUnauthorized).Send(t)
	server.Client().Get("/api/v1/ai/endpoint-presets").WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)

	// The admin creates the endpoint. The raw response never holds the key.
	created := server.Client().Post("/api/v1/ai/endpoints", body).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	assert.NotContains(t, created.Body, aiTestKey)
	assert.Contains(t, created.Body, `"keyFingerprint"`)
	assert.Contains(t, created.Body, `"keyPrefix":"sk-`)
	listed := server.Client().Get("/api/v1/ai/endpoints").WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	assert.NotContains(t, listed.Body, aiTestKey)
	rotated := server.Client().Put("/api/v1/ai/endpoints/cle1/key", apigen.SetAIEndpointKeyRequest{Key: new(aiTestKeyTwo)}).
		WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	assert.NotContains(t, rotated.Body, aiTestKeyTwo)

	// The developer reads the choices of a DAG. The viewer cannot.
	choices := server.Client().Get("/api/v1/ai/endpoint-choices?dag=fix-bug").WithBearerToken(developerToken).ExpectStatus(http.StatusOK).Send(t)
	assert.Contains(t, choices.Body, `"cle1"`)
	assert.NotContains(t, choices.Body, "keyFingerprint")
	server.Client().Get("/api/v1/ai/endpoint-choices?dag=fix-bug").WithBearerToken(viewerToken).ExpectStatus(http.StatusForbidden).Send(t)

	server.Client().Delete("/api/v1/ai/endpoints/cle1").WithBearerToken(adminToken).ExpectStatus(http.StatusNoContent).Send(t)
	server.Client().Delete("/api/v1/ai/endpoints/cle1").WithBearerToken(adminToken).ExpectStatus(http.StatusNotFound).Send(t)
}

// The key of an endpoint is a managed secret, and only an administrator changes it
// (Spec 094, "Trust"). A manager can use the secret routes for other secrets, but
// not for the secrets of the refs ai/endpoint/<name>.
func TestAIEndpointsAPI_SecretRoutesProtectTheKeySecrets(t *testing.T) {
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(t, server, adminToken, "ai-secret-manager", "managerpass1", apigen.UserRoleManager)

	created := server.Client().Post("/api/v1/ai/endpoints", aiNoListBody("cle1")).
		WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)
	assert.NotContains(t, created.Body, aiTestKey)

	var secrets apigen.ListSecrets200JSONResponse
	server.Client().Get("/api/v1/secrets").WithBearerToken(managerToken).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &secrets)
	var keySecretID string
	for _, sec := range secrets.Secrets {
		if sec.Ref == "ai/endpoint/cle1" {
			keySecretID = sec.Id
		}
	}
	require.NotEmpty(t, keySecretID, "the secret routes list the metadata of the key secret")

	value := "replacement"
	server.Client().Post("/api/v1/secrets", apigen.CreateSecretRequest{
		Ref: "ai/endpoint/other", ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged, Value: &value,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Post("/api/v1/secrets", apigen.CreateSecretRequest{
		Ref: "prod/db-password", ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged, Value: &value,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusCreated).Send(t)

	server.Client().Post("/api/v1/secrets/"+keySecretID+"/versions", apigen.WriteSecretVersionRequest{Value: &value}).
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Post("/api/v1/secrets/"+keySecretID+"/disable", nil).
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Post("/api/v1/secrets/"+keySecretID+"/enable", nil).
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	// The whole prefix "ai/" is reserved, not only the two refs of Spec 093.
	server.Client().Post("/api/v1/secrets", apigen.CreateSecretRequest{
		Ref: "ai/anything", ProviderType: apigen.CreateSecretRequestProviderTypeDaguManaged, Value: &value,
	}).WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Delete("/api/v1/secrets/" + keySecretID).
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Patch("/api/v1/secrets/"+keySecretID, apigen.UpdateSecretRequest{Description: new("x")}).
		WithBearerToken(managerToken).ExpectStatus(http.StatusForbidden).Send(t)

	// The endpoint still has its key.
	listed := server.Client().Get("/api/v1/ai/endpoints").WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	_, fingerprint := aiendpoint.KeyDisplay(aiTestKey)
	assert.Contains(t, listed.Body, fingerprint)
}

// The route serves the table of presets of the server as the table holds it: one
// entry for each preset, the rows of its protocols, and the preset custom last.
func TestAIEndpointsAPI_Presets(t *testing.T) {
	api, _ := newAIEndpointsTestAPI(t)

	resp, err := api.ListAIEndpointPresets(context.Background(), apigen.ListAIEndpointPresetsRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(apigen.ListAIEndpointPresets200JSONResponse)
	require.True(t, ok, "%T", resp)

	byID := map[string]apigen.AIEndpointPreset{}
	var ids []string
	for _, preset := range list.Presets {
		byID[preset.Id] = preset
		ids = append(ids, preset.Id)
	}
	assert.Equal(t, []string{
		"anthropic", "openai", "google-gemini", "mistral", "deepseek", "xai", "groq",
		"openrouter", "zai", "together", "ollama", "vllm", "litellm", "custom",
	}, ids)

	anthropic := byID["anthropic"]
	assert.Equal(t, "Anthropic", anthropic.Label)
	assert.True(t, anthropic.NeedsKey)
	require.Len(t, anthropic.Protocols, 1)
	row := anthropic.Protocols[0]
	assert.Equal(t, apigen.AIEndpointProtocolAnthropic, row.Protocol)
	assert.Equal(t, "https://api.anthropic.com", *row.BaseUrl)
	assert.Equal(t, apigen.AIEndpointAuthXApiKey, *row.Auth)
	assert.Equal(t, "https://api.anthropic.com/v1/models", *row.ModelsUrl)
	assert.Equal(t, "anthropic", row.ChatType)
	assert.Equal(t, []apigen.AIEndpointConsumer{apigen.AIEndpointConsumerClaude}, row.Consumers)

	// A preset with two protocols has two rows.
	require.Len(t, byID["openrouter"].Protocols, 2)
	assert.Equal(t, "openrouter", byID["openrouter"].Protocols[0].ChatType)

	// A local model needs no key. A row with no base URL has no value for it.
	assert.False(t, byID["ollama"].NeedsKey)
	assert.Nil(t, byID["vllm"].Protocols[0].BaseUrl)
	assert.Equal(t, "{base_url}/models", *byID["vllm"].Protocols[0].ModelsUrl)

	// Values that the spec marks "To verify" are served as the table holds them.
	assert.Nil(t, byID["zai"].Protocols[0].ModelsUrl)

	// The native Gemini API is not a protocol: the row serves chat.completion only.
	assert.Equal(t, []apigen.AIEndpointConsumer{apigen.AIEndpointConsumerChatCompletion}, byID["google-gemini"].Protocols[0].Consumers)

	custom := byID["custom"]
	require.Len(t, custom.Protocols, 2)
	assert.Nil(t, custom.Protocols[0].BaseUrl)
	assert.Nil(t, custom.Protocols[0].Auth)
}
