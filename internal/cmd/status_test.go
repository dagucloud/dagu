// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// executeCommand runs a cobra command with silenced errors and usage output.
func executeCommand(ctx context.Context, c *cobra.Command, args []string) error {
	c.SetContext(ctx)
	c.SetArgs(test.WithConfigFlag(args, config.GetConfig(ctx)))
	c.SilenceErrors = true
	c.SilenceUsage = true
	return c.Execute()
}

// boundedWaitTimeout returns want, shortened when needed to stay inside the
// test deadline.
func boundedWaitTimeout(t *testing.T, want time.Duration) time.Duration {
	t.Helper()

	if deadline, ok := t.Deadline(); ok {
		remaining := time.Until(deadline) - 15*time.Second
		if remaining > 0 && remaining < want {
			return remaining
		}
	}
	return want
}

// waitForDAGRunning waits until the DAG is in running state. Reaching it means
// spawning a process and writing the first status file, and that cost varies
// widely with host load, so the budget is generous rather than fixed.
func waitForDAGRunning(t *testing.T, th test.Command, dagLocation string) {
	t.Helper()
	require.Eventually(t, func() bool {
		statuses := th.DAGRunStore.RecentStatuses(th.Context, dagLocation, 1)
		if len(statuses) < 1 {
			return false
		}
		return statuses[0].Status == ir.Running
	}, boundedWaitTimeout(t, time.Minute), time.Millisecond*50)
}

