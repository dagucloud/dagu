// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func retryOutputStepScript(counterFile string) string {
	if runtime.GOOS == "windows" {
		return strings.TrimPrefix(fmt.Sprintf(`
$counterFile = %s
if (-not (Test-Path $counterFile)) {
  Set-Content -Path $counterFile -Value "1" -NoNewline
  Write-Output "output_attempt_1"
  exit 1
}

$count = (Get-Content -Raw -Path $counterFile).Trim()
if ($count -eq "1") {
  Set-Content -Path $counterFile -Value "2" -NoNewline
  Write-Output "output_attempt_2_success"
  exit 0
}
`, test.PowerShellQuote(counterFile)), "\n")
	}

	counterFile = test.ShellPath(counterFile)
	return strings.TrimPrefix(fmt.Sprintf(`
COUNTER_FILE=%s
if [ ! -f "$COUNTER_FILE" ]; then
  printf '%%s' "1" > "$COUNTER_FILE"
  echo "output_attempt_1"
  exit 1
fi

COUNT=$(cat "$COUNTER_FILE")
if [ "$COUNT" -eq "1" ]; then
  printf '%%s' "2" > "$COUNTER_FILE"
  echo "output_attempt_2_success"
  exit 0
fi
`, test.PosixQuote(counterFile)), "\n")
}

func indentCommandBlock(command string, spaces int) string {
	lines := strings.Split(strings.TrimRight(command, "\n"), "\n")
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.Join(lines, "\n"+prefix)
}

