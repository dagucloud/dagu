// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/url"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceSelection(t *testing.T) {
	t.Run("defaults to all", func(t *testing.T) {
		selection, err := parseWorkspaceSelection(nil)
		require.NoError(t, err)
		require.Equal(t, workspaceSelectionAll, selection.mode)
		require.Empty(t, selection.workspace)
		require.False(t, selection.explicit)
	})

	t.Run("accepts named workspace", func(t *testing.T) {
		workspace := api.Workspace("ops")
		selection, err := parseWorkspaceSelection(&workspace)
		require.NoError(t, err)
		require.Equal(t, workspaceSelectionNamed, selection.mode)
		require.Equal(t, "ops", selection.workspace)
		require.True(t, selection.explicit)
	})

	t.Run("accepts all", func(t *testing.T) {
		workspace := api.Workspace("all")
		selection, err := parseWorkspaceSelection(&workspace)
		require.NoError(t, err)
		require.Equal(t, workspaceSelectionAll, selection.mode)
		require.Empty(t, selection.workspace)
		require.True(t, selection.explicit)
	})

	t.Run("accepts default", func(t *testing.T) {
		workspace := api.Workspace("default")
		selection, err := parseWorkspaceSelection(&workspace)
		require.NoError(t, err)
		require.Equal(t, workspaceSelectionDefault, selection.mode)
		require.Empty(t, selection.workspace)
		require.True(t, selection.explicit)
	})
}

func TestWorkspaceParamFromValuesRejectsExplicitEmptyWorkspace(t *testing.T) {
	params := url.Values{"workspace": []string{""}}

	workspace := workspaceParamFromValues(params)
	require.NotNil(t, workspace)
	require.Empty(t, *workspace)

	_, err := parseWorkspaceSelection(workspace)
	require.Error(t, err)
}

func TestWorkspaceParamFromValuesPreservesExplicitEmptyWorkspace(t *testing.T) {
	params := url.Values{"workspace": []string{""}}

	workspace := workspaceParamFromValues(params)
	require.NotNil(t, workspace)
	require.Empty(t, *workspace)
}

func TestWorkspaceFilterForContextMCPDefaultGrantKeepsDefaultWorkspace(t *testing.T) {
	var authSvc *authservice.Service
	api := &API{authService: authSvc}
	ctx := auth.WithUser(context.Background(), &auth.User{
		ID:       "user-1",
		Username: "viewer",
		Role:     auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{
			Grants: []auth.WorkspaceGrant{
				{Workspace: "default", Role: auth.RoleViewer},
			},
		},
	})
	ctx = audit.WithSourceContext(ctx, &audit.SourceContext{Source: "mcp"})

	filter := api.workspaceFilterForContext(ctx)

	require.NotNil(t, filter)
	require.True(t, filter.Enabled)
	require.Equal(t, []string{"default"}, filter.Workspaces)
	require.True(t, filter.IncludeUnlabelled)
}

// A server without users of its own lets every caller do anything, except a
// user Dagu Cloud vouches for, whom its role still limits.
func TestCloudUserRoleAppliesWithoutLocalUsers(t *testing.T) {
	a := &API{config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{config.PermissionWriteDAGs: true}}}}
	cloud := func(role auth.Role) context.Context {
		return auth.WithUser(context.Background(), &auth.User{ID: "cloud:user-1", Role: role, AuthProvider: auth.AuthProviderCloud})
	}
	require.NoError(t, a.requireExecuteForWorkspace(context.Background(), ""))
	require.NoError(t, a.requireDAGWrite(context.Background()))

	require.ErrorIs(t, a.requireExecuteForWorkspace(cloud(auth.RoleViewer), ""), errInsufficientPermissions)
	require.NoError(t, a.requireExecuteForWorkspace(cloud(auth.RoleOperator), ""))
	require.ErrorIs(t, a.requireDAGWrite(cloud(auth.RoleOperator)), errInsufficientPermissions)
	require.NoError(t, a.requireDAGWrite(cloud(auth.RoleDeveloper)))
	require.ErrorIs(t, a.requireManagerOrAbove(cloud(auth.RoleDeveloper)), errInsufficientPermissions)
}
