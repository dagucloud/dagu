// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

func workspaceStoreUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "Workspace store not configured",
	}
}

// ListWorkspaces returns all workspaces.
func (a *API) ListWorkspaces(ctx context.Context, _ api.ListWorkspacesRequestObject) (api.ListWorkspacesResponseObject, error) {
	if a.workspaceStore == nil {
		return nil, workspaceStoreUnavailable()
	}

	wsList, err := a.workspaceStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	response := make([]api.WorkspaceResponse, 0, len(wsList))
	for _, ws := range wsList {
		if !a.canAccessWorkspace(ctx, ws.Name) {
			continue
		}
		response = append(response, toWorkspaceResponse(ws))
	}

	return api.ListWorkspaces200JSONResponse{Workspaces: response}, nil
}

// CreateWorkspace creates a new workspace.
func (a *API) CreateWorkspace(ctx context.Context, request api.CreateWorkspaceRequestObject) (api.CreateWorkspaceResponseObject, error) {
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}
	if a.workspaceStore == nil {
		return nil, workspaceStoreUnavailable()
	}

	body := request.Body
	if body.Name == "" {
		return api.CreateWorkspace400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "Name is required",
		}, nil
	}
	if err := workspace.ValidateName(body.Name); err != nil {
		return api.CreateWorkspace400JSONResponse{
			Code:    api.ErrorCodeBadRequest,
			Message: "Workspace name must contain only letters, numbers, underscores, and hyphens",
		}, nil
	}

	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()

	wikiStore := a.wikiStore
	if wikiStore != nil {
		fileExists, directoryExists, err := wikiStore.PathExists(ctx, body.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect workspace Wiki page path: %w", err)
		}
		if fileExists || directoryExists {
			return api.CreateWorkspace409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: "Workspace name conflicts with an existing Wiki page path",
			}, nil
		}
	}

	ws := workspace.NewWorkspace(body.Name, valueOf(body.Description))
	if err := a.workspaceStore.Create(ctx, ws); err != nil {
		if errors.Is(err, workspace.ErrWorkspaceAlreadyExists) {
			return api.CreateWorkspace409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: "Workspace with this name already exists",
			}, nil
		}
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	a.logAudit(ctx, audit.CategoryWorkspace, "workspace_create", map[string]string{
		"id":   ws.ID,
		"name": ws.Name,
	})

	return api.CreateWorkspace201JSONResponse(toWorkspaceResponse(ws)), nil
}

// GetWorkspace returns a single workspace by ID.
func (a *API) GetWorkspace(ctx context.Context, request api.GetWorkspaceRequestObject) (api.GetWorkspaceResponseObject, error) {
	if a.workspaceStore == nil {
		return nil, workspaceStoreUnavailable()
	}

	ws, err := a.workspaceStore.GetByID(ctx, request.WorkspaceId)
	if err != nil {
		if errors.Is(err, workspace.ErrWorkspaceNotFound) {
			return api.GetWorkspace404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Workspace not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}
	if !a.canAccessWorkspace(ctx, ws.Name) {
		return api.GetWorkspace404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: "Workspace not found",
		}, nil
	}

	return api.GetWorkspace200JSONResponse(toWorkspaceResponse(ws)), nil
}

