// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec022_mcp_change_tool_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestChangeToolIdentityAndInputSchema(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")
	ctx := mcptest.Context(t)

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	var tool *mcpsdk.Tool
	for _, candidate := range result.Tools {
		if candidate.Name == "dagu_change" {
			tool = candidate
			break
		}
	}
	require.NotNil(t, tool)
	require.NotNil(t, tool.Annotations)
	require.NotNil(t, tool.Annotations.DestructiveHint)
	require.True(t, *tool.Annotations.DestructiveHint)
	require.NotNil(t, tool.Annotations.OpenWorldHint)
	require.False(t, *tool.Annotations.OpenWorldHint)

	schema := toolInputSchema(t, tool)
	require.Equal(t, "object", schema["type"])
	require.Equal(t, false, schema["additionalProperties"])

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	for _, field := range []string{"mode", "type", "name", "spec", "newName", "workspace", "path", "content", "newPath"} {
		property, ok := properties[field].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "string", property["type"])
	}

	type branchContract struct {
		types    []any
		required []any
	}
	expectedBranches := map[string]branchContract{
		"upsert_dag": {
			types:    []any{"upsert_dag"},
			required: []any{"name", "spec"},
		},
		"rename_dag": {
			types:    []any{"rename_dag"},
			required: []any{"type", "name", "newName"},
		},
		"delete_dag": {
			types:    []any{"delete_dag"},
			required: []any{"type", "name"},
		},
		"upsert_wiki_page": {
			types:    []any{"upsert_wiki_page"},
			required: []any{"type", "workspace", "path", "content"},
		},
		"rename_wiki_page": {
			types:    []any{"rename_wiki_page"},
			required: []any{"type", "workspace", "path", "newPath"},
		},
		"delete_wiki_page": {
			types:    []any{"delete_wiki_page"},
			required: []any{"type", "workspace", "path"},
		},
	}
	for _, rawBranch := range requireArray(t, schema, "oneOf") {
		branch, ok := rawBranch.(map[string]any)
		require.True(t, ok)
		branchProperties, ok := branch["properties"].(map[string]any)
		require.True(t, ok)
		typeSchema, ok := branchProperties["type"].(map[string]any)
		require.True(t, ok)
		typeValues := requireArray(t, typeSchema, "enum")
		changeType, ok := typeValues[0].(string)
		require.True(t, ok)
		contract, ok := expectedBranches[changeType]
		require.True(t, ok, "unexpected change type %q", changeType)
		require.Equal(t, contract.types, typeValues)
		require.ElementsMatch(t, contract.required, requireArray(t, branch, "required"))
		delete(expectedBranches, changeType)
	}
	require.Empty(t, expectedBranches)
}

func toolInputSchema(t *testing.T, tool *mcpsdk.Tool) map[string]any {
	t.Helper()

	data, err := json.Marshal(tool.InputSchema)
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(data, &schema))
	return schema
}
