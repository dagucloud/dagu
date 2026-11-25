// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const projectsOn = "experimental_projects: true\n"

const workspaceAccessYAML = `
auth:
  mode: builtin
  oidc:
    role_mapping:
      workspace_mappings:
        sre:
          - workspace: payments
            role: operator
      default_workspace_access: none
  proxy:
    enabled: true
    headers:
      user: X-User
      groups: X-Groups
    role_mapping:
      workspace_mappings:
        Team:
          - workspace: payments
            role: developer
      default_workspace_access: all
`

// With projects off, the project keys are not read and the workspace keys
// keep their v2 meaning with no warning.
func TestLoad_ProjectAccessKeysWhileProjectsOff(t *testing.T) {
	cfg := loadFromYAML(t, workspaceAccessYAML+`
      project_mappings:
        Team:
          - project: payments
            role: viewer
`)

	oidc, proxy := cfg.Server.Auth.OIDC.RoleMapping, cfg.Server.Auth.Proxy.RoleMapping
	require.Nil(t, oidc.ProjectMappings)
	require.Empty(t, oidc.DefaultProjectAccess)
	require.Nil(t, proxy.ProjectMappings)
	require.Empty(t, proxy.DefaultProjectAccess)
	require.Equal(t, map[string][]OIDCWorkspaceGrant{"sre": {{Workspace: "payments", Role: "operator"}}}, oidc.WorkspaceMappings)
	require.Equal(t, OIDCDefaultWorkspaceAccessNone, oidc.DefaultWorkspaceAccess)
	require.Equal(t, TrustedProxyDefaultWorkspaceAccessAll, proxy.DefaultWorkspaceAccess)
	for _, warning := range cfg.Warnings {
		require.NotContains(t, warning, "project")
	}
}

// With projects off, the project mapping variables are not read, so a value
// v2 never parsed cannot stop the config from loading.
func TestLoad_ProjectMappingEnvironmentWhileProjectsOff(t *testing.T) {
	t.Setenv("DAGU_AUTH_OIDC_PROJECT_MAPPINGS", "not json")
	t.Setenv("DAGU_AUTH_PROXY_PROJECT_MAPPINGS", "not json")

	cfg := loadFromYAML(t, "auth:\n  mode: builtin\n")
	require.Nil(t, cfg.Server.Auth.OIDC.RoleMapping.ProjectMappings)
	require.Nil(t, cfg.Server.Auth.Proxy.RoleMapping.ProjectMappings)

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte("auth:\n  mode: builtin\n"), 0600))
	_, err := NewOIDCPolicyLoader([]string{configFile}, OIDCPolicy{}, false).Load()
	require.NoError(t, err)

	err = loadWithErrorFromYAML(t, projectsOn+"auth:\n  mode: builtin\n")
	require.ErrorContains(t, err, "DAGU_AUTH_OIDC_PROJECT_MAPPINGS")
}

func TestLoad_ProjectAccessKeys(t *testing.T) {
	cfg := loadFromYAML(t, projectsOn+`
auth:
  mode: builtin
  oidc:
    role_mapping:
      project_mappings:
        sre:
          - project: payments
            role: operator
      default_project_access: none
  proxy:
    enabled: true
    headers:
      user: X-User
      groups: X-Groups
    role_mapping:
      project_mappings:
        Team:
          - project: payments
            role: developer
        team:
          - project: default
            role: viewer
      default_project_access: all
`)

	oidc, proxy := cfg.Server.Auth.OIDC.RoleMapping, cfg.Server.Auth.Proxy.RoleMapping
	require.Equal(t, map[string][]ProjectMappingGrant{"sre": {{Project: "payments", Role: "operator"}}}, oidc.ProjectMappings)
	require.Equal(t, projectAccessNone, oidc.DefaultProjectAccess)
	// Proxy group names keep their case, as with the workspace keys.
	require.Equal(t, map[string][]ProjectMappingGrant{
		"Team": {{Project: "payments", Role: "developer"}},
		"team": {{Project: "default", Role: "viewer"}},
	}, proxy.ProjectMappings)
	require.Equal(t, projectAccessAll, proxy.DefaultProjectAccess)
	require.Empty(t, cfg.Warnings)
}

func TestLoad_ProjectAccessDefaults(t *testing.T) {
	cfg := loadFromYAML(t, projectsOn+"auth:\n  mode: builtin\n")

	require.Equal(t, projectAccessAll, cfg.Server.Auth.OIDC.RoleMapping.DefaultProjectAccess)
	require.Equal(t, projectAccessNone, cfg.Server.Auth.Proxy.RoleMapping.DefaultProjectAccess)
}

func TestLoad_WorkspaceKeysReadAsProjectKeys(t *testing.T) {
	cfg := loadFromYAML(t, projectsOn+workspaceAccessYAML)

	oidc, proxy := cfg.Server.Auth.OIDC.RoleMapping, cfg.Server.Auth.Proxy.RoleMapping
	require.Equal(t, map[string][]ProjectMappingGrant{"sre": {{Project: "payments", Role: "operator"}}}, oidc.ProjectMappings)
	require.Equal(t, projectAccessNone, oidc.DefaultProjectAccess)
	require.Equal(t, map[string][]ProjectMappingGrant{"Team": {{Project: "payments", Role: "developer"}}}, proxy.ProjectMappings)
	require.Equal(t, projectAccessAll, proxy.DefaultProjectAccess)
	for _, prefix := range []string{"auth.oidc.role_mapping.", "auth.proxy.role_mapping."} {
		require.Contains(t, cfg.Warnings, prefix+"workspace_mappings is read as "+prefix+"project_mappings until v3.1; rename it")
		require.Contains(t, cfg.Warnings, prefix+"default_workspace_access is read as "+prefix+"default_project_access until v3.1; rename it")
	}
}

