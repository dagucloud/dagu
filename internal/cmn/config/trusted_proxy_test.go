// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func validProxyConfig() *Config {
	cfg := validBaseConfig()
	cfg.Server.Auth = Auth{
		Mode: AuthModeBuiltin,
		Builtin: AuthBuiltin{
			Token: TokenConfig{Secret: "secret", TTL: time.Hour},
		},
		Proxy: AuthTrustedProxy{
			Enabled:    true,
			Source:     "corp-sso",
			LoginLabel: "Continue with SSO",
			Headers:    TrustedProxyHeaders{User: "X-Auth-Request-User"},
			AutoSignup: true,
			RoleMapping: TrustedProxyRoleMapping{
				DefaultRole:            "viewer",
				DefaultWorkspaceAccess: TrustedProxyDefaultWorkspaceAccessNone,
			},
		},
	}
	cfg.Paths.UsersDir = "/tmp/users"
	return cfg
}

func TestConfigValidateProxy(t *testing.T) {
	t.Parallel()

	t.Run("ValidWithoutRequiredMapping", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validProxyConfig().Validate())
	})

	t.Run("ValidEmptySource", func(t *testing.T) {
		t.Parallel()
		cfg := validProxyConfig()
		cfg.Server.Auth.Proxy.Source = ""
		require.NoError(t, cfg.Validate())
	})

	t.Run("ValidMappings", func(t *testing.T) {
		t.Parallel()
		cfg := validProxyConfig()
		cfg.Server.Auth.Proxy.RoleMapping.RequireMapping = true
		cfg.Server.Auth.Proxy.Headers.Groups = "X-Auth-Request-Groups"
		cfg.Server.Auth.Proxy.RoleMapping.GroupMappings = map[string]string{"admins": "admin"}
		cfg.Server.Auth.Proxy.RoleMapping.WorkspaceMappings = map[string][]TrustedProxyWorkspaceGrant{
			"operators": {{Workspace: "payments", Role: "operator"}},
		}
		require.NoError(t, cfg.Validate())
	})

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "RejectsSourceWithSurroundingWhitespace",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Source = " corp-sso"
			},
			wantErr: "auth.proxy.source must not have surrounding whitespace",
		},
		{
			name: "RejectsControlInSource",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Source = "corp\nsso"
			},
			wantErr: "auth.proxy.source must not contain control characters",
		},
		{
			name: "RejectsInvalidUTF8Source",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Source = string([]byte{0xff})
			},
			wantErr: "auth.proxy.source must be valid UTF-8",
		},
		{
			name: "RejectsLongSource",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Source = strings.Repeat("認", 129)
			},
			wantErr: "auth.proxy.source must not exceed 128 characters",
		},
		{
			name: "RequiresBuiltinMode",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Mode = AuthModeNone
			},
			wantErr: "auth.proxy.enabled requires auth.mode",
		},
		{
			name: "RejectsHeadless",
			mutate: func(cfg *Config) {
				cfg.Server.Headless = true
			},
			wantErr: "auth.proxy.enabled is not supported when headless is true",
		},
		{
			name: "RejectsBuiltInTunnel",
			mutate: func(cfg *Config) {
				cfg.Tunnel.Enabled = true
			},
			wantErr: "auth.proxy.enabled is not supported when tunnel.enabled is true",
		},
		{
			name: "RequiresUserHeader",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.User = ""
			},
			wantErr: "auth.proxy.headers.user is required",
		},
		{
			name: "RejectsInvalidUserHeader",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.User = "X User"
			},
			wantErr: "auth.proxy.headers.user must be a valid HTTP header field name",
		},
		{
			name: "RejectsReservedUserHeader",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.User = "authorization"
			},
			wantErr: "reserved header",
		},
		{
			name: "RequiresConfiguredMapping",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.RoleMapping.RequireMapping = true
			},
			wantErr: "auth.proxy.role_mapping.require_mapping requires at least one group_mappings or workspace_mappings entry",
		},
		{
			name: "RequiresGroupsHeaderForMappings",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.RoleMapping.GroupMappings = map[string]string{"admins": "admin"}
			},
			wantErr: "auth.proxy.headers.groups is required",
		},
		{
			name: "RejectsDuplicateHeaderNames",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.Groups = "x-auth-request-user"
				cfg.Server.Auth.Proxy.RoleMapping.GroupMappings = map[string]string{"admins": "admin"}
			},
			wantErr: "headers.user and auth.proxy.headers.groups must be different",
		},
		{
			name: "RejectsBlankLoginLabel",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.LoginLabel = " \t"
			},
			wantErr: "auth.proxy.login_label must not be empty",
		},
		{
			name: "RejectsControlInLoginLabel",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.LoginLabel = "Continue\nwith SSO"
			},
			wantErr: "auth.proxy.login_label must not contain control characters",
		},
		{
			name: "RejectsLongLoginLabel",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.LoginLabel = strings.Repeat("認", 129)
			},
			wantErr: "must not exceed 128 characters",
		},
		{
			name: "RejectsInvalidDefaultRole",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.RoleMapping.DefaultRole = "owner"
			},
			wantErr: "auth.proxy.role_mapping.default_role",
		},
		{
			name: "RejectsInvalidDefaultWorkspaceAccess",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.RoleMapping.DefaultWorkspaceAccess = "restricted"
			},
			wantErr: "auth.proxy.role_mapping.default_workspace_access",
		},
		{
			name: "RejectsInvalidGroupRole",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.Groups = "X-Auth-Request-Groups"
				cfg.Server.Auth.Proxy.RoleMapping.GroupMappings = map[string]string{"admins": "owner"}
			},
			wantErr: `group_mappings["admins"]`,
		},
		{
			name: "RejectsGroupWithSurroundingWhitespace",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.Groups = "X-Auth-Request-Groups"
				cfg.Server.Auth.Proxy.RoleMapping.GroupMappings = map[string]string{" admins": "admin"}
			},
			wantErr: "group name must not have surrounding whitespace",
		},
		{
			name: "RejectsEmptyWorkspaceGrants",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.Groups = "X-Auth-Request-Groups"
				cfg.Server.Auth.Proxy.RoleMapping.WorkspaceMappings = map[string][]TrustedProxyWorkspaceGrant{"team": {}}
			},
			wantErr: "must contain at least one grant",
		},
		{
			name: "RejectsInvalidWorkspace",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.Groups = "X-Auth-Request-Groups"
				cfg.Server.Auth.Proxy.RoleMapping.WorkspaceMappings = map[string][]TrustedProxyWorkspaceGrant{
					"team": {{Workspace: "bad/name", Role: "viewer"}},
				}
			},
			wantErr: ".workspace",
		},
		{
			name: "RejectsDuplicateWorkspace",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.Groups = "X-Auth-Request-Groups"
				cfg.Server.Auth.Proxy.RoleMapping.WorkspaceMappings = map[string][]TrustedProxyWorkspaceGrant{
					"team": {
						{Workspace: "payments", Role: "viewer"},
						{Workspace: "payments", Role: "operator"},
					},
				}
			},
			wantErr: "duplicate workspace",
		},
		{
			name: "RejectsWorkspaceAdminRole",
			mutate: func(cfg *Config) {
				cfg.Server.Auth.Proxy.Headers.Groups = "X-Auth-Request-Groups"
				cfg.Server.Auth.Proxy.RoleMapping.WorkspaceMappings = map[string][]TrustedProxyWorkspaceGrant{
					"team": {{Workspace: "payments", Role: "admin"}},
				}
			},
			wantErr: ".role must not be admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validProxyConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadProxyDefaults(t *testing.T) {
	cfg := loadFromYAML(t, "# minimal config")

	require.False(t, cfg.Server.Auth.Proxy.Enabled)
	require.Empty(t, cfg.Server.Auth.Proxy.Source)
	require.Equal(t, "Continue with SSO", cfg.Server.Auth.Proxy.LoginLabel)
	require.True(t, cfg.Server.Auth.Proxy.AutoSignup)
	require.Equal(t, "viewer", cfg.Server.Auth.Proxy.RoleMapping.DefaultRole)
	require.Equal(t, TrustedProxyDefaultWorkspaceAccessNone, cfg.Server.Auth.Proxy.RoleMapping.DefaultWorkspaceAccess)
	require.True(t, cfg.Server.Auth.Proxy.RoleMapping.RequireMapping)
}

