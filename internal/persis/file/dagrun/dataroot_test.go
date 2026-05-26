// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun/dagrunindex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataRoot(t *testing.T) {
	t.Run("NewDataRoot", func(t *testing.T) {
		t.Parallel()

		t.Run("BasicName", func(t *testing.T) {
			baseDir := "/tmp"
			dagName := "test-dag"

			dr := NewDataRoot(baseDir, dagName)

			assert.Equal(t, "test-dag", dr.prefix, "prefix should be set correctly")
			assert.Equal(t, filepath.Join(baseDir, "test-dag", "dag-runs"), dr.dagRunsDir, "path should be set correctly")
			assert.Equal(t, filepath.Join(baseDir, "test-dag", "dag-runs", "*", "*", "*", DAGRunDirPrefix+"*"), dr.globPattern, "globPattern should be set correctly")
		})

		t.Run("WithYAMLExtension", func(t *testing.T) {
			baseDir := "/tmp"
			dagName := "test-dag.yaml"

			dr := NewDataRoot(baseDir, dagName)

			assert.Equal(t, "test-dag", dr.prefix, "prefix should have extension removed")
		})

		t.Run("WithUnsafeName", func(t *testing.T) {
			baseDir := "/tmp"
			dagName := "test/dag with spaces.yaml"

			dr := NewDataRoot(baseDir, dagName)

			// Check that the prefix is sanitized (doesn't contain unsafe characters)
			// The SafeName function converts to lowercase and replaces unsafe chars with _
			sanitizedPrefix := "dag_with_spaces"
			assert.True(t, strings.HasPrefix(dr.prefix, sanitizedPrefix), "prefix should be sanitized")

			// Check that there's a hash suffix
			hashSuffix := dr.prefix[len(sanitizedPrefix):]
			assert.True(t, len(hashSuffix) > 0, "prefix should include hash")

			// The hash length might vary based on implementation, so we just check it exists
			assert.True(t, len(hashSuffix) > 1, "hash suffix should be at least 2 characters")
		})
	})
}

