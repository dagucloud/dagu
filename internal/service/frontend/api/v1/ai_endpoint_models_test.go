// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

// aiModelsProvider is a fake models API. It records the Authorization header of
// each call and answers with the current status and body.
type aiModelsProvider struct {
	mu     sync.Mutex
	status int
	body   string
	auths  []string
	server *httptest.Server
}

func newAIModelsProvider(t *testing.T) *aiModelsProvider {
	t.Helper()
	p := &aiModelsProvider{status: http.StatusOK, body: `{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"}]}`}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.auths = append(p.auths, r.Header.Get("Authorization"))
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(p.body))
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *aiModelsProvider) set(status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.body = status, body
}

func (p *aiModelsProvider) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.auths...)
}

// newAIModelsTestAPI is newAIEndpointsTestAPI with the models list turned on.
func newAIModelsTestAPI(t *testing.T, provider *aiModelsProvider) (*apiv1.API, *aiAuditStore, *aiendpoint.Service) {
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
	modelsStore, err := persiststore.NewAIEndpointModelsStore(backend.Collection("ai_endpoint_models"))
	require.NoError(t, err)
	reader, err := aiendpoint.NewModelsReader(provider.server.Client(), 2*time.Second)
	require.NoError(t, err)
	require.NoError(t, service.EnableModels(modelsStore, reader))
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
	), auditStore, service
}

// aiCustomBody is a global endpoint of the preset custom that lists its models at the fake provider.
func aiCustomBody(name string, provider *aiModelsProvider, withList bool) apigen.CreateAIEndpointRequest {
	body := apigen.CreateAIEndpointRequest{
		Name:     apigen.AIEndpointName(name),
		Preset:   "custom",
		Protocol: apigen.AIEndpointProtocolOpenai,
		BaseUrl:  new(provider.server.URL),
		Auth:     new(apigen.AIEndpointAuthBearer),
		Scope:    "global",
		Allow:    apigen.AIEndpointAllow{Dags: []string{"fix-bug"}, Workspaces: &[]string{"*"}},
		Key:      new(aiTestKey),
	}
	if withList {
		body.ModelsUrl = new(provider.server.URL + "/v1/models")
	}
	return body
}

// aiNoListBody is a global endpoint that has no models URL, so a test that starts a
// full server never makes the server call a provider.
func aiNoListBody(name string) apigen.CreateAIEndpointRequest {
	return apigen.CreateAIEndpointRequest{
		Name:     apigen.AIEndpointName(name),
		Preset:   "custom",
		Protocol: apigen.AIEndpointProtocolAnthropic,
		BaseUrl:  new("https://models.invalid"),
		Scope:    "global",
		Auth:     new(apigen.AIEndpointAuthXApiKey),
		Allow:    apigen.AIEndpointAllow{Dags: []string{"fix-bug", "review"}, Workspaces: &[]string{"*"}},
		Key:      new(aiTestKey),
	}
}

func aiCreate(t *testing.T, api *apiv1.API, service *aiendpoint.Service, body apigen.CreateAIEndpointRequest) apigen.AIEndpoint {
	t.Helper()
	resp, err := api.CreateAIEndpoint(context.Background(), apigen.CreateAIEndpointRequestObject{Body: &body})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateAIEndpoint201JSONResponse)
	require.True(t, ok, "%T", resp)
	service.WaitModels()
	return apigen.AIEndpoint(created)
}

func getModels(t *testing.T, api *apiv1.API, name string) apigen.AIEndpointModels {
	t.Helper()
	resp, err := api.GetAIEndpointModels(context.Background(), apigen.GetAIEndpointModelsRequestObject{Name: apigen.AIEndpointName(name)})
	require.NoError(t, err)
	got, ok := resp.(apigen.GetAIEndpointModels200JSONResponse)
	require.True(t, ok, "%T", resp)
	return apigen.AIEndpointModels(got)
}

