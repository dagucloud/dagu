// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oidcprovision

import (
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleMapper_GroupMappings(t *testing.T) {
	tests := []struct {
		name          string
		config        RoleMapperConfig
		claims        map[string]any
		expectedRole  auth.Role
		expectedError error
	}{
		{
			name: "single_group_match",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"admins": "admin",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"admins"},
			},
			expectedRole: auth.RoleAdmin,
		},
		{
			name: "multiple_groups_highest_privilege",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"admins":     "admin",
					"developers": "developer",
					"operators":  "operator",
					"viewers":    "viewer",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"viewers", "operators", "developers", "admins"},
			},
			expectedRole: auth.RoleAdmin,
		},
		{
			name: "developer_wins_over_operator",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"developers": "developer",
					"operators":  "operator",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"operators", "developers"},
			},
			expectedRole: auth.RoleDeveloper,
		},
		{
			name: "no_matching_group_fallback_to_default",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"admins": "admin",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"developers"},
			},
			expectedRole: auth.RoleViewer,
		},
		{
			name: "no_groups_claim_fallback_to_default",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"admins": "admin",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims:       map[string]any{},
			expectedRole: auth.RoleViewer,
		},
		{
			name: "strict_mode_no_match",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"admins": "admin",
				},
				RoleAttributeStrict: true,
				DefaultRole:         auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"developers"},
			},
			expectedError: ErrNoRoleFound,
		},
		{
			name: "nested_claim_keycloak_style",
			config: RoleMapperConfig{
				GroupsClaim: "realm_access.roles",
				GroupMappings: map[string]string{
					"dagu_admin": "admin",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"realm_access": map[string]any{
					"roles": []any{"dagu_admin", "other_role"},
				},
			},
			expectedRole: auth.RoleAdmin,
		},
		{
			name: "cognito_groups_claim",
			config: RoleMapperConfig{
				GroupsClaim: "cognito:groups",
				GroupMappings: map[string]string{
					"AdminGroup": "admin",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"cognito:groups": []any{"AdminGroup"},
			},
			expectedRole: auth.RoleAdmin,
		},
		{
			name: "case_insensitive_role",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"admins": "ADMIN", // uppercase in config
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"admins"},
			},
			expectedRole: auth.RoleAdmin, // should be lowercased
		},
		{
			name: "space_separated_groups",
			config: RoleMapperConfig{
				GroupsClaim: "groups",
				GroupMappings: map[string]string{
					"admins": "admin",
				},
				DefaultRole: auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": "admins developers viewers", // space-separated string
			},
			expectedRole: auth.RoleAdmin,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rm, err := NewRoleMapper(tc.config)
			require.NoError(t, err)

			role, err := rm.MapRole(tc.claims)
			if tc.expectedError != nil {
				assert.ErrorIs(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedRole, role)
			}
		})
	}
}

