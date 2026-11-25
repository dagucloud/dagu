// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oidcprovision

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/service/authmapping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessLogin_MapsProjectGrants(t *testing.T) {
	ctx := context.Background()
	registry := projectregistry.New(t.TempDir())
	payments, err := registry.Create(ctx, project.CreateInput{Name: "payments"})
	require.NoError(t, err)

	store := newMockUserStore()
	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		AutoSignup:  true,
		DefaultRole: auth.RoleManager,
		RoleMapping: RoleMapperConfig{
			DefaultRole: auth.RoleManager,
			Projects:    registry,
			ProjectMappings: map[string][]authmapping.ProjectGrantConfig{
				"payments-team": {{Project: "payments", Role: auth.RoleDeveloper}},
			},
			DefaultProjectAccess: authmapping.DefaultWorkspaceAccessNone,
		},
	})
	require.NoError(t, err)
	claims := OIDCClaims{
		Subject:   "subject",
		Email:     "dev@example.com",
		RawClaims: map[string]any{"groups": []any{"payments-team"}},
	}

	user, isNew, err := svc.ProcessLogin(ctx, claims)
	require.NoError(t, err)
	assert.True(t, isNew)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, user.WorkspaceAccess)
	assert.Equal(t, []auth.ProjectGrant{{ProjectID: payments.ID, Role: auth.RoleDeveloper}}, user.ProjectGrants)
	assert.Equal(t, auth.RoleDeveloper, auth.RoleInProject(auth.RoleNone, user.ProjectGrants, payments.ID))

	// Leaving the group removes the grant at the next login.
	claims.RawClaims = map[string]any{"groups": []any{"other"}}
	user, _, err = svc.ProcessLogin(ctx, claims)
	require.NoError(t, err)
	assert.Empty(t, user.ProjectGrants)
	stored, err := store.GetByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.ProjectGrants)
}

// While projects are off, login leaves project grants as stored.
func TestProcessLogin_KeepsProjectGrantsWhileProjectsOff(t *testing.T) {
	ctx := context.Background()
	store := newMockUserStore()
	grants := []auth.ProjectGrant{{ProjectID: "project-a", Role: auth.RoleManager}}
	require.NoError(t, store.Create(ctx, &auth.User{
		ID:              "existing-id",
		Username:        "existing",
		Role:            auth.RoleViewer,
		WorkspaceAccess: auth.AllWorkspaceAccess(),
		ProjectGrants:   grants,
		AuthProvider:    auth.AuthProviderOIDC,
		OIDCIssuer:      "https://issuer.example.com",
		OIDCSubject:     "subject",
	}))

	svc, err := New(store, Config{
		Issuer:      "https://issuer.example.com",
		DefaultRole: auth.RoleViewer,
		RoleMapping: RoleMapperConfig{
			DefaultRole: auth.RoleViewer,
			WorkspaceMappings: map[string][]WorkspaceGrantConfig{
				"team": {{Workspace: "payments", Role: "operator"}},
			},
			DefaultWorkspaceAccess: authmapping.DefaultWorkspaceAccessNone,
		},
	})
	require.NoError(t, err)

	user, _, err := svc.ProcessLogin(ctx, OIDCClaims{
		Subject:   "subject",
		Email:     "existing@example.com",
		RawClaims: map[string]any{"groups": []any{"team"}},
	})
	require.NoError(t, err)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "payments", Role: auth.RoleOperator}}}, user.WorkspaceAccess)
	assert.Equal(t, grants, user.ProjectGrants)
}