func TestAIModelsAPI_CreateStartsTheFirstReadWithoutWaitingForIt(t *testing.T) {
	provider := newAIModelsProvider(t)
	api, _, service := newAIModelsTestAPI(t, provider)

	created := aiCreate(t, api, service, aiCustomBody("cle1", provider, true))

	// The response of the create has the state of the list. The read ran after it.
	require.NotNil(t, created.Models)
	assert.True(t, created.Models.Supported)

	got := getModels(t, api, "cle1")
	assert.True(t, got.Supported)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, got.Models)
	assert.Equal(t, 2, got.Count)
	require.NotNil(t, got.RefreshedAt)
	assert.Nil(t, got.Error)
	assert.Equal(t, []string{"Bearer " + aiTestKey}, provider.calls(), "the server calls the provider with the key of the endpoint")
	assert.NotContains(t, mustJSON(t, got), aiTestKey)
}

func TestAIModelsAPI_ReloadKeepsTheListOnAFailureAndShowsTheError(t *testing.T) {
	ctx := context.Background()
	provider := newAIModelsProvider(t)
	api, auditStore, service := newAIModelsTestAPI(t, provider)
	aiCreate(t, api, service, aiCustomBody("cle1", provider, true))
	before := getModels(t, api, "cle1")

	provider.set(http.StatusInternalServerError, "oops "+aiTestKey)
	resp, err := api.ReloadAIEndpointModels(ctx, apigen.ReloadAIEndpointModelsRequestObject{Name: "cle1"})
	require.NoError(t, err)
	reloaded, ok := resp.(apigen.ReloadAIEndpointModels200JSONResponse)
	require.True(t, ok, "%T", resp)

	assert.Equal(t, before.Models, reloaded.Models, "the last good list stays")
	assert.Equal(t, before.RefreshedAt, reloaded.RefreshedAt, "the date of the last good read stays")
	require.NotNil(t, reloaded.Error)
	assert.Contains(t, *reloaded.Error, "status 500")
	require.NotNil(t, reloaded.ErrorAt)
	assert.NotContains(t, mustJSON(t, reloaded), aiTestKey)
	assert.Equal(t, before.Models, getModels(t, api, "cle1").Models)

	// The list of endpoints shows the count, the date and the error, and no names.
	listResp, err := api.ListAIEndpoints(ctx, apigen.ListAIEndpointsRequestObject{})
	require.NoError(t, err)
	list := listResp.(apigen.ListAIEndpoints200JSONResponse)
	require.Len(t, list.Endpoints, 1)
	status := list.Endpoints[0].Models
	require.NotNil(t, status)
	assert.Equal(t, 2, status.Count)
	assert.NotNil(t, status.Error)
	assert.NotContains(t, mustJSON(t, list), "gpt-5")

	// A good read clears the error.
	provider.set(http.StatusOK, `{"data":[{"id":"gpt-6"}]}`)
	_, err = api.ReloadAIEndpointModels(ctx, apigen.ReloadAIEndpointModelsRequestObject{Name: "cle1"})
	require.NoError(t, err)
	after := getModels(t, api, "cle1")
	assert.Equal(t, []string{"gpt-6"}, after.Models)
	assert.Nil(t, after.Error)

	// Each reload writes one audit line of the category secret, with no key.
	var lines []*audit.Entry
	for _, entry := range auditStore.entries {
		if entry.Action == "ai_endpoint_models_reload" {
			lines = append(lines, entry)
		}
	}
	require.Len(t, lines, 2)
	assert.Equal(t, audit.CategorySecret, lines[0].Category)
	assert.Contains(t, lines[0].Details, `"name":"cle1"`)
	assert.Contains(t, lines[0].Details, `"ok":false`)
	assert.Contains(t, lines[1].Details, `"ok":true`)
	for _, entry := range auditStore.entries {
		assert.NotContains(t, entry.Details, aiTestKey)
	}
}

func TestAIModelsAPI_EndpointWithoutAModelsURL(t *testing.T) {
	ctx := context.Background()
	provider := newAIModelsProvider(t)
	api, _, service := newAIModelsTestAPI(t, provider)
	created := aiCreate(t, api, service, aiCustomBody("plain", provider, false))

	require.NotNil(t, created.Models)
	assert.False(t, created.Models.Supported)
	got := getModels(t, api, "plain")
	assert.False(t, got.Supported)
	assert.Empty(t, got.Models)
	assert.Empty(t, provider.calls(), "an endpoint with no models URL is never read")

	resp, err := api.ReloadAIEndpointModels(ctx, apigen.ReloadAIEndpointModelsRequestObject{Name: "plain"})
	require.NoError(t, err)
	assert.IsType(t, apigen.ReloadAIEndpointModels400JSONResponse{}, resp)

	resp, err = api.ReloadAIEndpointModels(ctx, apigen.ReloadAIEndpointModelsRequestObject{Name: "missing"})
	require.NoError(t, err)
	assert.IsType(t, apigen.ReloadAIEndpointModels404JSONResponse{}, resp)
	getResp, err := api.GetAIEndpointModels(ctx, apigen.GetAIEndpointModelsRequestObject{Name: "missing"})
	require.NoError(t, err)
	assert.IsType(t, apigen.GetAIEndpointModels404JSONResponse{}, getResp)
}