func TestRoleMapper_JqExpression(t *testing.T) {
	tests := []struct {
		name          string
		config        RoleMapperConfig
		claims        map[string]any
		expectedRole  auth.Role
		expectedError error
	}{
		{
			name: "simple_conditional",
			config: RoleMapperConfig{
				RoleAttributePath: `if (.groups | contains(["admins"])) then "admin" else "viewer" end`,
				DefaultRole:       auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"admins"},
			},
			expectedRole: auth.RoleAdmin,
		},
		{
			name: "chained_conditional",
			config: RoleMapperConfig{
				RoleAttributePath: `if (.groups | contains(["admins"])) then "admin" elif (.groups | contains(["managers"])) then "manager" else "viewer" end`,
				DefaultRole:       auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"managers"},
			},
			expectedRole: auth.RoleManager,
		},
		{
			name: "email_based_role",
			config: RoleMapperConfig{
				RoleAttributePath: `if (.email | endswith("@admin.example.com")) then "admin" else "viewer" end`,
				DefaultRole:       auth.RoleViewer,
			},
			claims: map[string]any{
				"email": "user@admin.example.com",
			},
			expectedRole: auth.RoleAdmin,
		},
		{
			name: "jq_returns_invalid_role_fallback",
			config: RoleMapperConfig{
				RoleAttributePath: `"superuser"`, // not a valid Dagu role
				DefaultRole:       auth.RoleViewer,
			},
			claims:       map[string]any{},
			expectedRole: auth.RoleViewer,
		},
		{
			name: "jq_returns_empty_string_fallback",
			config: RoleMapperConfig{
				RoleAttributePath: `""`,
				DefaultRole:       auth.RoleViewer,
			},
			claims:       map[string]any{},
			expectedRole: auth.RoleViewer,
		},
		{
			name: "jq_error_fallback",
			config: RoleMapperConfig{
				RoleAttributePath: `.nonexistent.path`,
				DefaultRole:       auth.RoleViewer,
			},
			claims:       map[string]any{},
			expectedRole: auth.RoleViewer,
		},
		{
			name: "jq_strict_mode_no_match",
			config: RoleMapperConfig{
				RoleAttributePath:   `if (.groups | contains(["admins"])) then "admin" else null end`,
				RoleAttributeStrict: true,
				DefaultRole:         auth.RoleViewer,
			},
			claims: map[string]any{
				"groups": []any{"developers"},
			},
			expectedError: ErrNoRoleFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rm, err := NewRoleMapper(tc.config)
			require.NoError(t, err)

			role, err := rm.MapRole(tc.claims)
			if tc.expectedError != nil {
				assert.ErrorIs(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedRole, role)
			}
		})
	}
}

func TestRoleMapper_JqTakesPrecedence(t *testing.T) {
	// When both jq expression and group mappings are configured,
	// jq expression should be evaluated first
	rm, err := NewRoleMapper(RoleMapperConfig{
		RoleAttributePath: `"operator"`, // jq returns operator
		GroupsClaim:       "groups",
		GroupMappings: map[string]string{
			"admins": "admin", // group mapping would return admin
		},
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	role, err := rm.MapRole(map[string]any{
		"groups": []any{"admins"},
	})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleOperator, role) // jq wins
}

func TestRoleMapper_InvalidJqExpression(t *testing.T) {
	_, err := NewRoleMapper(RoleMapperConfig{
		RoleAttributePath: `invalid jq {{{{ syntax`,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid roleAttributePath")
}

func TestRoleMapper_IsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		config   RoleMapperConfig
		expected bool
	}{
		{
			name:     "no_config",
			config:   RoleMapperConfig{},
			expected: false,
		},
		{
			name: "group_mappings_only",
			config: RoleMapperConfig{
				GroupMappings: map[string]string{"admin": "admin"},
			},
			expected: true,
		},
		{
			name: "jq_expression_only",
			config: RoleMapperConfig{
				RoleAttributePath: `"admin"`,
			},
			expected: true,
		},
		{
			name: "both_configured",
			config: RoleMapperConfig{
				RoleAttributePath: `"admin"`,
				GroupMappings:     map[string]string{"admin": "admin"},
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rm, err := NewRoleMapper(tc.config)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, rm.IsConfigured())
		})
	}
}

