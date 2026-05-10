// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

type parallelExecutionItemSourceCase struct {
	dag               string
	expectedNodes     int
	parallelNodeIndex int
	expectedChildren  int
	verify            func(*testing.T, *exec.DAGRunStatus, *exec.Node)
}

func yamlParallelItems(key string, items []string) string {
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "        - %s: %q\n", key, item)
	}
	return b.String()
}

func jsonConfigItems(items []struct {
	region string
	bucket string
}) string {
	var parts []string
	for _, item := range items {
		parts = append(parts, fmt.Sprintf(`{"region":"%s","bucket":"%s"}`, item.region, item.bucket))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func yamlEchoLines(lines []string) string {
	var b strings.Builder
	for _, line := range lines {
		fmt.Fprintf(&b, "      echo '%s'\n", line)
	}
	return b.String()
}

func parallelChildEchoDAG() string {
	if runtime.GOOS == "windows" {
		return `---
name: child-echo
params:
  - ITEM: "default"
steps:
  - command: |
      $item = "${ITEM}"
      if ([string]::IsNullOrEmpty($item) -and $args.Length -gt 0) {
        $item = "$($args[0])"
      }
      Write-Output ("Processing {0}" -f $item)
    output: PROCESSED_ITEM
`
	}

	return `---
name: child-echo
params:
  - ITEM: "default"
steps:
  - command: |
      item="${ITEM:-$1}"
      echo "Processing ${item}"
    output: PROCESSED_ITEM
`
}

func parallelChildProcessDAG() string {
	if runtime.GOOS == "windows" {
		return `---
name: child-process
params:
  - REGION: "us-east-1"
  - VERSION: "1.0.0"
steps:
  - command: |
      Write-Output ("Deploying version {0} to region {1}" -f "${VERSION}", "${REGION}")
    output: DEPLOYMENT_RESULT
`
	}

	return `---
name: child-process
params:
  - REGION: "us-east-1"
  - VERSION: "1.0.0"
steps:
  - command: echo "Deploying version ${VERSION} to region ${REGION}"
    output: DEPLOYMENT_RESULT
`
}

func parallelChildWithOutputDAG() string {
	if runtime.GOOS == "windows" {
		return `---
name: child-with-output
params:
  - ITEM: "default"
steps:
  - command: |
      $item = "${ITEM}"
      if ([string]::IsNullOrEmpty($item) -and $args.Length -gt 0) {
        $item = "$($args[0])"
      }
      Write-Output ("Processing task: {0}" -f $item)
      Write-Output ("TASK_RESULT_{0}" -f $item)
    output: TASK_OUTPUT
  - command: |
      $item = "${ITEM}"
      if ([string]::IsNullOrEmpty($item) -and $args.Length -gt 0) {
        $item = "$($args[0])"
      }
      Write-Output ("Task {0} completed with output {1}" -f $item, "${TASK_OUTPUT}")
`
	}

	return `---
name: child-with-output
params:
  - ITEM: "default"
steps:
  - command: |
      item="${ITEM:-${TASK:-$1}}"
      echo "Processing task: ${item}"
      echo "TASK_RESULT_${item}"
    output: TASK_OUTPUT
  - command: |
      item="${ITEM:-${TASK:-$1}}"
      echo "Task ${item} completed with output ${TASK_OUTPUT}"
`
}

func runParallelExecutionItemSourceCase(t *testing.T, tc parallelExecutionItemSourceCase) {
	t.Helper()

	th := test.Setup(t)
	dag := th.DAG(t, tc.dag)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, tc.expectedNodes)

	require.Greater(t, len(dagStatus.Nodes), tc.parallelNodeIndex, "node index out of range")
	parallelNode := dagStatus.Nodes[tc.parallelNodeIndex]
	require.Equal(t, core.NodeSucceeded, parallelNode.Status)
	require.Len(t, parallelNode.SubRuns, tc.expectedChildren)

	if tc.verify != nil {
		tc.verify(t, &dagStatus, parallelNode)
	}
}

func TestParallelExecution_ItemSources_SimpleItems(t *testing.T) {
	t.Parallel()

	runParallelExecutionItemSourceCase(t, parallelExecutionItemSourceCase{
		dag: `steps:
  - call: child-echo
    parallel:
      items:
        - "item1"
        - "item2"
        - "item3"
      max_concurrent: 3
` + parallelChildEchoDAG(),
		expectedNodes:     1,
		parallelNodeIndex: 0,
		expectedChildren:  3,
	})
}

func TestParallelExecution_ItemSources_ObjectItems(t *testing.T) {
	t.Parallel()

	runParallelExecutionItemSourceCase(t, parallelExecutionItemSourceCase{
		dag: `steps:
  - call: child-process
    parallel:
      items:
        - REGION: us-east-1
          VERSION: "1.0.0"
        - REGION: us-west-2
          VERSION: "1.0.1"
        - REGION: eu-west-1
          VERSION: "1.0.2"
      max_concurrent: 2
` + parallelChildProcessDAG(),
		expectedNodes:     1,
		parallelNodeIndex: 0,
		expectedChildren:  3,
		verify: func(t *testing.T, _ *exec.DAGRunStatus, node *exec.Node) {
			for _, child := range node.SubRuns {
				require.Contains(t, child.Params, `"REGION"`)
				require.Contains(t, child.Params, `"VERSION"`)
			}
		},
	})
}

func TestParallelExecution_ItemSources_VariableReference(t *testing.T) {
	t.Parallel()

	runParallelExecutionItemSourceCase(t, parallelExecutionItemSourceCase{
		dag: `params:
  - ITEMS: '["alpha", "beta", "gamma", "delta"]'
steps:
  - call: child-echo
    parallel: ${ITEMS}
` + parallelChildEchoDAG(),
		expectedNodes:     1,
		parallelNodeIndex: 0,
		expectedChildren:  4,
	})
}

func TestParallelExecution_ItemSources_SpaceSeparated(t *testing.T) {
	t.Parallel()

	runParallelExecutionItemSourceCase(t, parallelExecutionItemSourceCase{
		dag: `env:
  - SERVERS: "server1 server2 server3"
steps:
  - call: child-echo
    parallel: ${SERVERS}
` + parallelChildEchoDAG(),
		expectedNodes:     1,
		parallelNodeIndex: 0,
		expectedChildren:  3,
	})
}

func TestParallelExecution_ItemSources_DirectVariable(t *testing.T) {
	t.Parallel()

	runParallelExecutionItemSourceCase(t, parallelExecutionItemSourceCase{
		dag: `env:
  - ITEMS: '["task1", "task2", "task3"]'
steps:
  - call: child-with-output
    parallel: $ITEMS
  - name: aggregate-results
    command: echo "Completed parallel tasks"
    output: FINAL_RESULT
` + parallelChildWithOutputDAG(),
		expectedNodes:     2,
		parallelNodeIndex: 0,
		expectedChildren:  3,
		verify: func(t *testing.T, dagStatus *exec.DAGRunStatus, _ *exec.Node) {
			require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
			aggregate := dagStatus.Nodes[1]
			require.Equal(t, core.NodeSucceeded, aggregate.Status)
		},
	})
}

func TestParallelExecution_WithOutput(t *testing.T) {
	items := []string{"A", "B", "C"}
	if runtime.GOOS == "windows" {
		items = items[:2]
	}
	dagContent := fmt.Sprintf(`steps:
  - call: child-with-output
    parallel:
      items:
%s
    output: PARALLEL_RESULTS
  - command: |
      echo "Parallel execution results:"
      echo "${PARALLEL_RESULTS}"
    output: FINAL_OUTPUT
`, yamlParallelItems("ITEM", items)) + parallelChildWithOutputDAG()

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, 2)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	parallelNode := dagStatus.Nodes[0]
	require.Equal(t, core.NodeSucceeded, parallelNode.Status)
	require.Len(t, parallelNode.SubRuns, len(items))

	require.NotNil(t, parallelNode.OutputVariables, "no outputs recorded for node %s", parallelNode.Step.Name)
	rawOutput, ok := parallelNode.OutputVariables.Load("PARALLEL_RESULTS")
	require.True(t, ok, "output %q not found", "PARALLEL_RESULTS")
	raw, ok := rawOutput.(string)
	require.True(t, ok, "output %q is not a string", "PARALLEL_RESULTS")
	results := parseParallelResults(t, raw)
	require.Equal(t, len(items), results.Summary.Total)
	require.Equal(t, len(items), results.Summary.Succeeded)
	require.Equal(t, 0, results.Summary.Failed)

	outputs := collectOutputs(results.Outputs, "TASK_OUTPUT")
	require.Len(t, outputs, len(items))
	for _, item := range items {
		expected := "TASK_RESULT_" + item
		found := false
		for _, out := range outputs {
			if strings.Contains(out, expected) {
				found = true
				break
			}
		}
		require.Truef(t, found, "expected parallel output to contain %s", expected)
	}

	require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
	useOutputNode := dagStatus.Nodes[1]
	require.Equal(t, core.NodeSucceeded, useOutputNode.Status)
}

