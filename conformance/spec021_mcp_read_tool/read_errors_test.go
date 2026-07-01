// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"encoding/json"
	"testing"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/conformance/mcptest"
	"github.com/stretchr/testify/require"
)

func TestReadInputValidationErrors(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	tests := []struct {
		name      string
		arguments map[string]any
		code      string
		target    string
		field     string
		details   bool
	}{
		{
			name:      "missing mode",
			arguments: map[string]any{},
			code:      "invalid_tool_input",
		},
		{
			name: "unknown field",
			arguments: map[string]any{
				"target": "reference",
				"extra":  "value",
			},
			code:  "invalid_tool_input",
			field: "extra",
		},
		{
			name:      "non-string target",
			arguments: map[string]any{"target": 123},
			code:      "invalid_tool_input",
			field:     "target",
		},
		{
			name: "non-string name",
			arguments: map[string]any{
				"target": "reference",
				"name":   123,
			},
			code:  "invalid_tool_input",
			field: "name",
		},
		{
			name: "non-string dagRunId",
			arguments: map[string]any{
				"target":   "run",
				"name":     "mcp_read_contract",
				"dagRunId": 123,
			},
			code:  "invalid_tool_input",
			field: "dagRunId",
		},
		{
			name: "non-string query",
			arguments: map[string]any{
				"target": "dags",
				"query":  123,
			},
			code:  "invalid_tool_input",
			field: "query",
		},
		{
			name:      "non-string uri",
			arguments: map[string]any{"uri": 123},
			code:      "invalid_tool_input",
			field:     "uri",
		},
		{
			name:      "whitespace only target",
			arguments: map[string]any{"target": "   "},
			code:      "invalid_tool_input",
			field:     "target",
		},
		{
			name:      "case-sensitive unsupported target",
			arguments: map[string]any{"target": "Reference"},
			code:      "unsupported_read_target",
			target:    "Reference",
			field:     "target",
		},
		{
			name:      "unsupported target",
			arguments: map[string]any{"target": "unknown"},
			code:      "unsupported_read_target",
			target:    "unknown",
			field:     "target",
		},
		{
			name:      "missing required name",
			arguments: map[string]any{"target": "dag_spec"},
			code:      "invalid_tool_input",
			target:    "dag_spec",
			field:     "name",
		},
		{
			name: "missing required dagRunId",
			arguments: map[string]any{
				"target": "run",
				"name":   "mcp_read_contract",
			},
			code:   "invalid_tool_input",
			target: "run",
			field:  "dagRunId",
		},
		{
			name: "forbidden name",
			arguments: map[string]any{
				"target": "dags",
				"name":   "unexpected",
			},
			code:   "invalid_tool_input",
			target: "dags",
			field:  "name",
		},
		{
			name: "forbidden name on references",
			arguments: map[string]any{
				"target": "references",
				"name":   "authoring",
			},
			code:   "invalid_tool_input",
			target: "references",
			field:  "name",
		},
		{
			name: "forbidden dagRunId on references",
			arguments: map[string]any{
				"target":   "references",
				"dagRunId": "run-id",
			},
			code:   "invalid_tool_input",
			target: "references",
			field:  "dagRunId",
		},
		{
			name: "forbidden query on references",
			arguments: map[string]any{
				"target": "references",
				"query":  "page=1",
			},
			code:   "invalid_tool_input",
			target: "references",
			field:  "query",
		},
		{
			name: "forbidden dagRunId",
			arguments: map[string]any{
				"target":   "reference",
				"dagRunId": "run-id",
			},
			code:   "invalid_tool_input",
			target: "reference",
			field:  "dagRunId",
		},
		{
			name: "forbidden dagRunId on dags",
			arguments: map[string]any{
				"target":   "dags",
				"dagRunId": "run-id",
			},
			code:   "invalid_tool_input",
			target: "dags",
			field:  "dagRunId",
		},
		{
			name: "forbidden dagRunId on dag",
			arguments: map[string]any{
				"target":   "dag",
				"name":     "mcp_read_contract",
				"dagRunId": "run-id",
			},
			code:   "invalid_tool_input",
			target: "dag",
			field:  "dagRunId",
		},
		{
			name: "forbidden query on dag",
			arguments: map[string]any{
				"target": "dag",
				"name":   "mcp_read_contract",
				"query":  "page=1",
			},
			code:   "invalid_tool_input",
			target: "dag",
			field:  "query",
		},
		{
			name: "forbidden dagRunId on dag_spec",
			arguments: map[string]any{
				"target":   "dag_spec",
				"name":     "mcp_read_contract",
				"dagRunId": "run-id",
			},
			code:   "invalid_tool_input",
			target: "dag_spec",
			field:  "dagRunId",
		},
		{
			name: "forbidden query on dag_spec",
			arguments: map[string]any{
				"target": "dag_spec",
				"name":   "mcp_read_contract",
				"query":  "page=1",
			},
			code:   "invalid_tool_input",
			target: "dag_spec",
			field:  "query",
		},
		{
			name: "forbidden name on runs",
			arguments: map[string]any{
				"target": "runs",
				"name":   "mcp_read_contract",
			},
			code:   "invalid_tool_input",
			target: "runs",
			field:  "name",
		},
		{
			name: "forbidden dagRunId on runs",
			arguments: map[string]any{
				"target":   "runs",
				"dagRunId": "run-id",
			},
			code:   "invalid_tool_input",
			target: "runs",
			field:  "dagRunId",
		},
		{
			name: "forbidden query on run",
			arguments: map[string]any{
				"target":   "run",
				"name":     "mcp_read_contract",
				"dagRunId": "run-id",
				"query":    "tail=1",
			},
			code:   "invalid_tool_input",
			target: "run",
			field:  "query",
		},
		{
			name: "query on target that forbids query",
			arguments: map[string]any{
				"target": "reference",
				"query":  "tail=100",
			},
			code:   "invalid_tool_input",
			target: "reference",
			field:  "query",
		},
		{
			name: "query starts with question mark",
			arguments: map[string]any{
				"target": "dags",
				"query":  "?page=1",
			},
			code:   "invalid_tool_input",
			target: "dags",
			field:  "query",
		},
		{
			name: "query has unsupported parameter",
			arguments: map[string]any{
				"target": "dags",
				"query":  "unknown=1",
			},
			code:   "invalid_tool_input",
			target: "dags",
			field:  "query",
		},
		{
			name: "query has empty value",
			arguments: map[string]any{
				"target": "dags",
				"query":  "name=",
			},
			code:   "invalid_tool_input",
			target: "dags",
			field:  "query",
		},
		{
			name: "query has malformed percent encoding",
			arguments: map[string]any{
				"target": "dags",
				"query":  "name=%zz",
			},
			code:   "invalid_tool_input",
			target: "dags",
			field:  "query",
		},
		{
			name: "query has invalid range",
			arguments: map[string]any{
				"target": "runs",
				"query":  "limit=0",
			},
			code:   "invalid_tool_input",
			target: "runs",
			field:  "query",
		},
		{
			name: "query repeats non-repeatable parameter",
			arguments: map[string]any{
				"target": "dags",
				"query":  "name=a&name=b",
			},
			code:   "invalid_tool_input",
			target: "dags",
			field:  "query",
		},
		{
			name: "uri mode with name",
			arguments: map[string]any{
				"uri":  "dagu://reference/tools",
				"name": "tools",
			},
			code:    "invalid_tool_input",
			field:   "name",
			details: true,
		},
		{
			name: "uri mode with dagRunId",
			arguments: map[string]any{
				"uri":      "dagu://reference/tools",
				"dagRunId": "run-id",
			},
			code:    "invalid_tool_input",
			field:   "dagRunId",
			details: true,
		},
		{
			name: "uri mode with query field",
			arguments: map[string]any{
				"uri":   "dagu://runs",
				"query": "name=mcp_read_contract",
			},
			code:    "invalid_tool_input",
			field:   "query",
			details: true,
		},
		{
			name: "mixed addressing modes",
			arguments: map[string]any{
				"target": "reference",
				"uri":    "dagu://reference/tools",
			},
			code:    "invalid_tool_input",
			field:   "target",
			details: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, session, tt.arguments)
			output := requireReadError(t, result, tt.code)
			if tt.target != "" {
				require.Equal(t, tt.target, output["target"])
			}
			if tt.field != "" {
				require.Equal(t, tt.field, output["field"])
			}
			if tt.details {
				require.Contains(t, output, "details")
			}
		})
	}
}