func TestLoadProxyFromYAML(t *testing.T) {
	cfg := loadFromYAML(t, `
auth:
  mode: builtin
  proxy:
    enabled: true
    source: corp-sso
    login_label: Company SSO
    headers:
      user: X-Auth-Request-User
      groups: X-Auth-Request-Groups
    auto_signup: false
    role_mapping:
      default_role: operator
      default_workspace_access: none
      require_mapping: true
      group_mappings:
        Admins: admin
        admins: viewer
      workspace_mappings:
        Developers:
          - workspace: payments
            role: developer
        developers:
          - workspace: operations
            role: viewer
`)

	trustedProxy := cfg.Server.Auth.Proxy
	require.True(t, trustedProxy.Enabled)
	require.Equal(t, "corp-sso", trustedProxy.Source)
	require.Equal(t, "Company SSO", trustedProxy.LoginLabel)
	require.Equal(t, TrustedProxyHeaders{User: "X-Auth-Request-User", Groups: "X-Auth-Request-Groups"}, trustedProxy.Headers)
	require.False(t, trustedProxy.AutoSignup)
	require.Equal(t, "operator", trustedProxy.RoleMapping.DefaultRole)
	require.Equal(t, map[string]string{"Admins": "admin", "admins": "viewer"}, trustedProxy.RoleMapping.GroupMappings)
	require.Equal(t, map[string][]TrustedProxyWorkspaceGrant{
		"Developers": {{Workspace: "payments", Role: "developer"}},
		"developers": {{Workspace: "operations", Role: "viewer"}},
	}, trustedProxy.RoleMapping.WorkspaceMappings)
	require.Equal(t, TrustedProxyDefaultWorkspaceAccessNone, trustedProxy.RoleMapping.DefaultWorkspaceAccess)
	require.True(t, trustedProxy.RoleMapping.RequireMapping)
}