func TestInlineSubDAG(t *testing.T) {
	t.Run("SimpleExecution", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
steps:
  - name: run-local-child
    action: dag.run
    with:
      dag: local-child
      params: "NAME=World"
    output: SUB_RESULT

  - run: echo "Child said ${SUB_RESULT.outputs.GREETING}"
    depends: run-local-child

---

name: local-child
params:
  - NAME
steps:
  - run: echo "Hello, ${NAME}!"
    output: GREETING

  - run: echo "Greeting was ${GREETING}"
    depends: cmd_1
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 2)
		require.Equal(t, "run-local-child", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)

		logContent, err := os.ReadFile(dagRunStatus.Nodes[1].Stdout)
		require.NoError(t, err)
		require.Contains(t, string(logContent), "Child said Hello, World!")
	})

	t.Run("TwoLevelNesting", func(t *testing.T) {
		th := test.Setup(t)

		dag := th.DAG(t, `
steps:
  - name: call_child
    action: dag.run
    with:
      dag: child
      params: "MSG=hello"

---

name: child
params: "MSG=default"
steps:
  - name: echo_msg
    run: echo "${MSG}_from_child"
    output: RESULT
`)

		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
	})

	t.Run("ThreeLevelNesting", func(t *testing.T) {
		// 3-level nesting: root -> middle -> leaf
		th := test.Setup(t)

		dag := th.DAG(t, `
steps:
  - name: call_middle
    action: dag.run
    with:
      dag: middle
      params: "MSG=hello"

---

name: middle
params: "MSG=default"
steps:
  - name: call_leaf
    action: dag.run
    with:
      dag: leaf
      params: "MSG=${MSG}_middle"

---

name: leaf
params: "MSG=default"
steps:
  - name: echo_msg
    run: echo "${MSG}_from_leaf"
    output: RESULT
`)

		dag.Agent().RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
	})

	t.Run("ThreeLevelNestingWithOutputPassing", func(t *testing.T) {
		if runtime.GOOS == "windows" && raceEnabled() {
			t.Skip("Skipping nested inline subdag output passing on Windows race runs")
		}

		// middle-dag calls leaf-dag with parameter passing
		th := test.Setup(t)

		testDAG := th.DAG(t, `
steps:
  - name: run-middle-dag
    action: dag.run
    with:
      dag: middle-dag
      params: "ROOT_PARAM=FromRoot"

---

name: middle-dag
params:
  - ROOT_PARAM
steps:
  - run: echo "Received ${ROOT_PARAM}"
    output: MIDDLE_OUTPUT

  - name: run-leaf-dag
    action: dag.run
    with:
      dag: leaf-dag
      params: "MIDDLE_PARAM=${MIDDLE_OUTPUT} LEAF_PARAM=FromMiddle"

---

name: leaf-dag
params:
  - MIDDLE_PARAM
  - LEAF_PARAM
steps:
  - run: |
      echo "Middle: ${MIDDLE_PARAM}, Leaf: ${LEAF_PARAM}"
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 1)
		require.Equal(t, "run-middle-dag", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
		require.Len(t, dagRunStatus.Nodes[0].SubRuns, 1, "middle-dag should have one sub-run")
	})

	t.Run("ParallelExecution", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
steps:
  - name: parallel-tasks
    action: dag.run
    with:
      dag: worker-dag
    parallel:
      items:
        - TASK_ID=1 TASK_NAME=alpha
        - TASK_ID=2 TASK_NAME=beta
        - TASK_ID=3 TASK_NAME=gamma
      max_concurrent: 2

---

name: worker-dag
params:
  - TASK_ID
  - TASK_NAME
steps:
  - run: echo "Starting task ${TASK_ID} - ${TASK_NAME}"
  - run: echo "Processing ${TASK_NAME} with ID ${TASK_ID}"
  - run: echo "Completed ${TASK_NAME}"
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 1)
		require.Equal(t, "parallel-tasks", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
	})

	t.Run("ConditionalExecution", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
env:
  - ENVIRONMENT: production
steps:
  - name: check-env
    run: echo "${ENVIRONMENT}"
    output: ENV_TYPE

  - name: run-prod-dag
    action: dag.run
    with:
      dag: production-dag
    depends: check-env
    preconditions:
      - condition: "${ENV_TYPE}"
        expected: "production"

  - name: run-dev-dag
    action: dag.run
    with:
      dag: development-dag
    depends: check-env
    preconditions:
      - condition: "${ENV_TYPE}"
        expected: "development"

---

name: production-dag
steps:
  - run: echo "Deploying to production"
  - run: echo "Verifying production deployment"
    depends: cmd_1

---

name: development-dag
steps:
  - run: echo "Building for development"
  - run: echo "Running development tests"
    depends: cmd_1
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 3)
		require.Equal(t, "check-env", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
		require.Equal(t, "run-prod-dag", dagRunStatus.Nodes[1].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[1].Status)
		require.Equal(t, "run-dev-dag", dagRunStatus.Nodes[2].Step.Name)
		require.Equal(t, core.NodeSkipped, dagRunStatus.Nodes[2].Status)
	})

	t.Run("OutputPassingBetweenDAGs", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
steps:
  - name: generate-data
    action: dag.run
    with:
      dag: generator-dag
    output: GEN_OUTPUT

  - name: process-data
    action: dag.run
    with:
      dag: processor-dag
      params: "INPUT_DATA=${GEN_OUTPUT.outputs.DATA}"
    depends: generate-data

---

name: generator-dag
steps:
  - run: echo "test-value-42"
    output: DATA

---

name: processor-dag
params:
  - INPUT_DATA
steps:
  - run: echo "Processing ${INPUT_DATA}"
    output: RESULT

  - run: |
      echo "Validated: ${RESULT}"
    depends: cmd_1
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 2)
		require.Equal(t, "generate-data", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
		require.Equal(t, "process-data", dagRunStatus.Nodes[1].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[1].Status)
	})

	t.Run("NonExistentReference", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
steps:
  - name: run-missing-dag
    action: dag.run
    with:
      dag: non-existent-dag

---

name: some-other-dag
steps:
  - run: echo "test"
`)

		agent := testDAG.Agent()
		err := agent.Run(agent.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "non-existent-dag")

		testDAG.AssertLatestStatus(t, core.Failed)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 1)
		require.Equal(t, "run-missing-dag", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeFailed, dagRunStatus.Nodes[0].Status)
	})

	t.Run("ComplexDependencies", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
type: graph
steps:
  - name: setup
    run: echo "Setting up"
    output: SETUP_STATUS

  - name: task1
    action: dag.run
    with:
      dag: task-dag
      params: "TASK_NAME=Task1 SETUP=${SETUP_STATUS}"
    output: TASK1_RESULT
    depends: [setup]

  - name: task2
    action: dag.run
    with:
      dag: task-dag
      params: "TASK_NAME=Task2 SETUP=${SETUP_STATUS}"
    output: TASK2_RESULT
    depends: [setup]

  - name: combine
    run: |
      echo "Combining ${TASK1_RESULT.outputs.RESULT} and ${TASK2_RESULT.outputs.RESULT}"
    depends:
      - task1
      - task2

---

name: task-dag
params:
  - TASK_NAME
  - SETUP
steps:
  - run: echo "${TASK_NAME} processing with ${SETUP}"
    output: RESULT
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
		require.NoError(t, err)

		require.Len(t, dagRunStatus.Nodes, 4)
		require.Equal(t, "setup", dagRunStatus.Nodes[0].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
		require.Equal(t, "task1", dagRunStatus.Nodes[1].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[1].Status)
		require.Equal(t, "task2", dagRunStatus.Nodes[2].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[2].Status)
		require.Equal(t, "combine", dagRunStatus.Nodes[3].Step.Name)
		require.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[3].Status)

		logContent, err := os.ReadFile(dagRunStatus.Nodes[3].Stdout)
		require.NoError(t, err)
		require.Contains(t, string(logContent), "Combining")
		require.Contains(t, string(logContent), "Task1 processing with Setting up")
		require.Contains(t, string(logContent), "Task2 processing with Setting up")
	})

	t.Run("PartialSuccessParallel", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
steps:
  - name: parallel-tasks
    action: dag.run
    with:
      dag: worker-dag
    parallel:
      items:
        - TASK_ID=1 TASK_NAME=alpha
---

name: worker-dag
params:
  - TASK_ID
  - TASK_NAME
steps:
  - run: exit 1
    continue_on:
      failure: true

  - run: exit 0
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.PartiallySucceeded)
	})

	t.Run("PartialSuccessSubDAG", func(t *testing.T) {
		th := test.Setup(t)

		testDAG := th.DAG(t, `
steps:
  - name: parallel-tasks
    action: dag.run
    with:
      dag: worker-dag
---

name: worker-dag
params:
  - TASK_ID
  - TASK_NAME
steps:
  - run: exit 1
    continue_on:
      failure: true

  - run: exit 0
`)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.PartiallySucceeded)
	})
}

