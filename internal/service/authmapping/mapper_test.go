// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package authmapping

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
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
		result, err := mapper.Map(context.Background(), []string{"payments-devs", "operators", "admins"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleAdmin, result.Role)
		assert.Equal(t, auth.AllWorkspaceAccess(), result.WorkspaceAccess)
		assert.Equal(t, 3, result.MatchCount)
	})

	t.Run("workspace grants merge by strongest role", func(t *testing.T) {
		result, err := mapper.Map(context.Background(), []string{"payments-readers", "infra-operators", "payments-devs"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleViewer, result.Role)
		assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "infra", Role: auth.RoleOperator},
			{Workspace: "payments", Role: auth.RoleDeveloper},
		}}, result.WorkspaceAccess)
		assert.Equal(t, 3, result.MatchCount)
	})

	t.Run("none fallback has zero named workspace grants", func(t *testing.T) {
		result, err := mapper.Map(context.Background(), []string{"unknown"})
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

	_, err = mapper.Map(context.Background(), []string{"unknown"})
	assert.ErrorIs(t, err, ErrNoMatch)
	_, err = mapper.MapRole([]string{"unknown"})
	assert.ErrorIs(t, err, ErrNoMatch)
}

func TestMapperAllFallback(t *testing.T) {
	mapper, err := New(Config{DefaultRole: auth.RoleManager})
	require.NoError(t, err)
	result, err := mapper.Map(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, auth.RoleManager, result.Role)
	assert.Equal(t, auth.AllWorkspaceAccess(), result.WorkspaceAccess)
}

func TestMapperRejectsInvalidWorkspaceMapping(t *testing.T) {
	_, err := New(Config{DefaultRole: auth.RoleViewer, WorkspaceMappings: map[string][]WorkspaceGrantConfig{
		"admins": {{Workspace: "payments", Role: auth.RoleAdmin}},
	}})
	assert.ErrorContains(t, err, "admin cannot be scoped")

	_, err = New(Config{DefaultRole: auth.RoleViewer, WorkspaceMappings: map[string][]WorkspaceGrantConfig{
		"team": {
			{Workspace: "payments", Role: auth.RoleViewer},
			{Workspace: "payments", Role: auth.RoleOperator},
		},
	}})
	assert.ErrorContains(t, err, "duplicate workspace")
}

func TestMapperRejectsInvalidDefaultRole(t *testing.T) {
	_, err := New(Config{DefaultRole: auth.Role("unknown")})
	assert.ErrorContains(t, err, "invalid default role")
}

// newRegistry returns a registry holding projects with the given names, and
// the ID of each.
func newRegistry(t *testing.T, names ...string) (project.Registry, map[string]string) {
	t.Helper()
	registry := projectregistry.New(t.TempDir())
	ids := make(map[string]string, len(names))
	for _, name := range names {
		p, err := registry.Create(context.Background(), project.CreateInput{Name: name})
		require.NoError(t, err)
		ids[name] = p.ID
	}
	return registry, ids
}

func sortedGrants(grants ...auth.ProjectGrant) []auth.ProjectGrant {
	slices.SortFunc(grants, func(a, b auth.ProjectGrant) int { return strings.Compare(a.ProjectID, b.ProjectID) })
	return grants
}

func TestMapperMapProjects(t *testing.T) {
	registry, ids := newRegistry(t, "payments", "infra")
	mapper, err := New(Config{
		DefaultRole:   auth.RoleOperator,
		GroupMappings: map[string]auth.Role{"operators": auth.RoleOperator},
		// Workspace keys are ignored while project mapping is on.
		WorkspaceMappings: map[string][]WorkspaceGrantConfig{
			"operators": {{Workspace: "legacy", Role: auth.RoleManager}},
		},
		Projects: registry,
		ProjectMappings: map[string][]ProjectGrantConfig{
			"payments-readers": {{Project: "payments", Role: auth.RoleViewer}},
			"payments-devs":    {{Project: "Payments", Role: auth.RoleDeveloper}},
			"infra-managers":   {{Project: "infra", Role: auth.RoleManager}},
			"ghosts":           {{Project: "missing", Role: auth.RoleViewer}},
		},
		DefaultProjectAccess: DefaultWorkspaceAccessNone,
	})
	require.NoError(t, err)
	ctx := context.Background()
	noServerWideRole := &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}

	t.Run("grants merge by strongest role without a server-wide role", func(t *testing.T) {
		result, err := mapper.Map(ctx, []string{"payments-readers", "infra-managers", "payments-devs"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleViewer, result.Role)
		assert.Equal(t, noServerWideRole, result.WorkspaceAccess)
		assert.Equal(t, sortedGrants(
			auth.ProjectGrant{ProjectID: ids["payments"], Role: auth.RoleDeveloper},
			auth.ProjectGrant{ProjectID: ids["infra"], Role: auth.RoleManager},
		), result.ProjectGrants)
		assert.Equal(t, 3, result.MatchCount)
	})

	t.Run("global role keeps the grants", func(t *testing.T) {
		result, err := mapper.Map(ctx, []string{"operators", "infra-managers"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleOperator, result.Role)
		assert.Equal(t, auth.AllWorkspaceAccess(), result.WorkspaceAccess)
		assert.Equal(t, []auth.ProjectGrant{{ProjectID: ids["infra"], Role: auth.RoleManager}}, result.ProjectGrants)
	})

	t.Run("unknown project gives no grant", func(t *testing.T) {
		result, err := mapper.Map(ctx, []string{"ghosts"})
		require.NoError(t, err)
		assert.Equal(t, noServerWideRole, result.WorkspaceAccess)
		assert.Empty(t, result.ProjectGrants)
		assert.Equal(t, []string{"missing"}, result.UnknownProjects)
		assert.Equal(t, 1, result.MatchCount)
	})

	t.Run("none fallback gives no role", func(t *testing.T) {
		result, err := mapper.Map(ctx, []string{"unknown"})
		require.NoError(t, err)
		assert.Equal(t, auth.RoleViewer, result.Role)
		assert.Equal(t, noServerWideRole, result.WorkspaceAccess)
		assert.Empty(t, result.ProjectGrants)
		assert.Zero(t, result.MatchCount)
	})

	assert.True(t, mapper.WorkspaceAccessPolicyActive())
}

func TestMapperMapProjectsFallback(t *testing.T) {
	registry, _ := newRegistry(t)

	mapper, err := New(Config{DefaultRole: auth.RoleDeveloper, Projects: registry})
	require.NoError(t, err)
	result, err := mapper.Map(context.Background(), []string{"unknown"})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleDeveloper, result.Role)
	assert.Equal(t, auth.AllWorkspaceAccess(), result.WorkspaceAccess)
	assert.False(t, mapper.WorkspaceAccessPolicyActive())

	strict, err := New(Config{DefaultRole: auth.RoleViewer, Projects: registry, Strict: true})
	require.NoError(t, err)
	_, err = strict.Map(context.Background(), []string{"unknown"})
	assert.ErrorIs(t, err, ErrNoMatch)
}

func TestMapperRejectsInvalidProjectMapping(t *testing.T) {
	registry, _ := newRegistry(t)

	_, err := New(Config{DefaultRole: auth.RoleViewer, Projects: registry, ProjectMappings: map[string][]ProjectGrantConfig{
		"admins": {{Project: "payments", Role: auth.RoleAdmin}},
	}})
	assert.ErrorContains(t, err, "not a role below admin")

	_, err = New(Config{DefaultRole: auth.RoleViewer, Projects: registry, ProjectMappings: map[string][]ProjectGrantConfig{
		"team": {{Project: "payments", Role: auth.RoleViewer}, {Project: "PAYMENTS", Role: auth.RoleOperator}},
	}})
	assert.ErrorContains(t, err, "duplicate project")

	_, err = New(Config{DefaultRole: auth.RoleViewer, Projects: registry, DefaultProjectAccess: "some"})
	assert.ErrorContains(t, err, "invalid default project access")
}
