// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func heldStore(t *testing.T, ttl time.Duration) *store.DispatchTaskStore {
	t.Helper()
	return store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"), store.WithDispatchReservationTTL(ttl))
}

func heldTask(runID string, holdUntil time.Time) *dispatch.DispatchTask {
	return &dispatch.DispatchTask{
		DAGRunID: runID, Target: "dag-" + runID, AttemptID: "attempt-" + runID, AttemptKey: "key-" + runID,
		WorkerSelector: map[string]string{"type": "cpu"},
		HoldUntil:      holdUntil,
	}
}

// A pending task outlives the reservation TTL, held or not: it waits until a
// matching worker claims it or the coordinator drops it.
func TestDispatchTaskStore_PendingOutlivesReservationTTL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := heldStore(t, 30*time.Millisecond)

	require.NoError(t, s.Enqueue(ctx, heldTask("held", time.Now().Add(time.Hour))))
	require.NoError(t, s.Enqueue(ctx, heldTask("plain", time.Time{})))
	time.Sleep(60 * time.Millisecond)

	outstanding, err := s.HasOutstandingAttempt(ctx, "key-plain", time.Minute)
	require.NoError(t, err)
	assert.True(t, outstanding)

	var claimedRuns []string
	for range 2 {
		claimed, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "w", PollerID: "p", Labels: map[string]string{"type": "cpu"}})
		require.NoError(t, err)
		require.NotNil(t, claimed)
		claimedRuns = append(claimedRuns, claimed.Task.DAGRunID)
	}
	assert.ElementsMatch(t, []string{"held", "plain"}, claimedRuns)
}

// ListStalePending lists the unheld tasks enqueued before the cutoff and
// leaves them claimable.
func TestDispatchTaskStore_ListStalePending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := heldStore(t, time.Hour)

	require.NoError(t, s.Enqueue(ctx, heldTask("held", time.Now().Add(time.Hour))))
	require.NoError(t, s.Enqueue(ctx, heldTask("old", time.Time{})))
	time.Sleep(10 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, s.Enqueue(ctx, heldTask("new", time.Time{})))

	stale, err := s.ListStalePending(ctx, cutoff)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "old", stale[0].DAGRunID)

	outstanding, err := s.HasOutstandingAttempt(ctx, "key-old", time.Minute)
	require.NoError(t, err)
	assert.True(t, outstanding)
}

// Once its hold has passed, a held task is no longer handed out; it is left
// for the coordinator to fail the run and drop it.
func TestDispatchTaskStore_ExpiredHeldIsListedNotClaimed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := heldStore(t, time.Hour)
	now := time.Now()

	require.NoError(t, s.Enqueue(ctx, heldTask("late", now.Add(-time.Minute))))
	require.NoError(t, s.Enqueue(ctx, heldTask("fresh", now.Add(time.Hour))))

	claimed, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "w", PollerID: "p", Labels: map[string]string{"type": "cpu"}})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "fresh", claimed.Task.DAGRunID)

	claimed, err = s.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "w", PollerID: "p2", Labels: map[string]string{"type": "cpu"}})
	require.NoError(t, err)
	assert.Nil(t, claimed, "the expired held task is not claimable")

	expired, err := s.ListExpiredHeld(ctx, now)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, "late", expired[0].DAGRunID)
	assert.Equal(t, map[string]string{"type": "cpu"}, expired[0].WorkerSelector)

	// Listing does not remove; a coordinator that dies between failing the
	// run and dropping the task finds it again.
	expired, err = s.ListExpiredHeld(ctx, now)
	require.NoError(t, err)
	assert.Len(t, expired, 1)

	outstanding, err := s.HasOutstandingAttempt(ctx, "key-late", time.Hour)
	require.NoError(t, err)
	assert.True(t, outstanding, "a held task is never recycled by the TTL")

	require.NoError(t, s.DeletePending(ctx, "key-late"))
	expired, err = s.ListExpiredHeld(ctx, now)
	require.NoError(t, err)
	assert.Empty(t, expired)
	outstanding, err = s.HasOutstandingAttempt(ctx, "key-late", time.Hour)
	require.NoError(t, err)
	assert.False(t, outstanding)
}

func TestDispatchTaskStore_DeletePendingLeavesClaimsAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := heldStore(t, time.Hour)

	require.NoError(t, s.Enqueue(ctx, heldTask("claimed", time.Now().Add(time.Hour))))
	claimed, err := s.ClaimNext(ctx, dispatch.DispatchTaskClaim{WorkerID: "w", PollerID: "p", Labels: map[string]string{"type": "cpu"}})
	require.NoError(t, err)
	require.NotNil(t, claimed)

	require.NoError(t, s.DeletePending(ctx, "key-claimed"))
	require.NoError(t, s.DeletePending(ctx, ""))

	got, err := s.GetClaim(ctx, claimed.ClaimToken)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "claimed", got.Task.DAGRunID)
}

// The hold survives a rebuild of the index from the records on disk.
func TestDispatchTaskStore_HoldSurvivesIndexRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	now := time.Now()
	require.NoError(t, store.NewDispatchTaskStore(col, store.WithDispatchReservationTTL(time.Hour)).Enqueue(ctx, heldTask("kept", now.Add(-time.Minute))))

	reopened := store.NewDispatchTaskStore(col, store.WithDispatchReservationTTL(time.Hour))
	expired, err := reopened.ListExpiredHeld(ctx, now)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	assert.Equal(t, "kept", expired[0].DAGRunID)
	assert.WithinDuration(t, now.Add(-time.Minute), expired[0].HoldUntil, time.Second)
}