func TestLoadProxyFromEnvironment(t *testing.T) {
	cfg := loadWithEnv(t, `
auth:
  mode: builtin
  proxy:
    enabled: false
    headers:
      user: X-YAML-User
      groups: X-YAML-Groups
    role_mapping:
      group_mappings:
        YAMLOnly: admin
      workspace_mappings:
        YAMLOnly:
          - workspace: legacy
            role: viewer
`, map[string]string{
		"DAGU_AUTH_PROXY_ENABLED":                  "true",
		"DAGU_AUTH_PROXY_SOURCE":                   "env-sso",
		"DAGU_AUTH_PROXY_LOGIN_LABEL":              "Environment SSO",
		"DAGU_AUTH_PROXY_HEADERS_USER":             "X-Env-User",
		"DAGU_AUTH_PROXY_HEADERS_GROUPS":           "X-Env-Groups",
		"DAGU_AUTH_PROXY_AUTO_SIGNUP":              "false",
		"DAGU_AUTH_PROXY_DEFAULT_ROLE":             "developer",
		"DAGU_AUTH_PROXY_DEFAULT_WORKSPACE_ACCESS": "none",
		"DAGU_AUTH_PROXY_REQUIRE_MAPPING":          "false",
		"DAGU_AUTH_PROXY_GROUP_MAPPINGS":           `{"Admins":"admin","admins":"viewer"}`,
		"DAGU_AUTH_PROXY_WORKSPACE_MAPPINGS":       `{"Developers":[{"workspace":"payments","role":"developer"}],"developers":[{"workspace":"operations","role":"viewer"}]}`,
	})

	trustedProxy := cfg.Server.Auth.Proxy
	require.True(t, trustedProxy.Enabled)
	require.Equal(t, "env-sso", trustedProxy.Source)
	require.Equal(t, "Environment SSO", trustedProxy.LoginLabel)
	require.Equal(t, TrustedProxyHeaders{User: "X-Env-User", Groups: "X-Env-Groups"}, trustedProxy.Headers)
	require.False(t, trustedProxy.AutoSignup)
	require.Equal(t, "developer", trustedProxy.RoleMapping.DefaultRole)
	require.Equal(t, map[string]string{"Admins": "admin", "admins": "viewer"}, trustedProxy.RoleMapping.GroupMappings)
	require.Equal(t, map[string][]TrustedProxyWorkspaceGrant{
		"Developers": {{Workspace: "payments", Role: "developer"}},
		"developers": {{Workspace: "operations", Role: "viewer"}},
	}, trustedProxy.RoleMapping.WorkspaceMappings)
	require.Equal(t, TrustedProxyDefaultWorkspaceAccessNone, trustedProxy.RoleMapping.DefaultWorkspaceAccess)
	require.False(t, trustedProxy.RoleMapping.RequireMapping)
}

