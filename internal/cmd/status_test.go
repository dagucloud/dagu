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

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
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

// waitForDAGRunning waits until the DAG is in running state.
func waitForDAGRunning(t *testing.T, th test.Command, dagLocation string) {
	t.Helper()
	require.Eventually(t, func() bool {
		attempts := th.DAGRunStore.RecentAttempts(th.Context, dagLocation, 1)
		if len(attempts) < 1 {
			return false
		}
		status, err := attempts[0].ReadStatus(th.Context)
		if err != nil {
			return false
		}
		return status.Status == core.Running
	}, time.Second*3, time.Millisecond*50)
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
		done := make(chan struct{})
		go func() {
			th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dagFile.Location}})
			close(done)
		}()

		waitForDAGRunning(t, th, dagFile.Location)

		err := executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)

		releaseHoldFile(t, release)
		<-done
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

		dagFile.AssertLatestStatus(t, core.Succeeded)

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
		dag, err := th.DAGStore.GetMetadata(th.Context, dagFile.Location)
		require.NoError(t, err)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		err = attempt.Open(th.Context)
		require.NoError(t, err)

		status := exec.DAGRunStatus{
			Name:       dag.Name,
			DAGRunID:   dagRunID,
			Status:     core.Failed,
			StartedAt:  time.Now().Format(time.RFC3339),
			FinishedAt: time.Now().Format(time.RFC3339),
			AttemptID:  attempt.ID(),
			Nodes: []*exec.Node{
				{
					Step:   core.Step{Name: "error"},
					Status: core.NodeFailed,
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

		dagFile.AssertLatestStatus(t, core.Succeeded)

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

		dagFile.AssertLatestStatus(t, core.Succeeded)

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

		dagFile.AssertLatestStatus(t, core.Succeeded)

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
		dag, err := th.DAGStore.GetMetadata(th.Context, dagFile.Location)
		require.NoError(t, err)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		err = attempt.Open(th.Context)
		require.NoError(t, err)

		now := time.Now().Format(time.RFC3339)
		status := exec.DAGRunStatus{
			Name:       dag.Name,
			DAGRunID:   dagRunID,
			Status:     core.Failed,
			StartedAt:  now,
			FinishedAt: now,
			AttemptID:  attempt.ID(),
			Nodes: []*exec.Node{
				{
					Step:       core.Step{Name: "check"},
					Status:     core.NodeFailed,
					Error:      "exit status 1",
					StartedAt:  now,
					FinishedAt: now,
				},
				{
					Step:       core.Step{Name: "skipped"},
					Status:     core.NodeSkipped,
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
		done := make(chan struct{})
		go func() {
			th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dagFile.Location}})
			close(done)
		}()

		waitForDAGRunning(t, th, dagFile.Location)

		th.RunCommand(t, cmd.Stop(), test.CmdTest{Args: []string{"stop", dagFile.Location}})
		<-done

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

		dagFile.AssertLatestStatus(t, core.Succeeded)

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

		dagFile.AssertLatestStatus(t, core.Succeeded)

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
		done := make(chan struct{})
		go func() {
			th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", dagFile.Location}})
			close(done)
		}()

		waitForDAGRunning(t, th, dagFile.Location)

		err := executeCommand(th.Context, cmd.Status(), []string{dagFile.Location})
		require.NoError(t, err)

		releaseHoldFile(t, release)
		<-done
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

		dagFile.AssertLatestStatus(t, core.Succeeded)

		ctx := context.Background()
		dag, err := th.DAGStore.GetMetadata(ctx, dagFile.Location)
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

		dagFile.AssertLatestStatus(t, core.Succeeded)

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
		dag, err := th.DAGStore.GetMetadata(th.Context, dagFile.Location)
		require.NoError(t, err)

		dagRunID := uuid.Must(uuid.NewV7()).String()
		attempt, err := th.DAGRunStore.CreateAttempt(th.Context, dag, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
		require.NoError(t, err)

		err = attempt.Open(th.Context)
		require.NoError(t, err)

		now := time.Now().Format(time.RFC3339)
		status := exec.DAGRunStatus{
			Name:       dag.Name,
			DAGRunID:   dagRunID,
			Status:     core.Succeeded,
			StartedAt:  now,
			FinishedAt: now,
			AttemptID:  attempt.ID(),
			Nodes: []*exec.Node{
				{
					Step:   core.Step{Name: "binary_output"},
					Status: core.NodeSucceeded,
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

		parentRef := exec.NewDAGRunRef(dagFile.Location, parentRunID)
		var parentAttempt exec.DAGRunAttempt
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

		parentRef := exec.NewDAGRunRef(dagFile.Location, parentRunID)
		require.Eventually(t, func() bool {
			attempt, err := th.DAGRunStore.FindAttempt(th.Context, parentRef)
			if err != nil {
				return false
			}
			status, err := attempt.ReadStatus(th.Context)
			if err != nil {
				return false
			}
			return status.Status != core.Running
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
