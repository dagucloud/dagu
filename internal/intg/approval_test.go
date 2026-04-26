// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestWaitStepApproval(t *testing.T) {
	t.Run("WaitStepEntersWaitStatus", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: wait-step
    command: "true"
    approval:
      prompt: "Please approve"
  - name: after-wait
    depends: [wait-step]
    command: echo "approved"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 2)
		require.Equal(t, "wait-step", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[0].Status)
		require.Equal(t, "after-wait", dagRunStatus.Nodes[1].Step.Name)
		require.Equal(t, core.NodeNotStarted, dagRunStatus.Nodes[1].Status)
	})

	t.Run("WaitStepBlocksDependentNodes", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: before-wait
    command: echo "before"
  - name: wait-step
    depends: [before-wait]
    command: "true"
    approval: {}
  - name: after-wait-1
    depends: [wait-step]
    command: echo "after1"
  - name: after-wait-2
    depends: [wait-step]
    command: echo "after2"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 4)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status, "before-wait should succeed")
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[1].Status, "wait-step should be waiting")
		require.Equal(t, core.NodeNotStarted, dagRunStatus.Nodes[2].Status, "after-wait-1 should not start")
		require.Equal(t, core.NodeNotStarted, dagRunStatus.Nodes[3].Status, "after-wait-2 should not start")
	})

	t.Run("ParallelBranchWithWaitStep", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: branch-a-1
    command: echo "a1"
  - name: branch-a-2
    depends: [branch-a-1]
    command: echo "a2"
  - name: wait-branch
    command: "true"
    approval: {}
  - name: after-wait
    depends: [wait-branch]
    command: echo "after wait"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 4)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status, "branch-a-1 should succeed")
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[1].Status, "branch-a-2 should succeed")
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[2].Status, "wait-branch should be waiting")
		require.Equal(t, core.NodeNotStarted, dagRunStatus.Nodes[3].Status, "after-wait should not start")
	})
}

func TestApprovalField(t *testing.T) {
	t.Run("ApprovalFieldOnCommandStep", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: review-step
    command: echo "ready for review"
    approval:
      prompt: "Please review the output"
  - name: after-review
    depends: [review-step]
    command: echo "approved"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 2)
		// The command step should have executed (produced stdout) then entered waiting
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[0].Status, "review-step should be waiting")
		require.NotEmpty(t, dagRunStatus.Nodes[0].Stdout, "review-step should have stdout from command execution")
		require.Equal(t, core.NodeNotStarted, dagRunStatus.Nodes[1].Status, "after-review should not start")
	})

	t.Run("ApprovalFieldOnScriptStep", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: script-step
    script: |
      echo "script output line 1"
      echo "script output line 2"
    approval:
      prompt: "Review the script output"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 1)
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[0].Status, "script-step should be waiting")
		require.NotEmpty(t, dagRunStatus.Nodes[0].Stdout, "script-step should have stdout")
	})

	t.Run("ApprovalFieldWithInputConfig", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: review-step
    command: echo "needs feedback"
    approval:
      prompt: "Please provide feedback"
      input: [FEEDBACK, NOTES]
      required: [FEEDBACK]
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 1)
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[0].Status)
		require.NotNil(t, dagRunStatus.Nodes[0].Step.Approval)
		require.Equal(t, "Please provide feedback", dagRunStatus.Nodes[0].Step.Approval.Prompt)
		require.Equal(t, []string{"FEEDBACK", "NOTES"}, dagRunStatus.Nodes[0].Step.Approval.Input)
		require.Equal(t, []string{"FEEDBACK"}, dagRunStatus.Nodes[0].Step.Approval.Required)
	})

	t.Run("MultipleApprovalSteps", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: step-a
    command: echo "step a output"
    approval:
      prompt: "Approve step A"
  - name: step-b
    command: echo "step b output"
    approval:
      prompt: "Approve step B"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 2)
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[0].Status, "step-a should be waiting")
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[1].Status, "step-b should be waiting")
	})

	t.Run("ApprovalStepWithDependency", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: step-a
    command: echo "upstream"
  - name: step-b
    depends: [step-a]
    command: echo "needs approval"
    approval:
      prompt: "Review step-b"
  - name: step-c
    depends: [step-b]
    command: echo "downstream"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 3)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status, "step-a should succeed")
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[1].Status, "step-b should be waiting")
		require.Equal(t, core.NodeNotStarted, dagRunStatus.Nodes[2].Status, "step-c should not start")
	})

	t.Run("ApprovalFieldOnCallStep", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Create the sub-DAG first
		subDAG := th.DAG(t, `
type: graph
steps:
  - name: sub-step
    command: echo "sub-dag output"
`)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: call-step
    call: "`+subDAG.Name+`"
    approval:
      prompt: "Review sub-DAG results"
  - name: after-call
    depends: [call-step]
    command: echo "after call"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		testDAG.AssertLatestStatus(t, core.Waiting)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 2)
		require.Equal(t, core.NodeWaiting, dagRunStatus.Nodes[0].Status, "call-step should be waiting after sub-DAG completes")
		require.Equal(t, core.NodeNotStarted, dagRunStatus.Nodes[1].Status, "after-call should not start")
	})
}

