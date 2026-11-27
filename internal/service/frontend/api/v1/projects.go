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
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/service/projectaccess"
)

// ProjectLifecycle creates and deletes the projects of the registry that
// the API's project access reads. Create, Delete, and the CLI's project
// commands refuse the same requests.
type ProjectLifecycle interface {
	// Create registers a project. It refuses with an error wrapping
	// project.ErrInvalidName, project.ErrInvalidDisplayName,
	// project.ErrNameTaken, project.ErrStateDirTaken, or
	// project.ErrProtectedPath.
	Create(ctx context.Context, in project.CreateInput) (project.Project, error)
	// SetDisplayName changes the display name of the project with the
	// given ID, as project.Registry does.
	SetDisplayName(ctx context.Context, id, displayName string) (project.Project, error)
	// Delete deletes the project called name. It refuses with an error
	// wrapping project.ErrNotFound, project.ErrDefaultProject, or
	// project.ErrDeleteRefused.
	Delete(ctx context.Context, name string) (project.Project, error)
	// State returns the state of p, a registered project.
	State(ctx context.Context, p project.Project) (api.ProjectState, error)
	// Unattached returns the state directories that record a project the
	// registry lacks.
	Unattached(ctx context.Context) ([]api.UnattachedStateDir, error)
}

// WithProjectLifecycle sets what creates and deletes projects. The
// /projects paths answer only with it and project access set, while
// projects are on.
func WithProjectLifecycle(l ProjectLifecycle) APIOption {
	return func(a *API) {
		a.projectLifecycle = l
	}
}

// ListProjects lists the projects the caller has a role in.
func (a *API) ListProjects(ctx context.Context, _ api.ListProjectsRequestObject) (api.ListProjectsResponseObject, error) {
	caller, err := a.projectsCaller(ctx)
	if err != nil {
		return nil, err
	}
	visible, err := a.projectAccess.List(ctx, caller)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	slices.SortStableFunc(visible, func(x, y projectaccess.Access) int {
		return project.CompareNames(x.Project.Name, y.Project.Name)
	})
	resp := api.ListProjects200JSONResponse{Projects: make([]api.Project, 0, len(visible))}
	for _, access := range visible {
		p, err := a.toAPIProject(ctx, access)
		if err != nil {
			return nil, err
		}
		resp.Projects = append(resp.Projects, p)
	}
	if caller.Role.IsAdmin() {
		unattached, err := a.projectLifecycle.Unattached(ctx)
		if err != nil {
			return nil, fmt.Errorf("list unattached state directories: %w", err)
		}
		resp.UnattachedStateDirs = &unattached
	}
	return resp, nil
}

// CreateProject registers a project.
func (a *API) CreateProject(ctx context.Context, request api.CreateProjectRequestObject) (api.CreateProjectResponseObject, error) {
	caller, err := a.projectsAdmin(ctx)
	if err != nil {
		return nil, err
	}
	in := project.CreateInput{Name: request.Body.Name, DisplayName: valueOf(request.Body.DisplayName)}
	created, err := a.projectLifecycle.Create(ctx, in)
	if err != nil {
		return nil, projectError(err)
	}
	a.logAudit(ctx, audit.CategorySystem, "project_create", map[string]string{
		"project_id": created.ID,
		"project":    created.Name,
	})
	p, err := a.toAPIProject(ctx, projectaccess.Access{Project: created, Role: caller.Role})
	if err != nil {
		return nil, err
	}
	return api.CreateProject201JSONResponse(p), nil
}

// UpdateProject changes a project's display name.
func (a *API) UpdateProject(ctx context.Context, request api.UpdateProjectRequestObject) (api.UpdateProjectResponseObject, error) {
	caller, err := a.projectsAdmin(ctx)
	if err != nil {
		return nil, err
	}
	if request.Body.DisplayName == nil {
		return nil, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: "displayName is required"}
	}
	access, err := a.projectAccess.Get(ctx, caller, request.ProjectName)
	if err != nil {
		return nil, projectError(err)
	}
	updated, err := a.projectLifecycle.SetDisplayName(ctx, access.Project.ID, *request.Body.DisplayName)
	if err != nil {
		return nil, projectError(err)
	}
	a.logAudit(ctx, audit.CategorySystem, "project_update", map[string]string{
		"project_id":   updated.ID,
		"project":      updated.Name,
		"display_name": updated.DisplayName,
	})
	p, err := a.toAPIProject(ctx, projectaccess.Access{Project: updated, Role: access.Role})
	if err != nil {
		return nil, err
	}
	return api.UpdateProject200JSONResponse(p), nil
}