func TestDataRootRuns(t *testing.T) {
	t.Parallel()

	t.Run("FindByDAGRunID", func(t *testing.T) {
		ts := exec.NewUTC(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
		ctx := context.Background()

		dr := setupTestDataRoot(t)
		dagRun := dr.CreateTestDAGRun(t, "test-id1", ts)
		_ = dr.CreateTestDAGRun(t, "test-id2", ts)

		actual, err := dr.FindByDAGRunID(ctx, "test-id1")
		require.NoError(t, err)

		assert.Equal(t, dagRun.DAGRun, actual, "FindByDAGRunID should return the correct run")
	})

	t.Run("FindByDAGRunIDReturnsNewestDuplicate", func(t *testing.T) {
		ctx := context.Background()
		dr := setupTestDataRoot(t)

		oldRun := dr.CreateTestDAGRun(t, "duplicate-id", exec.NewUTC(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)))
		newRun := dr.CreateTestDAGRun(t, "duplicate-id", exec.NewUTC(time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)))

		actual, err := dr.FindByDAGRunID(ctx, "duplicate-id")
		require.NoError(t, err)

		assert.NotEqual(t, oldRun.baseDir, actual.baseDir, "older duplicate should not be returned")
		assert.Equal(t, newRun.baseDir, actual.baseDir, "newest duplicate should be returned")
	})

	t.Run("FindByDAGRunIDReturnsNewestDuplicateOnSameDay", func(t *testing.T) {
		ctx := context.Background()
		dr := setupTestDataRoot(t)

		oldRun := dr.CreateTestDAGRun(t, "same-day-duplicate-id", exec.NewUTC(time.Date(2021, 1, 2, 1, 0, 0, 0, time.UTC)))
		newRun := dr.CreateTestDAGRun(t, "same-day-duplicate-id", exec.NewUTC(time.Date(2021, 1, 2, 2, 0, 0, 0, time.UTC)))

		actual, err := dr.FindByDAGRunID(ctx, "same-day-duplicate-id")
		require.NoError(t, err)

		assert.NotEqual(t, oldRun.baseDir, actual.baseDir, "older same-day duplicate should not be returned")
		assert.Equal(t, newRun.baseDir, actual.baseDir, "newest same-day duplicate should be returned")
	})

	t.Run("FindByDAGRunIDUsesExactMatch", func(t *testing.T) {
		ctx := context.Background()
		dr := setupTestDataRoot(t)

		exactRun := dr.CreateTestDAGRun(t, "123", exec.NewUTC(time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)))
		_ = dr.CreateTestDAGRun(t, "foo_123", exec.NewUTC(time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC)))

		actual, err := dr.FindByDAGRunID(ctx, "123")
		require.NoError(t, err)

		assert.Equal(t, exactRun.baseDir, actual.baseDir, "suffix matches should not win over exact dag-run ID matches")
	})

	t.Run("FindByDAGRunIDNotFound", func(t *testing.T) {
		ctx := context.Background()
		dr := setupTestDataRoot(t)

		_, err := dr.FindByDAGRunID(ctx, "missing-id")
		require.ErrorIs(t, err, exec.ErrDAGRunIDNotFound)
	})

	t.Run("FindByDAGRunIDHonorsCanceledContext", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		dr := setupTestDataRoot(t)
		_ = dr.CreateTestDAGRun(t, "test-id1", exec.NewUTC(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)))

		_, err := dr.FindByDAGRunID(ctx, "test-id1")
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("Latest", func(t *testing.T) {
		root := setupTestDataRoot(t)

		ts1 := exec.NewUTC(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
		ts2 := exec.NewUTC(time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC))
		ts3 := exec.NewUTC(time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC))

		_ = root.CreateTestDAGRun(t, "test-id1", ts1)
		_ = root.CreateTestDAGRun(t, "test-id2", ts2)
		_ = root.CreateTestDAGRun(t, "test-id3", ts3)

		runs := root.Latest(context.Background(), 2)
		require.Len(t, runs, 2)

		assert.Equal(t, "test-id3", runs[0].dagRunID, "Latest should return the most recent runs")
	})

	t.Run("LatestAfter", func(t *testing.T) {
		root := setupTestDataRoot(t)

		ts1 := exec.NewUTC(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
		ts2 := exec.NewUTC(time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC))
		ts3 := exec.NewUTC(time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC))
		ts4 := exec.NewUTC(time.Date(2021, 1, 3, 0, 0, 0, 1, time.UTC))

		_ = root.CreateTestDAGRun(t, "test-id1", ts1)
		_ = root.CreateTestDAGRun(t, "test-id2", ts2)
		latest := root.CreateTestDAGRun(t, "test-id3", ts3)

		_, err := root.LatestAfter(context.Background(), ts4)
		require.ErrorIs(t, err, exec.ErrNoStatusData, "LatestAfter should return ErrNoStatusData when no runs are found")

		run, err := root.LatestAfter(context.Background(), ts3)
		require.NoError(t, err)

		assert.Equal(t, *latest.DAGRun, *run, "LatestAfter should return the most recent run after the given timestamp")
	})

	t.Run("ListInRange", func(t *testing.T) {
		root := setupTestDataRoot(t)

		for date := 1; date <= 31; date++ {
			for hour := range 24 {
				ts := exec.NewUTC(time.Date(2021, 1, date, hour, 0, 0, 0, time.UTC))
				_ = root.CreateTestDAGRun(t, fmt.Sprintf("test-id-%d-%d", date, hour), ts)
			}
		}

		// list between 2021-01-01 05:00 and 2021-01-02 02:00
		start := exec.NewUTC(time.Date(2021, 1, 1, 5, 0, 0, 0, time.UTC))
		end := exec.NewUTC(time.Date(2021, 1, 2, 2, 0, 0, 0, time.UTC))

		result := root.listDAGRunsInRange(context.Background(), start, end, &listDAGRunsInRangeOpts{})
		require.Len(t, result, 21, "ListInRange should return the correct")

		// Check the first and last timestamps
		first := result[0]
		assert.Equal(t, "2021-01-02 01:00", first.timestamp.Format("2006-01-02 15:04"))

		last := result[len(result)-1]
		assert.Equal(t, "2021-01-01 05:00", last.timestamp.Format("2006-01-02 15:04"))
	})
}