func TestApprovalPushBackExposesHistoricalFeedbackEnvAcrossRewoundScope(t *testing.T) {
	const username = "reviewer"
	const password = "secretpass123"

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBasic
		cfg.Server.Auth.Basic.Username = username
		cfg.Server.Auth.Basic.Password = password
	}))

	dagName := "intg_pushback_scope_env"
	spec := fmt.Sprintf(`name: %s
type: graph
steps:
  - name: prepare
    script: |
%s
  - name: draft
    depends: [prepare]
    script: |
%s
  - name: review
    depends: [draft]
    script: |
%s
    approval:
      prompt: "Review and revise"
      input: [FEEDBACK]
      rewind_to: prepare
  - name: publish
    depends: [review]
    script: |
%s
`, dagName,
		indentTestScript(pushBackSnapshotScript(), 6),
		indentTestScript(pushBackSnapshotScript(), 6),
		indentTestScript(pushBackSnapshotScript(), 6),
		indentTestScript(pushBackSnapshotScript(), 6))

	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).WithBasicAuth(username, password).ExpectStatus(http.StatusCreated).Send(t)

	runID := "pushback-history-env"
	startResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{DagRunId: &runID},
	).WithBasicAuth(username, password).ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.Equal(t, api.DAGRunId(runID), startBody.DagRunId)

	waitForApprovalStepWaitingStatus(t, server, dagName, runID, "review", 0)

	firstInputs := map[string]string{"FEEDBACK": "first pass"}
	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/review/push-back", dagName, runID),
		api.PushBackStepRequest{Inputs: &firstInputs},
	).WithBasicAuth(username, password).ExpectStatus(http.StatusOK).Send(t)

	waitForApprovalStepWaitingStatus(t, server, dagName, runID, "review", 1)

	secondInputs := map[string]string{"FEEDBACK": "second pass"}
	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/review/push-back", dagName, runID),
		api.PushBackStepRequest{Inputs: &secondInputs},
	).WithBasicAuth(username, password).ExpectStatus(http.StatusOK).Send(t)

	status := waitForApprovalStepWaitingStatus(t, server, dagName, runID, "review", 2)
	reviewNode := nodeByName(t, status, "review")
	require.Equal(t, core.NodeWaiting, reviewNode.Status)
	require.Equal(t, 2, reviewNode.ApprovalIteration)
	require.Equal(t, "second pass", reviewNode.PushBackInputs["FEEDBACK"])

	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/review/approve", dagName, runID),
		api.ApproveStepRequest{},
	).WithBasicAuth(username, password).ExpectStatus(http.StatusOK).Send(t)

	status = waitForDAGRunStatus(t, server, dagName, runID, core.Succeeded)

	for _, stepName := range []string{"prepare", "draft", "review", "publish"} {
		node := nodeByName(t, status, stepName)
		stdoutContent, err := os.ReadFile(node.Stdout)
		require.NoError(t, err)
		require.Equal(t, "second pass", lastLabeledOutputValue(string(stdoutContent), "FEEDBACK="), "%s did not receive latest feedback", stepName)
		requirePushBackPayload(t, stepName, lastLabeledOutputValue(string(stdoutContent), "DAG_PUSHBACK="), username)
	}
}

func pushBackSnapshotScript() string {
	return test.ForOS(
		"printf 'FEEDBACK=%s\\n' \"${FEEDBACK:-}\"\nprintf 'DAG_PUSHBACK=%s\\n' \"${DAG_PUSHBACK:-}\"\nprintf '%s\\n' ready",
		"Write-Output ('FEEDBACK=' + [string]$env:FEEDBACK)\nWrite-Output ('DAG_PUSHBACK=' + [string]$env:DAG_PUSHBACK)\nWrite-Output 'ready'",
	)
}

