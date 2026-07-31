// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package authmapping maps external group memberships to Dagu authorization.
package authmapping

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	// DefaultWorkspaceAccessAll grants the fallback role in every workspace.
	DefaultWorkspaceAccessAll = "all"
	// DefaultWorkspaceAccessNone grants no named workspaces on fallback.
	DefaultWorkspaceAccessNone = "none"
)

// ErrNoMatch is returned when strict mapping finds no matching group.
var ErrNoMatch = errors.New("no authorization mapping matched")

// WorkspaceGrantConfig maps a group membership to a role in one workspace.
type WorkspaceGrantConfig struct {
	Workspace string
	Role      auth.Role
}

// Config defines group-based authorization mapping.
type Config struct {
	DefaultRole            auth.Role
	GroupMappings          map[string]auth.Role
	WorkspaceMappings      map[string][]WorkspaceGrantConfig
	DefaultWorkspaceAccess string
	Strict                 bool
}

// Result is the authorization selected for a set of groups.
type Result struct {
	Role            auth.Role
	WorkspaceAccess *auth.WorkspaceAccess
	MatchCount      int
}

// Mapper is an immutable, compiled group authorization mapper.
type Mapper struct {
	defaultRole            auth.Role
	groupMappings          map[string]auth.Role
	workspaceMappings      map[string][]auth.WorkspaceGrant
	defaultWorkspaceAccess string
	strict                 bool
}

// New compiles group-based authorization configuration.
func New(config Config) (*Mapper, error) {
	if !config.DefaultRole.Valid() {
		return nil, fmt.Errorf("invalid default role %q", config.DefaultRole)
	}

	defaultWorkspaceAccess := config.DefaultWorkspaceAccess
	if defaultWorkspaceAccess == "" {
		defaultWorkspaceAccess = DefaultWorkspaceAccessAll
	}
	if defaultWorkspaceAccess != DefaultWorkspaceAccessAll && defaultWorkspaceAccess != DefaultWorkspaceAccessNone {
		return nil, fmt.Errorf("invalid default workspace access %q: must be all or none", config.DefaultWorkspaceAccess)
	}

	workspaceMappings, err := compileWorkspaceMappings(config.WorkspaceMappings)
	if err != nil {
		return nil, err
	}

	groupMappings := make(map[string]auth.Role, len(config.GroupMappings))
	for group, role := range config.GroupMappings {
		if !role.Valid() {
			return nil, fmt.Errorf("invalid global role mapping for group %q: invalid role %q", group, role)
		}
		groupMappings[group] = role
	}

	return &Mapper{
		defaultRole:            config.DefaultRole,
		groupMappings:          groupMappings,
		workspaceMappings:      workspaceMappings,
		defaultWorkspaceAccess: defaultWorkspaceAccess,
		strict:                 config.Strict,
	}, nil
}

func compileWorkspaceMappings(input map[string][]WorkspaceGrantConfig) (map[string][]auth.WorkspaceGrant, error) {
	groups := make([]string, 0, len(input))
	for group := range input {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	compiled := make(map[string][]auth.WorkspaceGrant, len(input))
	for _, group := range groups {
		grants := input[group]
		if strings.TrimSpace(group) == "" {
			return nil, errors.New("workspace mapping group must not be blank")
		}
		if len(grants) == 0 {
			return nil, fmt.Errorf("workspace mapping for group %q must contain at least one grant", group)
		}

		mapped := make([]auth.WorkspaceGrant, 0, len(grants))
		seenWorkspaces := make(map[string]struct{}, len(grants))
		for _, grant := range grants {
			if err := workspace.ValidateName(grant.Workspace); err != nil {
				return nil, fmt.Errorf("invalid workspace mapping for group %q: workspace %q: %w", group, grant.Workspace, err)
			}
			if _, ok := seenWorkspaces[grant.Workspace]; ok {
				return nil, fmt.Errorf("duplicate workspace %q in mapping for group %q", grant.Workspace, group)
			}
			seenWorkspaces[grant.Workspace] = struct{}{}
			if !grant.Role.Valid() {
				return nil, fmt.Errorf("invalid workspace mapping for group %q and workspace %q: invalid role %q", group, grant.Workspace, grant.Role)
			}
			if grant.Role == auth.RoleAdmin {
				return nil, fmt.Errorf("invalid workspace mapping for group %q and workspace %q: admin cannot be scoped to a workspace", group, grant.Workspace)
			}
			mapped = append(mapped, auth.WorkspaceGrant{Workspace: grant.Workspace, Role: grant.Role})
		}
		compiled[group] = mapped
	}
	return compiled, nil
}

// Map selects global and workspace authorization for groups.
func (m *Mapper) Map(groups []string) (Result, error) {
	var bestGlobal auth.Role
	mergedGrants := make(map[string]auth.Role)
	matchCount := 0

	for _, group := range groups {
		matched := false
		if role, ok := m.groupMappings[group]; ok {
			matched = true
			if rolePriority(role) > rolePriority(bestGlobal) {
				bestGlobal = role
			}
		}
		if grants, ok := m.workspaceMappings[group]; ok {
			matched = true
			for _, grant := range grants {
				current, exists := mergedGrants[grant.Workspace]
				if !exists || rolePriority(grant.Role) > rolePriority(current) {
					mergedGrants[grant.Workspace] = grant.Role
				}
			}
		}
		if matched {
			matchCount++
		}
	}

	if bestGlobal.Valid() {
		return Result{Role: bestGlobal, WorkspaceAccess: auth.AllWorkspaceAccess(), MatchCount: matchCount}, nil
	}
	if len(mergedGrants) > 0 {
		grants := make([]auth.WorkspaceGrant, 0, len(mergedGrants))
		for workspaceName, role := range mergedGrants {
			grants = append(grants, auth.WorkspaceGrant{Workspace: workspaceName, Role: role})
		}
		sort.Slice(grants, func(i, j int) bool { return grants[i].Workspace < grants[j].Workspace })
		return Result{
			Role:            auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: grants},
			MatchCount:      matchCount,
		}, nil
	}
	if m.strict {
		return Result{MatchCount: matchCount}, ErrNoMatch
	}
	if m.defaultWorkspaceAccess == DefaultWorkspaceAccessNone {
		return Result{
			Role:            auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}},
			MatchCount:      matchCount,
		}, nil
	}
	return Result{Role: m.defaultRole, WorkspaceAccess: auth.AllWorkspaceAccess(), MatchCount: matchCount}, nil
}

// MapRole selects only the global role for groups.
func (m *Mapper) MapRole(groups []string) (auth.Role, error) {
	var best auth.Role
	for _, group := range groups {
		role, ok := m.groupMappings[group]
		if ok && rolePriority(role) > rolePriority(best) {
			best = role
		}
	}
	if best.Valid() {
		return best, nil
	}
	if m.strict {
		return auth.RoleNone, ErrNoMatch
	}
	return m.defaultRole, nil
}

// WorkspaceAccessPolicyActive reports whether mapping controls workspace access.
func (m *Mapper) WorkspaceAccessPolicyActive() bool {
	return len(m.workspaceMappings) > 0 || m.defaultWorkspaceAccess == DefaultWorkspaceAccessNone
}

func rolePriority(role auth.Role) int {
	switch role {
	case auth.RoleAdmin:
		return 5
	case auth.RoleManager:
		return 4
	case auth.RoleDeveloper:
		return 3
	case auth.RoleOperator:
		return 2
	case auth.RoleViewer:
		return 1
	case auth.RoleNone:
		return 0
	default:
		return 0
	}
}
