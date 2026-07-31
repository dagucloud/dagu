// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	generatedapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type listUsersAuthService struct{ AuthService }

func (listUsersAuthService) ListUsers(context.Context) ([]*auth.User, error) {
	return []*auth.User{}, nil
}

type resetPasswordAuthService struct{ AuthService }

func (resetPasswordAuthService) GetUser(context.Context, string) (*auth.User, error) {
	return &auth.User{Username: "external-user"}, nil
}

func (resetPasswordAuthService) ResetPassword(context.Context, string, string) error {
	return authservice.ErrExternalAuthPasswordManagement
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
			mapping := a.currentOIDCMapping()
			assert.Equal(t, tt.want, a.oidcWorkspaceSync(mapping))
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
	assert.Equal(t, []generatedapi.UserAuthProvider{generatedapi.UserAuthProviderOidc}, response.ManagedRoleProviders)
	assert.Equal(t, []generatedapi.UserAuthProvider{generatedapi.UserAuthProviderOidc}, response.ManagedWorkspaceAccessProviders)
}

func TestListUsersReportsCurrentOIDCPolicy(t *testing.T) {
	t.Parallel()

	cfg := newOIDCWorkspaceSyncConfig()
	cfg.Server.Auth.OIDC.RoleMapping.DefaultWorkspaceAccess = config.OIDCDefaultWorkspaceAccessAll
	a := &API{
		config:      cfg,
		authService: listUsersAuthService{},
		oidcRoleMapping: func() config.OIDCRoleMapping {
			return config.OIDCRoleMapping{
				DefaultWorkspaceAccess: config.OIDCDefaultWorkspaceAccessNone,
			}
		},
	}
	ctx := auth.WithUser(context.Background(), &auth.User{Role: auth.RoleAdmin})

	result, err := a.ListUsers(ctx, generatedapi.ListUsersRequestObject{})
	require.NoError(t, err)
	response, ok := result.(generatedapi.ListUsers200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, response.OidcWorkspaceAccessSyncEnabled)
	assert.True(t, *response.OidcWorkspaceAccessSyncEnabled)
	assert.Equal(t, []generatedapi.UserAuthProvider{generatedapi.UserAuthProviderOidc}, response.ManagedRoleProviders)
	assert.Equal(t, []generatedapi.UserAuthProvider{generatedapi.UserAuthProviderOidc}, response.ManagedWorkspaceAccessProviders)
}

func TestListUsersReportsOIDCRoleOnlySyncAsManaged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		configure func(*config.OIDCRoleMapping)
		want      []generatedapi.UserAuthProvider
	}{
		{
			name: "group mapping",
			configure: func(mapping *config.OIDCRoleMapping) {
				mapping.GroupMappings = map[string]string{"developers": "developer"}
			},
			want: []generatedapi.UserAuthProvider{generatedapi.UserAuthProviderOidc},
		},
		{
			name: "role attribute",
			configure: func(mapping *config.OIDCRoleMapping) {
				mapping.RoleAttributePath = ".role"
			},
			want: []generatedapi.UserAuthProvider{generatedapi.UserAuthProviderOidc},
		},
		{
			name: "sync skipped",
			configure: func(mapping *config.OIDCRoleMapping) {
				mapping.GroupMappings = map[string]string{"developers": "developer"}
				mapping.SkipOrgRoleSync = true
			},
			want: []generatedapi.UserAuthProvider{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := newOIDCWorkspaceSyncConfig()
			mapping := &cfg.Server.Auth.OIDC.RoleMapping
			mapping.DefaultWorkspaceAccess = config.OIDCDefaultWorkspaceAccessAll
			tt.configure(mapping)
			a := &API{config: cfg, authService: listUsersAuthService{}}
			ctx := auth.WithUser(context.Background(), &auth.User{Role: auth.RoleAdmin})

			result, err := a.ListUsers(ctx, generatedapi.ListUsersRequestObject{})
			require.NoError(t, err)
			response, ok := result.(generatedapi.ListUsers200JSONResponse)
			require.True(t, ok)
			require.NotNil(t, response.OidcWorkspaceAccessSyncEnabled)
			assert.False(t, *response.OidcWorkspaceAccessSyncEnabled)
			assert.Equal(t, tt.want, response.ManagedRoleProviders)
			assert.Empty(t, response.ManagedWorkspaceAccessProviders)
		})
	}
}

func TestManagedProvidersIncludesProxySync(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Server: config.Server{Auth: config.Auth{
		Mode: config.AuthModeBuiltin,
		Proxy: config.AuthTrustedProxy{
			Enabled: true,
			RoleMapping: config.TrustedProxyRoleMapping{
				SkipOrgRoleSync: false,
			},
		},
	}}}
	a := &API{config: cfg}
	assert.Equal(t,
		[]generatedapi.UserAuthProvider{generatedapi.UserAuthProviderProxy},
		a.managedProviders(false),
	)

	cfg.Server.Auth.Proxy.RoleMapping.SkipOrgRoleSync = true
	assert.Empty(t, a.managedProviders(false))

	cfg.Server.Auth.Proxy.RoleMapping.SkipOrgRoleSync = false
	a.licenseManager = license.NewTestManager(license.FeatureRBAC)
	assert.Empty(t, a.managedProviders(false))

	a.licenseManager = license.NewTestManager(license.FeatureSSO)
	assert.Equal(t,
		[]generatedapi.UserAuthProvider{generatedapi.UserAuthProviderProxy},
		a.managedProviders(false),
	)

	cfg.Server.Auth.Mode = config.AuthModeBasic
	assert.Empty(t, a.managedProviders(false))
}

func TestResetUserPasswordRejectsExternalUser(t *testing.T) {
	t.Parallel()

	a := &API{authService: resetPasswordAuthService{}}
	ctx := auth.WithUser(context.Background(), &auth.User{Role: auth.RoleAdmin})

	result, err := a.ResetUserPassword(ctx, generatedapi.ResetUserPasswordRequestObject{
		UserId: "external-user-id",
		Body:   &generatedapi.ResetPasswordRequest{NewPassword: "newpassword1"},
	})

	assert.Nil(t, result)
	require.Error(t, err)
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.HTTPStatus)
	assert.Equal(t, generatedapi.ErrorCodeForbidden, apiErr.Code)
	assert.Equal(t, "Password is managed by the authentication provider for this user", apiErr.Message)
}