func waitForApprovalStepWaitingStatus(
	t *testing.T,
	server test.Server,
	dagName, dagRunID, stepName string,
	iteration int,
) *exec.DAGRunStatus {
	t.Helper()

	var status *exec.DAGRunStatus
	require.Eventually(t, func() bool {
		attempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, dagRunID))
		if err != nil {
			return false
		}

		status, err = attempt.ReadStatus(server.Context)
		if err != nil || status == nil || status.Status != core.Waiting {
			return false
		}

		for _, node := range status.Nodes {
			if node.Step.Name != stepName {
				continue
			}
			return node.Status == core.NodeWaiting && node.ApprovalIteration == iteration
		}

		return false
	}, intgTestTimeout(15*time.Second), 200*time.Millisecond)

	return status
}

func waitForDAGRunStatus(
	t *testing.T,
	server test.Server,
	dagName, dagRunID string,
	expected core.Status,
) *exec.DAGRunStatus {
	t.Helper()

	var status *exec.DAGRunStatus
	require.Eventually(t, func() bool {
		attempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, dagRunID))
		if err != nil {
			return false
		}

		status, err = attempt.ReadStatus(server.Context)
		if err != nil || status == nil {
			return false
		}

		return status.Status == expected
	}, intgTestTimeout(15*time.Second), 200*time.Millisecond)

	return status
}

func nodeByName(t *testing.T, status *exec.DAGRunStatus, stepName string) *exec.Node {
	t.Helper()

	for _, node := range status.Nodes {
		if node.Step.Name == stepName {
			return node
		}
	}

	require.FailNowf(t, "node not found", "step %s not found in DAG run status", stepName)
	return nil
}

func requirePushBackPayload(t *testing.T, stepName, raw, expectedUser string) {
	t.Helper()

	require.NotEmptyf(t, raw, "%s stdout did not contain DAG_PUSHBACK output", stepName)

	var payload struct {
		Iteration int               `json:"iteration"`
		By        string            `json:"by"`
		At        string            `json:"at"`
		Inputs    map[string]string `json:"inputs"`
		History   []struct {
			Iteration int               `json:"iteration"`
			By        string            `json:"by"`
			At        string            `json:"at"`
			Inputs    map[string]string `json:"inputs"`
		} `json:"history"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	require.Equalf(t, 2, payload.Iteration, "%s saw unexpected push-back iteration", stepName)
	require.Equalf(t, expectedUser, payload.By, "%s saw unexpected latest push-back actor", stepName)
	require.NotEmptyf(t, payload.At, "%s saw empty latest push-back timestamp", stepName)
	_, err := time.Parse(time.RFC3339, payload.At)
	require.NoErrorf(t, err, "%s saw invalid latest push-back timestamp", stepName)
	require.Equalf(t, "second pass", payload.Inputs["FEEDBACK"], "%s saw unexpected latest feedback", stepName)
	require.Lenf(t, payload.History, 2, "%s saw unexpected push-back history length", stepName)
	require.Equalf(t, 1, payload.History[0].Iteration, "%s saw unexpected first push-back iteration", stepName)
	require.Equalf(t, expectedUser, payload.History[0].By, "%s saw unexpected first push-back actor", stepName)
	require.NotEmptyf(t, payload.History[0].At, "%s saw empty first push-back timestamp", stepName)
	_, err = time.Parse(time.RFC3339, payload.History[0].At)
	require.NoErrorf(t, err, "%s saw invalid first push-back timestamp", stepName)
	require.Equalf(t, "first pass", payload.History[0].Inputs["FEEDBACK"], "%s saw unexpected first push-back feedback", stepName)
	require.Equalf(t, 2, payload.History[1].Iteration, "%s saw unexpected second push-back iteration", stepName)
	require.Equalf(t, expectedUser, payload.History[1].By, "%s saw unexpected second push-back actor", stepName)
	require.NotEmptyf(t, payload.History[1].At, "%s saw empty second push-back timestamp", stepName)
	_, err = time.Parse(time.RFC3339, payload.History[1].At)
	require.NoErrorf(t, err, "%s saw invalid second push-back timestamp", stepName)
	require.Equalf(t, payload.At, payload.History[1].At, "%s saw mismatched latest push-back timestamp", stepName)
	require.Equalf(t, "second pass", payload.History[1].Inputs["FEEDBACK"], "%s saw unexpected second push-back feedback", stepName)
}

func lastLabeledOutputValue(output, prefix string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}
