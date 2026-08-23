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

func TestReadRunTargets(t *testing.T) {
	fixture := newReadFixture(t)

	t.Run("runs target", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target": "runs",
			"query":  "name=" + fixture.dagName + "&limit=20&status=4",
		})
		output := requireReadSuccess(t, result, "runs", "", "", "")
		item := requireItem(t, requireItems(t, requireData(t, output)), "dagRunId", fixture.dagRunID)
		requireRunListItem(t, item, fixture.dagName, fixture.dagRunID)
	})

	t.Run("run target", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target":   "run",
			"name":     fixture.dagName,
			"dagRunId": fixture.dagRunID,
		})
		output := requireReadSuccess(t, result, "run", runURI(fixture.dagName, fixture.dagRunID), "dag_run", "application/json")
		requireRunData(t, requireData(t, output), fixture.dagName, fixture.dagRunID)
	})

	t.Run("run_logs target", func(t *testing.T) {
		query := "tail=100"
		result := callRead(t, fixture.session, map[string]any{
			"target":   "run_logs",
			"name":     fixture.dagName,
			"dagRunId": fixture.dagRunID,
			"query":    query,
		})
		output := requireReadSuccess(t, result, "run_logs", runLogsURI(fixture.dagName, fixture.dagRunID, query), "dag_run_logs", "application/json")
		requireRunLogsData(t, requireData(t, output))
	})
}

func TestReadStepLogQueries(t *testing.T) {
	fixture := newReadFixture(t)

	callStepLog := func(t *testing.T, query string) map[string]any {
		t.Helper()
		arguments := map[string]any{
			"target":   "step_log",
			"name":     fixture.dagName,
			"dagRunId": fixture.dagRunID,
			"stepName": "main",
		}
		if query != "" {
			arguments["query"] = query
		}
		result := callRead(t, fixture.session, arguments)
		require.False(t, result.IsError)
		output := mcptest.StructuredMap(t, result)
		require.Equal(t, "step_log", output["target"])
		return requireData(t, output)
	}

	t.Run("default returns stdout", func(t *testing.T) {
		data := callStepLog(t, "")
		require.Contains(t, requireString(t, data, "stdoutContent"), fixture.dagName)
	})

	t.Run("tail bounds returned lines", func(t *testing.T) {
		data := callStepLog(t, "tail=1")
		require.Contains(t, requireString(t, data, "stdoutContent"), fixture.dagName)
		requireNumber(t, data, "lineCount")
	})

	t.Run("stream selects stderr only", func(t *testing.T) {
		data := callStepLog(t, "stream=stderr")
		require.Empty(t, data["stdoutContent"])
	})

	t.Run("unknown parameter is rejected", func(t *testing.T) {
		result := callRead(t, fixture.session, map[string]any{
			"target":   "step_log",
			"name":     fixture.dagName,
			"dagRunId": fixture.dagRunID,
			"stepName": "main",
			"query":    "unknown=1",
		})
		requireReadError(t, result, "invalid_tool_input")
	})
}

func TestReadSubRunDrillDown(t *testing.T) {
	server := mcptest.NewServer(t)
	server.CreateDAG(t, "mcp_sub_parent", `steps:
  - name: call-child
    action: dag.run
    with:
      dag: child

---
name: child
steps:
  - name: boom
    run: "exit 1"
`)
	dagRunID := server.StartDAG(t, "mcp_sub_parent")
	server.WaitForDAGRunStatus(t, "mcp_sub_parent", dagRunID, api.StatusFailed)
	session := server.Connect(t, "")

	// The parent run identifies the failing child run.
	result := callRead(t, session, map[string]any{
		"target":   "run",
		"name":     "mcp_sub_parent",
		"dagRunId": dagRunID,
	})
	require.False(t, result.IsError)
	parent := requireData(t, mcptest.StructuredMap(t, result))
	parentStep := requireItem(t, parent["steps"].([]any), "name", "call-child")
	require.NotEmpty(t, parentStep["error"])
	subRuns, ok := parentStep["subRuns"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, subRuns)
	subRun, ok := subRuns[0].(map[string]any)
	require.True(t, ok)
	subRunID := requireString(t, subRun, "dagRunId")
	subURI := runURI("mcp_sub_parent", dagRunID) + "/sub/" + subRunID
	requireURIEqual(t, subURI, requireString(t, subRun, "uri"))

	// The child run carries its own step statuses and error message.
	requireChildRun := func(t *testing.T, result *mcpsdk.CallToolResult) {
		t.Helper()
		require.False(t, result.IsError)
		output := mcptest.StructuredMap(t, result)
		require.Equal(t, "run", output["target"])
		requireURIEqual(t, subURI, requireString(t, output, "uri"))
		child := requireData(t, output)
		require.Equal(t, "child", child["name"])
		require.Equal(t, subRunID, child["dagRunId"])
		childStep := requireItem(t, child["steps"].([]any), "name", "boom")
		require.NotEmpty(t, childStep["error"])
		requireURIEqual(t, subURI+"/steps/boom/logs", requireString(t, childStep, "logUri"))
	}
	requireChildRun(t, callRead(t, session, map[string]any{
		"target":   "run",
		"name":     "mcp_sub_parent",
		"dagRunId": dagRunID,
		"subRunId": subRunID,
	}))
	requireChildRun(t, callRead(t, session, map[string]any{"uri": subURI}))

	// The child step log resolves through the same sub addressing.
	logResult := callRead(t, session, map[string]any{
		"target":   "step_log",
		"name":     "mcp_sub_parent",
		"dagRunId": dagRunID,
		"subRunId": subRunID,
		"stepName": "boom",
	})
	require.False(t, logResult.IsError)
	logData := requireData(t, mcptest.StructuredMap(t, logResult))
	requireNumber(t, logData, "lineCount")
}

