// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package authmapping maps external group memberships to Dagu authorization.
package authmapping

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/project"
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

// ProjectGrantConfig maps a group membership to a role in the project with
// the given name.
type ProjectGrantConfig struct {
	Project string
	Role    auth.Role
}

// Config defines group-based authorization mapping.
type Config struct {
	DefaultRole            auth.Role
	GroupMappings          map[string]auth.Role
	WorkspaceMappings      map[string][]WorkspaceGrantConfig
	DefaultWorkspaceAccess string
	Strict                 bool
	// Projects turns project mapping on. ProjectMappings and
	// DefaultProjectAccess then replace WorkspaceMappings and
	// DefaultWorkspaceAccess, and each Map resolves project names through
	// Projects. DefaultProjectAccess takes the values of
	// DefaultWorkspaceAccess.
	Projects             project.Registry
	ProjectMappings      map[string][]ProjectGrantConfig
	DefaultProjectAccess string
}

// Result is the authorization selected for a set of groups.
type Result struct {
	Role            auth.Role
	WorkspaceAccess *auth.WorkspaceAccess
	// ProjectGrants are the grants the groups give on registered projects,
	// ordered by project ID. They are empty unless project mapping is on.
	ProjectGrants []auth.ProjectGrant
	// UnknownProjects are the sorted names of mapped projects that the
	// registry lacks. They give no grant.
	UnknownProjects []string
	MatchCount      int
}

// Mapper is an immutable, compiled group authorization mapper.
type Mapper struct {
	defaultRole       auth.Role
	groupMappings     map[string]auth.Role
	workspaceMappings map[string][]auth.WorkspaceGrant
	projects          project.Registry
	projectMappings   map[string][]ProjectGrantConfig
	// defaultAccess is the default workspace access, or the default project
	// access while project mapping is on.
	defaultAccess string
	strict        bool
}

// New compiles group-based authorization configuration.
func New(config Config) (*Mapper, error) {
	if !config.DefaultRole.Valid() {
		return nil, fmt.Errorf("invalid default role %q", config.DefaultRole)
	}

	mapper := &Mapper{defaultRole: config.DefaultRole, projects: config.Projects, strict: config.Strict}
	defaultAccess, accessKind := config.DefaultWorkspaceAccess, "workspace"
	if config.Projects != nil {
		defaultAccess, accessKind = config.DefaultProjectAccess, "project"
	}
	if defaultAccess == "" {
		defaultAccess = DefaultWorkspaceAccessAll
	}
	if defaultAccess != DefaultWorkspaceAccessAll && defaultAccess != DefaultWorkspaceAccessNone {
		return nil, fmt.Errorf("invalid default %s access %q: must be all or none", accessKind, defaultAccess)
	}
	mapper.defaultAccess = defaultAccess

	var err error
	if config.Projects != nil {
		mapper.projectMappings, err = compileProjectMappings(config.ProjectMappings)
	} else {
		mapper.workspaceMappings, err = compileWorkspaceMappings(config.WorkspaceMappings)
	}
	if err != nil {
		return nil, err
	}

	mapper.groupMappings = make(map[string]auth.Role, len(config.GroupMappings))
	for group, role := range config.GroupMappings {
		if !role.Valid() {
			return nil, fmt.Errorf("invalid global role mapping for group %q: invalid role %q", group, role)
		}
		mapper.groupMappings[group] = role
	}
	return mapper, nil
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

func compileProjectMappings(input map[string][]ProjectGrantConfig) (map[string][]ProjectGrantConfig, error) {
	compiled := make(map[string][]ProjectGrantConfig, len(input))
	for _, group := range slices.Sorted(maps.Keys(input)) {
		grants := input[group]
		if strings.TrimSpace(group) == "" {
			return nil, errors.New("project mapping group must not be blank")
		}
		if len(grants) == 0 {
			return nil, fmt.Errorf("project mapping for group %q must contain at least one grant", group)
		}
		seen := make(map[string]struct{}, len(grants))
		for _, grant := range grants {
			if strings.TrimSpace(grant.Project) == "" {
				return nil, fmt.Errorf("project mapping for group %q names no project", group)
			}
			key := strings.ToLower(grant.Project)
			if _, ok := seen[key]; ok {
				return nil, fmt.Errorf("duplicate project %q in mapping for group %q", grant.Project, group)
			}
			seen[key] = struct{}{}
			if !grant.Role.Valid() || grant.Role == auth.RoleAdmin {
				return nil, fmt.Errorf("invalid project mapping for group %q and project %q: role %q is not a role below admin",
					group, grant.Project, grant.Role)
			}
		}
		compiled[group] = slices.Clone(grants)
	}
	return compiled, nil
}

// Map selects global and workspace authorization for groups. While project
// mapping is on, it selects project grants in place of workspace access.
func (m *Mapper) Map(ctx context.Context, groups []string) (Result, error) {
	var bestGlobal auth.Role
	mergedGrants := make(map[string]auth.Role)
	// projectGrants holds the strongest grant on each project, keyed by the
	// lower-case name, since project names ignore case.
	projectGrants := make(map[string]ProjectGrantConfig)
	matchCount := 0

	for _, group := range groups {
		matched := false
		if role, ok := m.groupMappings[group]; ok {
			matched = true
			if role.Outranks(bestGlobal) {
				bestGlobal = role
			}
		}
		if grants, ok := m.workspaceMappings[group]; ok {
			matched = true
			for _, grant := range grants {
				current, exists := mergedGrants[grant.Workspace]
				if !exists || grant.Role.Outranks(current) {
					mergedGrants[grant.Workspace] = grant.Role
				}
			}
		}
		if grants, ok := m.projectMappings[group]; ok {
			matched = true
			for _, grant := range grants {
				key := strings.ToLower(grant.Project)
				current, exists := projectGrants[key]
				if !exists || grant.Role.Outranks(current.Role) {
					projectGrants[key] = grant
				}
			}
		}
		if matched {
			matchCount++
		}
	}
	if m.projects != nil {
		return m.mapProjects(ctx, bestGlobal, projectGrants, matchCount)
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
	if m.defaultAccess == DefaultWorkspaceAccessNone {
		return Result{
			Role:            auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}},
			MatchCount:      matchCount,
		}, nil
	}
	return Result{Role: m.defaultRole, WorkspaceAccess: auth.AllWorkspaceAccess(), MatchCount: matchCount}, nil
}

