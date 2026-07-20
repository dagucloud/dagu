// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceAccessNormalizeAndClone(t *testing.T) {
	normalized := NormalizeWorkspaceAccess(nil)
	require.True(t, normalized.All)
	require.Empty(t, normalized.Grants)

	normalized = NormalizeWorkspaceAccess(&WorkspaceAccess{
		Grants: []WorkspaceGrant{
			{Workspace: " ops ", Role: RoleDeveloper},
		},
	})
	require.False(t, normalized.All)
	require.Equal(t, []WorkspaceGrant{
		{Workspace: "ops", Role: RoleDeveloper},
	}, normalized.Grants)

	original := &WorkspaceAccess{
		Grants: []WorkspaceGrant{
			{Workspace: "ops", Role: RoleDeveloper},
		},
	}
	cloned := CloneWorkspaceAccess(original)
	require.NotSame(t, original, cloned)
	require.Equal(t, original.Grants, cloned.Grants)
	cloned.Grants[0].Role = RoleViewer
	assert.Equal(t, RoleDeveloper, original.Grants[0].Role)
}

func TestWorkspaceAccessEqual(t *testing.T) {
	assert.True(t, WorkspaceAccessEqual(nil, AllWorkspaceAccess()))
	assert.True(t, WorkspaceAccessEqual(
		&WorkspaceAccess{Grants: []WorkspaceGrant{
			{Workspace: "ops", Role: RoleDeveloper},
			{Workspace: "prod", Role: RoleOperator},
		}},
		&WorkspaceAccess{Grants: []WorkspaceGrant{
			{Workspace: " prod ", Role: RoleOperator},
			{Workspace: "ops", Role: RoleDeveloper},
		}},
	))
	assert.False(t, WorkspaceAccessEqual(
		&WorkspaceAccess{Grants: []WorkspaceGrant{{Workspace: "ops", Role: RoleDeveloper}}},
		&WorkspaceAccess{Grants: []WorkspaceGrant{{Workspace: "ops", Role: RoleViewer}}},
	))
}

func TestWorkspaceAccessEffectiveRole(t *testing.T) {
	scoped := &WorkspaceAccess{
		Grants: []WorkspaceGrant{
			{Workspace: "ops", Role: RoleDeveloper},
			{Workspace: "prod", Role: RoleOperator},
		},
	}

	role, ok := EffectiveRole(RoleViewer, scoped, "")
	require.True(t, ok)
	assert.Equal(t, RoleViewer, role)

	role, ok = EffectiveRole(RoleViewer, scoped, "ops")
	require.True(t, ok)
	assert.Equal(t, RoleDeveloper, role)

	role, ok = EffectiveRole(RoleViewer, scoped, "prod")
	require.True(t, ok)
	assert.Equal(t, RoleOperator, role)

	role, ok = EffectiveRole(RoleViewer, scoped, "missing")
	require.False(t, ok)
	assert.Equal(t, RoleNone, role)

	role, ok = EffectiveRole(RoleManager, AllWorkspaceAccess(), "ops")
	require.True(t, ok)
	assert.Equal(t, RoleManager, role)
}

func TestWorkspaceAccessDefaultWorkspaceUsesGlobalRole(t *testing.T) {
	scoped := &WorkspaceAccess{
		Grants: []WorkspaceGrant{
			{Workspace: "foo", Role: RoleDeveloper},
		},
	}

	defaultRole, ok := EffectiveRole(RoleViewer, scoped, "")
	require.True(t, ok)
	require.Equal(t, RoleViewer, defaultRole)
	require.False(t, defaultRole.CanWrite())

	fooRole, ok := EffectiveRole(RoleViewer, scoped, "foo")
	require.True(t, ok)
	require.Equal(t, RoleDeveloper, fooRole)
	require.True(t, fooRole.CanWrite())
}

func TestValidateWorkspaceAccess(t *testing.T) {
	workspaceExists := func(name string) bool {
		return name == "ops" || name == "prod"
	}

	tests := []struct {
		name       string
		globalRole Role
		access     *WorkspaceAccess
		wantErr    bool
	}{
		{
			name:       "nil access defaults to all workspaces",
			globalRole: RoleAdmin,
			access:     nil,
		},
		{
			name:       "all workspaces",
			globalRole: RoleDeveloper,
			access:     AllWorkspaceAccess(),
		},
		{
			name:       "scoped grants",
			globalRole: RoleViewer,
			access: &WorkspaceAccess{
				Grants: []WorkspaceGrant{
					{Workspace: "ops", Role: RoleDeveloper},
					{Workspace: "prod", Role: RoleOperator},
				},
			},
		},
		{
			name:       "all workspaces cannot include grants",
			globalRole: RoleViewer,
			access: &WorkspaceAccess{
				All: true,
				Grants: []WorkspaceGrant{
					{Workspace: "ops", Role: RoleViewer},
				},
			},
			wantErr: true,
		},
		{
			name:       "scoped access requires global viewer",
			globalRole: RoleDeveloper,
			access: &WorkspaceAccess{
				Grants: []WorkspaceGrant{
					{Workspace: "ops", Role: RoleDeveloper},
				},
			},
			wantErr: true,
		},
		{
			name:       "scoped access requires grants",
			globalRole: RoleViewer,
			access:     &WorkspaceAccess{},
			wantErr:    true,
		},
		{
			name:       "empty workspace",
			globalRole: RoleViewer,
			access: &WorkspaceAccess{
				Grants: []WorkspaceGrant{
					{Workspace: " ", Role: RoleViewer},
				},
			},
			wantErr: true,
		},
		{
			name:       "duplicate workspace",
			globalRole: RoleViewer,
			access: &WorkspaceAccess{
				Grants: []WorkspaceGrant{
					{Workspace: "ops", Role: RoleViewer},
					{Workspace: " ops ", Role: RoleOperator},
				},
			},
			wantErr: true,
		},
		{
			name:       "invalid grant role",
			globalRole: RoleViewer,
			access: &WorkspaceAccess{
				Grants: []WorkspaceGrant{
					{Workspace: "ops", Role: Role("owner")},
				},
			},
			wantErr: true,
		},
		{
			name:       "admin grant is rejected",
			globalRole: RoleViewer,
			access: &WorkspaceAccess{
				Grants: []WorkspaceGrant{
					{Workspace: "ops", Role: RoleAdmin},
				},
			},
			wantErr: true,
		},
		{
			name:       "unknown workspace",
			globalRole: RoleViewer,
			access: &WorkspaceAccess{
				Grants: []WorkspaceGrant{
					{Workspace: "unknown", Role: RoleViewer},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWorkspaceAccess(tt.globalRole, tt.access, workspaceExists)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrInvalidWorkspaceAccess))
				return
			}
			require.NoError(t, err)
		})
	}
}
