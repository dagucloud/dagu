// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/audit"
)

// The routes of the models list of an AI endpoint (Spec 093, "The models list").
// The error text of a failed read never holds the key.

// aiModelsStatus converts the models list of an endpoint to the status of the
// API, without the names. An endpoint with no models URL is not supported.
func (a *API) aiModelsStatus(ep *aiendpoint.Endpoint, models *aiendpoint.Models) api.AIEndpointModelsStatus {
	status := api.AIEndpointModelsStatus{
		Supported: a.aiEndpoints.ModelsEnabled() && ep.ListURL() != "",
	}
	if models == nil || !status.Supported {
		return status
	}
	status.Count = len(models.Names)
	if !models.RefreshedAt.IsZero() {
		refreshedAt := models.RefreshedAt
		status.RefreshedAt = &refreshedAt
	}
	if models.LastError != "" {
		status.Error = ptrOf(models.LastError)
		errorAt := models.LastErrorAt
		status.ErrorAt = &errorAt
	}
	return status
}

// aiModelInfoList returns one item for each name of the list, in the same order.
// An item has the display name and the efforts only when the provider gave them.
func aiModelInfoList(models *aiendpoint.Models) []api.AIModelInfo {
	details := make(map[string]aiendpoint.ModelInfo, len(models.Info))
	for _, info := range models.Info {
		details[info.ID] = info
	}
	out := make([]api.AIModelInfo, 0, len(models.Names))
	for _, name := range models.Names {
		item := api.AIModelInfo{Id: name}
		if info, ok := details[name]; ok {
			if info.DisplayName != "" {
				item.DisplayName = ptrOf(info.DisplayName)
			}
			if len(info.Efforts) > 0 {
				item.Efforts = ptrOf(slices.Clone(info.Efforts))
			}
		}
		out = append(out, item)
	}
	return out
}

// aiModelsResponse converts the models list of an endpoint to the response of the
// routes of the list.
func (a *API) aiModelsResponse(ep *aiendpoint.Endpoint, models *aiendpoint.Models) api.AIEndpointModels {
	status := a.aiModelsStatus(ep, models)
	resp := api.AIEndpointModels{
		Supported:   status.Supported,
		Count:       status.Count,
		RefreshedAt: status.RefreshedAt,
		Error:       status.Error,
		ErrorAt:     status.ErrorAt,
		Models:      []string{},
		ModelInfo:   &[]api.AIModelInfo{},
	}
	if status.Supported && models != nil {
		resp.Models = append(resp.Models, models.Names...)
		resp.ModelInfo = ptrOf(aiModelInfoList(models))
	}
	return resp
}

func (a *API) GetAIEndpointModels(ctx context.Context, request api.GetAIEndpointModelsRequestObject) (api.GetAIEndpointModelsResponseObject, error) {
	if a.aiEndpoints == nil {
		return nil, aiEndpointsUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.GetAIEndpointModels400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	ep, err := a.aiEndpoints.Get(ctx, scope, string(request.Name))
	if err != nil {
		if errors.Is(err, aiendpoint.ErrNotFound) {
			return api.GetAIEndpointModels404JSONResponse(aiEndpointNotFound()), nil
		}
		return nil, fmt.Errorf("failed to read AI endpoint: %w", err)
	}
	models, err := a.aiEndpoints.ModelsOf(ctx, ep)
	if err != nil {
		return nil, fmt.Errorf("failed to read the models list: %w", err)
	}
	return api.GetAIEndpointModels200JSONResponse(a.aiModelsResponse(ep, models)), nil
}

func (a *API) ReloadAIEndpointModels(ctx context.Context, request api.ReloadAIEndpointModelsRequestObject) (api.ReloadAIEndpointModelsResponseObject, error) {
	if a.aiEndpoints == nil || !a.aiEndpoints.ModelsEnabled() {
		return nil, aiEndpointsUnavailable()
	}
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	scope, err := aiEndpointScopeFromQuery(request.Params.Workspace)
	if err != nil {
		return api.ReloadAIEndpointModels400JSONResponse(aiEndpointBadRequest(err.Error())), nil
	}
	models, err := a.aiEndpoints.RefreshModels(ctx, scope, string(request.Name))
	if err != nil {
		switch {
		case errors.Is(err, aiendpoint.ErrNotFound):
			return api.ReloadAIEndpointModels404JSONResponse(aiEndpointNotFound()), nil
		case errors.Is(err, aiendpoint.ErrNoModelsURL):
			return api.ReloadAIEndpointModels400JSONResponse(aiEndpointBadRequest("The endpoint has no models URL")), nil
		}
		return nil, fmt.Errorf("failed to reload the models list: %w", err)
	}
	ep, err := a.aiEndpoints.Get(ctx, scope, string(request.Name))
	if err != nil {
		if errors.Is(err, aiendpoint.ErrNotFound) {
			return api.ReloadAIEndpointModels404JSONResponse(aiEndpointNotFound()), nil
		}
		return nil, fmt.Errorf("failed to read AI endpoint: %w", err)
	}

	details := aiEndpointAuditDetails(ep)
	details["ok"] = models.LastError == ""
	a.logAudit(ctx, audit.CategorySecret, "ai_endpoint_models_reload", details)
	return api.ReloadAIEndpointModels200JSONResponse(a.aiModelsResponse(ep, models)), nil
}
