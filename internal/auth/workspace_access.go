// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	// ErrInvalidWorkspaceAccess is returned when workspace grants are malformed.
	ErrInvalidWorkspaceAccess = errors.New("invalid workspace access")
)

// WorkspaceGrant assigns a role for one workspace.
type WorkspaceGrant struct {
	Workspace string `json:"workspace"`
	Role      Role   `json:"role"`
}

// WorkspaceAccess controls which workspaces a user or API key can access.
//
// Missing workspace access is treated as All=true by NormalizeWorkspaceAccess
// for backward compatibility with existing users and API keys.
type WorkspaceAccess struct {
	All    bool             `json:"all"`
	Grants []WorkspaceGrant `json:"grants,omitempty"`
}

// AllWorkspaceAccess returns an all-workspaces access policy.
func AllWorkspaceAccess() *WorkspaceAccess {
	return &WorkspaceAccess{All: true}
}

// NormalizeWorkspaceAccess returns a stable, non-nil workspace access value.
func NormalizeWorkspaceAccess(access *WorkspaceAccess) WorkspaceAccess {
	if access == nil {
		return WorkspaceAccess{All: true}
	}
	if access.All {
		return WorkspaceAccess{All: true}
	}

	grants := make([]WorkspaceGrant, 0, len(access.Grants))
	for _, grant := range access.Grants {
		grants = append(grants, WorkspaceGrant{
			Workspace: strings.TrimSpace(grant.Workspace),
			Role:      grant.Role,
		})
	}
	return WorkspaceAccess{
		All:    false,
		Grants: grants,
	}
}

// CloneWorkspaceAccess returns a normalized copy suitable for storage.
func CloneWorkspaceAccess(access *WorkspaceAccess) *WorkspaceAccess {
	normalized := NormalizeWorkspaceAccess(access)
	grants := make([]WorkspaceGrant, len(normalized.Grants))
	copy(grants, normalized.Grants)
	return &WorkspaceAccess{
		All:    normalized.All,
		Grants: grants,
	}
}

// WorkspaceAccessEqual reports whether two policies grant the same workspace access.
// Grant order and surrounding workspace whitespace do not affect equality.
func WorkspaceAccessEqual(left, right *WorkspaceAccess) bool {
	leftNormalized := NormalizeWorkspaceAccess(left)
	rightNormalized := NormalizeWorkspaceAccess(right)
	if leftNormalized.All != rightNormalized.All {
		return false
	}
	if leftNormalized.All {
		return true
	}

	slices.SortFunc(leftNormalized.Grants, compareWorkspaceGrants)
	slices.SortFunc(rightNormalized.Grants, compareWorkspaceGrants)
	return slices.Equal(leftNormalized.Grants, rightNormalized.Grants)
}

func compareWorkspaceGrants(left, right WorkspaceGrant) int {
	if result := strings.Compare(left.Workspace, right.Workspace); result != 0 {
		return result
	}
	return strings.Compare(string(left.Role), string(right.Role))
}

// EffectiveRole returns the role that applies to a workspace.
//
// Empty workspace names represent unlabelled resources and are governed by the
// global role so non-workspace workflows remain visible to all authenticated users.
func EffectiveRole(globalRole Role, access *WorkspaceAccess, workspaceName string) (Role, bool) {
	workspaceName = strings.TrimSpace(workspaceName)
	if workspaceName == "" {
		return globalRole, true
	}

	normalized := NormalizeWorkspaceAccess(access)
	if normalized.All {
		return globalRole, true
	}
	for _, grant := range normalized.Grants {
		if grant.Workspace == workspaceName {
			return grant.Role, true
		}
	}
	return RoleNone, false
}

// HasWorkspaceAccess reports whether a workspace is visible to the policy.
func HasWorkspaceAccess(access *WorkspaceAccess, workspaceName string) bool {
	_, ok := EffectiveRole(RoleViewer, access, workspaceName)
	return ok
}

// ValidateWorkspaceAccess validates role invariants and workspace names.
func ValidateWorkspaceAccess(globalRole Role, access *WorkspaceAccess, workspaceExists func(string) bool) error {
	if !globalRole.Valid() {
		return fmt.Errorf("%w: invalid global role %q", ErrInvalidWorkspaceAccess, globalRole)
	}
	if access != nil && access.All && len(access.Grants) != 0 {
		return fmt.Errorf("%w: all-workspaces access cannot include workspace grants", ErrInvalidWorkspaceAccess)
	}

	normalized := NormalizeWorkspaceAccess(access)
	if normalized.All {
		return nil
	}

	if globalRole != RoleViewer {
		return fmt.Errorf("%w: scoped workspace access requires global role viewer", ErrInvalidWorkspaceAccess)
	}
	if len(normalized.Grants) == 0 {
		return fmt.Errorf("%w: scoped workspace access requires at least one workspace grant", ErrInvalidWorkspaceAccess)
	}

	seen := make(map[string]struct{}, len(normalized.Grants))
	for _, grant := range normalized.Grants {
		workspaceName := strings.TrimSpace(grant.Workspace)
		if workspaceName == "" {
			return fmt.Errorf("%w: workspace name is required", ErrInvalidWorkspaceAccess)
		}
		if _, ok := seen[workspaceName]; ok {
			return fmt.Errorf("%w: duplicate workspace grant %q", ErrInvalidWorkspaceAccess, workspaceName)
		}
		seen[workspaceName] = struct{}{}

		if !grant.Role.Valid() {
			return fmt.Errorf("%w: invalid grant role %q", ErrInvalidWorkspaceAccess, grant.Role)
		}
		if grant.Role == RoleAdmin {
			return fmt.Errorf("%w: admin cannot be scoped to a workspace", ErrInvalidWorkspaceAccess)
		}
		if workspaceExists != nil && !workspaceExists(workspaceName) {
			return fmt.Errorf("%w: workspace %q does not exist", ErrInvalidWorkspaceAccess, workspaceName)
		}
	}

	return nil
}
