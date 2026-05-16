// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileproc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	baseDir := t.TempDir()
	store := New(baseDir)

	// Create a dagRun reference
	dagRun := exec.DAGRunRef{
		Name: "test_dag",
		ID:   "test_id",
	}

	// Get the process for the dag-run
	// Using different group name (queue) vs dag name to test hierarchy
	proc, err := store.Acquire(ctx, "test_queue", testProcMetaFromRun(dagRun))
	require.NoError(t, err, "failed to get proc")

	requireCountAlive(t, ctx, store, "test_queue", 1)

	err = proc.Stop(ctx)
	require.NoError(t, err, "failed to stop proc")

	requireCountAlive(t, ctx, store, "test_queue", 0)
}

func TestStore_IsRunAlive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir)

	t.Run("NoProcessFile", func(t *testing.T) {
		dagRun := exec.DAGRunRef{
			Name: "test-dag",
			ID:   "run-123",
		}

		// Test when no process file exists
		alive, err := store.IsRunAlive(ctx, "queue-1", dagRun)
		require.NoError(t, err)
		require.False(t, alive)
	})

	t.Run("AliveProcess", func(t *testing.T) {
		dagRun := exec.DAGRunRef{
			Name: "test-dag",
			ID:   "run-456",
		}

		// Create a process and start heartbeat
		// Use different group name (queue-2) vs dag name (test-dag)
		proc, err := store.Acquire(ctx, "queue-2", testProcMetaFromRun(dagRun))
		require.NoError(t, err)

		requireRunAliveState(t, ctx, store, "queue-2", dagRun, true)

		// Stop the process
		err = proc.Stop(ctx)
		require.NoError(t, err)

		requireRunAliveState(t, ctx, store, "queue-2", dagRun, false)
	})

	t.Run("DifferentRunID", func(t *testing.T) {
		// Create a process for one run ID
		dagRun1 := exec.DAGRunRef{
			Name: "test-dag-3",
			ID:   "run-789",
		}
		proc1, err := store.Acquire(ctx, "queue-3", testProcMetaFromRun(dagRun1))
		require.NoError(t, err)

		requireRunAliveState(t, ctx, store, "queue-3", dagRun1, true)

		// Check for a different run ID
		dagRun2 := exec.DAGRunRef{
			Name: "test-dag-3",
			ID:   "run-999",
		}
		alive, err := store.IsRunAlive(ctx, "queue-3", dagRun2)
		require.NoError(t, err)
		require.False(t, alive)

		// Check the original run is still alive
		requireRunAliveState(t, ctx, store, "queue-3", dagRun1, true)

		// Cleanup
		err = proc1.Stop(ctx)
		require.NoError(t, err)
	})

	t.Run("StaleProcess", func(t *testing.T) {
		shortStore := New(baseDir, WithStaleThreshold(100*time.Millisecond))

		dagRun := exec.DAGRunRef{
			Name: "test-dag-stale",
			ID:   "run-stale",
		}
		meta := testProcMetaFromRun(dagRun)
		staleAt := time.Now().Add(-10 * time.Second)
		path := procFilePath(filepath.Join(baseDir, "stale-queue"), exec.NewUTC(staleAt), meta)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))

		fd, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
		require.NoError(t, err)
		require.NoError(t, writeProcFile(fd, staleAt.Unix(), meta))
		require.NoError(t, fd.Sync())
		require.NoError(t, fd.Close())
		require.NoError(t, os.Chtimes(path, staleAt, staleAt))

		alive, err := shortStore.IsRunAlive(ctx, "stale-queue", dagRun)
		require.NoError(t, err)
		require.False(t, alive)
	})
}

func TestStore_IsAttemptAlive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir)
	dagRun := exec.DAGRunRef{
		Name: "attempt-dag",
		ID:   "run-1",
	}
	meta := testProcMetaFromRun(dagRun)
	meta.AttemptID = "attempt-1"

	proc, err := store.Acquire(ctx, "attempt-queue", meta)
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	require.Eventually(t, func() bool {
		alive, err := store.IsAttemptAlive(ctx, "attempt-queue", dagRun, "attempt-1")
		require.NoError(t, err)
		return alive
	}, heartbeatWait, heartbeatPoll, "expected exact attempt to be alive")

	otherAlive, err := store.IsAttemptAlive(ctx, "attempt-queue", dagRun, "attempt-2")
	require.NoError(t, err)
	require.False(t, otherAlive)
}