func TestParallelExecution_RetryBackoffDoesNotBlockScheduling(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())

	dag := th.DAG(t, `type: graph
steps:
  - name: process-items
    call: processor
    parallel:
      items:
        - "1"
        - "2"
        - "3"
      max_concurrent: 2

---
name: processor
params:
  - ITEM: ""
steps:
  - name: flaky
    command: exit 1
    retry_policy:
      limit: 1
      interval_sec: 2
`)

	agent := dag.Agent()
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(agent.Context)
	}()

	startedTimeout := 3 * time.Second
	allStartedTimeout := 3 * time.Second
	finalTimeout := 30 * time.Second
	if runtime.GOOS == "windows" {
		startedTimeout = 15 * time.Second
		allStartedTimeout = 20 * time.Second
		finalTimeout = 60 * time.Second
	}

	require.Eventually(t, func() bool {
		status, err := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
		return err == nil && len(status.Nodes) == 1 && len(status.Nodes[0].SubRuns) == 3
	}, startedTimeout, 50*time.Millisecond, "parallel sub-runs were not persisted")

	require.Eventually(t, func() bool {
		status, err := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
		if err != nil || len(status.Nodes) == 0 {
			return false
		}

		rootRun := exec.NewDAGRunRef(status.Name, status.DAGRunID)
		started := 0
		for _, subRun := range status.Nodes[0].SubRuns {
			if _, subErr := dag.DAGRunMgr.FindSubDAGRunStatus(dag.Context, rootRun, subRun.DAGRunID); subErr == nil {
				started++
			}
		}
		return started >= 2
	}, startedTimeout, 50*time.Millisecond, "first two items never started")

	require.Eventually(t, func() bool {
		status, err := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
		if err != nil || len(status.Nodes) == 0 {
			return false
		}

		rootRun := exec.NewDAGRunRef(status.Name, status.DAGRunID)
		started := 0
		for _, subRun := range status.Nodes[0].SubRuns {
			if _, subErr := dag.DAGRunMgr.FindSubDAGRunStatus(dag.Context, rootRun, subRun.DAGRunID); subErr == nil {
				started++
			}
		}
		return started == 3
	}, allStartedTimeout, 50*time.Millisecond, "third item should start before the retry backoff expires")

	select {
	case err := <-errCh:
		require.Error(t, err)
	case <-time.After(finalTimeout):
		t.Fatal("parallel retry run did not exit")
	}
}

func TestParallelExecution_AbortStopsPendingLaunches(t *testing.T) {
	th := test.Setup(t, test.WithBuiltExecutable())
	releaseFile := filepath.Join(t.TempDir(), "parallel-abort.release")
	startedDir := filepath.Join(t.TempDir(), "parallel-abort-started")
	require.NoError(t, os.Mkdir(startedDir, 0700))
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})

	dag := th.DAG(t, fmt.Sprintf(`type: graph
steps:
  - name: process-items
    call: child-slow
    parallel:
      items:
        - "one"
        - "two"
        - "three"
      max_concurrent: 1

---
name: child-slow
params:
  - ITEM: ""
steps:
  - name: hold
    command: |
%s
`, indentTestScript(markParallelItemStartedAndWaitCommand(startedDir, releaseFile), 6)))

	agent := dag.Agent()
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(agent.Context)
	}()

	require.Eventually(t, func() bool {
		return countStartedParallelItems(t, startedDir) >= 1
	}, intgTestTimeout(10*time.Second), 50*time.Millisecond, "expected one sub-run command to start before abort")

	agent.Abort()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("parallel abort run did not exit")
	}

	dag.AssertLatestStatus(t, core.Aborted)

	finalStatus, err := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, finalStatus.Nodes, 1)

	parallelNode := finalStatus.Nodes[0]
	require.Equal(t, core.NodeAborted, parallelNode.Status)
	require.Equal(t, 1, countStartedParallelItems(t, startedDir), "pending sub-runs should not start after abort")
	if runtime.GOOS == "windows" {
		return
	}

	rootRun := exec.NewDAGRunRef(finalStatus.Name, finalStatus.DAGRunID)
	startedRunID := ""
	for _, subRun := range parallelNode.SubRuns {
		if _, subErr := dag.DAGRunMgr.FindSubDAGRunStatus(dag.Context, rootRun, subRun.DAGRunID); subErr != nil {
			continue
		}
		startedRunID = subRun.DAGRunID
	}
	require.NotEmpty(t, startedRunID, "expected one persisted started sub-run")
}