func TestExternalSubDAG(t *testing.T) {
	t.Run("BasicOutputCapture", func(t *testing.T) {
		th := test.SetupCommand(t)

		th.CreateDAGFile(t, "parent_basic.yaml", `
steps:
  - name: call_sub
    action: dag.run
    with:
      dag: sub_basic
    output: SUB_OUTPUT
`)

		th.CreateDAGFile(t, "sub_basic.yaml", `
steps:
  - name: basic_step
    run: echo "hello_from_sub"
    output: STEP_OUTPUT
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		args := []string{"start", "--run-id", dagRunID, "parent_basic"}
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{"DAG run finished"},
		})

		ctx := context.Background()
		ref := exec.NewDAGRunRef("parent_basic", dagRunID)
		parentAttempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
		require.NoError(t, err)

		parentStatus, err := parentAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), parentStatus.Status.String())

		subNode := parentStatus.Nodes[0]
		require.Equal(t, core.NodeSucceeded.String(), subNode.Status.String())

		subAttempt, err := th.DAGRunStore.FindSubAttempt(ctx, ref, subNode.SubRuns[0].DAGRunID)
		require.NoError(t, err)

		subStatus, err := subAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), subStatus.Status.String())

		basicStep := subStatus.Nodes[0]
		require.Equal(t, core.NodeSucceeded.String(), basicStep.Status.String())

		require.NotNil(t, basicStep.OutputVariables, "OutputVariables should not be nil")
		variables := basicStep.OutputVariables.Variables()
		require.Contains(t, variables, "STEP_OUTPUT")
		require.Contains(t, variables["STEP_OUTPUT"], "hello_from_sub")
	})

	t.Run("RetrySubDAGRun", func(t *testing.T) {
		th := test.SetupCommand(t)

		th.CreateDAGFile(t, "parent.yaml", `
steps:
  - name: parent
    action: dag.run
    with:
      dag: sub_1
      params: "PARAM=FOO"
`)

		th.CreateDAGFile(t, "sub_1.yaml", `
params: "PARAM=BAR"
steps:
  - name: sub_2
    action: dag.run
    with:
      dag: sub_2
      params: "PARAM=$PARAM"
`)

		th.CreateDAGFile(t, "sub_2.yaml", `
params: "PARAM=BAZ"
steps:
  - name: sub_2
    run: echo "Hello, $PARAM"
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		args := []string{"start", "--run-id", dagRunID, "parent"}
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{"DAG run finished"},
		})

		// Update the sub_2 status to "failed" to simulate a retry
		ctx := context.Background()
		ref := exec.NewDAGRunRef("parent", dagRunID)
		parentAttempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
		require.NoError(t, err)

		updateStatus := func(rec exec.DAGRunAttempt, dagRunStatus *exec.DAGRunStatus) {
			err = rec.Open(ctx)
			require.NoError(t, err)
			err = rec.Write(ctx, *dagRunStatus)
			require.NoError(t, err)
			err = rec.Close(ctx)
			require.NoError(t, err)
		}

		// Find and update sub_1 node status
		parentStatus, err := parentAttempt.ReadStatus(ctx)
		require.NoError(t, err)

		sub1Node := parentStatus.Nodes[0]
		sub1Node.Status = core.NodeFailed
		updateStatus(parentAttempt, parentStatus)

		// Find and update sub_1 dag-run status
		sub1Attempt, err := th.DAGRunStore.FindSubAttempt(ctx, ref, sub1Node.SubRuns[0].DAGRunID)
		require.NoError(t, err)

		sub1Status, err := sub1Attempt.ReadStatus(ctx)
		require.NoError(t, err)

		// Find and update sub_2 node status
		sub2Node := sub1Status.Nodes[0]
		sub2Node.Status = core.NodeFailed
		updateStatus(sub1Attempt, sub1Status)

		// Find and update sub_2 dag-run status
		sub2Attempt, err := th.DAGRunStore.FindSubAttempt(ctx, ref, sub2Node.SubRuns[0].DAGRunID)
		require.NoError(t, err)

		sub2Status, err := sub2Attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), sub2Status.Status.String())

		// Update the step in sub_2 to "failed"
		sub2Status.Nodes[0].Status = core.NodeFailed
		updateStatus(sub2Attempt, sub2Status)

		// Verify sub_2 is now "failed"
		sub2Status, err = sub2Attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeFailed.String(), sub2Status.Nodes[0].Status.String())

		// Retry the DAG
		args = []string{"retry", "--run-id", dagRunID, "parent"}
		th.RunCommand(t, cmd.Retry(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{"DAG run finished"},
		})

		// Check if the sub_2 status is now "success"
		sub2Attempt, err = th.DAGRunStore.FindSubAttempt(ctx, ref, sub2Node.SubRuns[0].DAGRunID)
		require.NoError(t, err)
		sub2Status, err = sub2Attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), sub2Status.Nodes[0].Status.String())
		require.Equal(t, "parent", sub2Status.Root.Name)
		require.Equal(t, dagRunID, sub2Status.Root.ID)
	})

	t.Run("RetryPolicyWithOutputCapture", func(t *testing.T) {
		th := test.SetupCommand(t)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		counterFile := filepath.Join(t.TempDir(), "retry_counter_"+dagRunID)
		defer func() { _ = os.Remove(counterFile) }()

		th.CreateDAGFile(t, "parent_retry.yaml", `
steps:
  - name: call_sub
    action: dag.run
    with:
      dag: sub_retry
    output: SUB_OUTPUT
`)

		th.CreateDAGFile(t, "sub_retry.yaml", fmt.Sprintf(`
steps:
  - name: retry_step
    run: |
%s
    output: STEP_OUTPUT
    retry_policy:
      limit: 2
      interval_sec: 1
`, indentCommandBlock(retryOutputStepScript(counterFile), 6)))

		args := []string{"start", "--run-id", dagRunID, "parent_retry"}
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{"DAG run finished"},
		})

		ctx := context.Background()
		ref := exec.NewDAGRunRef("parent_retry", dagRunID)
		parentAttempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
		require.NoError(t, err)

		parentStatus, err := parentAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), parentStatus.Status.String())

		subNode := parentStatus.Nodes[0]
		require.Equal(t, core.NodeSucceeded.String(), subNode.Status.String())

		subAttempt, err := th.DAGRunStore.FindSubAttempt(ctx, ref, subNode.SubRuns[0].DAGRunID)
		require.NoError(t, err)

		subStatus, err := subAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), subStatus.Status.String())

		retryStep := subStatus.Nodes[0]
		require.Equal(t, core.NodeSucceeded.String(), retryStep.Status.String())
		require.NotNil(t, retryStep.OutputVariables)

		variables := retryStep.OutputVariables.Variables()
		require.Contains(t, variables, "STEP_OUTPUT")
		require.Contains(t, variables["STEP_OUTPUT"], "output_attempt_2_success")
	})
}

