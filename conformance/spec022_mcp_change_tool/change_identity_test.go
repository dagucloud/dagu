// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec022_mcp_change_tool_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/conformance/mcptest"
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
	require.ElementsMatch(t, []any{"name", "spec"}, requireArray(t, schema, "required"))

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	for _, field := range []string{"mode", "type", "name", "spec"} {
		property, ok := properties[field].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "string", property["type"])
	}
}

func toolInputSchema(t *testing.T, tool *mcpsdk.Tool) map[string]any {
	t.Helper()

	data, err := json.Marshal(tool.InputSchema)
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(data, &schema))
	return schema
}