func TestDataRootRemoveOld(t *testing.T) {
	t.Run("RemoveAllWhenRetentionIsZero", func(t *testing.T) {
		root := setupTestDataRoot(t)

		// Use old timestamps like the working store_test.go
		ts1 := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		ts2 := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)

		// Create dag-runs with old timestamps
		dagRun1 := root.CreateTestDAGRun(t, "dag-run-1", exec.NewUTC(ts1))
		dagRun2 := root.CreateTestDAGRun(t, "dag-run-2", exec.NewUTC(ts2))

		// Create actual attempts with status data using old timestamps
		createAttemptWithStatus := func(dagRunTest DAGRunTest, ts time.Time) *Attempt {
			attempt, err := dagRunTest.CreateAttempt(root.Context, exec.NewUTC(ts), nil, "")
			require.NoError(t, err)
			require.NoError(t, attempt.Open(root.Context))
			status := exec.DAGRunStatus{
				Name:     "test-dag",
				DAGRunID: dagRunTest.dagRunID,
				Status:   core.Succeeded,
			}
			require.NoError(t, attempt.Write(root.Context, status))
			require.NoError(t, attempt.Close(root.Context))

			// Set the file modification time to match the old timestamp
			err = os.Chtimes(attempt.file, ts, ts)
			require.NoError(t, err)

			return attempt
		}

		createAttemptWithStatus(dagRun1, ts1)
		createAttemptWithStatus(dagRun2, ts2)

		// Verify dag-runs exist
		assert.True(t, fileutil.FileExists(dagRun1.baseDir), "dag-run 1 should exist before cleanup")
		assert.True(t, fileutil.FileExists(dagRun2.baseDir), "dag-run 2 should exist before cleanup")

		// Remove all dag-runs (retention = 0)
		removedIDs, err := root.RemoveOld(root.Context, 0, false)
		require.NoError(t, err)
		assert.Len(t, removedIDs, 2)

		// Verify all dag-runs are removed
		assert.False(t, fileutil.FileExists(dagRun1.baseDir), "dag-run 1 should be removed")
		assert.False(t, fileutil.FileExists(dagRun2.baseDir), "dag-run 2 should be removed")
	})

	t.Run("KeepRecentWhenRetentionIsPositive", func(t *testing.T) {
		root := setupTestDataRoot(t)

		// Create dag-runs: one old and one recent
		oldTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		recentTime := time.Now().AddDate(0, 0, -1) // 1 day ago

		dagRun1 := root.CreateTestDAGRun(t, "old-dag-run", exec.NewUTC(oldTime))
		dagRun2 := root.CreateTestDAGRun(t, "recent-dag-run", exec.NewUTC(recentTime))

		// Create actual attempts with status data
		createAttemptWithStatus := func(dagRunTest DAGRunTest, ts time.Time) *Attempt {
			attempt, err := dagRunTest.CreateAttempt(root.Context, exec.NewUTC(ts), nil, "")
			require.NoError(t, err)
			require.NoError(t, attempt.Open(root.Context))
			status := exec.DAGRunStatus{
				Name:     "test-dag",
				DAGRunID: dagRunTest.dagRunID,
				Status:   core.Succeeded,
			}
			require.NoError(t, attempt.Write(root.Context, status))
			require.NoError(t, attempt.Close(root.Context))

			// Set the file modification time to match the timestamp
			err = os.Chtimes(attempt.file, ts, ts)
			require.NoError(t, err)

			return attempt
		}

		createAttemptWithStatus(dagRun1, oldTime)
		createAttemptWithStatus(dagRun2, recentTime)

		// Verify dag-runs exist
		assert.True(t, fileutil.FileExists(dagRun1.baseDir), "Old dag-run should exist before cleanup")
		assert.True(t, fileutil.FileExists(dagRun2.baseDir), "Recent dag-run should exist before cleanup")

		// Remove dag-runs older than 7 days (should remove old but keep recent)
		removedIDs, err := root.RemoveOld(root.Context, 7, false)
		require.NoError(t, err)
		assert.Len(t, removedIDs, 1)

		// Verify old dag-run is removed but recent one is kept
		assert.False(t, fileutil.FileExists(dagRun1.baseDir), "Old dag-run should be removed")
		assert.True(t, fileutil.FileExists(dagRun2.baseDir), "Recent dag-run should be kept")
	})

	t.Run("KeepNewestRunsWhenRetentionRunsIsPositive", func(t *testing.T) {
		root := setupTestDataRoot(t)

		times := []time.Time{
			time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC),
			time.Date(2021, 1, 3, 0, 0, 0, 0, time.UTC),
			time.Date(2021, 1, 4, 0, 0, 0, 0, time.UTC),
		}
		dagRuns := make([]DAGRunTest, 0, len(times))
		for i, ts := range times {
			dagRun := root.CreateTestDAGRun(t, fmt.Sprintf("dag-run-%d", i+1), exec.NewUTC(ts))
			attempt, err := dagRun.CreateAttempt(root.Context, exec.NewUTC(ts), nil, "")
			require.NoError(t, err)
			require.NoError(t, attempt.Open(root.Context))
			status := exec.DAGRunStatus{
				Name:     "test-dag",
				DAGRunID: dagRun.dagRunID,
				Status:   core.Succeeded,
			}
			require.NoError(t, attempt.Write(root.Context, status))
			require.NoError(t, attempt.Close(root.Context))
			require.NoError(t, os.Chtimes(attempt.file, ts, ts))
			dagRuns = append(dagRuns, dagRun)
		}

		removedIDs, err := root.RemoveOldByRuns(root.Context, 2, false)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"dag-run-1", "dag-run-2"}, removedIDs)

		assert.False(t, fileutil.FileExists(dagRuns[0].baseDir), "oldest dag-run should be removed")
		assert.False(t, fileutil.FileExists(dagRuns[1].baseDir), "second oldest dag-run should be removed")
		assert.True(t, fileutil.FileExists(dagRuns[2].baseDir), "second newest dag-run should be kept")
		assert.True(t, fileutil.FileExists(dagRuns[3].baseDir), "newest dag-run should be kept")
	})

	t.Run("RetentionRunsDryRunDoesNotDelete", func(t *testing.T) {
		root := setupTestDataRoot(t)

		oldTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		newTime := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		oldRun := root.CreateTestDAGRun(t, "old-dag-run", exec.NewUTC(oldTime))
		newRun := root.CreateTestDAGRun(t, "new-dag-run", exec.NewUTC(newTime))

		for _, item := range []struct {
			run DAGRunTest
			ts  time.Time
		}{
			{run: oldRun, ts: oldTime},
			{run: newRun, ts: newTime},
		} {
			attempt, err := item.run.CreateAttempt(root.Context, exec.NewUTC(item.ts), nil, "")
			require.NoError(t, err)
			require.NoError(t, attempt.Open(root.Context))
			require.NoError(t, attempt.Write(root.Context, exec.DAGRunStatus{
				Name:     "test-dag",
				DAGRunID: item.run.dagRunID,
				Status:   core.Succeeded,
			}))
			require.NoError(t, attempt.Close(root.Context))
		}

		removedIDs, err := root.RemoveOldByRuns(root.Context, 1, true)
		require.NoError(t, err)
		assert.Equal(t, []string{"old-dag-run"}, removedIDs)
		assert.True(t, fileutil.FileExists(oldRun.baseDir), "dry-run should not delete old dag-run")
		assert.True(t, fileutil.FileExists(newRun.baseDir), "dry-run should keep new dag-run")
	})

	t.Run("RetentionRunsPreservesActiveRunsOutsideWindow", func(t *testing.T) {
		root := setupTestDataRoot(t)

		oldTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		newTime := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		activeRun := root.CreateTestDAGRun(t, "active-old-run", exec.NewUTC(oldTime))
		newRun := root.CreateTestDAGRun(t, "new-dag-run", exec.NewUTC(newTime))

		attempt, err := activeRun.CreateAttempt(root.Context, exec.NewUTC(oldTime), nil, "")
		require.NoError(t, err)
		require.NoError(t, attempt.Open(root.Context))
		require.NoError(t, attempt.Write(root.Context, exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: activeRun.dagRunID,
			Status:   core.Running,
		}))
		require.NoError(t, attempt.Close(root.Context))

		attempt, err = newRun.CreateAttempt(root.Context, exec.NewUTC(newTime), nil, "")
		require.NoError(t, err)
		require.NoError(t, attempt.Open(root.Context))
		require.NoError(t, attempt.Write(root.Context, exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: newRun.dagRunID,
			Status:   core.Succeeded,
		}))
		require.NoError(t, attempt.Close(root.Context))

		removedIDs, err := root.RemoveOldByRuns(root.Context, 1, false)
		require.NoError(t, err)
		assert.Empty(t, removedIDs)
		assert.True(t, fileutil.FileExists(activeRun.baseDir), "active dag-run should be preserved")
		assert.True(t, fileutil.FileExists(newRun.baseDir), "newest dag-run should be kept")
	})

	t.Run("RetentionRunsPreservesRunsWithNewerStatuslessAttempts", func(t *testing.T) {
		root := setupTestDataRoot(t)

		oldTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		newTime := time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC)
		oldRun := root.CreateTestDAGRun(t, "old-dag-run", exec.NewUTC(oldTime))
		newRun := root.CreateTestDAGRun(t, "new-dag-run", exec.NewUTC(newTime))

		attempt, err := oldRun.CreateAttempt(root.Context, exec.NewUTC(oldTime), nil, "")
		require.NoError(t, err)
		require.NoError(t, attempt.Open(root.Context))
		require.NoError(t, attempt.Write(root.Context, exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: oldRun.dagRunID,
			Status:   core.Succeeded,
		}))
		require.NoError(t, attempt.Close(root.Context))

		_, err = oldRun.CreateAttempt(root.Context, exec.NewUTC(oldTime.Add(time.Hour)), nil, "")
		require.NoError(t, err)

		attempt, err = newRun.CreateAttempt(root.Context, exec.NewUTC(newTime), nil, "")
		require.NoError(t, err)
		require.NoError(t, attempt.Open(root.Context))
		require.NoError(t, attempt.Write(root.Context, exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: newRun.dagRunID,
			Status:   core.Succeeded,
		}))
		require.NoError(t, attempt.Close(root.Context))

		removedIDs, err := root.RemoveOldByRuns(root.Context, 1, false)
		require.NoError(t, err)
		assert.Empty(t, removedIDs)
		assert.True(t, fileutil.FileExists(oldRun.baseDir), "dag-run with a newer statusless attempt should be preserved")
		assert.True(t, fileutil.FileExists(newRun.baseDir), "newest dag-run should be kept")
	})

	t.Run("RemoveEmptyDirectories", func(t *testing.T) {
		root := setupTestDataRoot(t)

		// Create dag-runs in different date directories
		date1 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		date2 := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)

		dagRun1 := root.CreateTestDAGRun(t, "dag-run-1", exec.NewUTC(date1))
		dagRun2 := root.CreateTestDAGRun(t, "dag-run-2", exec.NewUTC(date2))

		// Create actual attempts with status data
		createAttemptWithStatus := func(dagRunTest DAGRunTest, ts time.Time) *Attempt {
			attempt, err := dagRunTest.CreateAttempt(root.Context, exec.NewUTC(ts), nil, "")
			require.NoError(t, err)
			require.NoError(t, attempt.Open(root.Context))
			status := exec.DAGRunStatus{
				Name:     "test-dag",
				DAGRunID: dagRunTest.dagRunID,
				Status:   core.Succeeded,
			}
			require.NoError(t, attempt.Write(root.Context, status))
			require.NoError(t, attempt.Close(root.Context))

			// Set the file modification time to match the old timestamp
			err = os.Chtimes(attempt.file, ts, ts)
			require.NoError(t, err)

			return attempt
		}

		createAttemptWithStatus(dagRun1, date1)
		createAttemptWithStatus(dagRun2, date2)

		// Verify directory structure exists
		assert.True(t, fileutil.FileExists(dagRun1.baseDir), "dag-run 1 should exist")
		assert.True(t, fileutil.FileExists(dagRun2.baseDir), "dag-run 2 should exist")

		// Remove all old dag-runs (retention = 0)
		removedIDs, err := root.RemoveOld(root.Context, 0, false)
		require.NoError(t, err)
		assert.Len(t, removedIDs, 2)

		// Verify dag-runs are removed
		assert.False(t, fileutil.FileExists(dagRun1.baseDir), "dag-run 1 should be removed")
		assert.False(t, fileutil.FileExists(dagRun2.baseDir), "dag-run 2 should be removed")

		// Verify that the cleanup also removes empty directories
		// The method should clean up empty year/month/day directories
		assert.True(t, root.IsEmpty(), "Root should be empty after cleanup")
	})

	t.Run("PreserveWaitStatusDAGRuns", func(t *testing.T) {
		root := setupTestDataRoot(t)

		// Create old dag-runs: one completed (should be deleted), one waiting (should be preserved)
		oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

		completedRun := root.CreateTestDAGRun(t, "completed-run", exec.NewUTC(oldTime))
		waitingRun := root.CreateTestDAGRun(t, "waiting-run", exec.NewUTC(oldTime))

		// Create attempts with different statuses
		createAttemptWithStatusType := func(dagRunTest DAGRunTest, ts time.Time, status core.Status) *Attempt {
			attempt, err := dagRunTest.CreateAttempt(root.Context, exec.NewUTC(ts), nil, "")
			require.NoError(t, err)
			require.NoError(t, attempt.Open(root.Context))
			dagStatus := exec.DAGRunStatus{
				Name:     "test-dag",
				DAGRunID: dagRunTest.dagRunID,
				Status:   status,
			}
			require.NoError(t, attempt.Write(root.Context, dagStatus))
			require.NoError(t, attempt.Close(root.Context))

			// Set the file modification time to match the old timestamp
			err = os.Chtimes(attempt.file, ts, ts)
			require.NoError(t, err)

			return attempt
		}

		createAttemptWithStatusType(completedRun, oldTime, core.Succeeded)
		createAttemptWithStatusType(waitingRun, oldTime, core.Waiting)

		// Verify dag-runs exist
		assert.True(t, fileutil.FileExists(completedRun.baseDir), "Completed dag-run should exist before cleanup")
		assert.True(t, fileutil.FileExists(waitingRun.baseDir), "Waiting dag-run should exist before cleanup")

		// Remove all old dag-runs (retention = 0)
		// Wait status should be preserved because it's considered "active"
		removedIDs, err := root.RemoveOld(root.Context, 0, false)
		require.NoError(t, err)
		assert.Len(t, removedIDs, 1, "Only completed run should be removed")
		assert.Contains(t, removedIDs, "completed-run", "Completed run should be in removed list")

		// Verify completed dag-run is removed but waiting one is kept
		assert.False(t, fileutil.FileExists(completedRun.baseDir), "Completed dag-run should be removed")
		assert.True(t, fileutil.FileExists(waitingRun.baseDir), "Waiting dag-run should be preserved")
	})
	t.Run("RemoveOldRemovesArtifactDirs", func(t *testing.T) {
		root := setupTestDataRoot(t)

		oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		dagRun := root.CreateTestDAGRun(t, "artifact-run", exec.NewUTC(oldTime))

		artifactDir := filepath.Join(root.artifactDir, "artifact-run")
		require.NoError(t, os.MkdirAll(artifactDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(artifactDir, "report.md"), []byte("artifact"), 0o600))

		attempt, err := dagRun.CreateAttempt(root.Context, exec.NewUTC(oldTime), nil, "")
		require.NoError(t, err)
		require.NoError(t, attempt.Open(root.Context))

		status := exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   dagRun.dagRunID,
			Status:     core.Succeeded,
			ArchiveDir: artifactDir,
		}
		require.NoError(t, attempt.Write(root.Context, status))
		require.NoError(t, attempt.Close(root.Context))

		err = os.Chtimes(attempt.file, oldTime, oldTime)
		require.NoError(t, err)

		require.DirExists(t, artifactDir)

		removedIDs, err := root.RemoveOld(root.Context, 0, false)
		require.NoError(t, err)
		assert.Contains(t, removedIDs, "artifact-run")
		assert.NoDirExists(t, artifactDir)
	})
}