func TestReadNonObjectInputErrors(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	tests := []struct {
		name      string
		arguments json.RawMessage
	}{
		{
			name:      "array",
			arguments: json.RawMessage(`[]`),
		},
		{
			name:      "string",
			arguments: json.RawMessage(`"reference"`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callReadWithArguments(t, session, tt.arguments)
			requireReadError(t, result, "invalid_tool_input")
		})
	}
}

func TestReadURIValidationErrors(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	tests := []struct {
		name string
		uri  string
		code string
	}{
		{
			name: "wrong scheme",
			uri:  "https://example.com/reference/tools",
			code: "invalid_resource_uri",
		},
		{
			name: "unsupported resource family",
			uri:  "dagu://queues",
			code: "unsupported_resource",
		},
		{
			name: "unsupported path shape",
			uri:  "dagu://dags/name/details",
			code: "invalid_resource_uri",
		},
		{
			name: "malformed uri",
			uri:  "://not-a-uri",
			code: "invalid_resource_uri",
		},
		{
			name: "malformed percent encoding",
			uri:  "dagu://dags/%zz/spec",
			code: "invalid_resource_uri",
		},
		{
			name: "malformed query",
			uri:  "dagu://dags?unknown=1",
			code: "invalid_resource_uri",
		},
		{
			name: "empty query value",
			uri:  "dagu://runs?name=",
			code: "invalid_resource_uri",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, session, map[string]any{"uri": tt.uri})
			output := requireReadError(t, result, tt.code)
			require.Equal(t, tt.uri, output["uri"])
		})
	}
}