func TestAIModelsAPI_RotationReadsWithTheNewKey(t *testing.T) {
	ctx := context.Background()
	provider := newAIModelsProvider(t)
	api, _, service := newAIModelsTestAPI(t, provider)
	aiCreate(t, api, service, aiCustomBody("cle1", provider, true))

	_, err := api.SetAIEndpointKey(ctx, apigen.SetAIEndpointKeyRequestObject{Name: "cle1", Body: &apigen.SetAIEndpointKeyRequest{Key: new(aiTestKeyTwo)}})
	require.NoError(t, err)
	service.WaitModels()

	assert.Equal(t, []string{"Bearer " + aiTestKey, "Bearer " + aiTestKeyTwo}, provider.calls())
}

func TestAIModelsAPI_ChoicesCarryTheModels(t *testing.T) {
	ctx := context.Background()
	provider := newAIModelsProvider(t)
	api, _, service := newAIModelsTestAPI(t, provider)
	aiCreate(t, api, service, aiCustomBody("cle1", provider, true))
	aiCreate(t, api, service, aiCustomBody("plain", provider, false))

	resp, err := api.ListAIEndpointChoices(ctx, apigen.ListAIEndpointChoicesRequestObject{Params: apigen.ListAIEndpointChoicesParams{Dag: "fix-bug"}})
	require.NoError(t, err)
	list, ok := resp.(apigen.ListAIEndpointChoices200JSONResponse)
	require.True(t, ok, "%T", resp)
	require.Len(t, list.Choices, 2)
	byName := map[string]apigen.AIEndpointChoice{}
	for _, choice := range list.Choices {
		byName[string(choice.Name)] = choice
	}
	require.NotNil(t, byName["cle1"].Models)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, *byName["cle1"].Models)
	require.NotNil(t, byName["plain"].Models)
	assert.Empty(t, *byName["plain"].Models, "no list gives an empty list, not an absent one")
	// The protocol openai gives no details, so each model has its name only.
	require.NotNil(t, byName["cle1"].ModelInfo)
	assert.Equal(t, []apigen.AIModelInfo{{Id: "gpt-5"}, {Id: "gpt-5-mini"}}, *byName["cle1"].ModelInfo)
	require.NotNil(t, byName["plain"].ModelInfo)
	assert.Empty(t, *byName["plain"].ModelInfo)
	assert.NotContains(t, mustJSON(t, list), aiTestKey)
	// The UI marks a model that the list lacks, so it needs the aliases of the CLI.
	require.NotNil(t, list.CliModelAliases)
	assert.Equal(t, aiendpoint.CLIModelAliases, *list.CliModelAliases)
}

// The protocol anthropic gives the display name and the efforts of a model. The
// route of choices and the route of the models list carry them in the order of models.
func TestAIModelsAPI_ModelInfoCarriesDisplayNameAndEfforts(t *testing.T) {
	ctx := context.Background()
	provider := newAIModelsProvider(t)
	provider.set(http.StatusOK, `{"data":[
		{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","capabilities":{"effort":{"supported":true,"low":{"supported":true},"medium":{"supported":true},"high":{"supported":true},"xhigh":{"supported":true},"max":{"supported":true}}}},
		{"id":"claude-old-3","capabilities":{"effort":{"supported":false}}}]}`)
	api, _, service := newAIModelsTestAPI(t, provider)
	body := aiCustomBody("claude1", provider, true)
	body.Protocol = apigen.AIEndpointProtocolAnthropic
	body.Auth = new(apigen.AIEndpointAuthXApiKey)
	aiCreate(t, api, service, body)

	want := []apigen.AIModelInfo{
		{Id: "claude-opus-5-5", DisplayName: new("Claude Opus 5.5"), Efforts: &[]string{"low", "medium", "high", "xhigh", "max"}},
		{Id: "claude-old-3"},
	}
	got := getModels(t, api, "claude1")
	assert.Equal(t, []string{"claude-opus-5-5", "claude-old-3"}, got.Models)
	require.NotNil(t, got.ModelInfo)
	assert.Equal(t, want, *got.ModelInfo)

	resp, err := api.ListAIEndpointChoices(ctx, apigen.ListAIEndpointChoicesRequestObject{Params: apigen.ListAIEndpointChoicesParams{Dag: "fix-bug"}})
	require.NoError(t, err)
	list, ok := resp.(apigen.ListAIEndpointChoices200JSONResponse)
	require.True(t, ok, "%T", resp)
	require.Len(t, list.Choices, 1)
	require.NotNil(t, list.Choices[0].ModelInfo)
	assert.Equal(t, want, *list.Choices[0].ModelInfo)
	assert.NotContains(t, mustJSON(t, list), `"efforts":null`)
}