func TestDataRootRename(t *testing.T) {
	root := setupTestDataRoot(t)

	for date := 1; date <= 3; date++ {
		ts := exec.NewUTC(time.Date(2021, 1, date, 0, 0, 0, 0, time.UTC))
		_ = root.CreateTestDAGRun(t, fmt.Sprintf("test-id-%d", date), ts)
	}

	newRoot := NewDataRoot(root.baseDir, "new-dag")
	err := root.Rename(root.Context, newRoot)
	require.NoError(t, err)

	// Check that the old directory is removed
	assert.False(t, root.Exists(), "Old directory should be removed")
	// Check files are moved to the new directory
	assert.True(t, newRoot.Exists(), "New directory should exist")

	matches, err := filepath.Glob(newRoot.globPattern)
	require.NoError(t, err)
	assert.Len(t, matches, 3, "All files should be moved to the new directory")
}

func TestDataRootUtils(t *testing.T) {
	t.Parallel()

	root := setupTestDataRoot(t)

	// Directory does not exist
	assert.False(t, root.Exists(), "Exists should return false when directory does not exist")

	// Create the directory
	err := root.Create()
	require.NoError(t, err)

	// Directory exists
	exists := root.Exists()
	assert.True(t, exists, "Exists should return true when directory exists")

	// IsEmpty should return true for empty directory
	isEmpty := root.IsEmpty()
	assert.True(t, isEmpty, "IsEmpty should return true for empty directory")

	// Add a file to the directory
	root.CreateTestDAGRun(t, "test-id", exec.NewUTC(time.Now()))
	require.NoError(t, err)

	// IsEmpty should return false for non-empty directory
	isEmpty = root.IsEmpty()
	assert.False(t, isEmpty, "IsEmpty should return false for non-empty directory")

	// Remove the directory
	err = root.Remove()
	require.NoError(t, err)

	// Directory does not exist
	assert.False(t, root.Exists(), "Exists should return false when directory does not exist")
}