func TestRetryPolicy(t *testing.T) {
	t.Run("BasicOutputCapture", func(t *testing.T) {
		th := test.SetupCommand(t)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		counterFile := filepath.Join(t.TempDir(), "retry_counter_basic_"+dagRunID)
		defer func() { _ = os.Remove(counterFile) }()

		th.CreateDAGFile(t, "basic_retry.yaml", fmt.Sprintf(`
steps:
  - name: retry_step
    run: |
%s
    output: STEP_OUTPUT
    retry_policy:
      limit: 2
      interval_sec: 1
`, indentCommandBlock(retryOutputStepScript(counterFile), 6)))

		args := []string{"start", "--run-id", dagRunID, "basic_retry"}
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{"DAG run finished"},
		})

		ctx := context.Background()
		ref := exec.NewDAGRunRef("basic_retry", dagRunID)
		attempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
		require.NoError(t, err)

		dagRunStatus, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), dagRunStatus.Status.String())

		retryStep := dagRunStatus.Nodes[0]
		require.Equal(t, core.NodeSucceeded.String(), retryStep.Status.String())
		require.NotNil(t, retryStep.OutputVariables)

		variables := retryStep.OutputVariables.Variables()
		require.Contains(t, variables, "STEP_OUTPUT")
		require.Contains(t, variables["STEP_OUTPUT"], "output_attempt_2_success")
	})

	t.Run("NoRetryOutputCapture", func(t *testing.T) {
		th := test.SetupCommand(t)

		th.CreateDAGFile(t, "no_retry.yaml", `
steps:
  - name: success_step
    run: echo "output_first_attempt_success"
    output: STEP_OUTPUT
`)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		args := []string{"start", "--run-id", dagRunID, "no_retry"}
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        args,
			ExpectedOut: []string{"DAG run finished"},
		})

		ctx := context.Background()
		ref := exec.NewDAGRunRef("no_retry", dagRunID)
		attempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
		require.NoError(t, err)

		dagRunStatus, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.NodeSucceeded.String(), dagRunStatus.Status.String())

		successStep := dagRunStatus.Nodes[0]
		require.Equal(t, core.NodeSucceeded.String(), successStep.Status.String())
		require.NotNil(t, successStep.OutputVariables)

		variables := successStep.OutputVariables.Variables()
		require.Contains(t, variables, "STEP_OUTPUT")
		require.Contains(t, variables["STEP_OUTPUT"], "output_first_attempt_success")
	})
}