// UpdateWorkspace updates a workspace with PATCH semantics.
func (a *API) UpdateWorkspace(ctx context.Context, request api.UpdateWorkspaceRequestObject) (api.UpdateWorkspaceResponseObject, error) {
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}
	if a.workspaceStore == nil {
		return nil, workspaceStoreUnavailable()
	}

	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()

	existing, err := a.workspaceStore.GetByID(ctx, request.WorkspaceId)
	if err != nil {
		if errors.Is(err, workspace.ErrWorkspaceNotFound) {
			return api.UpdateWorkspace404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Workspace not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}
	if !a.canAccessWorkspace(ctx, existing.Name) {
		return api.UpdateWorkspace404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: "Workspace not found",
		}, nil
	}

	updated := *existing
	body := request.Body
	if body.Name != nil {
		if err := workspace.ValidateName(*body.Name); err != nil {
			return nil, &Error{
				Code:       api.ErrorCodeBadRequest,
				Message:    "Workspace name must contain only letters, numbers, underscores, and hyphens",
				HTTPStatus: http.StatusBadRequest,
			}
		}
		updated.Name = *body.Name
	}
	if body.Description != nil {
		updated.Description = *body.Description
	}

	updated.UpdatedAt = time.Now().UTC()

	wikiStore := a.wikiStore
	wikiMoved := false
	if wikiStore != nil && updated.Name != existing.Name {
		oldFileExists, oldDirectoryExists, err := wikiStore.PathExists(ctx, existing.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect current workspace Wiki page path: %w", err)
		}
		newFileExists, newDirectoryExists, err := wikiStore.PathExists(ctx, updated.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect new workspace Wiki page path: %w", err)
		}
		if oldFileExists || newFileExists || newDirectoryExists {
			return api.UpdateWorkspace409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: "Workspace rename conflicts with an existing Wiki page path",
			}, nil
		}
		if oldDirectoryExists {
			if err := wikiStore.RenameDirectory(ctx, existing.Name, updated.Name); err != nil {
				if errors.Is(err, wiki.ErrPageAlreadyExists) || errors.Is(err, wiki.ErrPagePathConflict) {
					return api.UpdateWorkspace409JSONResponse{
						Code:    api.ErrorCodeAlreadyExists,
						Message: "Workspace rename conflicts with an existing Wiki page path",
					}, nil
				}
				return nil, fmt.Errorf("failed to rename workspace Wiki pages: %w", err)
			}
			wikiMoved = true
		}
	}

	if err := a.workspaceStore.Update(ctx, &updated); err != nil {
		if wikiMoved {
			if rollbackErr := wikiStore.RenameDirectory(ctx, updated.Name, existing.Name); rollbackErr != nil {
				logger.Error(ctx, "Failed to restore workspace Wiki pages after update failure",
					tag.String("current-page-id", updated.Name),
					tag.String("restore-page-id", existing.Name),
					tag.Error(rollbackErr),
				)
				return nil, errors.Join(
					fmt.Errorf("failed to update workspace: %w", err),
					fmt.Errorf("failed to restore workspace Wiki pages: %w", rollbackErr),
				)
			}
		}
		if errors.Is(err, workspace.ErrWorkspaceAlreadyExists) {
			return api.UpdateWorkspace409JSONResponse{
				Code:    api.ErrorCodeAlreadyExists,
				Message: "Workspace with this name already exists",
			}, nil
		}
		return nil, fmt.Errorf("failed to update workspace: %w", err)
	}

	a.logAudit(ctx, audit.CategoryWorkspace, "workspace_update", map[string]string{
		"id":   updated.ID,
		"name": updated.Name,
	})

	return api.UpdateWorkspace200JSONResponse(toWorkspaceResponse(&updated)), nil
}

// DeleteWorkspace deletes a workspace by ID.
func (a *API) DeleteWorkspace(ctx context.Context, request api.DeleteWorkspaceRequestObject) (api.DeleteWorkspaceResponseObject, error) {
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return nil, err
	}
	if a.workspaceStore == nil {
		return nil, workspaceStoreUnavailable()
	}

	a.workspaceWikiMu.Lock()
	defer a.workspaceWikiMu.Unlock()

	ws, err := a.workspaceStore.GetByID(ctx, request.WorkspaceId)
	if err != nil {
		if errors.Is(err, workspace.ErrWorkspaceNotFound) {
			return api.DeleteWorkspace404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Workspace not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}
	if !a.canAccessWorkspace(ctx, ws.Name) {
		return api.DeleteWorkspace404JSONResponse{
			Code:    api.ErrorCodeNotFound,
			Message: "Workspace not found",
		}, nil
	}

	wikiStore := a.wikiStore
	if wikiStore != nil {
		fileExists, directoryExists, err := wikiStore.PathExists(ctx, ws.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect workspace Wiki page path: %w", err)
		}
		if fileExists || directoryExists {
			return api.DeleteWorkspace409JSONResponse{
				Code:    api.ErrorCodeConflict,
				Message: "Delete workspace Wiki pages before deleting the workspace",
			}, nil
		}
	}

	if err := a.workspaceStore.Delete(ctx, request.WorkspaceId); err != nil {
		if errors.Is(err, workspace.ErrWorkspaceNotFound) {
			return api.DeleteWorkspace404JSONResponse{
				Code:    api.ErrorCodeNotFound,
				Message: "Workspace not found",
			}, nil
		}
		return nil, fmt.Errorf("failed to delete workspace: %w", err)
	}

	a.logAudit(ctx, audit.CategoryWorkspace, "workspace_delete", map[string]string{
		"id":   ws.ID,
		"name": ws.Name,
	})

	return api.DeleteWorkspace204Response{}, nil
}

func toWorkspaceResponse(ws *workspace.Workspace) api.WorkspaceResponse {
	resp := api.WorkspaceResponse{
		Id:   ws.ID,
		Name: ws.Name,
	}
	if ws.Description != "" {
		resp.Description = ptrOf(ws.Description)
	}
	if !ws.CreatedAt.IsZero() {
		resp.CreatedAt = ptrOf(ws.CreatedAt)
	}
	if !ws.UpdatedAt.IsZero() {
		resp.UpdatedAt = ptrOf(ws.UpdatedAt)
	}
	return resp
}
