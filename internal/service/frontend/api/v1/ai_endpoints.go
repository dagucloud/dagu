// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// The routes of the AI endpoints (Spec 093, "Routes and audit"). All routes need
// the role admin, except the route of choices. No response and no log line holds a
// key. The audit lines hold the fingerprint of a key, never the key or its prefix.

func aiEndpointsUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "AI endpoint service not configured",
	}
}

func aiEndpointBadRequest(message string) api.Error {
	return api.Error{Code: api.ErrorCodeBadRequest, Message: message}
}

func aiEndpointNotFound() api.Error {
	return api.Error{Code: api.ErrorCodeNotFound, Message: "AI endpoint not found"}
}

func aiEndpointConflict() api.Error {
	return api.Error{Code: api.ErrorCodeAlreadyExists, Message: "An AI endpoint with this name exists in the scope"}
}

// aiEndpointScopeFromQuery reads the query workspace of the routes that name an
// endpoint. An absent value, an empty value and the value global mean the global
// scope. Any other value is the name of a workspace.
func aiEndpointScopeFromQuery(raw *string) (aiendpoint.Scope, error) {
	if raw == nil {
		return aiendpoint.GlobalScope(), nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" || value == "global" {
		return aiendpoint.GlobalScope(), nil
	}
	if err := workspace.ValidateName(value); err != nil {
		return aiendpoint.Scope{}, fmt.Errorf("query workspace: %w", err)
	}
	return aiendpoint.WorkspaceScope(value), nil
}

func (a *API) ListAIEndpoints(ctx context.Context, _ api.ListAIEndpointsRequestObject) (api.ListAIEndpointsResponseObject, error) {
	if a.aiEndpoints == nil {
		return nil, aiEndpointsUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	endpoints, err := a.aiEndpoints.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list AI endpoints: %w", err)
	}
	lists, err := a.aiEndpoints.ModelsByEndpoint(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list the models lists: %w", err)
	}
	out := make([]api.AIEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		resp := toAIEndpointResponse(ep)
		status := a.aiModelsStatus(ep, lists[aiendpoint.ModelsKey{Scope: ep.Scope, Name: ep.Name}])
		resp.Models = &status
		out = append(out, resp)
	}
	return api.ListAIEndpoints200JSONResponse{Endpoints: out}, nil
}

