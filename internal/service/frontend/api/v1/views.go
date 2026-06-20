// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/view"
	"github.com/google/uuid"
)

func viewStoreUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "View store not configured",
	}
}

func viewBadRequest(message string) api.Error {
	return api.Error{Code: api.ErrorCodeBadRequest, Message: message}
}

func viewNotFound() api.Error {
	return api.Error{Code: api.ErrorCodeNotFound, Message: "View not found"}
}

// ListViews returns the saved views visible to the caller. Workspace-scoped
// views are returned only when the caller can access that workspace;
// all-workspace views (empty workspace) are visible to every authenticated user.
func (a *API) ListViews(ctx context.Context, _ api.ListViewsRequestObject) (api.ListViewsResponseObject, error) {
	if a.viewStore == nil {
		return nil, viewStoreUnavailable()
	}
	views, err := a.viewStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list views: %w", err)
	}
	out := make([]api.View, 0, len(views))
	for _, v := range views {
		if !a.canViewViewWorkspace(ctx, v.Workspace) {
			continue
		}
		out = append(out, toViewResponse(v))
	}
	return api.ListViews200JSONResponse{Views: out}, nil
}

// CreateView creates a saved view. Requires write access to the target
// workspace (the caller's effective role for that workspace).
func (a *API) CreateView(ctx context.Context, request api.CreateViewRequestObject) (api.CreateViewResponseObject, error) {
	if a.viewStore == nil {
		return nil, viewStoreUnavailable()
	}
	if request.Body == nil {
		return api.CreateView400JSONResponse(viewBadRequest("Request body is required")), nil
	}

	now := time.Now().UTC()
	v := viewFromSpec(*request.Body)
	v.ID = uuid.NewString()
	v.CreatedBy = viewActor(ctx)
	v.CreatedAt = now
	v.UpdatedAt = now
	v.Normalize()
	if err := v.Validate(); err != nil {
		return api.CreateView400JSONResponse(viewBadRequest(err.Error())), nil
	}
	if err := a.requireViewWriteForWorkspace(ctx, v.Workspace); err != nil {
		return nil, err
	}

	if err := a.viewStore.Create(ctx, v); err != nil {
		if errors.Is(err, view.ErrViewExists) {
			return api.CreateView400JSONResponse(viewBadRequest("View already exists")), nil
		}
		return nil, fmt.Errorf("failed to create view: %w", err)
	}

	a.logAudit(ctx, audit.CategorySystem, "view_create", viewAuditDetails(v))
	return api.CreateView201JSONResponse(toViewResponse(v)), nil
}

// GetView returns a single view by ID. It returns 404 when the view is scoped
// to a workspace the caller cannot access, so its existence is not revealed.
func (a *API) GetView(ctx context.Context, request api.GetViewRequestObject) (api.GetViewResponseObject, error) {
	if a.viewStore == nil {
		return nil, viewStoreUnavailable()
	}
	v, err := a.viewStore.GetByID(ctx, request.ViewId)
	if err != nil {
		if errors.Is(err, view.ErrViewNotFound) {
			return api.GetView404JSONResponse(viewNotFound()), nil
		}
		return nil, err
	}
	if !a.canViewViewWorkspace(ctx, v.Workspace) {
		return api.GetView404JSONResponse(viewNotFound()), nil
	}
	return api.GetView200JSONResponse(toViewResponse(v)), nil
}

// UpdateView replaces a view's configuration. Requires write access to both the
// existing and the new target workspace. ID, CreatedBy, and CreatedAt are
// preserved from the existing record.
func (a *API) UpdateView(ctx context.Context, request api.UpdateViewRequestObject) (api.UpdateViewResponseObject, error) {
	if a.viewStore == nil {
		return nil, viewStoreUnavailable()
	}

	existing, err := a.viewStore.GetByID(ctx, request.ViewId)
	if err != nil {
		if errors.Is(err, view.ErrViewNotFound) {
			return api.UpdateView404JSONResponse(viewNotFound()), nil
		}
		return nil, err
	}
	if !a.canViewViewWorkspace(ctx, existing.Workspace) {
		return api.UpdateView404JSONResponse(viewNotFound()), nil
	}
	if err := a.requireViewWriteForWorkspace(ctx, existing.Workspace); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return api.UpdateView400JSONResponse(viewBadRequest("Request body is required")), nil
	}

	updated := viewFromSpec(*request.Body)
	updated.ID = existing.ID
	updated.CreatedBy = existing.CreatedBy
	updated.CreatedAt = existing.CreatedAt
	updated.Normalize()
	if err := updated.Validate(); err != nil {
		return api.UpdateView400JSONResponse(viewBadRequest(err.Error())), nil
	}
	if err := a.requireViewWriteForWorkspace(ctx, updated.Workspace); err != nil {
		return nil, err
	}

	if err := a.viewStore.Update(ctx, updated); err != nil {
		if errors.Is(err, view.ErrViewNotFound) {
			return api.UpdateView404JSONResponse(viewNotFound()), nil
		}
		return nil, fmt.Errorf("failed to update view: %w", err)
	}

	a.logAudit(ctx, audit.CategorySystem, "view_update", viewAuditDetails(updated))
	return api.UpdateView200JSONResponse(toViewResponse(updated)), nil
}

