// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStopCommand(t *testing.T) {
	t.Run("StopDAGRun", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)

		release := newHoldFile(t)
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: "1"
    run: %q
`, holdUntilFileExistsCommand(release)))

		done := make(chan error, 1)
		go func() {
			// Start the DAG to stop.
			args := []string{"start", dag.Location}
			done <- th.ExecuteCommand(cmd.Start(), test.CmdTest{Args: args})
		}()

		// Wait for the dag-run running.
		dag.AssertLatestStatus(t, ir.Running)

		// Stop the dag-run.
		th.RunCommand(t, cmd.Stop(), test.CmdTest{
			Args:        []string{"stop", dag.Location},
			ExpectedOut: []string{"stop/cancel completed"}})

		// Check the dag-run is stopped.
		dag.AssertLatestStatus(t, ir.Aborted)
		require.NoError(t, <-done)
	})
	t.Run("StopDAGRunWithRunID", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)

		release := newHoldFile(t)
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: "1"
    run: %q
`, holdUntilFileExistsCommand(release)))

		done := make(chan error, 1)
		dagRunID := uuid.Must(uuid.NewV7()).String()
		go func() {
			// Start the dag-run to stop.
			args := []string{"start", "--run-id=" + dagRunID, dag.Location}
			done <- th.ExecuteCommand(cmd.Start(), test.CmdTest{Args: args})
		}()

		// Wait for the dag-run running
		dag.AssertLatestStatus(t, ir.Running)

		// Stop the dag-run with a specific run ID.
		th.RunCommand(t, cmd.Stop(), test.CmdTest{
			Args:        []string{"stop", dag.Location, "--run-id=" + dagRunID},
			ExpectedOut: []string{"stop/cancel completed"}})

		// Check the dag-run is stopped.
		dag.AssertLatestStatus(t, ir.Aborted)
		require.NoError(t, <-done)
	})
	t.Run("CancelFailedAutoRetryPendingDAGRunWithRunID", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)

		dag := th.DAG(t, `name: cancel-stop-retry
retry_policy:
  limit: 3
  interval_sec: 60
steps:
  - name: "1"
    run: "echo fail"
`)

		dagRunID := "failed-auto-retry-run"
		seedFailedAutoRetryPendingRun(t, th, dag, dagRunID, 1)

		th.RunCommand(t, cmd.Stop(), test.CmdTest{
			Args:        []string{"stop", dag.Location, "--run-id=" + dagRunID},
			ExpectedOut: []string{"stop/cancel completed"},
		})

		dag.AssertLatestStatus(t, ir.Aborted)
	})
	t.Run("StopWithoutRunIDDoesNotCancelFailedAutoRetryPendingDAGRun", func(t *testing.T) {
		t.Parallel()
		th := test.SetupCommand(t)

		dag := th.DAG(t, `name: stop-running-only
retry_policy:
  limit: 3
  interval_sec: 60
steps:
  - name: "1"
    run: "echo fail"
`)

		dagRunID := "failed-auto-retry-run"
		seedFailedAutoRetryPendingRun(t, th, dag, dagRunID, 1)

		th.RunCommand(t, cmd.Stop(), test.CmdTest{
			Args:        []string{"stop", dag.Location},
			ExpectedOut: []string{"No running DAG runs found"},
		})

		dag.AssertLatestStatus(t, ir.Failed)
	})
}

func seedFailedAutoRetryPendingRun(t *testing.T, th test.Command, dag test.DAG, dagRunID string, autoRetryCount int) {
	t.Helper()

	attempt, err := th.DAGRunStore.CreateAttempt(
		th.Context,
		dag.DAG,
		time.Now(),
		dagRunID,
		dagrun.NewDAGRunAttemptOptions{},
	)
	require.NoError(t, err)

	status := dagrun.NewStatusBuilder(dag.DAG).Create(
		dagRunID,
		ir.Failed,
		0,
		time.Now().Add(-time.Minute),
		dagrun.WithAttemptID(attempt.ID()),
		dagrun.WithAutoRetryCount(autoRetryCount),
		dagrun.WithError("step failed"),
	)
	status.FinishedAt = time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339)

	require.NoError(t, attempt.Open(th.Context))
	require.NoError(t, attempt.Write(th.Context, status))
	require.NoError(t, attempt.Close(th.Context))
}
