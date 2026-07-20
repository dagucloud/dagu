// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oidcprovision

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/itchyny/gojq"
)

const (
	defaultWorkspaceAccessAll  = "all"
	defaultWorkspaceAccessNone = "none"
)

// WorkspaceGrantConfig maps an IdP group to a role in one workspace.
type WorkspaceGrantConfig struct {
	Workspace string
	Role      string
}

// RoleMapperConfig holds configuration for role mapping.
type RoleMapperConfig struct {
	// GroupsClaim specifies the claim name containing groups (default: "groups")
	GroupsClaim string
	// GroupMappings maps IdP group names to Dagu roles
	GroupMappings map[string]string
	// WorkspaceMappings maps IdP group names to workspace grants.
	WorkspaceMappings map[string][]WorkspaceGrantConfig
	// DefaultWorkspaceAccess controls access when no mapping matches.
	DefaultWorkspaceAccess string
	// RoleAttributePath is a jq expression to extract role from claims
	RoleAttributePath string
	// RoleAttributeStrict denies login when neither global nor workspace mapping matches.
	RoleAttributeStrict bool
	// SkipOrgRoleSync skips role and workspace-access sync on subsequent logins.
	SkipOrgRoleSync bool
	// DefaultRole is the fallback role when no mapping matches
	DefaultRole auth.Role
}

// RoleMapper maps OIDC claims to Dagu authorization.
type RoleMapper struct {
	groupsClaim             string
	defaultRole             auth.Role
	roleAttributeStrict     bool
	jqQuery                 *gojq.Code
	groupMappings           map[string]auth.Role
	groupMappingsConfigured bool
	workspaceMappings       map[string][]auth.WorkspaceGrant
	defaultWorkspaceAccess  string
}

// ErrNoRoleFound is returned when strict mode finds no global or workspace mapping.
var ErrNoRoleFound = errors.New("no valid role found from OIDC claims")

// NewRoleMapper creates a new RoleMapper with the given configuration.
func NewRoleMapper(config RoleMapperConfig) (*RoleMapper, error) {
	groupsClaim := config.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}

	groupMappings := make(map[string]auth.Role, len(config.GroupMappings))
	for group, roleValue := range config.GroupMappings {
		role := auth.Role(strings.ToLower(roleValue))
		if role.Valid() {
			groupMappings[group] = role
		}
	}

	defaultWorkspaceAccess := config.DefaultWorkspaceAccess
	if defaultWorkspaceAccess == "" {
		defaultWorkspaceAccess = defaultWorkspaceAccessAll
	}
	if defaultWorkspaceAccess != defaultWorkspaceAccessAll && defaultWorkspaceAccess != defaultWorkspaceAccessNone {
		return nil, fmt.Errorf("invalid default workspace access %q: must be all or none", config.DefaultWorkspaceAccess)
	}
	workspaceMappings, err := compileWorkspaceMappings(config.WorkspaceMappings)
	if err != nil {
		return nil, err
	}

	rm := &RoleMapper{
		groupsClaim:             groupsClaim,
		defaultRole:             config.DefaultRole,
		roleAttributeStrict:     config.RoleAttributeStrict,
		groupMappings:           groupMappings,
		groupMappingsConfigured: len(config.GroupMappings) > 0,
		workspaceMappings:       workspaceMappings,
		defaultWorkspaceAccess:  defaultWorkspaceAccess,
	}

	if config.RoleAttributePath != "" {
		query, err := gojq.Parse(config.RoleAttributePath)
		if err != nil {
			return nil, fmt.Errorf("invalid roleAttributePath jq expression: %w", err)
		}
		code, err := gojq.Compile(query)
		if err != nil {
			return nil, fmt.Errorf("failed to compile roleAttributePath jq expression: %w", err)
		}
		rm.jqQuery = code
	}

	return rm, nil
}

func compileWorkspaceMappings(input map[string][]WorkspaceGrantConfig) (map[string][]auth.WorkspaceGrant, error) {
	groups := make([]string, 0, len(input))
	for group := range input {
		groups = append(groups, group)
	}
	sort.Strings(groups)

	compiledMappings := make(map[string][]auth.WorkspaceGrant, len(input))
	for _, group := range groups {
		grants := input[group]
		if strings.TrimSpace(group) == "" {
			return nil, errors.New("workspace mapping group must not be blank")
		}
		if len(grants) == 0 {
			return nil, fmt.Errorf("workspace mapping for group %q must contain at least one grant", group)
		}

		compiled := make([]auth.WorkspaceGrant, 0, len(grants))
		seenWorkspaces := make(map[string]struct{}, len(grants))
		for _, grant := range grants {
			if err := workspace.ValidateName(grant.Workspace); err != nil {
				return nil, fmt.Errorf("invalid workspace mapping for group %q: workspace %q: %w", group, grant.Workspace, err)
			}
			if _, ok := seenWorkspaces[grant.Workspace]; ok {
				return nil, fmt.Errorf("duplicate workspace %q in mapping for group %q", grant.Workspace, group)
			}
			seenWorkspaces[grant.Workspace] = struct{}{}

			role, err := auth.ParseRole(grant.Role)
			if err != nil {
				return nil, fmt.Errorf("invalid workspace mapping for group %q and workspace %q: %w", group, grant.Workspace, err)
			}
			if role == auth.RoleAdmin {
				return nil, fmt.Errorf("invalid workspace mapping for group %q and workspace %q: admin cannot be scoped to a workspace", group, grant.Workspace)
			}
			compiled = append(compiled, auth.WorkspaceGrant{
				Workspace: grant.Workspace,
				Role:      role,
			})
		}
		compiledMappings[group] = compiled
	}

	return compiledMappings, nil
}

