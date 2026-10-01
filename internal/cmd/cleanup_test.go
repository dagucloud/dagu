// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/test"
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
		dag.AssertLatestStatus(t, ir.Succeeded)

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
		dag.AssertLatestStatus(t, ir.Succeeded)

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
		dag.AssertLatestStatus(t, ir.Succeeded)

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
		dag.AssertLatestStatus(t, ir.Running)

		// Try to cleanup while running (nothing to delete since only active run exists)
		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--yes", dag.Name},
		})

		// Verify the running DAG is still there (should be preserved)
		dag.AssertLatestStatus(t, ir.Running)

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

	t.Run("ClearsReplayCachesWhenHistoryIsDeleted", func(t *testing.T) {
		t.Parallel()

		// Default cleanup and explicit zero retention both delete every
		// completed run, matching `dagu rm --history`, and clear this DAG's
		// browser and computer replay caches. Another DAG's caches stay.
		tests := []struct {
			name string
			args []string
		}{
			{name: "Default", args: []string{"cleanup", "--yes"}},
			{name: "RetentionZero", args: []string{"cleanup", "--retention-days", "0", "--yes"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				th := test.SetupCommand(t)
				dag := completedEchoDAG(t, th)
				const otherDAG = "other-workflow"
				seedBothReplayCaches(t, th, dag.Name)
				seedBothReplayCaches(t, th, otherDAG)

				th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
					Args: append(tt.args, dag.Name),
				})

				dag.AssertDAGRunCount(t, 0)
				assert.Empty(t, browserReplayCacheSteps(t, th, dag.Name))
				assert.Empty(t, computerReplayCacheSteps(t, th, dag.Name))
				assert.Equal(t, []string{"login"}, browserReplayCacheSteps(t, th, otherDAG))
				assert.Equal(t, []string{"post"}, computerReplayCacheSteps(t, th, otherDAG))
			})
		}
	})

	t.Run("PreservesReplayCachesWithRetentionDays", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := completedEchoDAG(t, th)
		seedBothReplayCaches(t, th, dag.Name)

		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--retention-days", "30", "--yes", dag.Name},
		})

		dag.AssertDAGRunCount(t, 1)
		assert.Equal(t, []string{"login"}, browserReplayCacheSteps(t, th, dag.Name))
		assert.Equal(t, []string{"post"}, computerReplayCacheSteps(t, th, dag.Name))
	})

	t.Run("ClearsReplayCachesWithoutHistory", func(t *testing.T) {
		t.Parallel()

		// A full-history cleanup still clears seeded caches when there is no
		// run record to remove.
		tests := []struct {
			name string
			args []string
		}{
			{name: "Default", args: []string{"cleanup", "--yes"}},
			{name: "RetentionZero", args: []string{"cleanup", "--retention-days", "0", "--yes"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				th := test.SetupCommand(t)
				const (
					dagName  = "no-history"
					otherDAG = "other-workflow"
				)
				seedBothReplayCaches(t, th, dagName)
				seedBothReplayCaches(t, th, otherDAG)

				th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
					Args: append(tt.args, dagName),
				})

				assert.Empty(t, browserReplayCacheSteps(t, th, dagName))
				assert.Empty(t, computerReplayCacheSteps(t, th, dagName))
				assert.Equal(t, []string{"login"}, browserReplayCacheSteps(t, th, otherDAG))
				assert.Equal(t, []string{"post"}, computerReplayCacheSteps(t, th, otherDAG))
			})
		}
	})

	t.Run("EmptyReplayCachesAreNoOp", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		const dagName = "empty-caches"

		th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
			Args: []string{"cleanup", "--yes", dagName},
		})

		assert.Empty(t, browserReplayCacheSteps(t, th, dagName))
		assert.Empty(t, computerReplayCacheSteps(t, th, dagName))
	})

	// Preview lines are printed with fmt.Printf. The command harness records
	// logs only, so these tests read process stdout and must not run in
	// parallel.
	t.Run("DryRunListsReplayCaches", func(t *testing.T) {
		tests := []struct {
			name string
			args []string
		}{
			{name: "Default", args: []string{"cleanup", "--dry-run"}},
			{name: "RetentionZero", args: []string{"cleanup", "--retention-days", "0", "--dry-run"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				th := test.SetupCommand(t)
				dag := completedEchoDAG(t, th)
				seedBothReplayCaches(t, th, dag.Name)

				out := captureStdout(t, func() {
					th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
						Args: append(tt.args, dag.Name),
					})
				})

				dag.AssertDAGRunCount(t, 1)
				assert.Equal(t, []string{"login"}, browserReplayCacheSteps(t, th, dag.Name))
				assert.Equal(t, []string{"post"}, computerReplayCacheSteps(t, th, dag.Name))
				assert.Contains(t, out, fmt.Sprintf("Dry run: Would delete 1 run(s) for DAG %q:", dag.Name))
				assert.Contains(t, out, fmt.Sprintf("Dry run: Would also delete browser replay cache for 1 step(s) of DAG %q", dag.Name))
				assert.Contains(t, out, fmt.Sprintf("Dry run: Would also delete computer replay cache for 1 step(s) of DAG %q", dag.Name))
			})
		}
	})

	t.Run("DryRunWithRetentionDaysOmitsReplayCaches", func(t *testing.T) {
		th := test.SetupCommand(t)
		dag := completedEchoDAG(t, th)
		seedBothReplayCaches(t, th, dag.Name)

		out := captureStdout(t, func() {
			th.RunCommand(t, cmd.Cleanup(), test.CmdTest{
				Args: []string{"cleanup", "--retention-days", "30", "--dry-run", dag.Name},
			})
		})

		dag.AssertDAGRunCount(t, 1)
		assert.Equal(t, []string{"login"}, browserReplayCacheSteps(t, th, dag.Name))
		assert.Equal(t, []string{"post"}, computerReplayCacheSteps(t, th, dag.Name))
		assert.Contains(t, out, fmt.Sprintf("Dry run: No runs to delete for DAG %q", dag.Name))
		assert.NotContains(t, out, "replay cache")
	})
}

