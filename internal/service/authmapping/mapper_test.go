// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package authmapping

import (
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapperMap(t *testing.T) {
	mapper, err := New(Config{
		DefaultRole: auth.RoleOperator,
		GroupMappings: map[string]auth.Role{
			"operators": auth.RoleOperator,
			"admins":    auth.RoleAdmin,
		},
		WorkspaceMappings: map[string][]WorkspaceGrantConfig{
			"payments-readers": {{Workspace: "payments", Role: auth.RoleViewer}},
			"payments-devs":    {{Workspace: "payments", Role: auth.RoleDeveloper}},
			"infra-operators":  {{Workspace: "infra", Role: auth.RoleOperator}},
		},
		DefaultWorkspaceAccess: DefaultWorkspaceAccessNone,
	})
	require.NoError(t, err)

	t.Run("highest global role replaces workspace grants", func(t *testing.T) {
		result, err := mapper.Map([]string{"payments-devs", "operators", "admins"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleAdmin, result.Role)
		assert.Equal(t, auth.AllWorkspaceAccess(), result.WorkspaceAccess)
		assert.Equal(t, 3, result.MatchCount)
	})

	t.Run("workspace grants merge by strongest role", func(t *testing.T) {
		result, err := mapper.Map([]string{"payments-readers", "infra-operators", "payments-devs"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleViewer, result.Role)
		assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "infra", Role: auth.RoleOperator},
			{Workspace: "payments", Role: auth.RoleDeveloper},
		}}, result.WorkspaceAccess)
		assert.Equal(t, 3, result.MatchCount)
	})

	t.Run("none fallback has zero named workspace grants", func(t *testing.T) {
		result, err := mapper.Map([]string{"unknown"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleViewer, result.Role)
		assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, result.WorkspaceAccess)
		assert.Zero(t, result.MatchCount)
	})
}

func TestMapperStrictNoMatch(t *testing.T) {
	mapper, err := New(Config{
		DefaultRole:   auth.RoleViewer,
		GroupMappings: map[string]auth.Role{"admins": auth.RoleAdmin},
		Strict:        true,
	})
	require.NoError(t, err)

	_, err = mapper.Map([]string{"unknown"})
	assert.ErrorIs(t, err, ErrNoMatch)
	_, err = mapper.MapRole([]string{"unknown"})
	assert.ErrorIs(t, err, ErrNoMatch)
}

func TestMapperAllFallback(t *testing.T) {
	mapper, err := New(Config{DefaultRole: auth.RoleManager})
	require.NoError(t, err)
	result, err := mapper.Map(nil)
	require.NoError(t, err)
	assert.Equal(t, auth.RoleManager, result.Role)
	assert.Equal(t, auth.AllWorkspaceAccess(), result.WorkspaceAccess)
}

func TestMapperRejectsInvalidWorkspaceMapping(t *testing.T) {
	_, err := New(Config{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
		"admins": {{Workspace: "payments", Role: auth.RoleAdmin}},
	}})
	assert.ErrorContains(t, err, "admin cannot be scoped")

	_, err = New(Config{WorkspaceMappings: map[string][]WorkspaceGrantConfig{
		"team": {
			{Workspace: "payments", Role: auth.RoleViewer},
			{Workspace: "payments", Role: auth.RoleOperator},
		},
	}})
	assert.ErrorContains(t, err, "duplicate workspace")
}
