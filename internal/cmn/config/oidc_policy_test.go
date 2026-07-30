// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestOIDCProvisioningPolicyLoaderReadsCurrentMergedPolicy(t *testing.T) {
	homeDir := t.TempDir()
	configFile := filepath.Join(homeDir, "config.yaml")
	adminFile := filepath.Join(homeDir, "admin.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
auth:
  mode: builtin
  oidc:
    auto_signup: true
    allowed_domains: [example.com]
    role_mapping:
      default_role: viewer
      group_mappings:
        team: viewer
`), 0600))
	require.NoError(t, os.WriteFile(adminFile, []byte(`
auth:
  oidc:
    role_mapping:
      group_mappings:
        team: manager
`), 0600))

	cfg, err := NewConfigLoader(
		viper.New(),
		WithAppHomeDir(homeDir),
		WithService(ServiceServer),
	).Load()
	require.NoError(t, err)
	require.Equal(t, []string{configFile, adminFile}, cfg.Paths.ConfigFilesUsed)

	loader := NewOIDCProvisioningPolicyLoader(
		cfg.Paths.ConfigFilesUsed,
		cfg.Server.Auth.OIDC.ProvisioningPolicy(),
	)
	policy, err := loader.Load()
	require.NoError(t, err)
	require.True(t, policy.AutoSignup)
	require.Equal(t, []string{"example.com"}, policy.AllowedDomains)
	require.Equal(t, "manager", policy.RoleMapping.GroupMappings["team"])

	require.NoError(t, os.WriteFile(adminFile, []byte(`
auth:
  oidc:
    auto_signup: false
    allowed_domains: [new.example.com]
    role_mapping:
      group_mappings:
        team: operator
`), 0600))
	policy, err = loader.Load()
	require.NoError(t, err)
	require.False(t, policy.AutoSignup)
	require.Equal(t, []string{"new.example.com"}, policy.AllowedDomains)
	require.Equal(t, "operator", policy.RoleMapping.GroupMappings["team"])
}

func TestOIDCProvisioningPolicyLoaderRejectsInvalidCurrentMapping(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        team:
          - workspace: payments
            role: invalid
      default_workspace_access: none
`), 0600))

	loader := NewOIDCProvisioningPolicyLoader(
		[]string{configFile},
		OIDCProvisioningPolicy{},
	)
	_, err := loader.Load()
	require.ErrorContains(t, err, "invalid role")
}

func TestOIDCProvisioningPolicyLoaderAppliesEnvironmentOverrides(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
auth:
  oidc:
    auto_signup: true
    allowed_domains: [yaml.example.com]
    role_mapping:
      default_role: viewer
`), 0600))
	t.Setenv("DAGU_AUTH_OIDC_AUTO_SIGNUP", "false")
	t.Setenv("DAGU_AUTH_OIDC_ALLOWED_DOMAINS", "env.example.com")
	t.Setenv("DAGU_AUTH_OIDC_DEFAULT_ROLE", "developer")

	policy, err := NewOIDCProvisioningPolicyLoader(
		[]string{configFile},
		OIDCProvisioningPolicy{},
	).Load()
	require.NoError(t, err)
	require.False(t, policy.AutoSignup)
	require.Equal(t, []string{"env.example.com"}, policy.AllowedDomains)
	require.Equal(t, "developer", policy.RoleMapping.DefaultRole)
}

func TestOIDCProvisioningPolicyLoaderAppliesPolicyDefaults(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte("# provider configured through environment"), 0600))

	policy, err := NewOIDCProvisioningPolicyLoader(
		[]string{configFile},
		OIDCProvisioningPolicy{},
	).Load()
	require.NoError(t, err)
	require.True(t, policy.AutoSignup)
	require.Equal(t, "viewer", policy.RoleMapping.DefaultRole)
	require.Equal(t, OIDCDefaultWorkspaceAccessAll, policy.RoleMapping.DefaultWorkspaceAccess)
}

func TestOIDCProvisioningPolicyLoaderUsesFallbackWithoutConfigFiles(t *testing.T) {
	fallback := OIDCProvisioningPolicy{
		AutoSignup:     true,
		AllowedDomains: []string{"example.com"},
		RoleMapping: OIDCRoleMapping{
			DefaultRole: "developer",
		},
	}

	policy, err := NewOIDCProvisioningPolicyLoader(nil, fallback).Load()
	require.NoError(t, err)
	require.Equal(t, fallback, policy)
}