func TestReadResourceNotFoundErrors(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	tests := []struct {
		name      string
		arguments map[string]any
		target    string
		uri       string
	}{
		{
			name: "missing reference",
			arguments: map[string]any{
				"target": "reference",
				"name":   "missing",
			},
			target: "reference",
			uri:    "dagu://reference/missing",
		},
		{
			name: "missing dag",
			arguments: map[string]any{
				"target": "dag",
				"name":   "missing-dag",
			},
			target: "dag",
		},
		{
			name: "missing dag spec",
			arguments: map[string]any{
				"target": "dag_spec",
				"name":   "missing-dag",
			},
			target: "dag_spec",
			uri:    "dagu://dags/missing-dag/spec",
		},
		{
			name: "missing run",
			arguments: map[string]any{
				"target":   "run",
				"name":     "missing-dag",
				"dagRunId": "missing-run",
			},
			target: "run",
			uri:    "dagu://runs/missing-dag/missing-run",
		},
		{
			name: "missing run logs",
			arguments: map[string]any{
				"target":   "run_logs",
				"name":     "missing-dag",
				"dagRunId": "missing-run",
			},
			target: "run_logs",
			uri:    "dagu://runs/missing-dag/missing-run/logs",
		},
		{
			name: "missing reference uri",
			arguments: map[string]any{
				"uri": "dagu://reference/missing",
			},
			target: "reference",
			uri:    "dagu://reference/missing",
		},
		{
			name: "missing dag spec uri",
			arguments: map[string]any{
				"uri": "dagu://dags/missing-dag/spec",
			},
			target: "dag_spec",
			uri:    "dagu://dags/missing-dag/spec",
		},
		{
			name: "missing run uri",
			arguments: map[string]any{
				"uri": "dagu://runs/missing-dag/missing-run",
			},
			target: "run",
			uri:    "dagu://runs/missing-dag/missing-run",
		},
		{
			name: "missing run logs uri",
			arguments: map[string]any{
				"uri": "dagu://runs/missing-dag/missing-run/logs",
			},
			target: "run_logs",
			uri:    "dagu://runs/missing-dag/missing-run/logs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, session, tt.arguments)
			output := requireReadError(t, result, "resource_not_found")
			require.Equal(t, tt.target, output["target"])
			if tt.uri == "" {
				require.NotContains(t, output, "uri")
			} else {
				require.Equal(t, tt.uri, output["uri"])
			}
		})
	}
}

func TestReadAuthenticationErrors(t *testing.T) {
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

	mcpKey := server.CreateAPIKey(t, "mcp", api.CreateAPIKeyRequestAllowedSurfacesMcp)
	session := server.Connect(t, mcpKey)
	result := callRead(t, session, map[string]any{"target": "reference"})
	requireReadSuccess(t, result, "reference", "dagu://reference/authoring", "dagu_reference", "text/markdown")
}

func TestReadNullTargetAllowsURIMode(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	result := callRead(t, session, map[string]any{
		"target": nil,
		"uri":    "dagu://reference/tools",
	})
	output := requireReadSuccess(t, result, "reference", "dagu://reference/tools", "dagu_reference", "text/markdown")
	data := requireData(t, output)
	require.Contains(t, requireString(t, data, "text"), "# Dagu MCP tools")
}

func TestReadNullAndTrimmedFields(t *testing.T) {
	fixture := newReadFixture(t)

	t.Run("null optional name defaults reference", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "reference",
			"name":   nil,
		})
		requireReadSuccess(t, result, "reference", "dagu://reference/authoring", "dagu_reference", "text/markdown")
	})

	t.Run("null forbidden fields are absent", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target":   "dags",
			"name":     nil,
			"dagRunId": nil,
			"query":    nil,
		})
		output := requireReadSuccess(t, result, "dags", "", "", "")
		requireItems(t, requireData(t, output))
	})

	t.Run("uri mode ignores null target-mode fields", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"uri":      "dagu://reference/tools",
			"target":   nil,
			"name":     nil,
			"dagRunId": nil,
			"query":    nil,
		})
		requireReadSuccess(t, result, "reference", "dagu://reference/tools", "dagu_reference", "text/markdown")
	})

	t.Run("uri is trimmed", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"uri": " dagu://reference/tools ",
		})
		requireReadSuccess(t, result, "reference", "dagu://reference/tools", "dagu_reference", "text/markdown")
	})

	t.Run("dag name is trimmed", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "dag_spec",
			"name":   " " + fixture.dagName + " ",
		})
		output := requireReadSuccess(t, result, "dag_spec", dagSpecURI(fixture.dagName), "dag_spec", "application/yaml")
		requireDAGSpecData(t, requireData(t, output), fixture.dagName)
	})

	t.Run("run id is trimmed", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target":   "run",
			"name":     " " + fixture.dagName + " ",
			"dagRunId": " " + fixture.dagRunID + " ",
		})
		output := requireReadSuccess(t, result, "run", runURI(fixture.dagName, fixture.dagRunID), "dag_run", "application/json")
		requireRunData(t, requireData(t, output), fixture.dagName, fixture.dagRunID)
	})
}