func TestParallelExecution_AbortSuppressesPendingRetry(t *testing.T) {
	markerFile := filepath.Join(t.TempDir(), "parallel-first-attempt.marker")
	childScript := test.JoinLines(
		test.ForOS(
			fmt.Sprintf(": > %s", test.PosixQuote(markerFile)),
			fmt.Sprintf("New-Item -ItemType File -Path %s -Force | Out-Null", test.PowerShellQuote(markerFile)),
		),
		"exit 1",
	)

	th := test.Setup(t, test.WithBuiltExecutable())
	dag := th.DAG(t, fmt.Sprintf(`type: graph
steps:
  - name: process-items
    call: child-flaky
    parallel:
      items:
        - "item1"

---
name: child-flaky
params:
  - ITEM: ""
steps:
  - name: flaky
    command: |
%s
    retry_policy:
      limit: 1
      interval_sec: 60
`, indentTestScript(childScript, 6)))

	agent := dag.Agent()
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(agent.Context)
	}()

	require.Eventually(t, func() bool {
		_, err := os.Stat(markerFile)
		return err == nil
	}, intgTestTimeout(45*time.Second), 50*time.Millisecond, "expected first attempt to create marker file before abort")

	startedRunID := ""
	rootRun := exec.DAGRunRef{}
	require.Eventually(t, func() bool {
		attempt, err := dag.DAGRunStore.LatestAttempt(dag.Context, dag.Name)
		if err != nil {
			return false
		}
		status, err := attempt.ReadStatus(dag.Context)
		if err != nil || status == nil || len(status.Nodes) == 0 {
			return false
		}

		if len(status.Nodes[0].SubRuns) != 1 {
			return false
		}

		rootRun = exec.NewDAGRunRef(status.Name, status.DAGRunID)
		startedRunID = status.Nodes[0].SubRuns[0].DAGRunID
		_, err = dag.DAGRunMgr.FindSubDAGRunStatus(dag.Context, rootRun, startedRunID)
		return err == nil
	}, intgTestTimeout(45*time.Second), 50*time.Millisecond, "expected parent DAG to persist started sub-run before abort")

	agent.Abort()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("parallel retry abort run did not exit")
	}

	dag.AssertLatestStatus(t, core.Aborted)

	finalStatus, err := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, finalStatus.Nodes, 1)
	require.Equal(t, core.NodeAborted, finalStatus.Nodes[0].Status)

	rootRun = exec.NewDAGRunRef(finalStatus.Name, finalStatus.DAGRunID)
	startedRunID = ""
	for _, subRun := range finalStatus.Nodes[0].SubRuns {
		if _, subErr := dag.DAGRunMgr.FindSubDAGRunStatus(dag.Context, rootRun, subRun.DAGRunID); subErr != nil {
			continue
		}
		startedRunID = subRun.DAGRunID
	}
	require.NotEmpty(t, startedRunID, "expected one persisted started sub-run")

	childStatus, err := dag.DAGRunMgr.FindSubDAGRunStatus(dag.Context, rootRun, startedRunID)
	require.NoError(t, err)
	require.Len(t, childStatus.Nodes, 1)
	require.LessOrEqual(t, childStatus.Nodes[0].DoneCount, 1, "retry should not launch after abort")
}

func TestParallelExecution_DeterministicIDs(t *testing.T) {
	dagContent := `steps:
  - call: child-echo
    parallel:
      items:
        - "test1"
        - "test2"
        - "test1"
        - "test3"
        - "test2"
` + parallelChildEchoDAG()

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, 1)
	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	node := dagStatus.Nodes[0]
	require.Equal(t, core.NodeSucceeded, node.Status)
	require.Len(t, node.SubRuns, 3)

	unique := make(map[string]string)
	for _, child := range node.SubRuns {
		unique[child.Params] = child.DAGRunID
	}

	require.Len(t, unique, 3)
	require.Contains(t, unique, "test1")
	require.Contains(t, unique, "test2")
	require.Contains(t, unique, "test3")
}

func TestParallelExecution_PartialFailure(t *testing.T) {
	childScript := `
      if [ "${INPUT}" = "fail" ]; then
        echo "Failing as requested"
        exit 1
      fi
      echo "Processing: ${INPUT}"
`
	if runtime.GOOS == "windows" {
		childScript = `
      if ("${INPUT}" -eq "fail") {
        Write-Output "Failing as requested"
        exit 1
      }
      Write-Output ("Processing: {0}" -f "${INPUT}")
`
	}

	dagContent := fmt.Sprintf(`steps:
  - call: child-conditional-fail
    parallel:
      items:
        - INPUT: "ok1"
        - INPUT: "fail"
        - INPUT: "ok2"
        - INPUT: "fail"
        - INPUT: "ok3"
---
name: child-conditional-fail
params:
  - INPUT: "default"
steps:
  - command: |
%s
`, strings.TrimPrefix(childScript, "\n"))

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.Error(t, err)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, 1)
	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	node := dagStatus.Nodes[0]
	require.Equal(t, core.NodeFailed, node.Status)
	require.Len(t, node.SubRuns, 4)
}

func TestParallelExecution_OutputCaptureWithFailures(t *testing.T) {
	childScript := `
      INPUT="${INPUT}"
      echo "Output for ${INPUT}"
      if [ "${INPUT}" = "fail" ]; then
        exit 1
      fi
`
	if runtime.GOOS == "windows" {
		childScript = `
      Write-Output ("Output for {0}" -f "${INPUT}")
      if ("${INPUT}" -eq "fail") {
        exit 1
      }
`
	}

	dagContent := fmt.Sprintf(`steps:
  - call: child-output-fail
    parallel:
      items:
        - INPUT: "success"
        - INPUT: "fail"
    output: RESULTS
    continue_on:
      failure: true
---
name: child-output-fail
params:
  - INPUT: "default"
steps:
  - command: |
%s
    output: RESULT
`, strings.TrimPrefix(childScript, "\n"))

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.Error(t, err)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	node := dagStatus.Nodes[0]
	require.Equal(t, core.NodeFailed, node.Status)

	require.NotNil(t, node.OutputVariables, "no outputs recorded for node %s", node.Step.Name)
	rawOutput, ok := node.OutputVariables.Load("RESULTS")
	require.True(t, ok, "output %q not found", "RESULTS")
	raw, ok := rawOutput.(string)
	require.True(t, ok, "output %q is not a string", "RESULTS")
	results := parseParallelResults(t, raw)
	require.Equal(t, 2, results.Summary.Total)
	require.Equal(t, 1, results.Summary.Succeeded)
	require.Equal(t, 1, results.Summary.Failed)

	outputs := collectOutputs(results.Outputs, "RESULT")
	require.Len(t, outputs, 1)
	require.Contains(t, outputs[0], "Output for success")
	require.NotContains(t, outputs[0], "Output for fail")
}

