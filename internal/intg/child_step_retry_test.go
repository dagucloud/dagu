// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// TestChildStepRetryReExecutesTargetedChildRun verifies that retrying a step
// inside one child DAG run of a parallel step re-executes that child run and
// reuses its siblings.
func TestChildStepRetryReExecutesTargetedChildRun(t *testing.T) {
	th := test.Setup(t)
	marker := test.ShellPath(filepath.Join(t.TempDir(), "retry-marker"))

	work := test.ForOS(
		fmt.Sprintf("if [ -f %s ]; then exit 0; fi\nexit ${CODE}", test.PosixQuote(marker)),
		fmt.Sprintf("if (Test-Path %s) { exit 0 }\nexit $env:CODE", test.PowerShellQuote(marker)),
	)

	dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: parallel_2
    action: dag.run
    with:
      dag: child_step_retry
    parallel:
      items:
        - CODE: "0"
        - CODE: "1"
---
name: child_step_retry
params:
  - CODE: "0"
steps:
  - name: work
    run: |
%s
    with:
      shell: %s
`, indentCommandBlock(work, 6), test.ForOS("sh", "powershell")))

	dagAgent := dag.Agent()
	dagAgent.RunError(t)

	rootStatus := dagAgent.Status(th.Context)
	require.Len(t, rootStatus.Nodes, 1)
	require.Equal(t, ir.NodeFailed, rootStatus.Nodes[0].Status)
	require.Len(t, rootStatus.Nodes[0].SubRuns, 2)

	rootRef := exec.NewDAGRunRef(dag.Name, rootStatus.DAGRunID)
	var target, sibling string
	var targetAttempt, siblingAttempt string
	for _, subRun := range rootStatus.Nodes[0].SubRuns {
		status, attemptID := readChildRun(t, th, rootRef, subRun.DAGRunID)
		if status == ir.Failed {
			target, targetAttempt = subRun.DAGRunID, attemptID
		} else {
			sibling, siblingAttempt = subRun.DAGRunID, attemptID
		}
	}
	require.NotEmpty(t, target, "no failed child DAG run")
	require.NotEmpty(t, sibling, "no succeeded child DAG run")

	path, _, err := exec.ResolveRetryPath(th.Context, th.DAGRunStore, rootRef, target, "work")
	require.NoError(t, err)
	require.Equal(t, "parallel_2", path.RootStep())
	require.Equal(t, "work", path.Step)

	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0o600))

	retryAgent := dag.Agent(test.WithAgentOptions(agent.Options{
		RetryTarget: &rootStatus,
		StepRetry:   path.RootStep(),
		RetryPath:   path,
	}))
	require.NoError(t, retryAgent.Run(th.Context))

	retriedStatus := retryAgent.Status(th.Context)
	require.Equal(t, ir.Succeeded, retriedStatus.Status)
	require.Len(t, retriedStatus.Nodes, 1)

	retriedIDs := make([]string, 0, len(retriedStatus.Nodes[0].SubRuns))
	for _, subRun := range retriedStatus.Nodes[0].SubRuns {
		retriedIDs = append(retriedIDs, subRun.DAGRunID)
	}
	require.ElementsMatch(t, []string{target, sibling}, retriedIDs)

	targetStatus, retriedTargetAttempt := readChildRun(t, th, rootRef, target)
	require.Equal(t, ir.Succeeded, targetStatus)
	require.NotEqual(t, targetAttempt, retriedTargetAttempt, "targeted child DAG run was not re-executed")

	_, retriedSiblingAttempt := readChildRun(t, th, rootRef, sibling)
	require.Equal(t, siblingAttempt, retriedSiblingAttempt, "sibling child DAG run was re-executed instead of reused")
}

// readChildRun returns the status and latest attempt ID of a child DAG run.
func readChildRun(t *testing.T, th test.Helper, root exec.DAGRunRef, runID string) (ir.Status, string) {
	t.Helper()
	attempt, err := th.DAGRunStore.FindSubAttempt(th.Context, root, runID)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)
	return status.Status, attempt.ID()
}
