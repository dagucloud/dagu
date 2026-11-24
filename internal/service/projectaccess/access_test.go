// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectaccess_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/service/projectaccess"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is a registry with the default project and two others.
type fixture struct {
	checker *projectaccess.Checker
	def     project.Project
	weather project.Project
	grid    project.Project
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := t.Context()
	reg := projectregistry.New(t.TempDir())
	def, err := reg.EnsureDefault(ctx)
	require.NoError(t, err)
	weather, err := reg.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	grid, err := reg.Create(ctx, project.CreateInput{Name: "grid"})
	require.NoError(t, err)
	return fixture{checker: projectaccess.New(reg), def: def, weather: weather, grid: grid}
}

func TestList(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	tests := []struct {
		name string
		user *auth.User
		want []projectaccess.Access
	}{
		{
			name: "ServerRoleSeesEveryProject",
			user: &auth.User{Role: auth.RoleOperator},
			want: []projectaccess.Access{
				{Project: f.def, Role: auth.RoleOperator},
				{Project: f.grid, Role: auth.RoleOperator},
				{Project: f.weather, Role: auth.RoleOperator},
			},
		},
		{
			name: "GrantsOnly",
			user: &auth.User{ProjectGrants: []auth.ProjectGrant{
				{ProjectID: f.weather.ID, Role: auth.RoleDeveloper},
			}},
			want: []projectaccess.Access{{Project: f.weather, Role: auth.RoleDeveloper}},
		},
		{
			name: "HigherOfServerRoleAndGrant",
			user: &auth.User{Role: auth.RoleViewer, ProjectGrants: []auth.ProjectGrant{
				{ProjectID: f.grid.ID, Role: auth.RoleManager},
			}},
			want: []projectaccess.Access{
				{Project: f.def, Role: auth.RoleViewer},
				{Project: f.grid, Role: auth.RoleManager},
				{Project: f.weather, Role: auth.RoleViewer},
			},
		},
		{name: "NoRole", user: &auth.User{}, want: []projectaccess.Access{}},
		{name: "NilUser", want: []projectaccess.Access{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.checker.List(t.Context(), tt.user)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	user := &auth.User{ProjectGrants: []auth.ProjectGrant{
		{ProjectID: f.weather.ID, Role: auth.RoleOperator},
	}}

	got, err := f.checker.Get(t.Context(), user, "Weather")
	require.NoError(t, err)
	assert.Equal(t, projectaccess.Access{Project: f.weather, Role: auth.RoleOperator}, got)

	admin := &auth.User{Role: auth.RoleAdmin}
	got, err = f.checker.Get(t.Context(), admin, project.DefaultName)
	require.NoError(t, err)
	assert.Equal(t, projectaccess.Access{Project: f.def, Role: auth.RoleAdmin}, got)
}

// A project the user has no role in looks exactly like one that does not
// exist, so the API can answer 404 for both.
func TestGetHidesProjectWithoutRole(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	user := &auth.User{ProjectGrants: []auth.ProjectGrant{
		{ProjectID: f.weather.ID, Role: auth.RoleOperator},
	}}

	_, hiddenErr := f.checker.Get(t.Context(), user, "grid")
	require.ErrorIs(t, hiddenErr, project.ErrNotFound)
	_, missingErr := f.checker.Get(t.Context(), user, "solar")
	require.ErrorIs(t, missingErr, project.ErrNotFound)
	assert.Equal(t, `project not found: "grid"`, hiddenErr.Error())
	assert.Equal(t, `project not found: "solar"`, missingErr.Error())

	_, err := f.checker.Get(t.Context(), nil, "weather")
	require.ErrorIs(t, err, project.ErrNotFound)
}