func TestGetNestedClaim(t *testing.T) {
	claims := map[string]any{
		"simple": "value",
		"nested": map[string]any{
			"level1": map[string]any{
				"level2": "deep_value",
			},
		},
		"realm_access": map[string]any{
			"roles": []any{"role1", "role2"},
		},
	}

	tests := []struct {
		path     string
		expected any
	}{
		{"simple", "value"},
		{"nested.level1.level2", "deep_value"},
		{"realm_access.roles", []any{"role1", "role2"}},
		{"nonexistent", nil},
		{"nested.nonexistent", nil},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := getNestedClaim(claims, tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestRoleMapper_MapAccess(t *testing.T) {
	workspaceMappings := map[string][]WorkspaceGrantConfig{
		"sre-team": {
			{Workspace: "payments", Role: "operator"},
			{Workspace: "infra", Role: "developer"},
		},
	}

	tests := []struct {
		name           string
		config         RoleMapperConfig
		claims         map[string]any
		expectedRole   auth.Role
		expectedAccess *auth.WorkspaceAccess
		expectedError  error
	}{
		{
			name: "explicit jq viewer is global",
			config: RoleMapperConfig{
				RoleAttributePath:      `"viewer"`,
				WorkspaceMappings:      workspaceMappings,
				DefaultRole:            auth.RoleManager,
				DefaultWorkspaceAccess: "none",
			},
			claims:         map[string]any{"groups": []any{"sre-team"}},
			expectedRole:   auth.RoleViewer,
			expectedAccess: auth.AllWorkspaceAccess(),
		},
		{
			name: "global group mapping replaces workspace mapping",
			config: RoleMapperConfig{
				GroupMappings:          map[string]string{"staff": "operator"},
				WorkspaceMappings:      workspaceMappings,
				DefaultRole:            auth.RoleViewer,
				DefaultWorkspaceAccess: "none",
			},
			claims:         map[string]any{"groups": []any{"staff", "sre-team"}},
			expectedRole:   auth.RoleOperator,
			expectedAccess: auth.AllWorkspaceAccess(),
		},
		{
			name: "workspace-only match",
			config: RoleMapperConfig{
				WorkspaceMappings: workspaceMappings,
				DefaultRole:       auth.RoleManager,
			},
			claims:       map[string]any{"groups": []any{"sre-team"}},
			expectedRole: auth.RoleViewer,
			expectedAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
				{Workspace: "infra", Role: auth.RoleDeveloper},
				{Workspace: "payments", Role: auth.RoleOperator},
			}},
		},
		{
			name: "unmatched all fallback",
			config: RoleMapperConfig{
				WorkspaceMappings: workspaceMappings,
				DefaultRole:       auth.RoleManager,
			},
			claims:         map[string]any{"groups": []any{"other"}},
			expectedRole:   auth.RoleManager,
			expectedAccess: auth.AllWorkspaceAccess(),
		},
		{
			name: "unmatched none fallback uses viewer",
			config: RoleMapperConfig{
				DefaultRole:            auth.RoleManager,
				DefaultWorkspaceAccess: "none",
			},
			claims:         map[string]any{"groups": []any{"other"}},
			expectedRole:   auth.RoleViewer,
			expectedAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}},
		},
		{
			name: "strict workspace-only match succeeds",
			config: RoleMapperConfig{
				WorkspaceMappings:   workspaceMappings,
				RoleAttributeStrict: true,
				DefaultRole:         auth.RoleViewer,
			},
			claims:       map[string]any{"groups": []any{"sre-team"}},
			expectedRole: auth.RoleViewer,
			expectedAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
				{Workspace: "infra", Role: auth.RoleDeveloper},
				{Workspace: "payments", Role: auth.RoleOperator},
			}},
		},
		{
			name: "strict total miss fails",
			config: RoleMapperConfig{
				WorkspaceMappings:   workspaceMappings,
				RoleAttributeStrict: true,
				DefaultRole:         auth.RoleViewer,
			},
			claims:        map[string]any{"groups": []any{"other"}},
			expectedError: ErrNoRoleFound,
		},
		{
			name: "group matching is case sensitive",
			config: RoleMapperConfig{
				WorkspaceMappings:      workspaceMappings,
				DefaultRole:            auth.RoleViewer,
				DefaultWorkspaceAccess: "none",
			},
			claims:         map[string]any{"groups": []any{"SRE-TEAM"}},
			expectedRole:   auth.RoleViewer,
			expectedAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mapper, err := NewRoleMapper(tc.config)
			require.NoError(t, err)

			role, access, err := mapper.MapAccess(tc.claims)
			if tc.expectedError != nil {
				assert.ErrorIs(t, err, tc.expectedError)
				assert.Nil(t, access)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectedRole, role)
			assert.Equal(t, tc.expectedAccess, access)
		})
	}
}