func TestInTimeRange(t *testing.T) {
	base := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	start := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)

	// Within range.
	assert.True(t, inTimeRange(base, start, end, false, false))

	// Before range.
	assert.False(t, inTimeRange(start.Add(-time.Hour), start, end, false, false))

	// At end boundary (exclusive).
	assert.False(t, inTimeRange(end, start, end, false, false))

	// Start zero means no lower bound.
	assert.True(t, inTimeRange(start.Add(-time.Hour), start, end, true, false))

	// End zero means no upper bound.
	assert.True(t, inTimeRange(end.Add(time.Hour), start, end, false, true))

	// Both zero.
	assert.True(t, inTimeRange(base, start, end, true, true))
}

func TestSummaryFromIndexEntry(t *testing.T) {
	entry := dagrunindex.Entry{
		DagRunDir:        "dag-run_20240115_120000Z_test",
		DagRunID:         "test-id",
		LatestAttemptDir: "attempt_20240115_120000_001Z_abc",
		Status:           core.Succeeded,
		StartedAtUnix:    1705320000,
		FinishedAtUnix:   1705320060,
		Labels:           []string{"env=prod"},
		Name:             "test-dag",
		WorkerID:         "worker-1",
		Params:           "key=val",
		QueuedAt:         "2024-01-15T12:00:00Z",
		ScheduleTime:     "2024-01-15T11:55:00Z",
		TriggerType:      core.TriggerType(1),
		CreatedAt:        1705320000000,
		LeaseAt:          1705320030000,
	}

	summary := summaryFromIndexEntry(entry)
	require.NotNil(t, summary)
	assert.Equal(t, entry.LatestAttemptDir, summary.LatestAttemptDir)
	assert.Equal(t, entry.Status, summary.Status)
	assert.Equal(t, entry.StartedAtUnix, summary.StartedAtUnix)
	assert.Equal(t, entry.FinishedAtUnix, summary.FinishedAtUnix)
	assert.Equal(t, entry.Labels, summary.Labels)
	assert.Equal(t, entry.Name, summary.Name)
	assert.Equal(t, entry.DagRunID, summary.DagRunID)
	assert.Equal(t, entry.WorkerID, summary.WorkerID)
	assert.Equal(t, entry.Params, summary.Params)
	assert.Equal(t, entry.QueuedAt, summary.QueuedAt)
	assert.Equal(t, entry.ScheduleTime, summary.ScheduleTime)
	assert.Equal(t, entry.TriggerType, summary.TriggerType)
	assert.Equal(t, entry.CreatedAt, summary.CreatedAt)
	assert.Equal(t, entry.LeaseAt, summary.LeaseAt)
}

