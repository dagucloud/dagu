// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oidcprovision

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/service/authmapping"
	"github.com/itchyny/gojq"
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
	jqQuery                 *gojq.Code
	groupMapper             *authmapping.Mapper
	groupMappingsConfigured bool
}

// ErrNoRoleFound is returned when strict mode finds no global or workspace mapping.
var ErrNoRoleFound = errors.New("no valid role found from OIDC claims")

// NewRoleMapper creates a new RoleMapper with the given configuration.
func NewRoleMapper(config RoleMapperConfig) (*RoleMapper, error) {
	groupsClaim := config.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	defaultRole := config.DefaultRole
	if defaultRole == auth.RoleNone {
		defaultRole = auth.RoleViewer
	}

	groupMappings := make(map[string]auth.Role, len(config.GroupMappings))
	for group, roleValue := range config.GroupMappings {
		role := auth.Role(strings.ToLower(roleValue))
		if role.Valid() {
			groupMappings[group] = role
		}
	}

	workspaceMappings := make(map[string][]authmapping.WorkspaceGrantConfig, len(config.WorkspaceMappings))
	for group, grants := range config.WorkspaceMappings {
		converted := make([]authmapping.WorkspaceGrantConfig, 0, len(grants))
		for _, grant := range grants {
			role, err := auth.ParseRole(grant.Role)
			if err != nil {
				return nil, fmt.Errorf("invalid workspace mapping for group %q and workspace %q: %w", group, grant.Workspace, err)
			}
			converted = append(converted, authmapping.WorkspaceGrantConfig{Workspace: grant.Workspace, Role: role})
		}
		workspaceMappings[group] = converted
	}

	groupMapper, err := authmapping.New(authmapping.Config{
		DefaultRole:            defaultRole,
		GroupMappings:          groupMappings,
		WorkspaceMappings:      workspaceMappings,
		DefaultWorkspaceAccess: config.DefaultWorkspaceAccess,
		Strict:                 config.RoleAttributeStrict,
	})
	if err != nil {
		return nil, err
	}

	rm := &RoleMapper{
		groupsClaim:             groupsClaim,
		groupMapper:             groupMapper,
		groupMappingsConfigured: len(config.GroupMappings) > 0,
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

// MapRole determines the Dagu role from OIDC claims.
// Evaluation order:
//  1. RoleAttributePath (jq expression) if configured
//  2. GroupMappings if configured
//  3. DefaultRole as fallback (or error if strict mode)
func (rm *RoleMapper) MapRole(rawClaims map[string]any) (auth.Role, error) {
	if rm.jqQuery != nil {
		if role, found := rm.evaluateJqExpression(rawClaims); found {
			return role, nil
		}
	}
	role, err := rm.groupMapper.MapRole(rm.extractGroups(rawClaims))
	if errors.Is(err, authmapping.ErrNoMatch) {
		return auth.RoleNone, ErrNoRoleFound
	}
	return role, err
}

// MapAccess determines the global role and workspace access from OIDC claims.
func (rm *RoleMapper) MapAccess(rawClaims map[string]any) (auth.Role, *auth.WorkspaceAccess, error) {
	if rm.jqQuery != nil {
		if role, found := rm.evaluateJqExpression(rawClaims); found {
			return role, auth.AllWorkspaceAccess(), nil
		}
	}
	result, err := rm.groupMapper.Map(rm.extractGroups(rawClaims))
	if errors.Is(err, authmapping.ErrNoMatch) {
		return auth.RoleNone, nil, ErrNoRoleFound
	}
	return result.Role, result.WorkspaceAccess, err
}

// evaluateJqExpression runs the jq query against claims and returns the role.
func (rm *RoleMapper) evaluateJqExpression(claims map[string]any) (auth.Role, bool) {
	iter := rm.jqQuery.Run(claims)
	v, ok := iter.Next()
	if !ok {
		return auth.RoleNone, false
	}
	if _, isErr := v.(error); isErr {
		return auth.RoleNone, false
	}

	roleStr, ok := v.(string)
	if !ok || roleStr == "" {
		return auth.RoleNone, false
	}

	role := auth.Role(strings.ToLower(roleStr))
	if !role.Valid() {
		return auth.RoleNone, false
	}
	return role, true
}

// extractGroups extracts group names from claims using the configured claim name.
// Supports nested claims using dot notation (e.g., "realm_access.roles").
func (rm *RoleMapper) extractGroups(claims map[string]any) []string {
	value := getNestedClaim(claims, rm.groupsClaim)
	if value == nil {
		return nil
	}

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
		return strings.Fields(v)
	default:
		return nil
	}
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
	return rm.groupMapper.WorkspaceAccessPolicyActive()
}
