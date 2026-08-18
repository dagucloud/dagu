// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestConfigSchemaTopLevelPropertiesCoverDefinition(t *testing.T) {
	t.Parallel()

	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(ConfigSchemaJSON, &doc))

	defType := reflect.TypeFor[config.Definition]()
	for field := range defType.Fields() {
		key := field.Tag.Get("mapstructure")
		if key == "" || key == "-" {
			continue
		}
		key = strings.Split(key, ",")[0]
		require.Containsf(
			t,
			doc.Properties,
			key,
			"config schema is missing top-level property for Definition.%s (%q)",
			field.Name,
			key,
		)
	}
}

func TestConfigSchemaCheckUpdatesValidation(t *testing.T) {
	t.Parallel()

	resolved := mustResolveConfigSchema(t)

	tests := []struct {
		name string
		spec string
	}{
		{
			name: "CheckUpdatesTrue",
			spec: `
check_updates: true
`,
		},
		{
			name: "CheckUpdatesFalse",
			spec: `
check_updates: false
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			require.NoError(t, resolved.Validate(doc))
		})
	}
}

func TestConfigSchemaIPAccess(t *testing.T) {
	t.Parallel()

	resolved := mustResolveConfigSchema(t)
	tests := []struct {
		name    string
		spec    string
		wantErr bool
	}{
		{
			name: "AddressesAndNetworks",
			spec: `
ip_access:
  allowed_ips:
    - 203.0.113.10
    - 10.0.0.0/8
  trusted_proxies:
    - 127.0.0.1
    - 2001:db8::/32
`,
		},
		{
			name: "RejectsNonStringEntry",
			spec: `
ip_access:
  allowed_ips:
    - 42
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestConfigSchemaOIDCWorkspaceMappings(t *testing.T) {
	t.Parallel()

	resolved := mustResolveConfigSchema(t)
	tests := []struct {
		name    string
		spec    string
		wantErr bool
	}{
		{
			name: "Valid",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: payments
            role: operator
          - workspace: infra
            role: developer
      default_workspace_access: none
`,
		},
		{
			name: "ValidExplicitAll",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: payments
            role: viewer
      default_workspace_access: all
`,
		},
		{
			name: "ValidEmptyMappingsWithoutDefault",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings: {}
`,
		},
		{
			name: "MissingDefaultWithMappings",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: payments
            role: viewer
`,
			wantErr: true,
		},
		{
			name: "AdminGrant",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: payments
            role: admin
`,
			wantErr: true,
		},
		{
			name: "BlankGroup",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        " ":
          - workspace: payments
            role: viewer
`,
			wantErr: true,
		},
		{
			name: "EmptyGrants",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team: []
`,
			wantErr: true,
		},
		{
			name: "InvalidWorkspace",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: bad/name
            role: viewer
`,
			wantErr: true,
		},
		{
			name: "ReservedWorkspaceAll",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: all
            role: viewer
      default_workspace_access: none
`,
			wantErr: true,
		},
		{
			name: "ReservedWorkspaceDefaultMixedCase",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: DeFaUlT
            role: viewer
      default_workspace_access: none
`,
			wantErr: true,
		},
		{
			name: "ReservedWorkspaceGlobal",
			spec: `
auth:
  oidc:
    role_mapping:
      workspace_mappings:
        sre-team:
          - workspace: global
            role: viewer
      default_workspace_access: none
`,
			wantErr: true,
		},
		{
			name: "InvalidDefault",
			spec: `
auth:
  oidc:
    role_mapping:
      default_workspace_access: restricted
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestConfigSchemaProxy(t *testing.T) {
	t.Parallel()

	resolved := mustResolveConfigSchema(t)
	tests := []struct {
		name    string
		spec    string
		wantErr bool
	}{
		{
			name: "DisabledByDefault",
			spec: `
auth:
  mode: builtin
  proxy: {}
`,
		},
		{
			name: "ValidWithDefaultMappingPolicy",
			spec: `
auth:
  mode: builtin
  proxy:
    enabled: true
    headers:
      user: X-Auth-Request-User
`,
		},
		{
			name: "ValidMappings",
			spec: `
auth:
  mode: builtin
  proxy:
    enabled: true
    source: corp-sso
    button_label: Company SSO
    headers:
      user: X-Auth-Request-User
      groups: X-Auth-Request-Groups
    auto_signup: false
    role_mapping:
      default_role: viewer
      default_workspace_access: none
      require_mapping: true
      skip_org_role_sync: false
      group_mappings:
        admins: admin
      workspace_mappings:
        developers:
          - workspace: payments
            role: developer
`,
		},
		{
			name: "RequiredMappingMustBeConfigured",
			spec: `
auth:
  mode: builtin
  proxy:
    enabled: true
    headers:
      user: X-Auth-Request-User
    role_mapping:
      require_mapping: true
`,
			wantErr: true,
		},
		{
			name: "SourceTooLong",
			spec: `
auth:
  proxy:
    source: ` + strings.Repeat("x", 129) + `
`,
			wantErr: true,
		},
		{
			name: "EnabledRequiresUserHeader",
			spec: `
auth:
  proxy:
    enabled: true
`,
			wantErr: true,
		},
		{
			name: "GroupMappingsRequireGroupsHeader",
			spec: `
auth:
  proxy:
    headers:
      user: X-Auth-Request-User
    role_mapping:
      group_mappings:
        admins: admin
`,
			wantErr: true,
		},
		{
			name: "WorkspaceMappingsRequireGroupsHeader",
			spec: `
auth:
  proxy:
    headers:
      user: X-Auth-Request-User
    role_mapping:
      workspace_mappings:
        developers:
          - workspace: payments
            role: developer
`,
			wantErr: true,
		},
		{
			name: "InvalidGlobalRole",
			spec: `
auth:
  proxy:
    role_mapping:
      group_mappings:
        admins: owner
`,
			wantErr: true,
		},
		{
			name: "WorkspaceAdminRole",
			spec: `
auth:
  proxy:
    headers:
      groups: X-Auth-Request-Groups
    role_mapping:
      workspace_mappings:
        developers:
          - workspace: payments
            role: admin
`,
			wantErr: true,
		},
		{
			name: "InvalidWorkspace",
			spec: `
auth:
  proxy:
    headers:
      groups: X-Auth-Request-Groups
    role_mapping:
      workspace_mappings:
        developers:
          - workspace: bad/name
            role: viewer
`,
			wantErr: true,
		},
		{
			name: "UnexpectedProperty",
			spec: `
auth:
  proxy:
    enabled: false
    unexpected: true
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, tt.spec)
			err := resolved.Validate(doc)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestConfigSchemaRepoCopyMatchesEmbeddedSchema(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	repoSchemaPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "schemas", "config.schema.json")
	repoSchemaJSON, err := os.ReadFile(repoSchemaPath)
	require.NoError(t, err)
	require.Equal(t, string(ConfigSchemaJSON), string(repoSchemaJSON))
}

func mustResolveConfigSchema(t *testing.T) *jsonschema.Resolved {
	t.Helper()

	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(ConfigSchemaJSON, &schema))

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{})
	require.NoError(t, err)
	return resolved
}