// DeleteProject deletes a project once every live process has released it.
func (a *API) DeleteProject(ctx context.Context, request api.DeleteProjectRequestObject) (api.DeleteProjectResponseObject, error) {
	if _, err := a.projectsAdmin(ctx); err != nil {
		return nil, err
	}
	// The delete removes grants from API keys, which other key writes must
	// not overwrite.
	a.apiKeyWriteMu.Lock()
	deleted, err := a.projectLifecycle.Delete(ctx, request.ProjectName)
	a.apiKeyWriteMu.Unlock()
	if err != nil {
		return nil, projectError(err)
	}
	a.logAudit(ctx, audit.CategorySystem, "project_delete", map[string]string{
		"project_id": deleted.ID,
		"project":    deleted.Name,
	})
	return api.DeleteProject204Response{}, nil
}

// projectsCaller returns the user whose access the /projects paths check:
// the caller, or an admin when roles do not apply to the caller. The paths
// are not found while projects are off.
func (a *API) projectsCaller(ctx context.Context) (*auth.User, error) {
	if a.config == nil || !a.config.ProjectsEnabled || a.projectAccess == nil || a.projectLifecycle == nil {
		return nil, &Error{HTTPStatus: http.StatusNotFound, Code: api.ErrorCodeNotFound, Message: "not found"}
	}
	if !a.rolesApply(ctx) {
		return &auth.User{Role: auth.RoleAdmin}, nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, errAuthRequired
	}
	return user, nil
}

// projectsAdmin is projectsCaller for a path that only admins may use.
func (a *API) projectsAdmin(ctx context.Context) (*auth.User, error) {
	caller, err := a.projectsCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.Role.IsAdmin() {
		return nil, errInsufficientPermissions
	}
	return caller, nil
}

func (a *API) toAPIProject(ctx context.Context, access projectaccess.Access) (api.Project, error) {
	state, err := a.projectLifecycle.State(ctx, access.Project)
	if err != nil {
		return api.Project{}, fmt.Errorf("read the state of project %s: %w", access.Project.Name, err)
	}
	return api.Project{
		Name:        access.Project.Name,
		DisplayName: access.Project.DisplayName,
		CreatedAt:   access.Project.CreatedAt,
		State:       state,
		Role:        api.UserRole(access.Role),
	}, nil
}

// isProjectDelete reports whether r deletes a project.
func isProjectDelete(r *http.Request, apiBasePath string) bool {
	name, ok := strings.CutPrefix(r.URL.Path, strings.TrimRight(apiBasePath, "/")+"/projects/")
	return r.Method == http.MethodDelete && ok && name != "" && !strings.Contains(name, "/")
}

// projectError answers a refused project request with its status, and
// passes other errors on.
func projectError(err error) error {
	status, code := 0, api.ErrorCodeConflict
	switch {
	case errors.Is(err, project.ErrNotFound):
		status, code = http.StatusNotFound, api.ErrorCodeNotFound
	case errors.Is(err, project.ErrInvalidName), errors.Is(err, project.ErrInvalidDisplayName):
		status, code = http.StatusBadRequest, api.ErrorCodeBadRequest
	case errors.Is(err, project.ErrNameTaken):
		status, code = http.StatusConflict, api.ErrorCodeAlreadyExists
	case errors.Is(err, project.ErrStateDirTaken), errors.Is(err, project.ErrProtectedPath),
		errors.Is(err, project.ErrDeleteRefused), errors.Is(err, project.ErrDefaultProject):
		status = http.StatusConflict
	default:
		return err
	}
	return &Error{HTTPStatus: status, Code: code, Message: err.Error()}
}