func TestReadRunURIMode(t *testing.T) {
	fixture := newReadFixture(t)

	tests := []struct {
		name     string
		uri      string
		target   string
		linkName string
		mimeType string
	}{
		{
			name:     "runs collection",
			uri:      "dagu://runs?name=" + fixture.dagName + "&limit=20&status=4",
			target:   "runs",
			linkName: "dag_runs",
			mimeType: "application/json",
		},
		{
			name:     "run detail",
			uri:      runURI(fixture.dagName, fixture.dagRunID),
			target:   "run",
			linkName: "dag_run",
			mimeType: "application/json",
		},
		{
			name:     "run logs",
			uri:      runLogsURI(fixture.dagName, fixture.dagRunID, "tail=100"),
			target:   "run_logs",
			linkName: "dag_run_logs",
			mimeType: "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callRead(t, fixture.session, map[string]any{"uri": tt.uri})
			output := requireReadSuccess(t, result, tt.target, tt.uri, tt.linkName, tt.mimeType)
			data := requireData(t, output)
			switch tt.target {
			case "runs":
				item := requireItem(t, requireItems(t, data), "dagRunId", fixture.dagRunID)
				requireRunListItem(t, item, fixture.dagName, fixture.dagRunID)
			case "run":
				requireRunData(t, data, fixture.dagName, fixture.dagRunID)
			case "run_logs":
				requireRunLogsData(t, data)
			}
		})
	}
}

func TestReadRunURIUsesCanonicalNameSegment(t *testing.T) {
	fixture := newDottedDAGReadFixture(t)

	result := callRead(t, fixture.session, map[string]any{
		"target":   "run",
		"name":     fixture.dagName,
		"dagRunId": fixture.dagRunID,
	})
	requireReadSuccess(t, result, "run", runURI(fixture.dagName, fixture.dagRunID), "dag_run", "application/json")
	require.Equal(t, "dagu://runs/mcp.read-contract/"+fixture.dagRunID, runURI(fixture.dagName, fixture.dagRunID))
}

func requireRunListItem(t *testing.T, item map[string]any, dagName, dagRunID string) {
	t.Helper()

	require.Equal(t, dagName, item["name"])
	require.Equal(t, dagRunID, item["dagRunId"])
	require.Equal(t, runURI(dagName, dagRunID), item["uri"])
	requireNumber(t, item, "status")
	require.NotEmpty(t, requireString(t, item, "statusLabel"))
	// The fixture run is finished, so both run timestamps must be present.
	require.NotEmpty(t, requireString(t, item, "startedAt"))
	require.NotEmpty(t, requireString(t, item, "finishedAt"))
}

func requireRunData(t *testing.T, data map[string]any, dagName, dagRunID string) {
	t.Helper()

	require.Equal(t, dagName, data["name"])
	require.Equal(t, dagRunID, data["dagRunId"])
	require.Equal(t, runURI(dagName, dagRunID), data["uri"])
	require.Equal(t, runLogsURI(dagName, dagRunID, ""), data["logsUri"])
	requireNumber(t, data, "status")
	require.NotEmpty(t, requireString(t, data, "statusLabel"))
	// The fixture run is finished, so both run timestamps must be present.
	require.NotEmpty(t, requireString(t, data, "startedAt"))
	require.NotEmpty(t, requireString(t, data, "finishedAt"))

	steps, ok := data["steps"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, steps)
	step := requireItem(t, steps, "name", "main")
	requireNumber(t, step, "status")
	require.NotEmpty(t, requireString(t, step, "statusLabel"))
	require.Equal(t, runURI(dagName, dagRunID)+"/steps/main/logs", step["logUri"])
}

func requireRunLogsData(t *testing.T, data map[string]any) {
	t.Helper()

	schedulerLog, ok := data["schedulerLog"].(map[string]any)
	require.True(t, ok)
	requireString(t, schedulerLog, "content")
	requireNumber(t, schedulerLog, "lineCount")
	requireNumber(t, schedulerLog, "totalLines")
	requireBool(t, schedulerLog, "hasMore")

	stepLogs, ok := data["stepLogs"].([]any)
	require.True(t, ok)
	for _, stepLog := range stepLogs {
		step, ok := stepLog.(map[string]any)
		require.True(t, ok)
		requireString(t, step, "stepName")
		requireNumber(t, step, "status")
		requireString(t, step, "statusLabel")
		requireBool(t, step, "hasStdout")
		requireBool(t, step, "hasStderr")
	}
}
