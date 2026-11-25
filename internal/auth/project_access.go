// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"fmt"
)

// ErrInvalidProjectGrant is returned when project grants are malformed.
var ErrInvalidProjectGrant = errors.New("invalid project grant")

// ProjectGrant gives a user or API key a role in one project.
type ProjectGrant struct {
	// ProjectID identifies the project. A project created again under a
	// deleted one's name has a new ID, so the grant does not carry over.
	ProjectID string `json:"project_id"`
	// Role is below RoleAdmin, which applies only server-wide.
	Role Role `json:"role"`
}

// RoleInProject returns the role that applies in the project with the given
// ID: the higher of serverRole and the role grants give that project.
// RoleNone means no access to the project. A grant never gives RoleAdmin.
func RoleInProject(serverRole Role, grants []ProjectGrant, projectID string) Role {
	role := RoleNone
	if serverRole.Valid() {
		role = serverRole
	}
	for _, grant := range grants {
		if grant.ProjectID == projectID && grant.Role != RoleAdmin && grant.Role.Outranks(role) {
			role = grant.Role
		}
	}
	return role
}

// ValidateProjectGrants returns an error wrapping ErrInvalidProjectGrant when
// a grant lacks a project ID, repeats a project, has a role that is not valid
// or is RoleAdmin, or names a project for which projectExists returns false.
// A nil projectExists accepts every project ID.
func ValidateProjectGrants(grants []ProjectGrant, projectExists func(id string) bool) error {
	seen := make(map[string]struct{}, len(grants))
	for _, grant := range grants {
		if grant.ProjectID == "" {
			return fmt.Errorf("%w: project ID is required", ErrInvalidProjectGrant)
		}
		if _, ok := seen[grant.ProjectID]; ok {
			return fmt.Errorf("%w: more than one grant on project %q", ErrInvalidProjectGrant, grant.ProjectID)
		}
		seen[grant.ProjectID] = struct{}{}

		if !grant.Role.Valid() {
			return fmt.Errorf("%w: invalid role %q", ErrInvalidProjectGrant, grant.Role)
		}
		if grant.Role == RoleAdmin {
			return fmt.Errorf("%w: admin applies only server-wide", ErrInvalidProjectGrant)
		}
		if projectExists != nil && !projectExists(grant.ProjectID) {
			return fmt.Errorf("%w: project %q does not exist", ErrInvalidProjectGrant, grant.ProjectID)
		}
	}
	return nil
}
