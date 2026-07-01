// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec022_mcp_change_tool_test

import (
	"testing"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/conformance/mcptest"
	"github.com/stretchr/testify/require"
)

func TestChangeInputValidationErrors(t *testing.T) {
	fixture := newChangeFixture(t)
	validSpec := fixtureSpec(t, "valid_initial.yaml")

	tests := []struct {
		name       string
		arguments  any
		code       string
		field      string
		mode       string
		changeType string
		dagName    string
		dagURI     string
	}{
		{
			name:      "non-object input",
			arguments: []any{"not", "object"},
			code:      "invalid_tool_input",
		},
		{
			name: "unknown field",
			arguments: map[string]any{
				"name":  "mcp_change_error_unknown",
				"spec":  validSpec,
				"extra": "value",
			},
			code:  "invalid_tool_input",
			field: "extra",
		},
		{
			name: "non-string mode",
			arguments: map[string]any{
				"mode": 123,
				"name": "mcp_change_error_mode_type",
				"spec": validSpec,
			},
			code:  "invalid_tool_input",
			field: "mode",
		},
		{
			name: "non-string type",
			arguments: map[string]any{
				"type": 123,
				"name": "mcp_change_error_type_type",
				"spec": validSpec,
			},
			code:  "invalid_tool_input",
			field: "type",
		},
		{
			name: "non-string name",
			arguments: map[string]any{
				"name": 123,
				"spec": validSpec,
			},
			code:  "invalid_tool_input",
			field: "name",
		},
		{
			name: "non-string spec",
			arguments: map[string]any{
				"name": "mcp_change_error_spec_type",
				"spec": 123,
			},
			code:  "invalid_tool_input",
			field: "spec",
		},
		{
			name: "missing name",
			arguments: map[string]any{
				"spec": validSpec,
			},
			code:  "invalid_tool_input",
			field: "name",
		},
		{
			name: "null name",
			arguments: map[string]any{
				"name": nil,
				"spec": validSpec,
			},
			code:  "invalid_tool_input",
			field: "name",
		},
		{
			name: "empty name",
			arguments: map[string]any{
				"name": "   ",
				"spec": validSpec,
			},
			code:  "invalid_tool_input",
			field: "name",
		},
		{
			name: "missing spec",
			arguments: map[string]any{
				"name": "mcp_change_error_missing_spec",
			},
			code:    "invalid_tool_input",
			field:   "spec",
			dagName: "mcp_change_error_missing_spec",
			dagURI:  "dagu://dags/mcp_change_error_missing_spec/spec",
		},
		{
			name: "null spec",
			arguments: map[string]any{
				"name": "mcp_change_error_null_spec",
				"spec": nil,
			},
			code:    "invalid_tool_input",
			field:   "spec",
			dagName: "mcp_change_error_null_spec",
			dagURI:  "dagu://dags/mcp_change_error_null_spec/spec",
		},
		{
			name: "empty spec",
			arguments: map[string]any{
				"name": "mcp_change_error_empty_spec",
				"spec": "   ",
			},
			code:    "invalid_tool_input",
			field:   "spec",
			dagName: "mcp_change_error_empty_spec",
			dagURI:  "dagu://dags/mcp_change_error_empty_spec/spec",
		},
		{
			name: "unsupported mode",
			arguments: map[string]any{
				"mode": "write",
				"name": "mcp_change_error_mode",
				"spec": validSpec,
			},
			code:    "unsupported_change_mode",
			field:   "mode",
			mode:    "write",
			dagName: "mcp_change_error_mode",
			dagURI:  "dagu://dags/mcp_change_error_mode/spec",
		},
		{
			name: "case-sensitive unsupported mode",
			arguments: map[string]any{
				"mode": "Preview",
				"name": "mcp_change_error_mode_case",
				"spec": validSpec,
			},
			code:    "unsupported_change_mode",
			field:   "mode",
			mode:    "Preview",
			dagName: "mcp_change_error_mode_case",
			dagURI:  "dagu://dags/mcp_change_error_mode_case/spec",
		},
		{
			name: "unsupported type",
			arguments: map[string]any{
				"type": "delete_dag",
				"name": "mcp_change_error_type",
				"spec": validSpec,
			},
			code:       "unsupported_change_type",
			field:      "type",
			changeType: "delete_dag",
			dagName:    "mcp_change_error_type",
			dagURI:     "dagu://dags/mcp_change_error_type/spec",
		},
		{
			name: "case-sensitive unsupported type",
			arguments: map[string]any{
				"type": "UPSERT_DAG",
				"name": "mcp_change_error_type_case",
				"spec": validSpec,
			},
			code:       "unsupported_change_type",
			field:      "type",
			changeType: "UPSERT_DAG",
			dagName:    "mcp_change_error_type_case",
			dagURI:     "dagu://dags/mcp_change_error_type_case/spec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callChange(t, fixture.session, tt.arguments)
			output := requireChangeError(t, result, tt.code)
			if tt.field == "" {
				require.NotContains(t, output, "field")
			} else {
				require.Equal(t, tt.field, output["field"])
			}
			if tt.mode != "" {
				require.Equal(t, tt.mode, output["mode"])
			}
			if tt.changeType != "" {
				require.Equal(t, tt.changeType, output["type"])
			}
			if tt.dagName != "" {
				require.Equal(t, tt.dagName, output["dagName"])
			}
			if tt.dagURI != "" {
				require.Equal(t, tt.dagURI, output["dagUri"])
			}
		})
	}
}

func TestChangeAuthenticationErrors(t *testing.T) {
	server := mcptest.NewAuthServer(t)
	rejected, err := server.TryConnect(t, "")
	if rejected != nil {
		t.Cleanup(func() { _ = rejected.Close() })
	}
	require.Error(t, err)

	restOnlyKey := server.CreateAPIKey(t, "rest-only", api.CreateAPIKeyRequestAllowedSurfacesRestApi)
	rejected, err = server.TryConnect(t, restOnlyKey)
	if rejected != nil {
		t.Cleanup(func() { _ = rejected.Close() })
	}
	require.Error(t, err)

	mcpKey := server.CreateAPIKey(t, "mcp-viewer", api.CreateAPIKeyRequestAllowedSurfacesMcp)
	session := server.Connect(t, mcpKey)
	result := callChange(t, session, changeArguments("apply", "upsert_dag", "mcp_change_auth_denied", fixtureSpec(t, "valid_initial.yaml")))
	output := requireChangeError(t, result, "unauthorized")
	require.Equal(t, "apply", output["mode"])
	require.Equal(t, "upsert_dag", output["type"])
	require.Equal(t, "mcp_change_auth_denied", output["dagName"])
	require.Equal(t, "dagu://dags/mcp_change_auth_denied/spec", output["dagUri"])
}