func TestLoadProxyMappingsMergesLegacyAdminYAML(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "config.yaml"), []byte(`
auth:
  mode: builtin
  proxy:
    headers:
      user: X-Proxy-User
      groups: X-Proxy-Groups
    role_mapping:
      group_mappings:
        Finance: admin
        Shared: viewer
      workspace_mappings:
        Team:
          - workspace: payments
            role: viewer
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "admin.yaml"), []byte(`
auth:
  proxy:
    role_mapping:
      group_mappings:
        finance: viewer
        Shared: operator
      workspace_mappings:
        Team:
          - workspace: operations
            role: developer
        team:
          - workspace: support
            role: viewer
`), 0600))

	cfg := testLoad(t, WithAppHomeDir(homeDir))
	require.Equal(t, map[string]string{
		"Finance": "admin",
		"finance": "viewer",
		"Shared":  "operator",
	}, cfg.Server.Auth.Proxy.RoleMapping.GroupMappings)
	require.Equal(t, map[string][]TrustedProxyWorkspaceGrant{
		"Team": {{Workspace: "operations", Role: "developer"}},
		"team": {{Workspace: "support", Role: "viewer"}},
	}, cfg.Server.Auth.Proxy.RoleMapping.WorkspaceMappings)
}

func TestLoadProxyMappingsEnvironmentRejectsInvalidJSON(t *testing.T) {
	tests := []struct {
		name     string
		envName  string
		value    string
		wantText string
	}{
		{name: "Empty", envName: "DAGU_AUTH_PROXY_GROUP_MAPPINGS", value: "", wantText: "JSON"},
		{name: "ArrayRoot", envName: "DAGU_AUTH_PROXY_GROUP_MAPPINGS", value: `[]`, wantText: "must be a JSON object"},
		{name: "WrongGroupRoleType", envName: "DAGU_AUTH_PROXY_GROUP_MAPPINGS", value: `{"admins":1}`, wantText: "cannot unmarshal"},
		{name: "DuplicateGroup", envName: "DAGU_AUTH_PROXY_GROUP_MAPPINGS", value: `{"admins":"admin","admins":"viewer"}`, wantText: "duplicate object key"},
		{name: "TrailingObject", envName: "DAGU_AUTH_PROXY_GROUP_MAPPINGS", value: `{} {}`, wantText: "single object"},
		{name: "UnknownGrantField", envName: "DAGU_AUTH_PROXY_WORKSPACE_MAPPINGS", value: `{"team":[{"workspace":"payments","role":"viewer","other":true}]}`, wantText: "unknown field"},
		{name: "CaseVariantGrantField", envName: "DAGU_AUTH_PROXY_WORKSPACE_MAPPINGS", value: `{"team":[{"workspace":"payments","role":"viewer","Role":"operator"}]}`, wantText: "unknown field"},
		{name: "DuplicateGrantField", envName: "DAGU_AUTH_PROXY_WORKSPACE_MAPPINGS", value: `{"team":[{"workspace":"payments","role":"viewer","role":"operator"}]}`, wantText: "duplicate object key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envName, tt.value)
			err := loadWithErrorFromYAML(t, "# minimal config")
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.envName)
			require.Contains(t, err.Error(), tt.wantText)
		})
	}
}

func TestLoadProxyMappingsYAMLRejectsDuplicateGroup(t *testing.T) {
	err := loadWithErrorFromYAML(t, `
auth:
  proxy:
    role_mapping:
      group_mappings:
        Finance: admin
        Finance: viewer
`)
	require.Error(t, err)
}

func TestLoadProxyRejectsUnknownKeys(t *testing.T) {
	tests := []struct {
		name string
		spec string
		key  string
	}{
		{
			name: "TopLevel",
			spec: `
auth:
  proxy:
    auto_sign_up: false
`,
			key: "auto_sign_up",
		},
		{
			name: "RoleMapping",
			spec: `
auth:
  proxy:
    role_mapping:
      require_mappnig: true
`,
			key: "require_mappnig",
		},
		{
			name: "WorkspaceGrant",
			spec: `
auth:
  proxy:
    role_mapping:
      workspace_mappings:
        team:
          - workspace: payments
            role: viewer
            unexpected: true
`,
			key: "unexpected",
		},
		{
			name: "CaseDistinctWorkspaceGrant",
			spec: `
auth:
  proxy:
    role_mapping:
      workspace_mappings:
        Team:
          - workspace: payments
            role: viewer
            unexpected: true
        team:
          - workspace: operations
            role: viewer
`,
			key: "unexpected",
		},
		{
			name: "CaseVariantStructuralKey",
			spec: `
auth:
  proxy:
    role_mapping:
      default_role: viewer
      DEFAULT_ROLE: admin
`,
			key: "DEFAULT_ROLE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := loadWithErrorFromYAML(t, tt.spec)
			require.Error(t, err)
			require.ErrorContains(t, err, "invalid auth.proxy config")
			require.ErrorContains(t, err, tt.key)
		})
	}
}

func TestLoadProxyEnvironmentIsServerScoped(t *testing.T) {
	t.Setenv("DAGU_AUTH_PROXY_ENABLED", "true")
	t.Setenv("DAGU_AUTH_PROXY_GROUP_MAPPINGS", "not-json")
	t.Setenv("DAGU_AUTH_PROXY_WORKSPACE_MAPPINGS", "not-json")

	cfg := testLoad(t, WithService(ServiceScheduler))
	require.False(t, cfg.Server.Auth.Proxy.Enabled)
}

func TestLoadProxyCamelCaseKeyHints(t *testing.T) {
	tests := []struct {
		legacy string
		want   string
	}{
		{
			legacy: "auth.proxy.roleMapping.workspaceMappings",
			want:   "auth.proxy.rolemapping.workspacemappings -> auth.proxy.role_mapping.workspace_mappings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.legacy, func(t *testing.T) {
			v := viper.New()
			v.Set(tt.legacy, "value")

			err := checkForLegacyKeys(v)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}