func TestParallelExecution_OutputCaptureWithRetry(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "test_retry_counter.txt")
	t.Cleanup(func() { _ = os.Remove(counterFile) })
	childScript := retryOutputStepScript(counterFile)

	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - call: child-retry-simple
    parallel:
      items:
        - "item1"
    output: RESULTS
    retry_policy:
      limit: 1
      interval_sec: 0
---
name: child-retry-simple
steps:
  - command: |
%s
    output: OUTPUT
`, indentCommandBlock(childScript, 6)))

	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	node := dagStatus.Nodes[0]
	require.Equal(t, core.NodeSucceeded, node.Status)

	require.NotNil(t, node.OutputVariables, "no outputs recorded for node %s", node.Step.Name)
	rawRaw, ok := node.OutputVariables.Load("RESULTS")
	require.True(t, ok, "output %q not found", "RESULTS")
	raw, ok := rawRaw.(string)
	require.True(t, ok, "output %q is not a string", "RESULTS")
	results := parseParallelResults(t, raw)
	require.Equal(t, 1, results.Summary.Total)
	require.Equal(t, 1, results.Summary.Succeeded)
	require.Equal(t, 0, results.Summary.Failed)

	outputs := collectOutputs(results.Outputs, "OUTPUT")
	require.Len(t, outputs, 1)
	require.Contains(t, outputs[0], "output_attempt_2_success")
	require.NotContains(t, outputs[0], "output_attempt_1")
	require.NotContains(t, raw, "output_attempt_1")
}

func TestParallelExecution_MinimalRetry(t *testing.T) {
	const dagContent = `steps:
  - call: child-fail
    parallel:
      items:
        - "item1"
    retry_policy:
      limit: 1
      interval_sec: 1
    output: RESULTS
---
name: child-fail
steps:
  - exit 1
`

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.Error(t, err)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	node := dagStatus.Nodes[0]
	require.Equal(t, core.NodeFailed, node.Status)
	require.Equal(t, 1, node.RetryCount)
}

func TestParallelExecution_RetryAndContinueOn(t *testing.T) {
	const dagContent = `steps:
  - call: child-fail-both
    parallel:
      items:
        - "item1"
    retry_policy:
      limit: 1
      interval_sec: 1
    continue_on:
      failure: true
    output: RESULTS
  - echo "This should run"
---
name: child-fail-both
steps:
  - exit 1
`

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.Error(t, err)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, 2)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	parallelNode := dagStatus.Nodes[0]
	require.Equal(t, core.NodeFailed, parallelNode.Status)
	require.Equal(t, 1, parallelNode.RetryCount)

	require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
	nextNode := dagStatus.Nodes[1]
	require.Equal(t, core.NodeSucceeded, nextNode.Status)

	require.NotNil(t, parallelNode.OutputVariables, "no outputs recorded for node %s", parallelNode.Step.Name)
	_, ok := parallelNode.OutputVariables.Load("RESULTS")
	require.True(t, ok, "output %q not found", "RESULTS")
}

func TestParallelExecution_OutputsArray(t *testing.T) {
	dagContent := `steps:
  - call: child-with-output
    parallel:
      items:
        - ITEM: "task1"
        - ITEM: "task2"
        - ITEM: "task3"
    output: RESULTS
  - command: |
      echo "First output: ${RESULTS.outputs[0].TASK_OUTPUT}"
    output: FIRST_OUTPUT
  - command: |
      echo "Output 0: ${RESULTS.outputs[0].TASK_OUTPUT}"
      echo "Output 1: ${RESULTS.outputs[1].TASK_OUTPUT}"
      echo "Output 2: ${RESULTS.outputs[2].TASK_OUTPUT}"
    output: ALL_OUTPUTS
` + parallelChildWithOutputDAG()

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	parallelNode := dagStatus.Nodes[0]
	require.Equal(t, core.NodeSucceeded, parallelNode.Status)
	require.Len(t, parallelNode.SubRuns, 3)

	require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
	firstNode := dagStatus.Nodes[1]
	require.NotNil(t, firstNode.OutputVariables, "no outputs recorded for node %s", firstNode.Step.Name)
	firstOutputRaw, ok := firstNode.OutputVariables.Load("FIRST_OUTPUT")
	require.True(t, ok, "output %q not found", "FIRST_OUTPUT")
	firstOutput, ok := firstOutputRaw.(string)
	require.True(t, ok, "output %q is not a string", "FIRST_OUTPUT")
	require.Contains(t, firstOutput, "First output:")

	require.Greater(t, len(dagStatus.Nodes), 2, "node index out of range")
	allNode := dagStatus.Nodes[2]
	require.NotNil(t, allNode.OutputVariables, "no outputs recorded for node %s", allNode.Step.Name)
	allOutputsRaw, ok := allNode.OutputVariables.Load("ALL_OUTPUTS")
	require.True(t, ok, "output %q not found", "ALL_OUTPUTS")
	allOutputs, ok := allOutputsRaw.(string)
	require.True(t, ok, "output %q is not a string", "ALL_OUTPUTS")
	require.Contains(t, allOutputs, "TASK_RESULT_task1")
	require.Contains(t, allOutputs, "TASK_RESULT_task2")
	require.Contains(t, allOutputs, "TASK_RESULT_task3")
}

func TestParallelExecution_ExceedsMaxLimit(t *testing.T) {
	items := make([]string, 1001)
	for i := range items {
		items[i] = fmt.Sprintf("        - \"item%d\"", i)
	}

	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - call: child-echo
    parallel:
      items:
%s
`, strings.Join(items, "\n"))+parallelChildEchoDAG())

	agent := dag.Agent()
	err := agent.Run(agent.Context)

	_, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parallel execution exceeds maximum limit")
}

func TestParallelExecution_ExactlyMaxLimit(t *testing.T) {
	helper := test.Setup(t)

	items := make([]string, 1000)
	for i := range items {
		items[i] = fmt.Sprintf("        - \"item%d\"", i)
	}

	dag := helper.DAG(t, fmt.Sprintf(`steps:
  - call: child-echo
    parallel:
      items:
%s
      max_concurrent: 10
`, strings.Join(items, "\n"))+parallelChildEchoDAG())

	agent := dag.Agent()
	errChan := make(chan error, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		agent.Context = ctx
		errChan <- agent.Run(agent.Context)
	}()

	select {
	case err := <-errChan:
		require.NoError(t, err, "DAG should not fail with exactly 1000 items")
	case <-time.After(time.Second):
		cancel()
		<-errChan
	}
}

