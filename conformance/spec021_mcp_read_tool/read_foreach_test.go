// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec021_mcp_read_tool_test

import (
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// requireForeachRead checks a foreach read like requireReadSuccess does,
// allowing the echoed name, dagRunId, stepName, item, and bodyStepName keys.
func requireForeachRead(t *testing.T, result *mcpsdk.CallToolResult, target, uri, linkName, mimeType string) map[string]any {
	t.Helper()

	require.False(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	requireResultContent(t, result, target, output["data"], uri, linkName, mimeType)
	require.Equal(t, target, output["target"])
	requireURIEqual(t, uri, requireString(t, output, "uri"))
	requireReferences(t, output["references"])
	for key := range output {
		require.Contains(t, []string{"target", "uri", "data", "references", "name", "dagRunId", "subRunId", "stepName", "item", "bodyStepName"}, key)
	}
	return output
}

// A failing foreach item is diagnosed by walking run -> foreachUri ->
// foreach_items -> foreach_item -> step_log with item and bodyStepName.
func TestReadForeachDrillDown(t *testing.T) {
	server := mcptest.NewServer(t)
	// Each item exits with its own value, so item 3 fails and item 0 succeeds
	// under every shell the runner may pick.
	server.CreateDAG(t, "mcp_foreach", `steps:
  - name: each
    foreach:
      items: [0, 3]
      steps:
        - id: body
          run: |
            echo "item ${foreach.item}"
            exit ${foreach.item}
`)
	dagRunID := server.StartDAG(t, "mcp_foreach")
	server.WaitForDAGRunStatus(t, "mcp_foreach", dagRunID, api.StatusPartialSuccess)
	session := server.Connect(t, "")

	itemsURI := runURI("mcp_foreach", dagRunID) + "/steps/each/foreach"

	// The run points at the foreach items of its step.
	result := callRead(t, session, map[string]any{"target": "run", "name": "mcp_foreach", "dagRunId": dagRunID})
	require.False(t, result.IsError)
	step := requireItem(t, requireData(t, mcptest.StructuredMap(t, result))["steps"].([]any), "name", "each")
	requireURIEqual(t, itemsURI, requireString(t, step, "foreachUri"))

	// The item list leads with the failed item.
	result = callRead(t, session, map[string]any{"target": "foreach_items", "name": "mcp_foreach", "dagRunId": dagRunID, "stepName": "each"})
	output := requireForeachRead(t, result, "foreach_items", itemsURI, "dag_run_foreach_items", "application/json")
	require.Equal(t, "each", output["stepName"])
	list := requireData(t, output)
	require.Equal(t, float64(2), list["total"])
	counts, ok := list["counts"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(1), counts["failed"])
	require.Equal(t, float64(1), counts["succeeded"])
	items := requireItems(t, list)
	require.Len(t, items, 2)
	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "failed", first["statusLabel"])
	require.Equal(t, float64(1), first["index"])
	item := requireString(t, first, "item")
	require.Equal(t, "1", item)

	// The status filter keeps one status; URI mode carries the query.
	result = callRead(t, session, map[string]any{"uri": itemsURI + "?status=succeeded"})
	output = requireForeachRead(t, result, "foreach_items", itemsURI+"?status=succeeded", "dag_run_foreach_items", "application/json")
	require.Len(t, requireItems(t, requireData(t, output)), 1)

	// The item reports its body steps and where their logs are.
	result = callRead(t, session, map[string]any{"target": "foreach_item", "name": "mcp_foreach", "dagRunId": dagRunID, "stepName": "each", "item": item})
	output = requireForeachRead(t, result, "foreach_item", itemsURI+"/"+item, "dag_run_foreach_item", "application/json")
	require.Equal(t, item, output["item"])
	detail := requireData(t, output)
	require.Equal(t, "failed", detail["statusLabel"])
	require.NotEmpty(t, detail["error"])
	body := requireItem(t, detail["steps"].([]any), "name", "body")
	require.Equal(t, "failed", body["statusLabel"])
	require.Equal(t, true, body["hasStdout"])

	// The body step log reads the item's own output.
	result = callRead(t, session, map[string]any{
		"target": "step_log", "name": "mcp_foreach", "dagRunId": dagRunID,
		"stepName": "each", "item": item, "bodyStepName": "body", "query": "stream=stdout",
	})
	logURI := itemsURI + "/" + item + "/steps/body/logs?stream=stdout"
	output = requireForeachRead(t, result, "step_log", logURI, "dag_run_foreach_step_log", "application/json")
	require.Equal(t, "body", output["bodyStepName"])
	logData := requireData(t, output)
	require.Contains(t, requireString(t, logData, "stdoutContent"), "item 3")
	require.Empty(t, logData["stderrContent"])

	// An item that has no record is not found.
	result = callRead(t, session, map[string]any{"target": "foreach_item", "name": "mcp_foreach", "dagRunId": dagRunID, "stepName": "each", "item": "9"})
	require.True(t, result.IsError)
	require.Equal(t, "resource_not_found", mcptest.StructuredMap(t, result)["code"])
}