func TestListDAGRunsInRange_IndexPath(t *testing.T) {
	root := setupTestDataRoot(t)

	// Create 12 runs on the same day with actual status files to trigger index.
	baseTime := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	for i := range 12 {
		ts := exec.NewUTC(baseTime.Add(time.Duration(i) * time.Hour))
		run := root.CreateTestDAGRun(t, fmt.Sprintf("idx-run-%d", i), ts)
		run.WriteStatus(t, ts, core.Succeeded)
	}

	start := exec.NewUTC(baseTime)
	end := exec.NewUTC(baseTime.Add(12 * time.Hour))

	result := root.listDAGRunsInRange(context.Background(), start, end, nil)
	assert.Len(t, result, 12, "should find all 12 runs via index-accelerated path")

	// Verify summaries are populated from the index.
	for _, r := range result {
		assert.NotNil(t, r.summary, "run should have summary from index")
	}
}

func TestListDAGRunsInRange_FallbackPath(t *testing.T) {
	root := setupTestDataRoot(t)

	// Create fewer than MinRunsForIndex (10) runs to stay on fallback path.
	baseTime := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		ts := exec.NewUTC(baseTime.Add(time.Duration(i) * time.Hour))
		run := root.CreateTestDAGRun(t, fmt.Sprintf("fb-run-%d", i), ts)
		run.WriteStatus(t, ts, core.Succeeded)
	}

	start := exec.NewUTC(baseTime)
	end := exec.NewUTC(baseTime.Add(5 * time.Hour))

	result := root.listDAGRunsInRange(context.Background(), start, end, nil)
	assert.Len(t, result, 5, "should find all 5 runs via fallback path")
}