func TestParallelExecution_ObjectItemProperties(t *testing.T) {
	configItems := []struct {
		region string
		bucket string
	}{
		{region: "us-east-1", bucket: "data-us"},
		{region: "eu-west-1", bucket: "data-eu"},
		{region: "ap-south-1", bucket: "data-ap"},
	}
	if runtime.GOOS == "windows" {
		configItems = configItems[:2]
	}
	childSpec := `steps:
  - script: |
      echo "Syncing data from region: ${REGION}"
      echo "Using bucket: ${BUCKET}"
      echo "Sync completed for ${BUCKET} in ${REGION}"
    output: SYNC_RESULT
`
	if runtime.GOOS == "windows" {
		childSpec = `steps:
  - script: |
      Write-Output ("Syncing data from region: " + $env:REGION)
      Write-Output ("Using bucket: " + $env:BUCKET)
      Write-Output ("Sync completed for " + $env:BUCKET + " in " + $env:REGION)
    output: SYNC_RESULT
`
	}

	dagContent := fmt.Sprintf(`steps:
  - command: |
      echo '%s'
    output: CONFIGS

  - call: sync-data
    parallel:
      items: ${CONFIGS}
      max_concurrent: 2
    params:
      - REGION: ${ITEM.region}
      - BUCKET: ${ITEM.bucket}
    output: RESULTS

---
name: sync-data
params:
  - REGION: ""
  - BUCKET: ""
`, jsonConfigItems(configItems)) + childSpec

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, 2)

	require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
	syncNode := dagStatus.Nodes[1]
	require.Equal(t, core.NodeSucceeded, syncNode.Status)
	require.Len(t, syncNode.SubRuns, len(configItems))

	require.NotNil(t, syncNode.OutputVariables, "no outputs recorded for node %s", syncNode.Step.Name)
	rawRaw, ok := syncNode.OutputVariables.Load("RESULTS")
	require.True(t, ok, "output %q not found", "RESULTS")
	raw, ok := rawRaw.(string)
	require.True(t, ok, "output %q is not a string", "RESULTS")
	results := parseParallelResults(t, raw)
	require.Equal(t, len(configItems), results.Summary.Total)
	require.Equal(t, len(configItems), results.Summary.Succeeded)
	require.Equal(t, 0, results.Summary.Failed)

	for _, item := range configItems {
		require.Contains(t, raw, "Syncing data from region: "+item.region)
		require.Contains(t, raw, "Using bucket: "+item.bucket)
		require.Contains(t, raw, "Sync completed for "+item.bucket+" in "+item.region)
	}
}

func TestParallelExecution_DynamicFileDiscovery(t *testing.T) {
	helper := test.Setup(t)

	testDataDir := filepath.Join(helper.Config.Paths.DAGsDir, "test-data")
	require.NoError(t, os.MkdirAll(testDataDir, 0755))
	t.Cleanup(func() { _ = os.RemoveAll(testDataDir) })

	testFiles := []string{"data1.csv", "data2.csv", "data3.csv"}
	for _, file := range testFiles {
		content := fmt.Sprintf("id,name\n1,%s\n", file)
		require.NoError(t, os.WriteFile(filepath.Join(testDataDir, file), []byte(content), 0644))
	}

	processFileScript := `
      FILE="${ITEM}"
      echo "Processing file: ${FILE}"
      if [ -f "${FILE}" ]; then
        LINE_COUNT=$(wc -l < "${FILE}")
        echo "File has ${LINE_COUNT} lines"
      else
        echo "ERROR: File not found"
        exit 1
      fi
`
	discoverCommand := fmt.Sprintf("find %s -name '*.csv' -type f", test.PosixQuote(test.ShellPath(testDataDir)))
	if runtime.GOOS == "windows" {
		processFileScript = `
      $file = "${ITEM}"
      Write-Output ("Processing file: {0}" -f $file)
      if (Test-Path -LiteralPath $file) {
        $lineCount = (Get-Content -Path $file | Measure-Object -Line).Lines
        Write-Output ("File has {0} lines" -f $lineCount)
      } else {
        Write-Output "ERROR: File not found"
        exit 1
      }
`
		discoverCommand = fmt.Sprintf(
			"Get-ChildItem -Path %s -Filter '*.csv' -File -Recurse | Select-Object -ExpandProperty FullName",
			test.PowerShellQuote(testDataDir),
		)
	}

	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "process-file", fmt.Appendf(nil, `
params:
  - ITEM: ""
steps:
  - script: |
%s
    output: PROCESS_RESULT
`, strings.TrimPrefix(processFileScript, "\n")))

	dag := helper.DAG(t, fmt.Sprintf(`
steps:
  - command: %s
    output: FILES

  - call: process-file
    parallel: ${FILES}
    params:
      - ITEM: ${ITEM}
    output: RESULTS
`, discoverCommand))

	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	getFiles := dagStatus.Nodes[0]
	require.NotNil(t, getFiles.OutputVariables, "no outputs recorded for node %s", getFiles.Step.Name)
	filesOutputRaw, ok := getFiles.OutputVariables.Load("FILES")
	require.True(t, ok, "output %q not found", "FILES")
	filesOutput, ok := filesOutputRaw.(string)
	require.True(t, ok, "output %q is not a string", "FILES")
	for _, file := range testFiles {
		require.Contains(t, filesOutput, file)
	}

	require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
	processFiles := dagStatus.Nodes[1]
	require.Equal(t, core.NodeSucceeded, processFiles.Status)
	require.Len(t, processFiles.SubRuns, 3)

	require.NotNil(t, processFiles.OutputVariables, "no outputs recorded for node %s", processFiles.Step.Name)
	rawRaw, ok := processFiles.OutputVariables.Load("RESULTS")
	require.True(t, ok, "output %q not found", "RESULTS")
	raw, ok := rawRaw.(string)
	require.True(t, ok, "output %q is not a string", "RESULTS")
	results := parseParallelResults(t, raw)
	require.Equal(t, 3, results.Summary.Total)
	require.Equal(t, 3, results.Summary.Succeeded)
	require.Equal(t, 0, results.Summary.Failed)

	require.Regexp(t, `File has\s+2 lines`, raw)
	require.Contains(t, raw, "Processing file:")
}