// mapProjects selects authorization while project mapping is on. A matched
// global role keeps the project grants, since a user's role in a project is
// the higher of the two. Without one, the user has no server-wide role:
// RoleViewer limited to no workspace, which project access reads as no role
// outside the grants.
func (m *Mapper) mapProjects(ctx context.Context, bestGlobal auth.Role, named map[string]ProjectGrantConfig, matchCount int) (Result, error) {
	if !bestGlobal.Valid() && len(named) == 0 && m.strict {
		return Result{MatchCount: matchCount}, ErrNoMatch
	}
	grants, unknown, err := m.resolveProjects(ctx, named)
	if err != nil {
		return Result{}, err
	}
	result := Result{ProjectGrants: grants, UnknownProjects: unknown, MatchCount: matchCount}
	switch {
	case bestGlobal.Valid():
		result.Role, result.WorkspaceAccess = bestGlobal, auth.AllWorkspaceAccess()
	case len(named) > 0 || m.defaultAccess == DefaultWorkspaceAccessNone:
		result.Role, result.WorkspaceAccess = auth.RoleViewer, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}
	default:
		result.Role, result.WorkspaceAccess = m.defaultRole, auth.AllWorkspaceAccess()
	}
	return result, nil
}

// resolveProjects returns the grants of named on registered projects, ordered
// by project ID, and the sorted names of the projects the registry lacks.
func (m *Mapper) resolveProjects(ctx context.Context, named map[string]ProjectGrantConfig) ([]auth.ProjectGrant, []string, error) {
	if len(named) == 0 {
		return nil, nil, nil
	}
	projects, err := m.projects.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list projects for group mappings: %w", err)
	}
	ids := make(map[string]string, len(projects))
	for _, p := range projects {
		ids[strings.ToLower(p.Name)] = p.ID
	}
	var (
		grants  []auth.ProjectGrant
		unknown []string
	)
	for key, grant := range named {
		id, ok := ids[key]
		if !ok {
			unknown = append(unknown, grant.Project)
			continue
		}
		grants = append(grants, auth.ProjectGrant{ProjectID: id, Role: grant.Role})
	}
	slices.SortFunc(grants, func(a, b auth.ProjectGrant) int { return strings.Compare(a.ProjectID, b.ProjectID) })
	slices.Sort(unknown)
	return grants, unknown, nil
}

// MapRole selects only the global role for groups.
func (m *Mapper) MapRole(groups []string) (auth.Role, error) {
	var best auth.Role
	for _, group := range groups {
		role, ok := m.groupMappings[group]
		if ok && role.Outranks(best) {
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

// WorkspaceAccessPolicyActive reports whether mapping controls workspace
// access, or project grants while project mapping is on.
func (m *Mapper) WorkspaceAccessPolicyActive() bool {
	return len(m.workspaceMappings) > 0 || len(m.projectMappings) > 0 || m.defaultAccess == DefaultWorkspaceAccessNone
}
