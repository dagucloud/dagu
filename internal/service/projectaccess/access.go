// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package projectaccess decides which projects a user can see and the role
// the user has in each.
package projectaccess

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// Access is a project a user has a role in.
type Access struct {
	Project project.Project
	// Role is the higher of the user's server-wide role and the user's
	// grant on the project. It is never auth.RoleNone.
	Role auth.Role
}

// Checker decides access to the projects of a registry.
//
// A nil user has no role in any project. A caller to which roles do not
// apply, such as a request under auth mode none, passes a user with
// auth.RoleAdmin. A user limited to some workspaces has no server-wide role,
// only its project grants.
type Checker struct {
	registry project.Registry
}

// New returns a Checker for the projects of registry.
func New(registry project.Registry) *Checker {
	return &Checker{registry: registry}
}

// Get returns the project whose name matches name without regard to case,
// and the user's role in it. It returns the same error, wrapping
// project.ErrNotFound, when no project has the name and when the user has no
// role in it, so a hidden project cannot be told from a missing one.
func (c *Checker) Get(ctx context.Context, user *auth.User, name string) (Access, error) {
	p, err := c.registry.Get(ctx, name)
	if errors.Is(err, project.ErrNotFound) {
		return Access{}, notFound(name)
	}
	if err != nil {
		return Access{}, err
	}
	role := roleIn(user, p)
	if !role.IsSet() {
		return Access{}, notFound(name)
	}
	return Access{Project: p, Role: role}, nil
}

// List returns the projects the user has a role in, ordered by name.
func (c *Checker) List(ctx context.Context, user *auth.User) ([]Access, error) {
	projects, err := c.registry.List(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]Access, 0, len(projects))
	for _, p := range projects {
		if role := roleIn(user, p); role.IsSet() {
			visible = append(visible, Access{Project: p, Role: role})
		}
	}
	return visible, nil
}

func roleIn(user *auth.User, p project.Project) auth.Role {
	if user == nil {
		return auth.RoleNone
	}
	// Workspace rules require RoleViewer of a user limited to some
	// workspaces, so that role does not reach every project.
	return auth.RoleInProject(auth.ServerRole(user.Role, user.WorkspaceAccess), user.ProjectGrants, p.ID)
}

func notFound(name string) error {
	return fmt.Errorf("%w: %q", project.ErrNotFound, name)
}