func TestParallelExecution_StaticObjectItems(t *testing.T) {
	dagContent := `steps:
  - call: deploy-service
    parallel:
      max_concurrent: 3
      items:
        - name: web-service
          port: 8080
          replicas: 3
        - name: api-service
          port: 8081
          replicas: 2
        - name: worker-service
          port: 8082
          replicas: 5
    params:
      - SERVICE_NAME: ${ITEM.name}
      - PORT: ${ITEM.port}
      - REPLICAS: ${ITEM.replicas}
    continue_on:
      failure: true
    output: DEPLOYMENT_RESULTS
---
name: deploy-service
params:
  - SERVICE_NAME: ""
  - PORT: ""
  - REPLICAS: ""
steps:
  - script: |
      echo "Validating deployment parameters..."
      if [ -z "${SERVICE_NAME}" ] || [ -z "${PORT}" ] || [ -z "${REPLICAS}" ]; then
        echo "ERROR: Missing required parameters"
        exit 1
      fi
      echo "Service: ${SERVICE_NAME}"
      echo "Port: ${PORT}"
      echo "Replicas: ${REPLICAS}"
    output: VALIDATE_RESULT
  - script: |
      echo "Deploying ${SERVICE_NAME}..."
      echo "  - Binding to port ${PORT}"
      echo "  - Scaling to ${REPLICAS} replicas"
      if [ "${SERVICE_NAME}" = "api-service" ]; then
        echo "ERROR: Failed to deploy ${SERVICE_NAME} - port ${PORT} already in use"
        exit 1
      fi
      echo "Successfully deployed ${SERVICE_NAME}"
    output: DEPLOY_RESULT
`
	if runtime.GOOS == "windows" {
		dagContent = `steps:
  - call: deploy-service
    parallel:
      max_concurrent: 3
      items:
        - name: web-service
          port: 8080
          replicas: 3
        - name: api-service
          port: 8081
          replicas: 2
        - name: worker-service
          port: 8082
          replicas: 5
    params:
      - SERVICE_NAME: ${ITEM.name}
      - PORT: ${ITEM.port}
      - REPLICAS: ${ITEM.replicas}
    continue_on:
      failure: true
    output: DEPLOYMENT_RESULTS
---
name: deploy-service
params:
  - SERVICE_NAME: ""
  - PORT: ""
  - REPLICAS: ""
steps:
  - script: |
      Write-Output "Validating deployment parameters..."
      if ([string]::IsNullOrEmpty("${SERVICE_NAME}") -or [string]::IsNullOrEmpty("${PORT}") -or [string]::IsNullOrEmpty("${REPLICAS}")) {
        Write-Output "ERROR: Missing required parameters"
        exit 1
      }
      Write-Output ("Service: {0}" -f "${SERVICE_NAME}")
      Write-Output ("Port: {0}" -f "${PORT}")
      Write-Output ("Replicas: {0}" -f "${REPLICAS}")
    output: VALIDATE_RESULT
  - script: |
      Write-Output ("Deploying {0}..." -f "${SERVICE_NAME}")
      Write-Output ("  - Binding to port {0}" -f "${PORT}")
      Write-Output ("  - Scaling to {0} replicas" -f "${REPLICAS}")
      if ("${SERVICE_NAME}" -eq "api-service") {
        Write-Output ("ERROR: Failed to deploy {0} - port {1} already in use" -f "${SERVICE_NAME}", "${PORT}")
        exit 1
      }
      Write-Output ("Successfully deployed {0}" -f "${SERVICE_NAME}")
    output: DEPLOY_RESULT
`
	}

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.Error(t, err)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Greater(t, len(dagStatus.Nodes), 0, "node index out of range")
	node := dagStatus.Nodes[0]
	require.Equal(t, core.NodeFailed, node.Status)
	require.Len(t, node.SubRuns, 3)

	require.NotNil(t, node.OutputVariables, "no outputs recorded for node %s", node.Step.Name)
	rawRaw, ok := node.OutputVariables.Load("DEPLOYMENT_RESULTS")
	require.True(t, ok, "output %q not found", "DEPLOYMENT_RESULTS")
	raw, ok := rawRaw.(string)
	require.True(t, ok, "output %q is not a string", "DEPLOYMENT_RESULTS")
	results := parseParallelResults(t, raw)
	require.Equal(t, 3, results.Summary.Total)
	require.Equal(t, 2, results.Summary.Succeeded)
	require.Equal(t, 1, results.Summary.Failed)

	require.Contains(t, raw, "Service: web-service")
	require.Contains(t, raw, "Successfully deployed worker-service")
	require.NotContains(t, raw, "Successfully deployed api-service")
}

// TestIssue1274_ParallelJSONSingleItem tests that parallel execution
// correctly handles a single JSON item from output (should dispatch 1 job)
func TestIssue1274_ParallelJSONSingleItem(t *testing.T) {
	const dagContent = `steps:
  - command: |
      echo '{"file": "params.txt", "config": "env"}'
    output: jsonList

  - call: issue-1274-worker
    parallel:
      items: ${jsonList}
      max_concurrent: 1
    params:
      aJson: ${ITEM}
    continue_on:
      skipped: true

---
name: issue-1274-worker
params:
  aJson: ""
steps:
  - name: Process JSON item
    command: echo "Processing file=${aJson.file} config=${aJson.config}"
`

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, 2)

	require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
	parallelNode := dagStatus.Nodes[1]
	require.Equal(t, core.NodeSucceeded, parallelNode.Status)
	require.Len(t, parallelNode.SubRuns, 1, "should dispatch exactly 1 worker instance for 1 JSON item")
}

// TestIssue1274_ParallelJSONMultipleItems tests that parallel execution
// correctly handles multiple JSON items from output (should dispatch N jobs)
func TestIssue1274_ParallelJSONMultipleItems(t *testing.T) {
	jsonLines := []string{
		`{"file": "file1.txt", "config": "prod"}`,
		`{"file": "file2.txt", "config": "test"}`,
		`{"file": "file3.txt", "config": "dev"}`,
	}
	if runtime.GOOS == "windows" {
		jsonLines = jsonLines[:2]
	}
	dagContent := fmt.Sprintf(`steps:
  - command: |
%s
    output: jsonList

  - call: issue-1274-worker-multi
    parallel:
      items: ${jsonList}
      max_concurrent: 1
    params:
      aJson: ${ITEM}
    continue_on:
      skipped: true

---
name: issue-1274-worker-multi
params:
  aJson: ""
steps:
  - name: Process JSON item
    command: echo "Processing file=${aJson.file} config=${aJson.config}"
`, yamlEchoLines(jsonLines))

	th := test.Setup(t)
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	err := agent.Run(agent.Context)
	require.NoError(t, err)
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, statusErr)

	require.Len(t, dagStatus.Nodes, 2)

	require.Greater(t, len(dagStatus.Nodes), 1, "node index out of range")
	parallelNode := dagStatus.Nodes[1]
	require.Equal(t, core.NodeSucceeded, parallelNode.Status)
	require.Len(t, parallelNode.SubRuns, len(jsonLines), "should dispatch one worker instance per JSON item")
}