// DeleteView removes a view by ID. Requires write access to the view's workspace.
func (a *API) DeleteView(ctx context.Context, request api.DeleteViewRequestObject) (api.DeleteViewResponseObject, error) {
	if a.viewStore == nil {
		return nil, viewStoreUnavailable()
	}

	existing, err := a.viewStore.GetByID(ctx, request.ViewId)
	if err != nil {
		if errors.Is(err, view.ErrViewNotFound) {
			return api.DeleteView404JSONResponse(viewNotFound()), nil
		}
		return nil, err
	}
	if !a.canViewViewWorkspace(ctx, existing.Workspace) {
		return api.DeleteView404JSONResponse(viewNotFound()), nil
	}
	if err := a.requireViewWriteForWorkspace(ctx, existing.Workspace); err != nil {
		return nil, err
	}

	if err := a.viewStore.Delete(ctx, request.ViewId); err != nil {
		if errors.Is(err, view.ErrViewNotFound) {
			return api.DeleteView404JSONResponse(viewNotFound()), nil
		}
		return nil, fmt.Errorf("failed to delete view: %w", err)
	}

	a.logAudit(ctx, audit.CategorySystem, "view_delete", map[string]any{"id": request.ViewId})
	return api.DeleteView204Response{}, nil
}

// canViewViewWorkspace reports whether the caller may see a view scoped to the
// given workspace. All-workspace views (empty workspace) are visible to every
// authenticated user; a workspace-scoped view is visible only to callers who
// can access that workspace.
func (a *API) canViewViewWorkspace(ctx context.Context, workspace string) bool {
	return workspace == "" || a.canAccessWorkspace(ctx, workspace)
}

// requireViewWriteForWorkspace authorizes a write to a view scoped to the given
// workspace using the caller's effective role for that workspace, so a
// workspace-scoped developer (a global viewer with a per-workspace write grant)
// can manage views in workspaces they can write. All-workspace views (empty
// workspace) are governed by the global role.
func (a *API) requireViewWriteForWorkspace(ctx context.Context, workspace string) error {
	role, ok, err := a.effectiveRoleForWorkspace(ctx, workspace)
	if err != nil {
		return err
	}
	if !ok || !role.CanWrite() {
		return errInsufficientPermissions
	}
	return nil
}

func viewFromSpec(spec api.ViewSpec) *view.View {
	v := &view.View{
		Name:         spec.Name,
		IntervalDays: spec.IntervalDays,
		Workspace:    valueOf(spec.Workspace),
		DAGName:      valueOf(spec.DagName),
		Pinned:       valueOf(spec.Pinned),
	}
	if spec.Type != nil {
		v.Type = string(*spec.Type)
	}
	if spec.Labels != nil {
		v.Labels = slices.Clone(*spec.Labels)
	}
	return v
}

func toViewResponse(v *view.View) api.View {
	resp := api.View{
		Id:           v.ID,
		Name:         v.Name,
		Type:         v.Type,
		IntervalDays: v.IntervalDays,
		CreatedAt:    v.CreatedAt,
		UpdatedAt:    v.UpdatedAt,
	}
	resp.Workspace = ptrOf(v.Workspace)
	resp.DagName = ptrOf(v.DAGName)
	resp.Pinned = ptrOf(v.Pinned)
	resp.CreatedBy = ptrOf(v.CreatedBy)
	if len(v.Labels) > 0 {
		labels := slices.Clone(v.Labels)
		resp.Labels = &labels
	}
	return resp
}

// viewActor returns the display name recorded as a view's creator. It prefers
// the username, falls back to the user ID, and defaults to "admin" when no
// user is present (no-auth mode).
func viewActor(ctx context.Context) string {
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		if user.Username != "" {
			return user.Username
		}
		if user.ID != "" {
			return user.ID
		}
	}
	return "admin"
}

func viewAuditDetails(v *view.View) map[string]any {
	return map[string]any{
		"id":   v.ID,
		"name": v.Name,
		"type": v.Type,
	}
}