func TestListDAGRunsInRange_StartAfterEnd(t *testing.T) {
	root := setupTestDataRoot(t)

	start := exec.NewUTC(time.Date(2024, 6, 16, 0, 0, 0, 0, time.UTC))
	end := exec.NewUTC(time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC))

	result := root.listDAGRunsInRange(context.Background(), start, end, nil)
	assert.Nil(t, result, "should return nil when start is after end")
}

// setupTestDataRoot creates a DataRootTest instance for testing purposes.
func setupTestDataRoot(t *testing.T) *DataRootTest {
	t.Helper()

	tmpDir := t.TempDir()
	root := NewDataRootWithArtifactDir(tmpDir, "test-dag", filepath.Join(tmpDir, "artifacts"))
	return &DataRootTest{DataRoot: root, TB: t, Context: context.Background()}
}

// DataRootTest extends DataRoot with testing utilities and context.
type DataRootTest struct {
	DataRoot
	TB      testing.TB
	Context context.Context
}

// CreateTestDAGRun creates a test dag-run with the specified ID and timestamp.
// It ensures the DataRoot directory exists before creating the dag-run.
func (drt *DataRootTest) CreateTestDAGRun(t *testing.T, dagRunID string, ts exec.TimeInUTC) DAGRunTest {
	t.Helper()

	err := drt.Create()
	require.NoError(t, err)

	run, err := drt.CreateDAGRun(ts, dagRunID)
	require.NoError(t, err)

	return DAGRunTest{
		DataRootTest: *drt,
		DAGRun:       run,
		TB:           t,
	}
}
