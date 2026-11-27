// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/service/projectaccess"
)

// apiKeyManager is the caller of an API key route and the keys it manages.
//
// An admin manages every key. While projects are on, a manager of a project
// also manages each key whose only roles are grants on projects in which the
// caller is manager, up to the manager role, and that is a service account
// or owned by the caller. A key with a server-wide role, a workspace grant,
// or another user as owner needs an admin.
type apiKeyManager struct {
	// user is the caller while projects are on and roles apply to it.
	user  *auth.User
	admin bool
	// projectsOn reports that the API surface of projects is on.
	projectsOn bool
	// projects holds the projects the caller can see, by ID.
	projects map[string]projectaccess.Access
}

// apiKeyManager returns what the caller of an API key route manages. While
// projects are off, the caller must be an admin, as before projects.
func (a *API) apiKeyManager(ctx context.Context) (*apiKeyManager, error) {
	if a.projectAccess == nil {
		if err := a.requireAdmin(ctx); err != nil {
			return nil, err
		}
		return &apiKeyManager{admin: true}, nil
	}

	manager := &apiKeyManager{projectsOn: true}
	checkedUser := &auth.User{Role: auth.RoleAdmin}
	if a.rolesApply(ctx) {
		user, ok := auth.UserFromContext(ctx)
		if !ok {
			return nil, errAuthRequired
		}
		manager.user, checkedUser = user, user
	}
	manager.admin = checkedUser.Role.IsAdmin()

	visible, err := a.projectAccess.List(ctx, checkedUser)
	if err != nil {
		return nil, fmt.Errorf("list projects for API key access: %w", err)
	}
	manager.projects = make(map[string]projectaccess.Access, len(visible))
	managesProject := false
	for _, access := range visible {
		manager.projects[access.Project.ID] = access
		managesProject = managesProject || access.Role == auth.RoleManager
	}
	if !manager.admin && !managesProject {
		return nil, errInsufficientPermissions
	}
	return manager, nil
}

// manages reports whether the caller may create, see, change, or delete a key
// with the workspace access, project grants, and owner of key.
func (m *apiKeyManager) manages(key *auth.APIKey) bool {
	if m.admin {
		return true
	}
	if key.AttributionClass == auth.APIKeyAttributionUserOwned && (m.user == nil || m.user.ID != key.OwnerUserID) {
		return false
	}
	access := auth.NormalizeWorkspaceAccess(key.WorkspaceAccess)
	if access.All || len(access.Grants) > 0 || len(key.ProjectGrants) == 0 {
		return false
	}
	for _, grant := range key.ProjectGrants {
		if project, ok := m.projects[grant.ProjectID]; !ok || project.Role != auth.RoleManager {
			return false
		}
	}
	return true
}

// projectGrants resolves the project names of grants. A project the caller
// cannot see is reported as one that does not exist.
func (m *apiKeyManager) projectGrants(grants []api.ProjectGrant) ([]auth.ProjectGrant, error) {
	idsByName := make(map[string]string, len(m.projects))
	for id, access := range m.projects {
		idsByName[strings.ToLower(access.Project.Name)] = id
	}
	resolved := make([]auth.ProjectGrant, 0, len(grants))
	for _, grant := range grants {
		role, err := auth.ParseRole(string(grant.Role))
		if err != nil {
			return nil, badProjectGrantError(fmt.Sprintf("Invalid role for project %q", grant.Project))
		}
		id, ok := idsByName[strings.ToLower(grant.Project)]
		if !ok {
			return nil, badProjectGrantError(fmt.Sprintf("Project %q not found", grant.Project))
		}
		resolved = append(resolved, auth.ProjectGrant{ProjectID: id, Role: role})
	}
	return resolved, nil
}

// refuseGrantsOnDeleting refuses grants on a project that is marked
// deleting or no longer registered. Call it holding apiKeyWriteMu, which a
// project delete holds while it removes the project's grants, so a grant
// written after that removal cannot outlive the project.
func (a *API) refuseGrantsOnDeleting(ctx context.Context, grants []auth.ProjectGrant) error {
	if len(grants) == 0 || a.projectAccess == nil {
		return nil
	}
	registered, err := a.projectAccess.List(ctx, &auth.User{Role: auth.RoleAdmin})
	if err != nil {
		return fmt.Errorf("list projects for API key grants: %w", err)
	}
	for _, grant := range grants {
		i := slices.IndexFunc(registered, func(access projectaccess.Access) bool {
			return access.Project.ID == grant.ProjectID
		})
		if i < 0 {
			return badProjectGrantError("Project not found")
		}
		if registered[i].Project.Deleting {
			return badProjectGrantError(fmt.Sprintf("Project %q is being deleted", registered[i].Project.Name))
		}
	}
	return nil
}

// toAPIKey converts key, naming each project of its grants that the caller
// can see. Grants are left out while projects are off.
func (m *apiKeyManager) toAPIKey(key *auth.APIKey) api.APIKey {
	apiKey := toAPIKey(key)
	if !m.projectsOn || len(key.ProjectGrants) == 0 {
		return apiKey
	}
	grants := make(api.ProjectGrants, 0, len(key.ProjectGrants))
	for _, grant := range key.ProjectGrants {
		if access, ok := m.projects[grant.ProjectID]; ok {
			grants = append(grants, api.ProjectGrant{Project: access.Project.Name, Role: api.UserRole(grant.Role)})
		}
	}
	apiKey.ProjectGrants = &grants
	return apiKey
}

// parseAPIKeyWorkspaceAccess parses the workspace access of a key. A key
// with project grants may be limited to no workspace, which leaves it no
// server-wide role; the auth service checks the rest of that case.
func (a *API) parseAPIKeyWorkspaceAccess(
	ctx context.Context,
	role auth.Role,
	access *api.WorkspaceAccess,
	hasProjectGrants bool,
) (*auth.WorkspaceAccess, error) {
	if hasProjectGrants && access != nil && !access.All && len(access.Grants) == 0 {
		return &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, nil
	}
	return a.parseAndValidateWorkspaceAccess(ctx, role, access)
}

func badProjectGrantError(message string) *Error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

func apiKeyNotFoundError() *Error {
	return &Error{
		Code:       api.ErrorCodeNotFound,
		Message:    "API key not found",
		HTTPStatus: http.StatusNotFound,
	}
}

// errAPIKeyNeedsAdmin is returned when a manager asks for a key that only an
// admin may create or change.
var errAPIKeyNeedsAdmin = &Error{
	Code:       api.ErrorCodeForbidden,
	Message:    "Only an admin can manage an API key with a server-wide role, a workspace grant, a grant on a project the caller does not manage, or another user as owner",
	HTTPStatus: http.StatusForbidden,
}