func TestStore_LatestFreshEntryByDAGName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir)

	older := exec.ProcMeta{
		StartedAt:    100,
		Name:         "latest-dag",
		DAGRunID:     "run-older",
		AttemptID:    "attempt-older",
		RootName:     "latest-dag",
		RootDAGRunID: "run-older",
	}
	newer := exec.ProcMeta{
		StartedAt:    200,
		Name:         "latest-dag",
		DAGRunID:     "run-newer",
		AttemptID:    "attempt-newer",
		RootName:     "latest-dag",
		RootDAGRunID: "run-newer",
	}

	proc1, err := store.Acquire(ctx, "latest-queue", older)
	require.NoError(t, err)
	defer func() { _ = proc1.Stop(ctx) }()

	proc2, err := store.Acquire(ctx, "latest-queue", newer)
	require.NoError(t, err)
	defer func() { _ = proc2.Stop(ctx) }()

	require.Eventually(t, func() bool {
		entry, err := store.LatestFreshEntryByDAGName(ctx, "latest-queue", "latest-dag")
		require.NoError(t, err)
		return entry != nil && entry.Meta.DAGRunID == "run-newer" && entry.Meta.AttemptID == "attempt-newer"
	}, heartbeatWait, heartbeatPoll, "expected newest started attempt to be selected")

	entry, err := store.LatestFreshEntryByDAGName(ctx, "latest-queue", "missing-dag")
	require.NoError(t, err)
	require.Nil(t, entry)
}

func TestStore_ListAllAlive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir)

	t.Run("EmptyStore", func(t *testing.T) {
		// Test when no processes exist
		result, err := store.ListAllAlive(ctx)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("SingleGroup", func(t *testing.T) {
		// Create processes in a single group
		dagRun1 := exec.DAGRunRef{
			Name: "dag1",
			ID:   "run1",
		}
		dagRun2 := exec.DAGRunRef{
			Name: "dag2",
			ID:   "run2",
		}

		proc1, err := store.Acquire(ctx, "queue1", testProcMetaFromRun(dagRun1))
		require.NoError(t, err)
		defer func() { _ = proc1.Stop(ctx) }()

		proc2, err := store.Acquire(ctx, "queue1", testProcMetaFromRun(dagRun2))
		require.NoError(t, err)
		defer func() { _ = proc2.Stop(ctx) }()

		requireListAllAlive(t, ctx, store, func(result map[string][]exec.DAGRunRef) bool {
			queueRuns, ok := result["queue1"]
			return ok && len(result) == 1 && len(queueRuns) == 2
		}, "expected queue1 to contain both runs")

		result, err := store.ListAllAlive(ctx)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Contains(t, result, "queue1")
		require.Len(t, result["queue1"], 2)

		// Check that both DAG runs are in the result
		runIDs := make(map[string]bool)
		for _, ref := range result["queue1"] {
			runIDs[ref.ID] = true
		}
		require.True(t, runIDs["run1"])
		require.True(t, runIDs["run2"])
	})

	t.Run("MultipleGroups", func(t *testing.T) {
		// Create processes in multiple groups
		dagRun1 := exec.DAGRunRef{
			Name: "dag-a",
			ID:   "run-a1",
		}
		dagRun2 := exec.DAGRunRef{
			Name: "dag-b",
			ID:   "run-b1",
		}
		dagRun3 := exec.DAGRunRef{
			Name: "dag-c",
			ID:   "run-c1",
		}

		proc1, err := store.Acquire(ctx, "queue-alpha", testProcMetaFromRun(dagRun1))
		require.NoError(t, err)
		defer func() { _ = proc1.Stop(ctx) }()

		proc2, err := store.Acquire(ctx, "queue-beta", testProcMetaFromRun(dagRun2))
		require.NoError(t, err)
		defer func() { _ = proc2.Stop(ctx) }()

		proc3, err := store.Acquire(ctx, "queue-alpha", testProcMetaFromRun(dagRun3))
		require.NoError(t, err)
		defer func() { _ = proc3.Stop(ctx) }()

		requireListAllAlive(t, ctx, store, func(result map[string][]exec.DAGRunRef) bool {
			queueAlpha, alphaOK := result["queue-alpha"]
			queueBeta, betaOK := result["queue-beta"]
			return len(result) == 2 && alphaOK && betaOK && len(queueAlpha) == 2 && len(queueBeta) == 1
		}, "expected both queues to be populated")

		result, err := store.ListAllAlive(ctx)
		require.NoError(t, err)
		require.Len(t, result, 2)
		require.Contains(t, result, "queue-alpha")
		require.Contains(t, result, "queue-beta")
		require.Len(t, result["queue-alpha"], 2)
		require.Len(t, result["queue-beta"], 1)

		// Verify specific runs
		require.Equal(t, "run-b1", result["queue-beta"][0].ID)
	})

	t.Run("MixedAliveAndStopped", func(t *testing.T) {
		// Create some processes and stop some
		dagRun1 := exec.DAGRunRef{
			Name: "dag-x",
			ID:   "run-x1",
		}
		dagRun2 := exec.DAGRunRef{
			Name: "dag-y",
			ID:   "run-y1",
		}
		dagRun3 := exec.DAGRunRef{
			Name: "dag-z",
			ID:   "run-z1",
		}

		proc1, err := store.Acquire(ctx, "mixed-queue", testProcMetaFromRun(dagRun1))
		require.NoError(t, err)

		proc2, err := store.Acquire(ctx, "mixed-queue", testProcMetaFromRun(dagRun2))
		require.NoError(t, err)

		proc3, err := store.Acquire(ctx, "mixed-queue", testProcMetaFromRun(dagRun3))
		require.NoError(t, err)

		// Stop proc2
		err = proc2.Stop(ctx)
		require.NoError(t, err)

		requireListAllAlive(t, ctx, store, func(result map[string][]exec.DAGRunRef) bool {
			queueRuns, ok := result["mixed-queue"]
			if !ok || len(result) != 1 || len(queueRuns) != 2 {
				return false
			}

			hasRunX := false
			hasRunY := false
			hasRunZ := false
			for _, ref := range queueRuns {
				switch ref.ID {
				case "run-x1":
					hasRunX = true
				case "run-y1":
					hasRunY = true
				case "run-z1":
					hasRunZ = true
				}
			}

			return hasRunX && hasRunZ && !hasRunY
		}, "expected only run-x1 and run-z1 to be alive")

		// List all alive processes
		result, err := store.ListAllAlive(ctx)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Contains(t, result, "mixed-queue")
		require.Len(t, result["mixed-queue"], 2) // Only proc1 and proc3 should be alive

		// Verify the stopped process is not in the result
		runIDs := make(map[string]bool)
		for _, ref := range result["mixed-queue"] {
			runIDs[ref.ID] = true
		}
		require.True(t, runIDs["run-x1"])
		require.False(t, runIDs["run-y1"]) // This one was stopped
		require.True(t, runIDs["run-z1"])

		// Cleanup
		err = proc1.Stop(ctx)
		require.NoError(t, err)
		err = proc3.Stop(ctx)
		require.NoError(t, err)
	})

	t.Run("NonExistentBaseDir", func(t *testing.T) {
		// Test with a base directory that doesn't exist
		nonExistentStore := New("/tmp/non-existent-dir-12345")
		result, err := nonExistentStore.ListAllAlive(ctx)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result)
	})
}

