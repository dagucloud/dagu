// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	generatedapi "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/license"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type listUsersAuthService struct{ AuthService }

func (listUsersAuthService) ListUsers(context.Context) ([]*auth.User, error) {
	return []*auth.User{}, nil
}

type resetPasswordAuthService struct{ AuthService }

func (resetPasswordAuthService) GetUser(context.Context, string) (*auth.User, error) {
	return &auth.User{Username: "oidc-user"}, nil
}

func (resetPasswordAuthService) ResetPassword(context.Context, string, string) error {
	return authservice.ErrOIDCPasswordManagement
}

func newOIDCWorkspaceSyncConfig() *config.Config {
	return &config.Config{Server: config.Server{
		Auth: config.Auth{
			Mode: config.AuthModeBuiltin,
			OIDC: config.AuthOIDC{
				ClientID:     "client-id",
				ClientSecret: "client-secret",
				ClientURL:    "https://dagu.example.com",
				Issuer:       "https://idp.example.com",
				RoleMapping: config.OIDCRoleMapping{
					DefaultWorkspaceAccess: config.OIDCDefaultWorkspaceAccessNone,
				},
			},
		},
	}}
}

func TestOIDCWorkspaceAccessSyncEnabled(t *testing.T) {
	t.Parallel()

	configuredPolicy := newOIDCWorkspaceSyncConfig()
	nonBuiltin := newOIDCWorkspaceSyncConfig()
	nonBuiltin.Server.Auth.Mode = config.AuthModeBasic
	incompleteOIDC := newOIDCWorkspaceSyncConfig()
	incompleteOIDC.Server.Auth.OIDC.ClientID = ""
	inactivePolicy := newOIDCWorkspaceSyncConfig()
	inactivePolicy.Server.Auth.OIDC.RoleMapping.DefaultWorkspaceAccess = config.OIDCDefaultWorkspaceAccessAll
	syncDisabled := newOIDCWorkspaceSyncConfig()
	syncDisabled.Server.Auth.OIDC.RoleMapping.SkipOrgRoleSync = true
	licensedManager := license.NewTestManager(license.FeatureSSO)
	unlicensedManager := license.NewTestManager(license.FeatureRBAC)

	tests := []struct {
		name           string
		config         *config.Config
		licenseManager *license.Manager
		want           bool
	}{
		{name: "missing config", config: nil, want: false},
		{name: "configured policy", config: configuredPolicy, want: true},
		{name: "licensed policy", config: configuredPolicy, licenseManager: licensedManager, want: true},
		{name: "SSO not licensed", config: configuredPolicy, licenseManager: unlicensedManager, want: false},
		{name: "non builtin auth", config: nonBuiltin, want: false},
		{name: "incomplete OIDC", config: incompleteOIDC, want: false},
		{name: "inactive policy", config: inactivePolicy, want: false},
		{name: "sync disabled", config: syncDisabled, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &API{config: tt.config, licenseManager: tt.licenseManager}
			assert.Equal(t, tt.want, a.oidcWorkspaceAccessSyncEnabled())
		})
	}
}

func TestListUsersReportsOIDCWorkspaceAccessSyncState(t *testing.T) {
	t.Parallel()

	a := &API{config: newOIDCWorkspaceSyncConfig(), authService: listUsersAuthService{}}
	ctx := auth.WithUser(context.Background(), &auth.User{Role: auth.RoleAdmin})

	result, err := a.ListUsers(ctx, generatedapi.ListUsersRequestObject{})
	require.NoError(t, err)
	response, ok := result.(generatedapi.ListUsers200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, response.OidcWorkspaceAccessSyncEnabled)
	assert.True(t, *response.OidcWorkspaceAccessSyncEnabled)
}

func TestResetUserPasswordRejectsOIDCUser(t *testing.T) {
	t.Parallel()

	a := &API{authService: resetPasswordAuthService{}}
	ctx := auth.WithUser(context.Background(), &auth.User{Role: auth.RoleAdmin})

	result, err := a.ResetUserPassword(ctx, generatedapi.ResetUserPasswordRequestObject{
		UserId: "oidc-user-id",
		Body:   &generatedapi.ResetPasswordRequest{NewPassword: "newpassword1"},
	})

	assert.Nil(t, result)
	require.Error(t, err)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.HTTPStatus)
	assert.Equal(t, generatedapi.ErrorCodeForbidden, apiErr.Code)
	assert.Equal(t, "Password is managed by the identity provider for this user", apiErr.Message)
}