func TestCleanupCommandRepository(t *testing.T) {
	t.Parallel()

	// Test cleanup through the DAG-run repository to verify the public behavior.
	t.Run("RemoveOldDAGRuns", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		dagName := "test-cleanup-dag"

		// Create old DAG runs through the repository.
		oldTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		recentTime := time.Now()

		// Create a minimal DAG for the test
		testDAG := &ir.DAG{Name: dagName}

		// Create an old run
		oldAttempt, err := th.DAGRunRepository.CreateAttempt(
			th.Context,
			testDAG,
			oldTime,
			"old-run-id",
			persis.DAGRunCreateAttemptOptions{},
		)
		require.NoError(t, err)
		require.NoError(t, oldAttempt.Open(th.Context))
		require.NoError(t, oldAttempt.Write(th.Context, ir.DAGRunStatus{
			Name:     dagName,
			DAGRunID: "old-run-id",
			Status:   ir.Succeeded,
		}))
		require.NoError(t, oldAttempt.Close(th.Context))

		// Create a recent run
		recentAttempt, err := th.DAGRunRepository.CreateAttempt(
			th.Context,
			testDAG,
			recentTime,
			"recent-run-id",
			persis.DAGRunCreateAttemptOptions{},
		)
		require.NoError(t, err)
		require.NoError(t, recentAttempt.Open(th.Context))
		require.NoError(t, recentAttempt.Write(th.Context, ir.DAGRunStatus{
			Name:     dagName,
			DAGRunID: "recent-run-id",
			Status:   ir.Succeeded,
		}))
		require.NoError(t, recentAttempt.Close(th.Context))

		// Manually set old file modification time
		setOldModTime(t, th.Config.Paths.DAGRunsDir, dagName, "", oldTime)

		// Verify both runs exist
		statuses, err := th.DAGRunRepository.RecentStatuses(th.Context, dagName, 10)
		require.NoError(t, err)
		require.Len(t, statuses, 2)

		// Remove runs older than 7 days
		removedIDs, err := th.DAGRunRepository.RemoveOldDAGRuns(th.Context, dagName, 7, persis.DAGRunRetentionOptions{})
		require.NoError(t, err)
		assert.Len(t, removedIDs, 1)

		// Verify old run is deleted, recent run remains
		statuses, err = th.DAGRunRepository.RecentStatuses(th.Context, dagName, 10)
		require.NoError(t, err)
		require.Len(t, statuses, 1)
		assert.Equal(t, "recent-run-id", statuses[0].DAGRunID)
	})
}

// completedEchoDAG starts a one-step DAG and waits until it succeeds.
func completedEchoDAG(t *testing.T, th test.Command) test.DAG {
	t.Helper()

	dag := th.DAG(t, `steps:
  - name: "1"
    run: echo "hello"
`)
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", dag.Location},
	})
	dag.AssertLatestStatus(t, ir.Succeeded)
	dag.AssertDAGRunCount(t, 1)
	return dag
}

func seedBothReplayCaches(t *testing.T, th test.Command, dagName string) {
	t.Helper()
	seedBrowserReplayCache(t, th, dagName, "login")
	seedComputerReplayCache(t, th, dagName, "post")
}

// captureStdout runs fn with process stdout redirected and returns what it
// wrote. Removal messages use fmt.Printf, which the command harness does not
// record. Callers must not be parallel tests. Cleanup restores the descriptor
// when fn stops before the pipe is read.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = writer
	closed := false
	closeWriter := func() {
		if closed {
			return
		}
		closed = true
		os.Stdout = original
		require.NoError(t, writer.Close())
	}
	t.Cleanup(func() {
		closeWriter()
		require.NoError(t, reader.Close())
	})

	fn()
	closeWriter()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, reader)
	require.NoError(t, err)
	return buf.String()
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