// TestIssue1658_ParallelCallExpandedParamsSplitting tests that when a call step
// uses parallel with items, params passed via variable expansion are correctly
// split into individual KEY=VALUE pairs after expansion.
// See: https://github.com/dagucloud/dagu/issues/1658
func TestIssue1658_ParallelCallExpandedParamsSplitting(t *testing.T) {
	expandedParamsScript := `
      if [ -z "${A}" ] || [ -z "${B}" ]; then
        echo "FAIL: A='${A}' B='${B}' (expected A=1 B=2)"
        exit 1
      fi
      echo "OK: NAME=${NAME} A=${A} B=${B}"
`
	multiExpandScript := `
      if [ -z "${X}" ] || [ -z "${Y}" ]; then
        echo "FAIL: NAME='${NAME}' X='${X}' Y='${Y}'"
        exit 1
      fi
      echo "OK: NAME=${NAME} X=${X} Y=${Y}"
`
	namedSpacesScript := `
      if [ "${LABEL}" != "hello world" ]; then
        echo "FAIL: LABEL='${LABEL}' (expected 'hello world')"
        exit 1
      fi
      if [ "${ID}" != "1" ]; then
        echo "FAIL: ID='${ID}' (expected '1')"
        exit 1
      fi
      echo "OK: LABEL=${LABEL} ID=${ID}"
`
	singleValueScript := `
      if [ "${1}" != "simple" ]; then
        echo "FAIL: TAG='${1}' (expected 'simple')"
        exit 1
      fi
      echo "OK: TAG=${1}"
`
	if runtime.GOOS == "windows" {
		expandedParamsScript = `
      if ([string]::IsNullOrEmpty("${A}") -or [string]::IsNullOrEmpty("${B}")) {
        Write-Output ("FAIL: A='{0}' B='{1}' (expected A=1 B=2)" -f "${A}", "${B}")
        exit 1
      }
      Write-Output ("OK: NAME={0} A={1} B={2}" -f "${NAME}", "${A}", "${B}")
`
		multiExpandScript = `
      if ([string]::IsNullOrEmpty("${X}") -or [string]::IsNullOrEmpty("${Y}")) {
        Write-Output ("FAIL: NAME='{0}' X='{1}' Y='{2}'" -f "${NAME}", "${X}", "${Y}")
        exit 1
      }
      Write-Output ("OK: NAME={0} X={1} Y={2}" -f "${NAME}", "${X}", "${Y}")
`
		namedSpacesScript = `
      if ("${LABEL}" -ne "hello world") {
        Write-Output ("FAIL: LABEL='{0}' (expected 'hello world')" -f "${LABEL}")
        exit 1
      }
      if ("${ID}" -ne "1") {
        Write-Output ("FAIL: ID='{0}' (expected '1')" -f "${ID}")
        exit 1
      }
      Write-Output ("OK: LABEL={0} ID={1}" -f "${LABEL}", "${ID}")
`
		singleValueScript = `
      if ("${1}" -ne "simple") {
        Write-Output ("FAIL: TAG='{0}' (expected 'simple')" -f "${1}")
        exit 1
      }
      Write-Output ("OK: TAG={0}" -f "${1}")
`
	}

	cases := []struct {
		name            string
		dag             string
		expectedSubRuns int
		verify          func(*testing.T, parallelResultsPayload)
	}{
		{
			name: "positional_expands_to_multiple_params",
			dag: fmt.Sprintf(`steps:
  - command: |
      echo '[{"name": "test", "extra": "A=1 B=2"}]'
    output: ITEMS

  - call: child-params-split
    parallel:
      items: ${ITEMS}
    params: "NAME=${ITEM.name} ${ITEM.extra}"
    output: RESULTS
---
name: child-params-split
params:
  - NAME: ""
  - A: ""
  - B: ""
steps:
  - script: |
%s
    output: CHECK_RESULT
`, strings.TrimPrefix(expandedParamsScript, "\n")),
			expectedSubRuns: 1,
			verify: func(t *testing.T, results parallelResultsPayload) {
				require.Equal(t, 1, results.Summary.Succeeded)
				outputs := collectOutputs(results.Outputs, "CHECK_RESULT")
				require.Len(t, outputs, 1)
				require.Contains(t, outputs[0], "OK: NAME=test A=1 B=2")
			},
		},
		{
			name: "multiple_items_different_expansions",
			dag: fmt.Sprintf(`steps:
  - command: |
      echo '[{"name":"alpha","extra":"X=10 Y=20"}, {"name":"beta","extra":"X=30 Y=40"}]'
    output: ITEMS

  - call: child-multi-expand
    parallel:
      items: ${ITEMS}
    params: "NAME=${ITEM.name} ${ITEM.extra}"
    output: RESULTS
---
name: child-multi-expand
params:
  - NAME: ""
  - X: ""
  - Y: ""
steps:
  - script: |
%s
    output: CHECK_RESULT
`, strings.TrimPrefix(multiExpandScript, "\n")),
			expectedSubRuns: 2,
			verify: func(t *testing.T, results parallelResultsPayload) {
				require.Equal(t, 2, results.Summary.Succeeded)
				outputs := collectOutputs(results.Outputs, "CHECK_RESULT")
				require.Len(t, outputs, 2)

				var foundAlpha, foundBeta bool
				for _, out := range outputs {
					if strings.Contains(out, "OK: NAME=alpha X=10 Y=20") {
						foundAlpha = true
					}
					if strings.Contains(out, "OK: NAME=beta X=30 Y=40") {
						foundBeta = true
					}
				}
				require.True(t, foundAlpha, "expected output for alpha item")
				require.True(t, foundBeta, "expected output for beta item")
			},
		},
		{
			name: "named_param_with_spaces_preserved",
			dag: fmt.Sprintf(`steps:
  - command: |
      echo '[{"label": "hello world", "id": "1"}]'
    output: ITEMS

  - call: child-named-spaces
    parallel:
      items: ${ITEMS}
    params: "LABEL=${ITEM.label} ID=${ITEM.id}"
    output: RESULTS
---
name: child-named-spaces
params:
  - LABEL: ""
  - ID: ""
steps:
  - script: |
%s
    output: CHECK_RESULT
`, strings.TrimPrefix(namedSpacesScript, "\n")),
			expectedSubRuns: 1,
			verify: func(t *testing.T, results parallelResultsPayload) {
				require.Equal(t, 1, results.Summary.Succeeded)
				outputs := collectOutputs(results.Outputs, "CHECK_RESULT")
				require.Len(t, outputs, 1)
				require.Contains(t, outputs[0], "OK: LABEL=hello world ID=1")
			},
		},
		{
			name: "positional_single_value_no_split",
			dag: fmt.Sprintf(`steps:
  - command: |
      echo '[{"tag": "simple"}]'
    output: ITEMS

  - call: child-positional-single
    parallel:
      items: ${ITEMS}
    params: "${ITEM.tag}"
    output: RESULTS
---
name: child-positional-single
params:
  - TAG: ""
steps:
  - script: |
%s
    output: CHECK_RESULT
`, strings.TrimPrefix(singleValueScript, "\n")),
			expectedSubRuns: 1,
			verify: func(t *testing.T, results parallelResultsPayload) {
				require.Equal(t, 1, results.Summary.Succeeded)
				outputs := collectOutputs(results.Outputs, "CHECK_RESULT")
				require.Len(t, outputs, 1)
				require.Contains(t, outputs[0], "OK: TAG=simple")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := test.Setup(t)
			dag := th.DAG(t, tc.dag)
			agent := dag.Agent()
			err := agent.Run(agent.Context)
			require.NoError(t, err)
			dag.AssertLatestStatus(t, core.Succeeded)

			dagStatus, statusErr := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
			require.NoError(t, statusErr)

			require.Greater(t, len(dagStatus.Nodes), 1, "expected at least 2 nodes")
			parallelNode := dagStatus.Nodes[1]
			require.Equal(t, core.NodeSucceeded, parallelNode.Status)
			require.Len(t, parallelNode.SubRuns, tc.expectedSubRuns)

			require.NotNil(t, parallelNode.OutputVariables, "no outputs recorded")
			rawRaw, ok := parallelNode.OutputVariables.Load("RESULTS")
			require.True(t, ok, "output RESULTS not found")
			raw, ok := rawRaw.(string)
			require.True(t, ok, "output RESULTS is not a string")
			results := parseParallelResults(t, raw)
			tc.verify(t, results)
		})
	}
}

