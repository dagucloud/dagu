// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec036_mcp_execute_tool_test

import (
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/conformance/mcptest"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
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

func TestExecuteStartWithWaitReturnsAtWaitingCheckpoint(t *testing.T) {
	server := mcptest.NewServer(t)
	server.CreateWaitingDAG(t, "mcp_execute_waiting")
	session := server.Connect(t, "")

	result := callExecute(t, session, map[string]any{
		"action":             "start",
		"name":               "mcp_execute_waiting",
		"wait":               true,
		"waitTimeoutSeconds": 30,
	})
	require.False(t, result.IsError)

	output := mcptest.StructuredMap(t, result)
	// A checkpoint is not completion; the run resumes only once an operator acts.
	require.Equal(t, false, output["completed"])
	require.Equal(t, "waiting", output["statusLabel"])

	// A run stopped at a checkpoint carries the detail an operator needs; a run
	// that merely timed out reports no detail at all.
	run, ok := output["run"].(map[string]any)
	require.Truef(t, ok, "wait returned no run details (completed=%v, statusLabel=%v)",
		output["completed"], output["statusLabel"])
	steps, ok := run["steps"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, steps)

	humanTask, ok := stepNamed(t, steps, "release_review")["humanTask"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Choose the release target", humanTask["prompt"])
	require.NotEmpty(t, humanTask["form"])
}

// stepNamed finds a step by name, since the two waiting steps in the fixture
// are independent and the checkpoint does not order them.
func stepNamed(t *testing.T, steps []any, name string) map[string]any {
	t.Helper()

	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		require.True(t, ok)
		if step["name"] == name {
			return step
		}
	}
	t.Fatalf("run details have no step named %q", name)
	return nil
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

// readRun returns the Spec 021 run detail model of a root run, or of its
// sub-DAG run when subRunID is not empty.
func readRun(t *testing.T, session *mcpsdk.ClientSession, name, dagRunID, subRunID string) map[string]any {
	t.Helper()

	arguments := map[string]any{"target": "run", "name": name, "dagRunId": dagRunID}
	if subRunID != "" {
		arguments["subRunId"] = subRunID
	}
	result, err := session.CallTool(mcptest.Context(t), &mcpsdk.CallToolParams{
		Name:      "dagu_read",
		Arguments: arguments,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	data, ok := mcptest.StructuredMap(t, result)["data"].(map[string]any)
	require.True(t, ok)
	return data
}

// pollRun reads a root run's detail model for a poll, reporting a failed
// read to c so the poll tries again.
func pollRun(t *testing.T, c *assert.CollectT, session *mcpsdk.ClientSession, name, dagRunID string) (map[string]any, bool) {
	result, err := session.CallTool(mcptest.Context(t), &mcpsdk.CallToolParams{
		Name:      "dagu_read",
		Arguments: map[string]any{"target": "run", "name": name, "dagRunId": dagRunID},
	})
	if !assert.NoError(c, err) || !assert.False(c, result.IsError, "dagu_read failed") {
		return nil, false
	}
	run, ok := mcptest.StructuredMap(t, result)["data"].(map[string]any)
	return run, assert.True(c, ok)
}

func readStep(t *testing.T, run map[string]any, name string) map[string]any {
	t.Helper()

	steps, ok := run["steps"].([]any)
	require.True(t, ok)
	return stepNamed(t, steps, name)
}

func requireAttemptID(t *testing.T, run map[string]any) string {
	t.Helper()

	attemptID, ok := run["attemptId"].(string)
	require.True(t, ok)
	require.NotEmpty(t, attemptID)
	return attemptID
}

// Deciding both steps of the waiting fixture lets the run resume and use the
// values the decisions supplied.
func TestExecuteDecisionsResumeWaitingRun(t *testing.T) {
	const dagName = "mcp_execute_decide"

	server := mcptest.NewServer(t)
	dagRunID := server.CreateWaitingRun(t, dagName)
	session := server.Connect(t, "")
	attemptID := requireAttemptID(t, readRun(t, session, dagName, dagRunID, ""))

	result := callExecute(t, session, map[string]any{
		"action":    "approve",
		"name":      dagName,
		"dagRunId":  dagRunID,
		"stepName":  "release_review",
		"inputs":    map[string]any{"environment": "production"},
		"attemptId": attemptID,
	})
	require.False(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	require.Equal(t, "approve", output["action"])
	require.Equal(t, "run", output["targetType"])
	require.Equal(t, dagName, output["dagName"])
	require.Equal(t, dagRunID, output["dagRunId"])
	require.Equal(t, "release_review", output["stepName"])
	require.Equal(t, "dagu://runs/"+dagName+"/"+dagRunID, output["runUri"])
	// The approval gate still holds the run.
	require.Equal(t, "waiting", output["statusLabel"])

	result = callExecute(t, session, map[string]any{
		"action":             "approve",
		"name":               dagName,
		"dagRunId":           dagRunID,
		"stepName":           "deploy_gate",
		"inputs":             map[string]any{"ticket": "CHG-42"},
		"attemptId":          attemptID,
		"wait":               true,
		"waitTimeoutSeconds": 25,
	})
	require.False(t, result.IsError)
	output = mcptest.StructuredMap(t, result)
	require.Equal(t, true, output["completed"])
	require.Equal(t, "succeeded", output["statusLabel"])

	run, ok := output["run"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "succeeded", readStep(t, run, "release_review")["statusLabel"])
	require.Equal(t, "succeeded", readStep(t, run, "deploy_gate")["statusLabel"])
	require.Equal(t, "succeeded", readStep(t, run, "record_release")["statusLabel"])
}

func TestExecuteRejectEndsRun(t *testing.T) {
	const dagName = "mcp_execute_reject"

	server := mcptest.NewServer(t)
	dagRunID := server.CreateWaitingRun(t, dagName)
	session := server.Connect(t, "")

	result := callExecute(t, session, map[string]any{
		"action":   "reject",
		"name":     dagName,
		"dagRunId": dagRunID,
		"stepName": "deploy_gate",
		"reason":   "change window closed",
	})
	require.False(t, result.IsError)
	require.Equal(t, "rejected", mcptest.StructuredMap(t, result)["statusLabel"])

	run := readRun(t, session, dagName, dagRunID, "")
	require.Equal(t, "rejected", run["statusLabel"])
	require.Equal(t, "rejected", readStep(t, run, "deploy_gate")["statusLabel"])
}

// A push-back resumes the run in a new attempt, so a decision based on the
// attempt read before the push-back is refused.
func TestExecuteRefusesDecisionFromEarlierAttempt(t *testing.T) {
	const dagName = "mcp_execute_push_back"

	server := mcptest.NewServer(t)
	dagRunID := server.CreateWaitingRun(t, dagName)
	session := server.Connect(t, "")
	earlier := requireAttemptID(t, readRun(t, session, dagName, dagRunID, ""))

	result := callExecute(t, session, map[string]any{
		"action":    "push_back",
		"name":      dagName,
		"dagRunId":  dagRunID,
		"stepName":  "deploy_gate",
		"inputs":    map[string]any{"ticket": "CHG-43"},
		"attemptId": earlier,
	})
	require.False(t, result.IsError)

	var current string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		// A read can fail while the run's new attempt starts, so a failed
		// read is polled again rather than failing the test.
		run, ok := pollRun(t, c, session, dagName, dagRunID)
		if !ok {
			return
		}
		current, _ = run["attemptId"].(string)
		assert.NotEqual(c, earlier, current)
		assert.Equal(c, "waiting", readStep(t, run, "deploy_gate")["statusLabel"])
		// The gate waits before the run does, and only a waiting run takes a decision.
		assert.Equal(c, "waiting", run["statusLabel"])
	}, mcptest.Timeout, 250*time.Millisecond, "the pushed-back run never waited again in a new attempt")

	result = callExecute(t, session, map[string]any{
		"action":    "approve",
		"name":      dagName,
		"dagRunId":  dagRunID,
		"stepName":  "deploy_gate",
		"inputs":    map[string]any{"ticket": "CHG-43"},
		"attemptId": earlier,
	})
	requireExecuteError(t, result, "conflict", "")
	details, ok := mcptest.StructuredMap(t, result)["details"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, current, details["attemptId"])
	require.Equal(t, "waiting", readStep(t, readRun(t, session, dagName, dagRunID, ""), "deploy_gate")["statusLabel"])

	result = callExecute(t, session, map[string]any{
		"action":    "approve",
		"name":      dagName,
		"dagRunId":  dagRunID,
		"stepName":  "deploy_gate",
		"inputs":    map[string]any{"ticket": "CHG-43"},
		"attemptId": current,
	})
	require.False(t, result.IsError)
}

func TestExecuteDecidesSubDAGRunStep(t *testing.T) {
	const (
		parentName = "mcp_execute_parent"
		childName  = "mcp_execute_child"
	)

	server := mcptest.NewServer(t)
	server.CreateDAG(t, childName, `steps:
  - name: child_gate
    run: "true"
    approval:
      prompt: Approve the child
`)
	server.CreateDAG(t, parentName, `steps:
  - name: call_child
    action: dag.run
    with:
      dag: `+childName+`
`)
	dagRunID := server.StartDAG(t, parentName)
	server.WaitForDAGRunStatus(t, parentName, dagRunID, api.StatusWaiting)
	session := server.Connect(t, "")

	subRuns, ok := readStep(t, readRun(t, session, parentName, dagRunID, ""), "call_child")["subRuns"].([]any)
	require.True(t, ok)
	require.Len(t, subRuns, 1)
	subRun, ok := subRuns[0].(map[string]any)
	require.True(t, ok)
	subRunID, ok := subRun["dagRunId"].(string)
	require.True(t, ok)

	child := readRun(t, session, parentName, dagRunID, subRunID)
	require.Contains(t, readStep(t, child, "child_gate"), "approval")

	result := callExecute(t, session, map[string]any{
		"action":      "approve",
		"name":        parentName,
		"dagRunId":    dagRunID,
		"subDAGRunId": subRunID,
		"stepName":    "child_gate",
		"attemptId":   requireAttemptID(t, child),
	})
	require.False(t, result.IsError)
	output := mcptest.StructuredMap(t, result)
	require.Equal(t, subRunID, output["subDAGRunId"])
	require.Equal(t, "child_gate", output["stepName"])

	require.NotEqual(t, "waiting", readStep(t, readRun(t, session, parentName, dagRunID, subRunID), "child_gate")["statusLabel"])
}

// Refused decisions leave the waiting run untouched, so the cases share one run.
func TestExecuteDecisionErrors(t *testing.T) {
	const dagName = "mcp_execute_decision_errors"

	server := mcptest.NewServer(t)
	dagRunID := server.CreateWaitingRun(t, dagName)
	session := server.Connect(t, "")

	tests := []struct {
		name      string
		arguments map[string]any
		code      string
		field     string
	}{
		{
			name:      "human task cannot be rejected",
			arguments: map[string]any{"action": "reject", "stepName": "release_review"},
			code:      "invalid_tool_input",
			field:     "action",
		},
		{
			name:      "step not waiting",
			arguments: map[string]any{"action": "approve", "stepName": "record_release"},
			code:      "conflict",
		},
		{
			name:      "unknown step",
			arguments: map[string]any{"action": "approve", "stepName": "no_such_step"},
			code:      "resource_not_found",
		},
		{
			name:      "stale attempt",
			arguments: map[string]any{"action": "approve", "stepName": "deploy_gate", "inputs": map[string]any{"ticket": "CHG-1"}, "attemptId": "stale"},
			code:      "conflict",
		},
		{
			name:      "missing required approval input",
			arguments: map[string]any{"action": "approve", "stepName": "deploy_gate"},
			code:      "invalid_tool_input",
		},
		{
			name:      "non-string approval input",
			arguments: map[string]any{"action": "approve", "stepName": "deploy_gate", "inputs": map[string]any{"ticket": 7}},
			code:      "invalid_tool_input",
			field:     "inputs",
		},
		{
			name:      "human task form refuses input",
			arguments: map[string]any{"action": "approve", "stepName": "release_review", "inputs": map[string]any{"environment": "moon"}},
			code:      "invalid_tool_input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.arguments["name"] = dagName
			tt.arguments["dagRunId"] = dagRunID
			requireExecuteError(t, callExecute(t, session, tt.arguments), tt.code, tt.field)
		})
	}

	run := readRun(t, session, dagName, dagRunID, "")
	require.Equal(t, "waiting", run["statusLabel"])
	require.Equal(t, "waiting", readStep(t, run, "release_review")["statusLabel"])
	require.Equal(t, "waiting", readStep(t, run, "deploy_gate")["statusLabel"])

	result := callExecute(t, session, map[string]any{
		"action":   "approve",
		"name":     dagName,
		"dagRunId": "no-such-run",
		"stepName": "deploy_gate",
	})
	requireExecuteError(t, result, "resource_not_found", "")
}
