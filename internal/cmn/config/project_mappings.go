// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Keys of the role mappings of OIDC and proxy authentication. While projects
// are on, each workspace key is read as its project key until v3.1.
const (
	oidcRoleMappingKey        = "auth.oidc.role_mapping."
	proxyRoleMappingKey       = "auth.proxy.role_mapping."
	workspaceMappingsKey      = "workspace_mappings"
	projectMappingsKey        = "project_mappings"
	defaultWorkspaceAccessKey = "default_workspace_access"
	defaultProjectAccessKey   = "default_project_access"
)

// Values of default_project_access.
const (
	projectAccessAll  = "all"
	projectAccessNone = "none"
)

// readProjectAccess sets the project mappings and default project access of
// OIDC and proxy authentication. While projects are off, both stay empty.
// While they are on, a workspace key that is set is read as its project
// key, with a warning, and setting both keys is an error.
func (l *ConfigLoader) readProjectAccess(cfg *Config) error {
	oidc := &cfg.Server.Auth.OIDC.RoleMapping
	proxy := &cfg.Server.Auth.Proxy.RoleMapping
	if !cfg.ProjectsEnabled {
		oidc.ProjectMappings, oidc.DefaultProjectAccess = nil, ""
		proxy.ProjectMappings, proxy.DefaultProjectAccess = nil, ""
		return nil
	}

	warnings, err := l.readOIDCProjectAccess(oidc)
	if err != nil {
		return err
	}
	l.warnings = append(l.warnings, warnings...)

	mappingsAliased, defaultAliased, warnings, err := workspaceAliases(proxyRoleMappingKey, func(key string) bool {
		switch key {
		case proxyRoleMappingKey + workspaceMappingsKey:
			return l.trustedProxyWorkspaceMappingsSet || l.v.IsSet(key)
		case proxyRoleMappingKey + projectMappingsKey:
			return l.trustedProxyProjectMappingsSet || l.v.IsSet(key)
		default:
			return l.v.IsSet(key)
		}
	})
	if err != nil {
		return err
	}
	l.warnings = append(l.warnings, warnings...)
	if mappingsAliased {
		proxy.ProjectMappings = projectMappingsFrom(proxy.WorkspaceMappings, func(grant TrustedProxyWorkspaceGrant) (string, string) {
			return grant.Workspace, grant.Role
		})
	}
	if defaultAliased {
		proxy.DefaultProjectAccess = proxy.DefaultWorkspaceAccess
	}
	return nil
}

// readOIDCProjectAccess sets the project mappings and default project access
// of OIDC authentication while projects are on, and returns a warning for
// each workspace key read in place of its project key.
func (l *ConfigLoader) readOIDCProjectAccess(mapping *OIDCRoleMapping) ([]string, error) {
	mappingsAliased, defaultAliased, warnings, err := workspaceAliases(oidcRoleMappingKey, l.v.IsSet)
	if err != nil {
		return nil, err
	}
	if mappingsAliased {
		mapping.ProjectMappings = projectMappingsFrom(mapping.WorkspaceMappings, func(grant OIDCWorkspaceGrant) (string, string) {
			return grant.Workspace, grant.Role
		})
	}
	if defaultAliased {
		mapping.DefaultProjectAccess = mapping.DefaultWorkspaceAccess
	}
	if mapping.DefaultProjectAccess == "" && len(mapping.ProjectMappings) == 0 {
		mapping.DefaultProjectAccess = projectAccessAll
	}
	return warnings, nil
}

// workspaceAliases reports whether the project mappings and the default
// project access under prefix are read from their workspace keys. It returns
// an error when a workspace key and its project key are both set.
func workspaceAliases(prefix string, isSet func(key string) bool) (mappings, defaultAccess bool, warnings []string, err error) {
	read := func(workspaceKey, projectKey string) (bool, error) {
		workspaceKey, projectKey = prefix+workspaceKey, prefix+projectKey
		if !isSet(workspaceKey) {
			return false, nil
		}
		if isSet(projectKey) {
			return false, fmt.Errorf("%s and %s are both set; remove %s", workspaceKey, projectKey, workspaceKey)
		}
		warnings = append(warnings, fmt.Sprintf("%s is read as %s until v3.1; rename it", workspaceKey, projectKey))
		return true, nil
	}
	if mappings, err = read(workspaceMappingsKey, projectMappingsKey); err != nil {
		return false, false, nil, err
	}
	if defaultAccess, err = read(defaultWorkspaceAccessKey, defaultProjectAccessKey); err != nil {
		return false, false, nil, err
	}
	return mappings, defaultAccess, warnings, nil
}