// TestIssue1790_ParallelCallPathItemResolution verifies that `${ITEM}` is
// resolved inside `call:` before each parallel sub-DAG is loaded.
// See: https://github.com/dagucloud/dagu/issues/1790
func TestIssue1790_ParallelCallPathItemResolution(t *testing.T) {
	th := test.Setup(t)

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "parallel-child_01.yaml", []byte(`
name: parallel-child-01
params:
  - ITEM: ""
steps:
  - command: echo "child=01 item=${ITEM}"
    output: CHILD_RESULT
`))

	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "parallel-child_02.yaml", []byte(`
name: parallel-child-02
params:
  - ITEM: ""
steps:
  - command: echo "child=02 item=${ITEM}"
    output: CHILD_RESULT
`))

	callPattern := filepath.Join(th.Config.Paths.DAGsDir, "parallel-child_${ITEM}.yaml")
	dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: dynamic-call
    call: %q
    parallel:
      items:
        - "01"
        - "02"
    params: "ITEM=${ITEM}"
    output: RESULTS
`, callPattern))

	agent := dag.Agent()
	require.NoError(t, agent.Run(agent.Context))
	dag.AssertLatestStatus(t, core.Succeeded)

	dagStatus, err := dag.DAGRunMgr.GetLatestStatus(dag.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, dagStatus.Nodes, 1)

	parallelNode := dagStatus.Nodes[0]
	require.Equal(t, core.NodeSucceeded, parallelNode.Status)
	require.Len(t, parallelNode.SubRuns, 2)

	gotNames := make(map[string]struct{}, len(parallelNode.SubRuns))
	rootRun := exec.NewDAGRunRef(dag.Name, dagStatus.DAGRunID)
	for _, subRun := range parallelNode.SubRuns {
		subStatus, err := dag.DAGRunMgr.FindSubDAGRunStatus(dag.Context, rootRun, subRun.DAGRunID)
		require.NoError(t, err)
		gotNames[subStatus.Name] = struct{}{}
	}
	require.Contains(t, gotNames, "parallel-child-01")
	require.Contains(t, gotNames, "parallel-child-02")

	require.NotNil(t, parallelNode.OutputVariables, "no outputs recorded")
	rawRaw, ok := parallelNode.OutputVariables.Load("RESULTS")
	require.True(t, ok, "output RESULTS not found")
	raw, ok := rawRaw.(string)
	require.True(t, ok, "output RESULTS is not a string")

	results := parseParallelResults(t, raw)
	require.Equal(t, 2, results.Summary.Total)
	require.Equal(t, 2, results.Summary.Succeeded)
	require.Equal(t, 0, results.Summary.Failed)

	outputs := collectOutputs(results.Outputs, "CHILD_RESULT")
	require.Len(t, outputs, 2)
	require.Contains(t, outputs, "child=01 item=01")
	require.Contains(t, outputs, "child=02 item=02")
}

type parallelSummary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type parallelResultsPayload struct {
	Summary parallelSummary  `json:"summary"`
	Outputs []map[string]any `json:"outputs"`
	Results []map[string]any `json:"results"`
}

func parseParallelResults(t *testing.T, raw string) parallelResultsPayload {
	t.Helper()
	_, value, found := strings.Cut(raw, "=")
	require.True(t, found, "expected key=value output format")

	var payload parallelResultsPayload
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(value)), &payload))
	return payload
}

func collectOutputs(entries []map[string]any, key string) []string {
	var out []string
	for _, entry := range entries {
		if value, ok := entry[key].(string); ok {
			out = append(out, value)
		}
	}
	return out
}

func markParallelItemStartedAndWaitCommand(startedDir, releaseFile string) string {
	return test.ForOS(
		fmt.Sprintf(`: > %s/"started-$$"
while [ ! -f %s ]; do
  sleep 0.05
done`, test.PosixQuote(startedDir), test.PosixQuote(releaseFile)),
		fmt.Sprintf(`New-Item -ItemType File -Path (Join-Path %s ("started-" + [guid]::NewGuid().ToString())) -Force | Out-Null
while (-not (Test-Path %s)) {
  Start-Sleep -Milliseconds 50
}`, test.PowerShellQuote(startedDir), test.PowerShellQuote(releaseFile)),
	)
}

func countStartedParallelItems(t *testing.T, startedDir string) int {
	t.Helper()

	entries, err := os.ReadDir(startedDir)
	require.NoError(t, err)
	return len(entries)
}
