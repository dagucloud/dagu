// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestRetryDAGAfterManualStatusUpdate reproduces a bug where retrying a DAG
// after manually updating a node status to failure does not properly retry
// the failed node. The expected behavior is that the retry should re-execute
// the failed node, but the bug causes it to skip or fail incorrectly.
func TestRetryDAGAfterManualStatusUpdate(t *testing.T) {
	th := test.SetupCommand(t)

	// Create a simple 3-step DAG where all steps should succeed
	th.CreateDAGFile(t, "test_retry.yaml", `type: graph
steps:
  - name: step1
    run: echo "step 1"
    output: OUT1

  - name: step2
    run: echo "step 2"
    output: OUT2
    depends:
      - step1

  - name: step3
    run: echo "step 3"
    output: OUT3
    depends:
      - step2
`)

	// First run - should succeed completely
	dagRunID := uuid.Must(uuid.NewV7()).String()
	args := []string{"start", "--run-id", dagRunID, "test_retry"}
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        args,
		ExpectedOut: []string{"DAG run finished"},
	})

	// Verify the initial run completed successfully
	ctx := context.Background()
	ref := exec.NewDAGRunRef("test_retry", dagRunID)
	attempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
	require.NoError(t, err)

	firstRunStatus, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, firstRunStatus.Status, core.Succeeded)

	// Manually update the second node status to failed
	// This simulates a scenario where the user wants to retry from a specific point
	firstRunStatus.Nodes[1].Status = core.NodeFailed

	err = th.DAGRunMgr.UpdateStatus(ctx, ref, *firstRunStatus)
	require.NoError(t, err)

	// Read back the status to verify it was persisted correctly
	readStatus, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.NodeFailed.String(), readStatus.Nodes[1].Status.String(), "step2 should be marked as failed in persisted status")

	// Now retry the DAG using the retry command
	retryArgs := []string{"retry", "--run-id", dagRunID, "test_retry"}
	th.RunCommand(t, cmd.Retry(), test.CmdTest{
		Args:        retryArgs,
		ExpectedOut: []string{"DAG run finished"},
	})

	// The bug: The retry does not succeed even though all commands are valid
	// Expected: The retry should re-execute step2 and step3 successfully
	// Actual: The retry fails or doesn't properly execute the failed nodes

	// Get the status after retry
	retryAttempt, err := th.DAGRunStore.FindAttempt(ctx, ref)
	require.NoError(t, err)
	retryStatus, err := retryAttempt.ReadStatus(ctx)
	require.NoError(t, err)

	// This assertion will fail if the bug exists
	// The retry should have succeeded since all commands are valid
	t.Logf("Retry status: %s", retryStatus.Status.String())
	for i, node := range retryStatus.Nodes {
		t.Logf("Node %d (%s): status=%s, error=%s", i, node.Step.Name, node.Status.String(), node.Error)
	}

	// The expected behavior is that the retry succeeds
	require.Equal(t, core.Succeeded.String(), retryStatus.Status.String(), "retry should succeed")

	// Verify that step2 and step3 were re-executed
	require.Equal(t, core.NodeSucceeded.String(), retryStatus.Nodes[1].Status.String(), "step2 should have succeeded after retry")
	require.Equal(t, core.NodeSucceeded.String(), retryStatus.Nodes[2].Status.String(), "step3 should have succeeded after retry")
}

func TestStepRetryReusesOriginalWorkingDir(t *testing.T) {
	th := test.SetupCommand(t)

	workDir := t.TempDir()
	stepDir := filepath.Join(workDir, "child")
	require.NoError(t, os.MkdirAll(stepDir, 0750))

	shell := test.ForOS("sh", "powershell")
	command := test.ForOS(`
pwd >> observed.txt
if [ ! -f marker ]; then
  touch marker
  exit 1
fi
echo retry ok >> observed.txt
`, `
(Get-Location).Path | Add-Content -Path observed.txt
if (-not (Test-Path marker)) {
  New-Item -ItemType File marker | Out-Null
  exit 1
}
"retry ok" | Add-Content -Path observed.txt
`)

	th.CreateDAGFile(t, "retry_working_dir.yaml", fmt.Sprintf(`steps:
  - name: target
    working_dir: child
    run: |
%s
    with:
      shell: %s
`, indentTestScript(command, 6), shell))

	dagRunID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", dagRunID, "--default-working-dir", workDir, "retry_working_dir"},
	})
	require.Error(t, err)

	ref := exec.NewDAGRunRef("retry_working_dir", dagRunID)
	failedAttempt, err := th.DAGRunStore.FindAttempt(th.Context, ref)
	require.NoError(t, err)
	failedStatus, err := failedAttempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, canonicalTestPath(workDir), canonicalTestPath(failedStatus.WorkingDir))
	require.Len(t, failedStatus.Nodes, 1)
	require.Equal(t, canonicalTestPath(stepDir), canonicalTestPath(failedStatus.Nodes[0].WorkingDir))

	th.RunCommand(t, cmd.Retry(), test.CmdTest{
		Args: []string{"retry", "--run-id", dagRunID, "--step", "target", "retry_working_dir"},
	})

	retryAttempt, err := th.DAGRunStore.FindAttempt(th.Context, ref)
	require.NoError(t, err)
	retryStatus, err := retryAttempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, retryStatus.Status)

	observed, err := os.ReadFile(filepath.Join(stepDir, "observed.txt"))
	require.NoError(t, err)
	observedOutput := string(observed)
	observedWorkingDirCount := 0
	expectedStepDir := canonicalTestPath(stepDir)
	for line := range strings.SplitSeq(strings.ReplaceAll(observedOutput, "\r\n", "\n"), "\n") {
		if canonicalTestPath(line) == expectedStepDir {
			observedWorkingDirCount++
		}
	}
	require.GreaterOrEqual(t, observedWorkingDirCount, 2, "step should run from the original step working directory before and during retry")
	require.Contains(t, observedOutput, "retry ok")
}
