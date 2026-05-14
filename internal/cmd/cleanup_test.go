// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupCommand(t *testing.T) {
	t.Run("DeletesAllHistoryWithRetentionZero", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		// Create a DAG and run it to generate history
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
		// Run the DAG to create history
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})

		// Wait for DAG to complete
		dag.AssertLatestStatus(t, core.Succeeded)

		// Verify history exists
		dag.AssertDAGRunCount(t, 1)

		// Run cleanup with --yes to skip confirmation
		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--yes", dag.Name},
		})

		// Verify history is deleted
		dag.AssertDAGRunCount(t, 0)
	})

	t.Run("PreservesRecentHistoryWithRetentionDays", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		// Create a DAG and run it
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
		// Run the DAG to create history
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})

		// Wait for DAG to complete
		dag.AssertLatestStatus(t, core.Succeeded)

		// Verify history exists
		dag.AssertDAGRunCount(t, 1)

		// Run cleanup with retention of 30 days (should keep recent history)
		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--retention-days", "30", "--yes", dag.Name},
		})

		// Verify history is still there (it's less than 30 days old)
		dag.AssertDAGRunCount(t, 1)
	})

	t.Run("DryRunDoesNotDelete", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		// Create a DAG and run it
		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
		// Run the DAG to create history
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", dag.Location},
		})

		// Wait for DAG to complete
		dag.AssertLatestStatus(t, core.Succeeded)

		// Verify history exists
		dag.AssertDAGRunCount(t, 1)

		// Run cleanup with --dry-run
		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--dry-run", dag.Name},
		})

		// Verify history is still there (dry run should not delete)
		dag.AssertDAGRunCount(t, 1)
	})

	t.Run("PreservesActiveRuns", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		// Create a DAG that runs for a while
		release := newHoldFile(t)
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - name: "1"
    run: %q
`, holdUntilFileExistsCommand(release)))

		done := make(chan struct{})
		go func() {
			// Start the DAG
			th.RunCommand(t, cmd.Start(), test.CmdTest{
				Args: []string{"start", dag.Location},
			})
			close(done)
		}()

		// Wait for DAG to start running
		dag.AssertLatestStatus(t, core.Running)

		// Try to cleanup while running (nothing to delete since only active run exists)
		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--yes", dag.Name},
		})

		// Verify the running DAG is still there (should be preserved)
		dag.AssertLatestStatus(t, core.Running)

		releaseHoldFile(t, release)
		<-done
	})

	t.Run("RejectsNegativeRetentionDays", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)

		err := th.RunCommandWithError(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--retention-days", "-1", dag.Name},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be negative")
	})

	t.Run("RejectsInvalidRetentionDays", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)

		err := th.RunCommandWithError(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--retention-days", "abc", dag.Name},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid retention-days")
	})

	t.Run("RequiresDAGNameArgument", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		err := th.RunCommandWithError(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--yes"},
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "accepts 1 arg")
	})

	t.Run("SucceedsForNonExistentDAG", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		// Cleanup for a DAG that doesn't exist should succeed silently
		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--yes", "non-existent-dag"},
		})
	})
}

func TestCleanupCommandDirectStore(t *testing.T) {
	t.Parallel()

	// Test cleanup using the DAGRunStore directly to verify underlying behavior
	t.Run("RemoveOldDAGRunsWithStore", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		dagName := "test-cleanup-dag"

		// Create old DAG runs directly in the store
		oldTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		recentTime := time.Now()

		// Create a minimal DAG for the test
		testDAG := &core.DAG{Name: dagName}

		// Create an old run
		oldAttempt, err := th.DAGRunStore.CreateAttempt(
			th.Context,
			testDAG,
			oldTime,
			"old-run-id",
			exec.NewDAGRunAttemptOptions{},
		)
		require.NoError(t, err)
		require.NoError(t, oldAttempt.Open(th.Context))
		require.NoError(t, oldAttempt.Write(th.Context, exec.DAGRunStatus{
			Name:     dagName,
			DAGRunID: "old-run-id",
			Status:   core.Succeeded,
		}))
		require.NoError(t, oldAttempt.Close(th.Context))

		// Create a recent run
		recentAttempt, err := th.DAGRunStore.CreateAttempt(
			th.Context,
			testDAG,
			recentTime,
			"recent-run-id",
			exec.NewDAGRunAttemptOptions{},
		)
		require.NoError(t, err)
		require.NoError(t, recentAttempt.Open(th.Context))
		require.NoError(t, recentAttempt.Write(th.Context, exec.DAGRunStatus{
			Name:     dagName,
			DAGRunID: "recent-run-id",
			Status:   core.Succeeded,
		}))
		require.NoError(t, recentAttempt.Close(th.Context))

		// Manually set old file modification time
		setOldModTime(t, th.Config.Paths.DAGRunsDir, dagName, "", oldTime)

		// Verify both runs exist
		runs := th.DAGRunStore.RecentAttempts(th.Context, dagName, 10)
		require.Len(t, runs, 2)

		// Remove runs older than 7 days
		removedIDs, err := th.DAGRunStore.RemoveOldDAGRuns(th.Context, dagName, 7)
		require.NoError(t, err)
		assert.Len(t, removedIDs, 1)

		// Verify old run is deleted, recent run remains
		runs = th.DAGRunStore.RecentAttempts(th.Context, dagName, 10)
		require.Len(t, runs, 1)

		status, err := runs[0].ReadStatus(th.Context)
		require.NoError(t, err)
		assert.Equal(t, "recent-run-id", status.DAGRunID)
	})
}

// setOldModTime sets old modification time on DAG run files
func setOldModTime(t *testing.T, baseDir, dagName, _ string, modTime time.Time) {
	t.Helper()

	// Find the run directory
	dagRunsDir := filepath.Join(baseDir, dagName, "dag-runs")
	err := filepath.Walk(dagRunsDir, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Set mod time on all files and directories
		return os.Chtimes(path, modTime, modTime)
	})
	// Ignore errors if directory doesn't exist
	if err != nil && !os.IsNotExist(err) {
		t.Logf("Warning: failed to set mod time: %v", err)
	}
}