func TestRoleMapper_MapAccessMergesHighestRoleDeterministically(t *testing.T) {
	mapper, err := NewRoleMapper(RoleMapperConfig{
		GroupsClaim: "realm_access.roles",
		WorkspaceMappings: map[string][]WorkspaceGrantConfig{
			"operators": {
				{Workspace: "payments", Role: "operator"},
				{Workspace: "zeta", Role: "viewer"},
			},
			"developers": {
				{Workspace: "payments", Role: "developer"},
				{Workspace: "alpha", Role: "manager"},
			},
		},
		DefaultRole: auth.RoleViewer,
	})
	require.NoError(t, err)

	role, access, err := mapper.MapAccess(map[string]any{
		"realm_access": map[string]any{"roles": "operators developers operators"},
	})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
		{Workspace: "alpha", Role: auth.RoleManager},
		{Workspace: "payments", Role: auth.RoleDeveloper},
		{Workspace: "zeta", Role: auth.RoleViewer},
	}}, access)
}

func TestNewRoleMapper_ValidatesWorkspaceMappings(t *testing.T) {
	tests := []struct {
		name          string
		config        RoleMapperConfig
		errorContains string
	}{
		{
			name:          "invalid default access",
			config:        RoleMapperConfig{DefaultWorkspaceAccess: "selected"},
			errorContains: "must be all or none",
		},
		{
			name: "blank group",
			config: RoleMapperConfig{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"  ": {{Workspace: "payments", Role: "viewer"}},
			}},
			errorContains: "must not be blank",
		},
		{
			name: "empty grant list",
			config: RoleMapperConfig{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {},
			}},
			errorContains: "at least one grant",
		},
		{
			name: "invalid workspace",
			config: RoleMapperConfig{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "invalid/name", Role: "viewer"}},
			}},
			errorContains: "invalid workspace mapping",
		},
		{
			name: "reserved workspace",
			config: RoleMapperConfig{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "global", Role: "viewer"}},
			}},
			errorContains: "reserved names",
		},
		{
			name: "invalid role",
			config: RoleMapperConfig{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "owner"}},
			}},
			errorContains: "invalid role",
		},
		{
			name: "admin role",
			config: RoleMapperConfig{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "admin"}},
			}},
			errorContains: "admin cannot be scoped",
		},
		{
			name: "duplicate workspace in group",
			config: RoleMapperConfig{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {
					{Workspace: "payments", Role: "viewer"},
					{Workspace: "payments", Role: "operator"},
				},
			}},
			errorContains: "duplicate workspace",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRoleMapper(tc.config)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errorContains)
		})
	}
}

func TestRoleMapper_WorkspaceAccessPolicyActive(t *testing.T) {
	mappingConfig := RoleMapperConfig{
		WorkspaceMappings: map[string][]WorkspaceGrantConfig{
			"team": {{Workspace: "payments", Role: "viewer"}},
		},
	}

	tests := []struct {
		name     string
		config   RoleMapperConfig
		expected bool
	}{
		{name: "absent", config: RoleMapperConfig{}, expected: false},
		{name: "explicit all", config: RoleMapperConfig{DefaultWorkspaceAccess: "all"}, expected: false},
		{name: "explicit none", config: RoleMapperConfig{DefaultWorkspaceAccess: "none"}, expected: true},
		{name: "workspace mappings", config: mappingConfig, expected: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.config.WorkspaceAccessPolicyActive())
			mapper, err := NewRoleMapper(tc.config)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, mapper.WorkspaceAccessPolicyActive())
			assert.Equal(t, tc.expected, mapper.IsConfigured())
		})
	}
}
