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
	for _, field := range []string{"mode", "type", "name", "spec", "workspace", "path", "content", "newPath"} {
		property, ok := properties[field].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "string", property["type"])
	}

	expectedRequired := map[string][]any{
		"upsert_dag": {"name", "spec"},
		"upsert_doc": {"type", "workspace", "path", "content"},
		"rename_doc": {"type", "workspace", "path", "newPath"},
		"delete_doc": {"type", "workspace", "path"},
	}
	for _, rawBranch := range requireArray(t, schema, "oneOf") {
		branch, ok := rawBranch.(map[string]any)
		require.True(t, ok)
		branchProperties, ok := branch["properties"].(map[string]any)
		require.True(t, ok)
		typeSchema, ok := branchProperties["type"].(map[string]any)
		require.True(t, ok)
		typeValues := requireArray(t, typeSchema, "enum")
		require.Len(t, typeValues, 1)
		changeType, ok := typeValues[0].(string)
		require.True(t, ok)
		requiredFields, ok := expectedRequired[changeType]
		require.True(t, ok, "unexpected change type %q", changeType)
		require.ElementsMatch(t, requiredFields, requireArray(t, branch, "required"))
		delete(expectedRequired, changeType)
	}
	require.Empty(t, expectedRequired)
}

func toolInputSchema(t *testing.T, tool *mcpsdk.Tool) map[string]any {
	t.Helper()

	data, err := json.Marshal(tool.InputSchema)
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(data, &schema))
	return schema
}