// projectMappingsFrom reads workspace mappings as project mappings, taking
// each workspace name as a project name.
func projectMappingsFrom[G any](mappings map[string][]G, nameAndRole func(G) (string, string)) map[string][]ProjectMappingGrant {
	if mappings == nil {
		return nil
	}
	converted := make(map[string][]ProjectMappingGrant, len(mappings))
	for group, grants := range mappings {
		projectGrants := make([]ProjectMappingGrant, 0, len(grants))
		for _, grant := range grants {
			name, role := nameAndRole(grant)
			projectGrants = append(projectGrants, ProjectMappingGrant{Project: name, Role: role})
		}
		converted[group] = projectGrants
	}
	return converted
}

func cloneProjectMappings(mappings map[string][]ProjectMappingGrant) map[string][]ProjectMappingGrant {
	cloned := make(map[string][]ProjectMappingGrant, len(mappings))
	for group, grants := range mappings {
		cloned[group] = slices.Clone(grants)
	}
	return cloned
}

// validateOIDCMappings checks the workspace mappings while projects are off,
// and the project mappings, which hold any workspace mappings read in their
// place, while projects are on.
func validateOIDCMappings(mapping OIDCRoleMapping, projects bool) error {
	if !projects {
		return validateOIDCWorkspaceMappings(mapping)
	}
	return validateOIDCProjectMappings(mapping)
}

func validateOIDCProjectMappings(mapping OIDCRoleMapping) error {
	const path = "auth.oidc.role_mapping."
	if mapping.DefaultProjectAccess == "" && len(mapping.ProjectMappings) > 0 {
		return fmt.Errorf("%s%s must be set to all or none when %s is set", path, defaultProjectAccessKey, projectMappingsKey)
	}
	switch mapping.DefaultProjectAccess {
	case "", projectAccessAll, projectAccessNone:
	default:
		return fmt.Errorf("%s%s must be one of: all, none (got: %q)", path, defaultProjectAccessKey, mapping.DefaultProjectAccess)
	}
	return validateProjectMappings(path+projectMappingsKey, mapping.ProjectMappings, func(groupPath, group string) error {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("%s must not use a blank group name", groupPath)
		}
		return nil
	})
}

func validateTrustedProxyProjectMappings(mapping TrustedProxyRoleMapping) error {
	switch mapping.DefaultProjectAccess {
	case projectAccessAll, projectAccessNone:
	default:
		return fmt.Errorf("%s%s must be one of: all, none (got: %q)", proxyRoleMappingKey, defaultProjectAccessKey, mapping.DefaultProjectAccess)
	}
	return validateProjectMappings(proxyRoleMappingKey+projectMappingsKey, mapping.ProjectMappings, validateTrustedProxyMappingGroup)
}

// validateProjectMappings checks the grants of each group, and each group
// name with checkGroup. Project names repeat without regard to case, as
// project names do.
func validateProjectMappings(path string, mappings map[string][]ProjectMappingGrant, checkGroup func(groupPath, group string) error) error {
	for _, group := range slices.Sorted(maps.Keys(mappings)) {
		groupPath := fmt.Sprintf("%s[%q]", path, group)
		if err := checkGroup(groupPath, group); err != nil {
			return err
		}
		grants := mappings[group]
		if len(grants) == 0 {
			return fmt.Errorf("%s must contain at least one grant", groupPath)
		}
		seen := make(map[string]struct{}, len(grants))
		for i, grant := range grants {
			grantPath := fmt.Sprintf("%s[%d]", groupPath, i)
			if !isProjectMappingName(grant.Project) {
				return fmt.Errorf("%s.project %q is not a project name", grantPath, grant.Project)
			}
			key := strings.ToLower(grant.Project)
			if _, ok := seen[key]; ok {
				return fmt.Errorf("%s contains duplicate project %q", groupPath, grant.Project)
			}
			seen[key] = struct{}{}
			role, err := auth.ParseRole(grant.Role)
			if err != nil {
				return fmt.Errorf("%s.role: %w", grantPath, err)
			}
			if role == auth.RoleAdmin {
				return fmt.Errorf("%s.role must not be admin", grantPath)
			}
		}
	}
	return nil
}

// isProjectMappingName reports whether name can name a project: the default
// project, any other project, or a v2 workspace that a workspace_mappings
// entry names.
func isProjectMappingName(name string) bool {
	return strings.EqualFold(name, project.DefaultName) ||
		project.ValidateName(name) == nil ||
		workspace.ValidateName(name) == nil
}
