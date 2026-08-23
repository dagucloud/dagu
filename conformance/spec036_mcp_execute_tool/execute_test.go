// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec036_mcp_execute_tool_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func callExecute(t *testing.T, session *mcpsdk.ClientSession, arguments map[string]any) *mcpsdk.CallToolResult {
	t.Helper()

	ctx := mcptest.Context(t)
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "dagu_execute",
		Arguments: arguments,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func requireExecuteError(t *testing.T, result *mcpsdk.CallToolResult, code, field string) {
	t.Helper()

	require.True(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	require.Equal(t, code, output["code"])
	require.NotEmpty(t, output["message"])
	if field != "" {
		require.Equal(t, field, output["field"])
	}
}

func TestExecuteStartWithWaitReturnsRunResult(t *testing.T) {
	server := mcptest.NewServer(t)
	server.CreateDAG(t, "mcp_execute_wait", `steps:
  - name: main
    run: "echo done"
`)
	session := server.Connect(t, "")

	result := callExecute(t, session, map[string]any{
		"action":             "start",
		"name":               "mcp_execute_wait",
		"wait":               true,
		"waitTimeoutSeconds": 30,
	})
	require.False(t, result.IsError)

	output := mcptest.StructuredMap(t, result)
	require.Equal(t, "start", output["action"])
	require.Equal(t, "dag", output["targetType"])
	require.NotEmpty(t, output["dagRunId"])
	require.NotEmpty(t, output["runUri"])
	require.Equal(t, true, output["completed"])

	run, ok := output["run"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_execute_wait", run["name"])
	require.NotEmpty(t, run["statusLabel"])

	steps, ok := run["steps"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, steps)
	step, ok := steps[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "main", step["name"])
}

func TestExecuteStartAcceptsParamsObject(t *testing.T) {
	server := mcptest.NewServer(t)
	server.CreateDAG(t, "mcp_execute_params", `params:
  - MSG: hello
steps:
  - name: main
    run: "echo ${params.MSG}"
`)
	session := server.Connect(t, "")

	result := callExecute(t, session, map[string]any{
		"action": "start",
		"name":   "mcp_execute_params",
		"params": map[string]any{"MSG": "world"},
		"wait":   true,
	})
	require.False(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	require.Equal(t, true, output["completed"])
}

func TestExecuteInputErrorsAreStructured(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	tests := []struct {
		name      string
		arguments map[string]any
		field     string
	}{
		{
			name:      "unknown action",
			arguments: map[string]any{"action": "restart"},
			field:     "action",
		},
		{
			name:      "unknown field",
			arguments: map[string]any{"action": "start", "name": "etl", "force": true},
			field:     "force",
		},
		{
			name:      "retry requires dagRunId",
			arguments: map[string]any{"action": "retry", "name": "etl"},
			field:     "dagRunId",
		},
		{
			name:      "wait timeout requires wait",
			arguments: map[string]any{"action": "start", "name": "etl", "waitTimeoutSeconds": 10},
			field:     "waitTimeoutSeconds",
		},
		{
			name:      "inline spec requires name",
			arguments: map[string]any{"action": "start", "spec": "steps: []"},
			field:     "name",
		},
		{
			name:      "start rejects enqueue-only field",
			arguments: map[string]any{"action": "start", "name": "etl", "queue": "batch"},
			field:     "queue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callExecute(t, session, tt.arguments)
			requireExecuteError(t, result, "invalid_tool_input", tt.field)
		})
	}
}

func TestExecuteStartUnknownDAGReturnsNotFound(t *testing.T) {
	server := mcptest.NewServer(t)
	session := server.Connect(t, "")

	result := callExecute(t, session, map[string]any{
		"action": "start",
		"name":   "does-not-exist",
	})
	requireExecuteError(t, result, "resource_not_found", "")
}