// MapRole determines the Dagu role from OIDC claims.
// Evaluation order:
//  1. RoleAttributePath (jq expression) if configured
//  2. GroupMappings if configured
//  3. DefaultRole as fallback (or error if strict mode)
func (rm *RoleMapper) MapRole(rawClaims map[string]any) (auth.Role, error) {
	role, found := rm.mapGlobalRole(rawClaims)
	if !found {
		if rm.roleAttributeStrict {
			return "", ErrNoRoleFound
		}
		return rm.defaultRole, nil
	}

	return role, nil
}

// MapAccess determines the global role and workspace access from OIDC claims.
func (rm *RoleMapper) MapAccess(rawClaims map[string]any) (auth.Role, *auth.WorkspaceAccess, error) {
	if role, found := rm.mapGlobalRole(rawClaims); found {
		return role, auth.AllWorkspaceAccess(), nil
	}

	if grants, found := rm.evaluateWorkspaceMappings(rawClaims); found {
		return auth.RoleViewer, &auth.WorkspaceAccess{Grants: grants}, nil
	}

	if rm.roleAttributeStrict {
		return "", nil, ErrNoRoleFound
	}

	if rm.defaultWorkspaceAccess == defaultWorkspaceAccessNone {
		return auth.RoleViewer, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, nil
	}

	return rm.defaultRole, auth.AllWorkspaceAccess(), nil
}

func (rm *RoleMapper) mapGlobalRole(rawClaims map[string]any) (auth.Role, bool) {
	if rm.jqQuery != nil {
		if role, found := rm.evaluateJqExpression(rawClaims); found {
			return role, true
		}
	}

	if len(rm.groupMappings) > 0 {
		return rm.evaluateGroupMappings(rawClaims)
	}

	return "", false
}

// evaluateJqExpression runs the jq query against claims and returns the role.
func (rm *RoleMapper) evaluateJqExpression(claims map[string]any) (auth.Role, bool) {
	iter := rm.jqQuery.Run(claims)
	v, ok := iter.Next()
	if !ok {
		return "", false
	}
	if _, isErr := v.(error); isErr {
		// jq evaluation error - treat as not found
		return "", false
	}

	roleStr, ok := v.(string)
	if !ok || roleStr == "" {
		return "", false
	}

	role := auth.Role(strings.ToLower(roleStr))
	if !role.Valid() {
		return "", false
	}

	return role, true
}

// evaluateGroupMappings checks the groups claim and maps to a role.
// Returns the highest-privilege matching role.
func (rm *RoleMapper) evaluateGroupMappings(claims map[string]any) (auth.Role, bool) {
	groups := rm.extractGroups(claims)
	if len(groups) == 0 {
		return "", false
	}

	var bestRole auth.Role
	var bestPriority int

	for _, group := range groups {
		if role, ok := rm.groupMappings[group]; ok {
			priority := rolePriority(role)
			if priority > bestPriority {
				bestRole = role
				bestPriority = priority
			}
		}
	}

	if bestPriority == 0 {
		return "", false
	}

	return bestRole, true
}

func (rm *RoleMapper) evaluateWorkspaceMappings(claims map[string]any) ([]auth.WorkspaceGrant, bool) {
	groups := rm.extractGroups(claims)
	if len(groups) == 0 {
		return nil, false
	}

	merged := make(map[string]auth.Role)
	matched := false
	for _, group := range groups {
		grants, ok := rm.workspaceMappings[group]
		if !ok {
			continue
		}
		matched = true
		for _, grant := range grants {
			current, exists := merged[grant.Workspace]
			if !exists || rolePriority(grant.Role) > rolePriority(current) {
				merged[grant.Workspace] = grant.Role
			}
		}
	}
	if !matched {
		return nil, false
	}

	grants := make([]auth.WorkspaceGrant, 0, len(merged))
	for workspaceName, role := range merged {
		grants = append(grants, auth.WorkspaceGrant{Workspace: workspaceName, Role: role})
	}
	sort.Slice(grants, func(i, j int) bool {
		return grants[i].Workspace < grants[j].Workspace
	})
	return grants, true
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

// extractGroups extracts group names from claims using the configured claim name.
// Supports nested claims using dot notation (e.g., "realm_access.roles").
func (rm *RoleMapper) extractGroups(claims map[string]any) []string {
	// Handle nested claims (e.g., "realm_access.roles" for Keycloak)
	value := getNestedClaim(claims, rm.groupsClaim)
	if value == nil {
		return nil
	}

	// Handle different formats
	switch v := value.(type) {
	case []any:
		groups := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				groups = append(groups, s)
			}
		}
		return groups
	case []string:
		return v
	case string:
		// Some providers send space-separated groups
		return strings.Fields(v)
	}

	return nil
}

// getNestedClaim retrieves a claim value using dot notation.
func getNestedClaim(claims map[string]any, path string) any {
	parts := strings.Split(path, ".")
	current := any(claims)

	for _, part := range parts {
		if m, ok := current.(map[string]any); ok {
			current = m[part]
		} else {
			return nil
		}
	}

	return current
}

// IsConfigured reports whether any authorization mapping or scoped fallback is configured.
func (rm *RoleMapper) IsConfigured() bool {
	return rm.jqQuery != nil || rm.groupMappingsConfigured || rm.WorkspaceAccessPolicyActive()
}

// WorkspaceAccessPolicyActive reports whether OIDC controls workspace access.
func (rm *RoleMapper) WorkspaceAccessPolicyActive() bool {
	return len(rm.workspaceMappings) > 0 || rm.defaultWorkspaceAccess == defaultWorkspaceAccessNone
}