func TestStore_ValidateAcceptsLegacyProcArtifacts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir)
	now := time.Now().UTC()

	writeLegacyProcFile(t, baseDir, "legacy-group", "legacy-dag", "legacy-run", now.Add(-time.Minute), now.Add(-10*time.Second))

	require.NoError(t, store.Validate(ctx))

	entries, err := store.ListEntries(ctx, "legacy-group")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "legacy-dag", entries[0].Meta.Name)
	require.Equal(t, "legacy-run", entries[0].Meta.DAGRunID)
	require.Equal(t, legacyProcAttemptID("legacy-run"), entries[0].Meta.AttemptID)
}

func TestStore_ValidateRejectsMalformedProcArtifacts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir)
	path := filepath.Join(baseDir, "bad-group", "bad-dag", "garbage.proc")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("bad"), 0o600))

	err := store.Validate(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid proc artifact detected")
}

func TestStore_IsRunAlive_LegacyProcFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir)
	now := time.Now().UTC()

	writeLegacyProcFile(t, baseDir, "legacy-group", "legacy-dag", "legacy-run", now.Add(-time.Minute), now.Add(-10*time.Second))

	alive, err := store.IsRunAlive(ctx, "legacy-group", exec.NewDAGRunRef("legacy-dag", "legacy-run"))
	require.NoError(t, err)
	require.True(t, alive)
}

func TestStore_RemoveIfStale_LegacyProcFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := New(baseDir, WithStaleThreshold(100*time.Millisecond))
	now := time.Now().UTC()

	path := writeLegacyProcFile(t, baseDir, "legacy-group", "legacy-dag", "legacy-run", now.Add(-time.Minute), now.Add(-time.Second))

	entries, err := store.ListEntries(ctx, "legacy-group")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.False(t, entries[0].Fresh)

	require.NoError(t, store.RemoveIfStale(ctx, entries[0]))

	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

const (
	heartbeatWait = 2 * time.Second
	heartbeatPoll = 10 * time.Millisecond
)

func requireCountAlive(t *testing.T, ctx context.Context, store *Store, queue string, expected int) {
	t.Helper()
	message := fmt.Sprintf("expected %d proc file(s) in %s", expected, queue)
	require.Eventually(t, func() bool {
		count, err := store.CountAlive(ctx, queue)
		require.NoError(t, err, "failed to count proc files")
		return count == expected
	}, heartbeatWait, heartbeatPoll, message)
}

func requireRunAliveState(t *testing.T, ctx context.Context, store *Store, queue string, dagRun exec.DAGRunRef, expected bool) {
	t.Helper()
	message := fmt.Sprintf("expected run %s/%s alive=%t", dagRun.Name, dagRun.ID, expected)
	require.Eventually(t, func() bool {
		alive, err := store.IsRunAlive(ctx, queue, dagRun)
		require.NoError(t, err)
		return alive == expected
	}, heartbeatWait, heartbeatPoll, message)
}

func requireListAllAlive(t *testing.T, ctx context.Context, store *Store, predicate func(map[string][]exec.DAGRunRef) bool, message string) {
	t.Helper()
	require.Eventually(t, func() bool {
		result, err := store.ListAllAlive(ctx)
		require.NoError(t, err)
		return predicate(result)
	}, heartbeatWait, heartbeatPoll, message)
}