func TestStatusCommand(t *testing.T) {
	t.Run("StatusDAGRunning", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		release := newHoldFile(t)
		dagFile := th.DAG(t, fmt.Sprintf(`steps:
  - name: "1"
    run: %q
`, holdUntilFileExistsCommand(release)))
		done := make(chan error, 1)
		go func() {
			done <- th.ExecuteCommand(cmd.Start(), test.CmdTest{Args: []string{"start", dagFile.Location}})
		}()

		waitForDAGRunning(t, th, dagFile.Location)

		err := executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)

		releaseHoldFile(t, release)
		require.NoError(t, <-done)
	})

	t.Run("StatusDAGSuccess", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGError", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "error"
    run: exit 1
`)
		dag, err := th.DAGRepository.GetMetadata(th.Context, dagFile.Location)
		require.NoError(t, err)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, time.Now(), dagRunID, dagrun.CreateAttemptOptions{})
		require.NoError(t, err)

		err = attempt.Open(th.Context)
		require.NoError(t, err)

		status := ir.DAGRunStatus{
			Name:       dag.Name,
			DAGRunID:   dagRunID,
			Status:     ir.Failed,
			StartedAt:  time.Now().Format(time.RFC3339),
			FinishedAt: time.Now().Format(time.RFC3339),
			AttemptID:  attempt.ID(),
			Nodes: []*ir.Node{
				{
					Step:   ir.Step{Name: "error"},
					Status: ir.NodeFailed,
					Error:  "exit status 1",
				},
			},
		}
		err = attempt.Write(th.Context, status)
		require.NoError(t, err)

		err = attempt.Close(th.Context)
		require.NoError(t, err)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGWithParams", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `params:
  - param1
  - param2
steps:
  - name: "print-params"
    run: "echo Param1: ${param1}, Param2: ${param2}"
`)
		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location, "--params=custom1 custom2"})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGWithSpecificRunID", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		runID := uuid.Must(uuid.NewV7()).String()

		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location, "--run-id=" + runID})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location, "--run-id=" + runID})
		require.NoError(t, err)
	})

	t.Run("StatusDAGMultipleRuns", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		err = executeCommand(th.Context, cmd.Start(), []string{dagFile.Location})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			statuses := dagFile.DAGRunMgr.ListRecentStatus(th.Context, dagFile.Name, 3)
			return len(statuses) == 2
		}, 5*time.Second, 50*time.Millisecond)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGWithSkippedSteps", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "check"
    run: "false"
    continue_on:
      failure: true
  - name: "skipped"
    run: "echo 'This will be skipped'"
    preconditions:
      - condition: "test -f /nonexistent"
`)
		dag, err := th.DAGRepository.GetMetadata(th.Context, dagFile.Location)
		require.NoError(t, err)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, time.Now(), dagRunID, dagrun.CreateAttemptOptions{})
		require.NoError(t, err)

		err = attempt.Open(th.Context)
		require.NoError(t, err)

		now := time.Now().Format(time.RFC3339)
		status := ir.DAGRunStatus{
			Name:       dag.Name,
			DAGRunID:   dagRunID,
			Status:     ir.Failed,
			StartedAt:  now,
			FinishedAt: now,
			AttemptID:  attempt.ID(),
			Nodes: []*ir.Node{
				{
					Step:       ir.Step{Name: "check"},
					Status:     ir.NodeFailed,
					Error:      "exit status 1",
					StartedAt:  now,
					FinishedAt: now,
				},
				{
					Step:       ir.Step{Name: "skipped"},
					Status:     ir.NodeSkipped,
					StartedAt:  "-",
					FinishedAt: now,
				},
			},
		}
		err = attempt.Write(th.Context, status)
		require.NoError(t, err)

		err = attempt.Close(th.Context)
		require.NoError(t, err)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGCancel", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		release := newHoldFile(t)
		dagFile := th.DAG(t, fmt.Sprintf(`steps:
  - name: "1"
    run: %q
`, holdUntilFileExistsCommand(release)))
		done := make(chan error, 1)
		go func() {
			done <- th.ExecuteCommand(cmd.Start(), test.CmdTest{Args: []string{"start", dagFile.Location}})
		}()

		waitForDAGRunning(t, th, dagFile.Location)

		th.RunCommand(t, cmd.Stop(), test.CmdTest{Args: []string{"stop", dagFile.Location}})
		require.NoError(t, <-done)

		err := executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGWithManySteps", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		stepCount := 10
		if runtime.GOOS == "windows" {
			// The status renderer behavior is the same with fewer steps, and
			// Windows CI pays a large per-step shell startup cost.
			stepCount = 4
		}
		var dagContent strings.Builder
		for i := range stepCount {
			fmt.Fprintf(&dagContent, "  - name: \"step%d\"\n    command: \"echo 'Step %d'\"\n", i+1, i+1)
		}
		dagFile := th.DAG(t, "steps:\n"+dagContent.String())
		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGByName", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGWithPID", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		release := newHoldFile(t)
		dagFile := th.DAG(t, fmt.Sprintf(`steps:
  - name: "1"
    run: %q
`, holdUntilFileExistsCommand(release)))
		done := make(chan error, 1)
		go func() {
			done <- th.ExecuteCommand(cmd.Start(), test.CmdTest{Args: []string{"start", dagFile.Location}})
		}()

		waitForDAGRunning(t, th, dagFile.Location)

		err := executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)

		releaseHoldFile(t, release)
		require.NoError(t, <-done)
	})

	t.Run("StatusDAGWithAttemptID", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		ctx := context.Background()
		dag, err := th.DAGRepository.GetMetadata(ctx, dagFile.Location)
		require.NoError(t, err)

		status, err := th.DAGRunMgr.GetLatestStatus(ctx, dag)
		require.NoError(t, err)
		require.NotEmpty(t, status.AttemptID)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGWithLogPaths", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location})
		require.NoError(t, err)

		dagFile.AssertLatestStatus(t, ir.Succeeded)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusDAGWithBinaryLogContent", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		dag, err := th.DAGRepository.GetMetadata(th.Context, dagFile.Location)
		require.NoError(t, err)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, time.Now(), dagRunID, dagrun.CreateAttemptOptions{})
		require.NoError(t, err)

		err = attempt.Open(th.Context)
		require.NoError(t, err)

		now := time.Now().Format(time.RFC3339)
		status := ir.DAGRunStatus{
			Name:       dag.Name,
			DAGRunID:   dagRunID,
			Status:     ir.Succeeded,
			StartedAt:  now,
			FinishedAt: now,
			AttemptID:  attempt.ID(),
			Nodes: []*ir.Node{
				{
					Step:   ir.Step{Name: "binary_output"},
					Status: ir.NodeSucceeded,
					Stdout: "/nonexistent/binary.log",
					Stderr: "",
				},
			},
		}
		err = attempt.Write(th.Context, status)
		require.NoError(t, err)

		err = attempt.Close(th.Context)
		require.NoError(t, err)

		err = executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)
	})

	t.Run("StatusSubDAGRun", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t, test.WithBuiltExecutable())
		dagFile := th.DAG(t, `steps:
  - name: run-child
    action: dag.run
    with:
      dag: child-dag
      params: "NAME=World"

---

name: child-dag
params:
  - NAME
steps:
  - name: greet
    run: echo "Hello, ${NAME}!"
`)
		parentRunID := uuid.Must(uuid.NewV7()).String()

		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location, "--run-id=" + parentRunID})
		require.NoError(t, err)

		parentRef := ir.NewDAGRunRef(dagFile.Location, parentRunID)
		var parentAttempt dagrun.Attempt
		require.Eventually(t, func() bool {
			var err error
			parentAttempt, err = th.DAGRunStore.FindAttempt(th.Context, parentRef)
			if err != nil {
				return false
			}
			parentStatus, err := parentAttempt.ReadStatus(th.Context)
			if err != nil {
				return false
			}
			return len(parentStatus.Nodes) > 0 && len(parentStatus.Nodes[0].SubRuns) > 0
		}, 5*time.Second, 50*time.Millisecond, "parent status should have nodes with sub-runs")

		parentStatus, err := parentAttempt.ReadStatus(th.Context)
		require.NoError(t, err)
		require.Len(t, parentStatus.Nodes, 1)
		require.NotEmpty(t, parentStatus.Nodes[0].SubRuns)

		subDAGRunID := parentStatus.Nodes[0].SubRuns[0].DAGRunID

		err = executeCommand(th.Context, cmd.Status(), []string{
			dagFile.Location,
			"--run-id=" + parentRunID,
			"--sub-run-id=" + subDAGRunID,
		})
		require.NoError(t, err)
	})

	t.Run("StatusSubDAGRunMissingParentRunID", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		err := executeCommand(th.Context, cmd.Status(), []string{dagFile.Location, "--sub-run-id=some-sub-id"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "--sub-run-id requires --run-id")
	})

	t.Run("StatusSubDAGRunNotFound", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.DAG(t, `steps:
  - name: "success"
    run: "echo 'Success!'"
`)
		parentRunID := uuid.Must(uuid.NewV7()).String()

		err := executeCommand(th.Context, cmd.Start(), []string{dagFile.Location, "--run-id=" + parentRunID})
		require.NoError(t, err)

		parentRef := ir.NewDAGRunRef(dagFile.Location, parentRunID)
		require.Eventually(t, func() bool {
			attempt, err := th.DAGRunStore.FindAttempt(th.Context, parentRef)
			if err != nil {
				return false
			}
			status, err := attempt.ReadStatus(th.Context)
			if err != nil {
				return false
			}
			return status.Status != ir.Running
		}, 5*time.Second, 50*time.Millisecond, "DAG run should complete")

		err = executeCommand(th.Context, cmd.Status(), []string{
			dagFile.Location,
			"--run-id=" + parentRunID,
			"--sub-run-id=non-existent-sub-id",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to find sub dag-run")
	})
}
