// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package trustedproxyprovision

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

func TestProcessLoginMapsProjectGrants(t *testing.T) {
	ctx := context.Background()
	registry := projectregistry.New(t.TempDir())
	payments, err := registry.Create(ctx, project.CreateInput{Name: "payments"})
	require.NoError(t, err)
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	service := newTestService(t, userStore, func(config *Config) {
		config.RoleMapping.Projects = registry
		config.RoleMapping.ProjectMappings = map[string][]authmapping.ProjectGrantConfig{
			"Payments": {{Project: "payments", Role: auth.RoleManager}},
		}
		config.RoleMapping.DefaultProjectAccess = authmapping.DefaultWorkspaceAccessNone
	})

	user, created, err := service.ProcessLogin(ctx, "alice", []string{"Payments"})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, user.WorkspaceAccess)
	assert.Equal(t, []auth.ProjectGrant{{ProjectID: payments.ID, Role: auth.RoleManager}}, user.ProjectGrants)

	// Group names keep their case, so "payments" matches no mapping.
	user, created, err = service.ProcessLogin(ctx, "alice", []string{"payments"})
	require.NoError(t, err)
	assert.False(t, created)
	assert.Empty(t, user.ProjectGrants)
	persisted, err := userStore.GetByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Empty(t, persisted.ProjectGrants)
}

// While projects are off, login leaves project grants as stored.
func TestProcessLoginKeepsProjectGrantsWhileProjectsOff(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	grants := []auth.ProjectGrant{{ProjectID: "project-a", Role: auth.RoleManager}}
	existing := auth.NewUser("trusted", "", auth.RoleViewer)
	existing.AuthProvider = auth.AuthProviderProxy
	existing.TrustedProxyUser = "opaque-user"
	existing.ProjectGrants = grants
	require.NoError(t, userStore.Create(ctx, existing))
	service := newTestService(t, userStore, func(config *Config) {
		config.RoleMapping.GroupMappings = map[string]auth.Role{"Operators": auth.RoleOperator}
	})

	user, _, err := service.ProcessLogin(ctx, "opaque-user", []string{"Operators"})
	require.NoError(t, err)
	assert.Equal(t, auth.RoleOperator, user.Role)
	assert.Equal(t, grants, user.ProjectGrants)
}