func (a *API) CreateAIEndpoint(ctx context.Context, request api.CreateAIEndpointRequestObject) (api.CreateAIEndpointResponseObject, error) {
	if a.aiEndpoints == nil {
		return nil, aiEndpointsUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.CreateAIEndpoint400JSONResponse(aiEndpointBadRequest("Request body is required")), nil
	}
	body := request.Body

	scope, err := aiendpoint.ParseScope(string(body.Scope))
	if err != nil {
		return api.CreateAIEndpoint400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	if !scope.IsGlobal() {
		if a.workspaceStore == nil {
			return nil, workspaceStoreUnavailable()
		}
		if _, err := a.workspaceStore.GetByName(ctx, scope.Workspace); err != nil {
			if errors.Is(err, workspace.ErrWorkspaceNotFound) {
				return api.CreateAIEndpoint404JSONResponse(api.Error{
					Code:    api.ErrorCodeNotFound,
					Message: "Workspace of the scope not found",
				}), nil
			}
			return nil, err
		}
	}

	input := aiendpoint.CreateInput{
		Name:        string(body.Name),
		Description: valueOf(body.Description),
		Preset:      body.Preset,
		Protocol:    aiendpoint.Protocol(body.Protocol),
		BaseURL:     valueOf(body.BaseUrl),
		ModelsURL:   valueOf(body.ModelsUrl),
		Scope:       scope,
		Allow:       fromAIEndpointAllow(body.Allow),
		Key:         valueOf(body.Key),
		Actor:       currentActorID(ctx),
	}
	if body.Auth != nil {
		input.Auth = aiendpoint.Auth(*body.Auth)
	}
	created, err := a.aiEndpoints.Create(ctx, input)
	if err != nil {
		switch {
		case errors.Is(err, aiendpoint.ErrInvalid):
			return api.CreateAIEndpoint400JSONResponse(aiEndpointBadRequest(err.Error())), nil
		case errors.Is(err, aiendpoint.ErrAlreadyExists):
			return api.CreateAIEndpoint409JSONResponse(aiEndpointConflict()), nil
		}
		return nil, fmt.Errorf("failed to create AI endpoint: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_endpoint_create", aiEndpointKeyAuditDetails(created))
	// The first read of the models list. The save never waits for the provider.
	a.aiEndpoints.RefreshModelsAsync(ctx, created.Scope, created.Name)
	return api.CreateAIEndpoint201JSONResponse(a.aiEndpointResponse(ctx, created)), nil
}

func (a *API) UpdateAIEndpoint(ctx context.Context, request api.UpdateAIEndpointRequestObject) (api.UpdateAIEndpointResponseObject, error) {
	if a.aiEndpoints == nil {
		return nil, aiEndpointsUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.UpdateAIEndpoint400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	if request.Body == nil {
		return api.UpdateAIEndpoint400JSONResponse(aiEndpointBadRequest("Request body is required")), nil
	}
	body := request.Body

	input := aiendpoint.UpdateInput{
		Description: body.Description,
		BaseURL:     body.BaseUrl,
		ModelsURL:   body.ModelsUrl,
		Actor:       currentActorID(ctx),
	}
	if body.Auth != nil {
		auth := aiendpoint.Auth(*body.Auth)
		input.Auth = &auth
	}
	if body.Allow != nil {
		allow := fromAIEndpointAllow(*body.Allow)
		input.Allow = &allow
	}
	updated, err := a.aiEndpoints.Update(ctx, scope, string(request.Name), input)
	if err != nil {
		switch {
		case errors.Is(err, aiendpoint.ErrNotFound):
			return api.UpdateAIEndpoint404JSONResponse(aiEndpointNotFound()), nil
		case errors.Is(err, aiendpoint.ErrInvalid):
			return api.UpdateAIEndpoint400JSONResponse(aiEndpointBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to update AI endpoint: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_endpoint_update", aiEndpointAuditDetails(updated))
	return api.UpdateAIEndpoint200JSONResponse(a.aiEndpointResponse(ctx, updated)), nil
}

func (a *API) SetAIEndpointKey(ctx context.Context, request api.SetAIEndpointKeyRequestObject) (api.SetAIEndpointKeyResponseObject, error) {
	if a.aiEndpoints == nil {
		return nil, aiEndpointsUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.SetAIEndpointKey400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	if request.Body == nil || request.Body.Key == nil {
		return api.SetAIEndpointKey400JSONResponse(aiEndpointBadRequest("Request body with a key is required")), nil
	}

	updated, err := a.aiEndpoints.RotateKey(ctx, scope, string(request.Name), *request.Body.Key, currentActorID(ctx))
	if err != nil {
		switch {
		case errors.Is(err, aiendpoint.ErrNotFound):
			return api.SetAIEndpointKey404JSONResponse(aiEndpointNotFound()), nil
		case errors.Is(err, aiendpoint.ErrInvalid):
			return api.SetAIEndpointKey400JSONResponse(aiEndpointBadRequest(err.Error())), nil
		}
		return nil, fmt.Errorf("failed to set the key of the AI endpoint: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_endpoint_key_rotate", aiEndpointKeyAuditDetails(updated))
	// A new key can list models that the old one could not.
	a.aiEndpoints.RefreshModelsAsync(ctx, updated.Scope, updated.Name)
	return api.SetAIEndpointKey200JSONResponse(a.aiEndpointResponse(ctx, updated)), nil
}

func (a *API) DeleteAIEndpoint(ctx context.Context, request api.DeleteAIEndpointRequestObject) (api.DeleteAIEndpointResponseObject, error) {
	if a.aiEndpoints == nil {
		return nil, aiEndpointsUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.DeleteAIEndpoint400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	existing, err := a.aiEndpoints.Get(ctx, scope, string(request.Name))
	if err != nil {
		if errors.Is(err, aiendpoint.ErrNotFound) {
			return api.DeleteAIEndpoint404JSONResponse(aiEndpointNotFound()), nil
		}
		return nil, fmt.Errorf("failed to read AI endpoint: %w", err)
	}
	if err := a.aiEndpoints.Delete(ctx, scope, string(request.Name)); err != nil {
		if errors.Is(err, aiendpoint.ErrNotFound) {
			return api.DeleteAIEndpoint404JSONResponse(aiEndpointNotFound()), nil
		}
		return nil, fmt.Errorf("failed to delete AI endpoint: %w", err)
	}

	a.logAudit(ctx, audit.CategorySecret, "ai_endpoint_delete", aiEndpointAuditDetails(existing))
	return api.DeleteAIEndpoint204Response{}, nil
}

func (a *API) ListAIEndpointChoices(ctx context.Context, request api.ListAIEndpointChoicesRequestObject) (api.ListAIEndpointChoicesResponseObject, error) {
	if a.aiEndpoints == nil {
		return nil, aiEndpointsUnavailable()
	}
	workspaceName := ""
	if request.Params.Workspace != nil {
		workspaceName = *request.Params.Workspace
	}
	// The roles that edit DAGs of the workspace can read the choices.
	if err := a.requireWorkspaceWrite(ctx, workspaceName); err != nil {
		return nil, err
	}
	if request.Params.Dag == "" {
		return api.ListAIEndpointChoices400JSONResponse(aiEndpointBadRequest("Query dag is required")), nil
	}
	choices, err := a.aiEndpoints.Choices(ctx, request.Params.Dag, workspaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to list AI endpoint choices: %w", err)
	}
	lists, err := a.aiEndpoints.ModelsByEndpoint(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list the models lists: %w", err)
	}
	out := make([]api.AIEndpointChoice, 0, len(choices))
	for _, ep := range choices {
		names := []string{}
		infos := []api.AIModelInfo{}
		if a.aiEndpoints.ModelsEnabled() && ep.ListURL() != "" {
			if list := lists[aiendpoint.ModelsKey{Scope: ep.Scope, Name: ep.Name}]; list != nil {
				names = append(names, list.Names...)
				infos = aiModelInfoList(list)
			}
		}
		out = append(out, api.AIEndpointChoice{
			Name:      api.AIEndpointName(ep.Name),
			Protocol:  api.AIEndpointProtocol(ep.Protocol),
			Preset:    ep.Preset,
			Scope:     api.AIEndpointScope(ep.Scope.String()),
			Models:    &names,
			ModelInfo: &infos,
		})
	}
	aliases := slices.Clone(aiendpoint.CLIModelAliases)
	return api.ListAIEndpointChoices200JSONResponse{Choices: out, CliModelAliases: &aliases}, nil
}

// ListAIEndpointPresets serves the table of presets of the server, as the table
// holds it. The preset custom comes last. The route needs no endpoint service,
// because the table is static data.
func (a *API) ListAIEndpointPresets(ctx context.Context, _ api.ListAIEndpointPresetsRequestObject) (api.ListAIEndpointPresetsResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	return api.ListAIEndpointPresets200JSONResponse{Presets: aiEndpointPresetList()}, nil
}

// aiEndpointPresetList groups the rows of the table by preset, in the order of the
// table.
func aiEndpointPresetList() []api.AIEndpointPreset {
	out := []api.AIEndpointPreset{}
	index := map[string]int{}
	for _, row := range append(aiendpoint.Presets(), aiendpoint.CustomPresets()...) {
		i, ok := index[row.Name]
		if !ok {
			i = len(out)
			index[row.Name] = i
			out = append(out, api.AIEndpointPreset{
				Id:        row.Name,
				Label:     row.Label,
				NeedsKey:  !row.AllowsNoKey(),
				Protocols: []api.AIEndpointPresetProtocol{},
			})
		}
		protocol := api.AIEndpointPresetProtocol{
			Protocol:  api.AIEndpointProtocol(row.Protocol),
			BaseUrl:   ptrOf(row.BaseURL),
			ModelsUrl: ptrOf(row.ModelsURL),
			ChatType:  row.ChatType,
			Consumers: []api.AIEndpointConsumer{},
		}
		if row.Auth != "" {
			auth := api.AIEndpointAuth(row.Auth)
			protocol.Auth = &auth
		}
		for _, consumer := range row.Consumers() {
			protocol.Consumers = append(protocol.Consumers, api.AIEndpointConsumer(consumer))
		}
		out[i].Protocols = append(out[i].Protocols, protocol)
	}
	return out
}

func fromAIEndpointAllow(in api.AIEndpointAllow) aiendpoint.Allow {
	out := aiendpoint.Allow{DAGs: in.Dags}
	if in.Workspaces != nil {
		out.Workspaces = *in.Workspaces
	}
	return out
}

// aiEndpointResponse converts an endpoint with the state of its models list. A
// store that fails to give the list shows the endpoint with an empty list.
func (a *API) aiEndpointResponse(ctx context.Context, ep *aiendpoint.Endpoint) api.AIEndpoint {
	resp := toAIEndpointResponse(ep)
	models, _ := a.aiEndpoints.ModelsOf(ctx, ep)
	status := a.aiModelsStatus(ep, models)
	resp.Models = &status
	return resp
}

// toAIEndpointResponse converts an endpoint. It has no key: the endpoint holds the
// prefix and the fingerprint only.
func toAIEndpointResponse(ep *aiendpoint.Endpoint) api.AIEndpoint {
	resp := api.AIEndpoint{
		Name:      api.AIEndpointName(ep.Name),
		Preset:    ep.Preset,
		Protocol:  api.AIEndpointProtocol(ep.Protocol),
		BaseUrl:   ep.BaseURL,
		Auth:      api.AIEndpointAuth(ep.Auth),
		Scope:     api.AIEndpointScope(ep.Scope.String()),
		Allow:     api.AIEndpointAllow{Dags: append([]string{}, ep.Allow.DAGs...)},
		CreatedAt: ep.CreatedAt,
		UpdatedAt: ep.UpdatedAt,
	}
	if len(ep.Allow.Workspaces) > 0 {
		workspaces := append([]string{}, ep.Allow.Workspaces...)
		resp.Allow.Workspaces = &workspaces
	}
	resp.Description = ptrOf(ep.Description)
	resp.ModelsUrl = ptrOf(ep.ModelsURL)
	if ep.HasKey() {
		resp.KeyPrefix = ptrOf(ep.KeyPrefix)
		resp.KeyFingerprint = ptrOf(ep.KeyFingerprint)
		updatedAt := ep.KeyUpdatedAt
		resp.KeyUpdatedAt = &updatedAt
	}
	return resp
}

// aiEndpointAuditDetails gives the fields of an audit line: the name and the scope.
func aiEndpointAuditDetails(ep *aiendpoint.Endpoint) map[string]any {
	return map[string]any{
		"name":  ep.Name,
		"scope": ep.Scope.String(),
	}
}

// aiEndpointKeyAuditDetails adds the fingerprint of the key. It never adds the key
// or its prefix.
func aiEndpointKeyAuditDetails(ep *aiendpoint.Endpoint) map[string]any {
	details := aiEndpointAuditDetails(ep)
	if ep.HasKey() {
		details["key_fingerprint"] = ep.KeyFingerprint
	}
	return details
}
