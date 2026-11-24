// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleInProject(t *testing.T) {
	const projectID = "project-a"
	tests := []struct {
		name       string
		serverRole Role
		grants     []ProjectGrant
		want       Role
	}{
		{name: "ServerRoleOnly", serverRole: RoleOperator, want: RoleOperator},
		{
			name:   "GrantOnly",
			grants: []ProjectGrant{{ProjectID: projectID, Role: RoleDeveloper}},
			want:   RoleDeveloper,
		},
		{
			name:       "GrantAboveServerRole",
			serverRole: RoleViewer,
			grants:     []ProjectGrant{{ProjectID: projectID, Role: RoleManager}},
			want:       RoleManager,
		},
		{
			name:       "ServerRoleAboveGrant",
			serverRole: RoleAdmin,
			grants:     []ProjectGrant{{ProjectID: projectID, Role: RoleViewer}},
			want:       RoleAdmin,
		},
		{
			name:   "GrantOnAnotherProject",
			grants: []ProjectGrant{{ProjectID: "project-b", Role: RoleManager}},
			want:   RoleNone,
		},
		{name: "Neither", want: RoleNone},
		{
			// A role this version does not know gives no access.
			name:       "UnknownRoles",
			serverRole: Role("owner"),
			grants:     []ProjectGrant{{ProjectID: projectID, Role: Role("owner")}},
			want:       RoleNone,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RoleInProject(tt.serverRole, tt.grants, projectID))
		})
	}
}

func TestValidateProjectGrants(t *testing.T) {
	exists := func(id string) bool { return id == "project-a" || id == "project-b" }
	tests := []struct {
		name    string
		grants  []ProjectGrant
		wantErr string
	}{
		{name: "None"},
		{
			name: "Valid",
			grants: []ProjectGrant{
				{ProjectID: "project-a", Role: RoleManager},
				{ProjectID: "project-b", Role: RoleViewer},
			},
		},
		{
			name:    "MissingProjectID",
			grants:  []ProjectGrant{{Role: RoleViewer}},
			wantErr: "project ID is required",
		},
		{
			name: "RepeatedProject",
			grants: []ProjectGrant{
				{ProjectID: "project-a", Role: RoleViewer},
				{ProjectID: "project-a", Role: RoleOperator},
			},
			wantErr: "more than one grant",
		},
		{
			name:    "InvalidRole",
			grants:  []ProjectGrant{{ProjectID: "project-a", Role: Role("owner")}},
			wantErr: "invalid role",
		},
		{
			name:    "Admin",
			grants:  []ProjectGrant{{ProjectID: "project-a", Role: RoleAdmin}},
			wantErr: "admin applies only server-wide",
		},
		{
			name:    "UnknownProject",
			grants:  []ProjectGrant{{ProjectID: "project-c", Role: RoleViewer}},
			wantErr: "does not exist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProjectGrants(tt.grants, exists)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrInvalidProjectGrant)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}

	t.Run("NilExistsAcceptsAnyProject", func(t *testing.T) {
		require.NoError(t, ValidateProjectGrants([]ProjectGrant{{ProjectID: "project-c", Role: RoleViewer}}, nil))
	})
}