func TestAIModelsAPI_DeleteRemovesTheList(t *testing.T) {
	ctx := context.Background()
	provider := newAIModelsProvider(t)
	api, _, service := newAIModelsTestAPI(t, provider)
	aiCreate(t, api, service, aiCustomBody("cle1", provider, true))

	resp, err := api.DeleteAIEndpoint(ctx, apigen.DeleteAIEndpointRequestObject{Name: "cle1"})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteAIEndpoint204Response{}, resp)

	// The same name again starts with no list.
	provider.set(http.StatusInternalServerError, "")
	created := aiCreate(t, api, service, aiCustomBody("cle1", provider, true))
	require.NotNil(t, created.Models)
	got := getModels(t, api, "cle1")
	assert.Empty(t, got.Models)
	assert.Nil(t, got.RefreshedAt)
}

// The routes of the list need the role admin. A role that edits DAGs reads the
// models of an endpoint that it may use from the route of choices.
func TestAIModelsAPI_RolesOverHTTP(t *testing.T) {
	provider := newAIModelsProvider(t)
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	managerToken := createRuntimeProfileUserToken(t, server, adminToken, "ai-manager", "managerpass1", apigen.UserRoleManager)
	viewerToken := createRuntimeProfileUserToken(t, server, adminToken, "ai-viewer", "viewerpass1", apigen.UserRoleViewer)
	developerToken := createRuntimeProfileUserToken(t, server, adminToken, "ai-developer", "developerpass1", apigen.UserRoleDeveloper)

	server.Client().Post("/api/v1/ai/endpoints", aiCustomBody("cle1", provider, true)).WithBearerToken(adminToken).ExpectStatus(http.StatusCreated).Send(t)

	for _, token := range []string{managerToken, developerToken, viewerToken} {
		server.Client().Get("/api/v1/ai/endpoints/cle1/models").WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
		server.Client().Post("/api/v1/ai/endpoints/cle1/models/reload", nil).WithBearerToken(token).ExpectStatus(http.StatusForbidden).Send(t)
	}
	server.Client().Get("/api/v1/ai/endpoints/cle1/models").ExpectStatus(http.StatusUnauthorized).Send(t)
	server.Client().Post("/api/v1/ai/endpoints/cle1/models/reload", nil).ExpectStatus(http.StatusUnauthorized).Send(t)

	reloaded := server.Client().Post("/api/v1/ai/endpoints/cle1/models/reload", nil).WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	assert.Contains(t, reloaded.Body, `"gpt-5"`)
	assert.NotContains(t, reloaded.Body, aiTestKey)
	read := server.Client().Get("/api/v1/ai/endpoints/cle1/models").WithBearerToken(adminToken).ExpectStatus(http.StatusOK).Send(t)
	assert.Contains(t, read.Body, `"gpt-5-mini"`)

	// The developer gets the models through the choices. The viewer cannot.
	choices := server.Client().Get("/api/v1/ai/endpoint-choices?dag=fix-bug").WithBearerToken(developerToken).ExpectStatus(http.StatusOK).Send(t)
	assert.Contains(t, choices.Body, `"models":["gpt-5","gpt-5-mini"]`)
	server.Client().Get("/api/v1/ai/endpoint-choices?dag=fix-bug").WithBearerToken(viewerToken).ExpectStatus(http.StatusForbidden).Send(t)
}