func TestLoad_WorkspaceKeysReadAsProjectKeysFromEnvironment(t *testing.T) {
	cfg := loadWithEnv(t, projectsOn+"auth:\n  mode: builtin\n", map[string]string{
		"DAGU_AUTH_OIDC_WORKSPACE_MAPPINGS":      `{"sre":[{"workspace":"payments","role":"viewer"}]}`,
		"DAGU_AUTH_OIDC_DEFAULT_PROJECT_ACCESS":  "none",
		"DAGU_AUTH_PROXY_PROJECT_MAPPINGS":       `{"Team":[{"project":"payments","role":"manager"}]}`,
		"DAGU_AUTH_PROXY_DEFAULT_PROJECT_ACCESS": "all",
	})

	oidc, proxy := cfg.Server.Auth.OIDC.RoleMapping, cfg.Server.Auth.Proxy.RoleMapping
	require.Equal(t, map[string][]ProjectMappingGrant{"sre": {{Project: "payments", Role: "viewer"}}}, oidc.ProjectMappings)
	require.Equal(t, projectAccessNone, oidc.DefaultProjectAccess)
	require.Equal(t, map[string][]ProjectMappingGrant{"Team": {{Project: "payments", Role: "manager"}}}, proxy.ProjectMappings)
	require.Equal(t, projectAccessAll, proxy.DefaultProjectAccess)
}

func TestLoad_ProjectAccessKeyErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		wantErr string
	}{
		{
			name: "WorkspaceAndProjectMappings",
			yaml: `
auth:
  mode: builtin
  oidc:
    role_mapping:
      workspace_mappings:
        sre: [{workspace: payments, role: viewer}]
      project_mappings:
        sre: [{project: payments, role: viewer}]
      default_project_access: none
`,
			wantErr: "auth.oidc.role_mapping.workspace_mappings and auth.oidc.role_mapping.project_mappings are both set",
		},
		{
			name: "WorkspaceAndProjectDefaultFromEnvironment",
			yaml: "auth:\n  mode: builtin\n",
			env: map[string]string{
				"DAGU_AUTH_PROXY_DEFAULT_WORKSPACE_ACCESS": "all",
				"DAGU_AUTH_PROXY_DEFAULT_PROJECT_ACCESS":   "all",
			},
			wantErr: "auth.proxy.role_mapping.default_workspace_access and auth.proxy.role_mapping.default_project_access are both set",
		},
		{
			name: "OIDCMappingsWithoutDefault",
			yaml: `
auth:
  mode: builtin
  oidc:
    role_mapping:
      project_mappings:
        sre: [{project: payments, role: viewer}]
`,
			wantErr: "auth.oidc.role_mapping.default_project_access must be set to all or none",
		},
		{
			name: "AdminRole",
			yaml: `
auth:
  mode: builtin
  oidc:
    role_mapping:
      project_mappings:
        sre: [{project: payments, role: admin}]
      default_project_access: none
`,
			wantErr: `auth.oidc.role_mapping.project_mappings["sre"][0].role must not be admin`,
		},
		{
			name: "ProjectRepeatedInOtherCase",
			yaml: `
auth:
  mode: builtin
  proxy:
    enabled: true
    headers: {user: X-User, groups: X-Groups}
    role_mapping:
      project_mappings:
        Team:
          - {project: payments, role: viewer}
          - {project: Payments, role: operator}
`,
			wantErr: `auth.proxy.role_mapping.project_mappings["Team"] contains duplicate project "Payments"`,
		},
		{
			name: "InvalidProjectName",
			yaml: `
auth:
  mode: builtin
  proxy:
    enabled: true
    headers: {user: X-User, groups: X-Groups}
    role_mapping:
      project_mappings:
        Team: [{project: "a/b", role: viewer}]
`,
			wantErr: `auth.proxy.role_mapping.project_mappings["Team"][0].project "a/b" is not a project name`,
		},
		{
			name: "InvalidProxyDefault",
			yaml: `
auth:
  mode: builtin
  proxy:
    enabled: true
    headers: {user: X-User}
    role_mapping:
      default_project_access: some
`,
			wantErr: "auth.proxy.role_mapping.default_project_access must be one of: all, none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			err := loadWithErrorFromYAML(t, projectsOn+tt.yaml)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestOIDCPolicyLoaderReadsProjectAccessKeys(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre:
          - workspace: payments
            role: developer
      default_project_access: none
`), 0600))

	policy, err := NewOIDCPolicyLoader([]string{configFile}, OIDCPolicy{}, true).Load()
	require.NoError(t, err)
	require.Equal(t, map[string][]ProjectMappingGrant{"sre": {{Project: "payments", Role: "developer"}}}, policy.RoleMapping.ProjectMappings)
	require.Equal(t, projectAccessNone, policy.RoleMapping.DefaultProjectAccess)

	// While projects are off, the workspace rules apply and the project key
	// does not stand in for default_workspace_access.
	_, err = NewOIDCPolicyLoader([]string{configFile}, OIDCPolicy{}, false).Load()
	require.ErrorContains(t, err, "defaultWorkspaceAccess must be explicitly set")
}
