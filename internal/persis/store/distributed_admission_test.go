// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func TestDispatchAdmissionStore_ReserveAdmissionRejectsDuplicateAcrossStores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	first, second := stores.first, stores.second
	req := dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 2)

	results := reserveAdmissionConcurrently(ctx, req, first, second)

	require.Len(t, results, 2)
	assert.Equal(t, 1, countReservedDecisions(results))
	assert.Equal(t, 1, countRejectReason(results, exec.DispatchAdmissionRejectedDuplicate))
}

func TestDispatchAdmissionStore_ReserveAdmissionHonorsQueueCapacityAcrossStores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	first, second := stores.first, stores.second

	results := make([]admissionReserveResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0] = reserveAdmission(ctx, first, dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 1))
	}()
	go func() {
		defer wg.Done()
		results[1] = reserveAdmission(ctx, second, dispatchAdmissionRequest("queue-a", "attempt-key-b", "attempt-b", 1))
	}()
	wg.Wait()

	assert.Equal(t, 1, countReservedDecisions(results))
	assert.Equal(t, 1, countRejectReason(results, exec.DispatchAdmissionRejectedNoCapacity))
}

func TestDispatchAdmissionStore_ReserveAdmissionCountsOldHighSlotsAfterConcurrencyDecrease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	s := stores.first

	for i, attempt := range []string{"a", "b", "c"} {
		result := reserveAdmission(ctx, s, dispatchAdmissionRequest("queue-a", "attempt-key-"+attempt, "attempt-"+attempt, 3))
		require.NoError(t, result.err)
		require.True(t, result.decision.Reserved, "initial reservation %d should fit", i)
	}

	result := reserveAdmission(ctx, s, dispatchAdmissionRequest("queue-a", "attempt-key-d", "attempt-d", 2))
	require.NoError(t, result.err)
	require.False(t, result.decision.Reserved)
	assert.Equal(t, exec.DispatchAdmissionRejectedNoCapacity, result.decision.Reason)
}

func TestDispatchAdmissionStore_ReserveAdmissionCountsNonAdmissionOccupancy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	s := stores.first
	req := dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 1)
	req.NonAdmissionOccupancy = 1

	result := reserveAdmission(ctx, s, req)

	require.NoError(t, result.err)
	require.False(t, result.decision.Reserved)
	assert.Equal(t, exec.DispatchAdmissionRejectedNoCapacity, result.decision.Reason)
}

func TestDispatchAdmissionStore_ReserveAdmissionCountsLegacyPendingClaimAndLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	s := stores.first
	leaseStore := stores.leaseStore
	require.NoError(t, s.Enqueue(ctx, dispatchAdmissionTask("queue-a", "legacy-pending-attempt", "legacy-pending")))
	require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      "legacy-lease-attempt",
		DAGRun:          exec.NewDAGRunRef("dag-a", "run-legacy"),
		Root:            exec.NewDAGRunRef("dag-a", "run-legacy"),
		AttemptID:       "legacy-lease",
		QueueName:       "queue-a",
		WorkerID:        "worker-a",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	result := reserveAdmission(ctx, s, dispatchAdmissionRequest("queue-a", "attempt-key-new", "attempt-new", 2))

	require.NoError(t, result.err)
	require.False(t, result.decision.Reserved)
	assert.Equal(t, exec.DispatchAdmissionRejectedNoCapacity, result.decision.Reason)
}

func TestDispatchAdmissionStore_BindAdmissionIsIdempotentForSameToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	s := stores.first
	req := dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 1)
	result := reserveAdmission(ctx, s, req)
	require.NoError(t, result.err)
	require.True(t, result.decision.Reserved)

	task := dispatchAdmissionTask("queue-a", req.AttemptKey, req.AttemptID)
	require.NoError(t, s.BindAdmission(ctx, exec.DispatchAdmissionBindRequest{
		ReservationToken: result.decision.ReservationToken,
		Task:             task,
	}))

	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID:     "worker-a",
		PollerID:     "poller-a",
		ClaimTimeout: time.Minute,
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)

	require.NoError(t, s.BindAdmission(ctx, exec.DispatchAdmissionBindRequest{
		ReservationToken: result.decision.ReservationToken,
		Task:             task,
	}))
	require.NoError(t, s.DeleteClaim(ctx, claimed.ClaimToken))
	require.NoError(t, s.BindAdmission(ctx, exec.DispatchAdmissionBindRequest{
		ReservationToken: result.decision.ReservationToken,
		Task:             task,
	}))

	claimedAgain, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID:     "worker-a",
		PollerID:     "poller-a",
		ClaimTimeout: time.Minute,
	})
	require.NoError(t, err)
	assert.Nil(t, claimedAgain)
}

func TestDispatchAdmissionStore_BindAdmissionRequiresMatchingSlotAndToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	s := stores.first
	req := dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 1)
	result := reserveAdmission(ctx, s, req)
	require.NoError(t, result.err)
	require.True(t, result.decision.Reserved)
	require.NoError(t, s.FinalizeAdmissionAttempt(ctx, req.AttemptKey))

	err := s.BindAdmission(ctx, exec.DispatchAdmissionBindRequest{
		ReservationToken: result.decision.ReservationToken,
		Task:             dispatchAdmissionTask("queue-a", req.AttemptKey, req.AttemptID),
	})

	require.Error(t, err)
}

func TestDispatchAdmissionStore_BindAdmissionRejectsExpiredReservation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	dispatchCol := backend.Collection("dispatch_tasks")
	leaseStore := store.NewDAGRunLeaseStore(backend.Collection("dag_run_leases"))
	activeStore := store.NewActiveDistributedRunStore(backend.Collection("active_runs"))
	s := store.NewDispatchTaskStore(
		dispatchCol,
		store.WithDispatchAdmissionLiveness(leaseStore, activeStore),
		store.WithDispatchReservationTTL(25*time.Millisecond),
	)
	req := dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 1)
	req.StaleThreshold = 25 * time.Millisecond
	result := reserveAdmission(ctx, s, req)
	require.NoError(t, result.err)
	require.True(t, result.decision.Reserved)

	time.Sleep(50 * time.Millisecond)

	err := s.BindAdmission(ctx, exec.DispatchAdmissionBindRequest{
		ReservationToken: result.decision.ReservationToken,
		Task:             dispatchAdmissionTask("queue-a", req.AttemptKey, req.AttemptID),
	})

	require.ErrorIs(t, err, exec.ErrDispatchAdmissionNotFound)
}

func TestDispatchAdmissionStore_FinalizeAdmissionAttemptReleasesSlot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := newDispatchAdmissionTestStores(t)
	s := stores.first

	first := reserveAdmission(ctx, s, dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 1))
	require.NoError(t, first.err)
	require.True(t, first.decision.Reserved)

	full := reserveAdmission(ctx, s, dispatchAdmissionRequest("queue-a", "attempt-key-b", "attempt-b", 1))
	require.NoError(t, full.err)
	require.False(t, full.decision.Reserved)

	require.NoError(t, s.FinalizeAdmissionAttempt(ctx, "attempt-key-a"))

	second := reserveAdmission(ctx, s, dispatchAdmissionRequest("queue-a", "attempt-key-b", "attempt-b", 1))
	require.NoError(t, second.err)
	require.True(t, second.decision.Reserved)
}

func TestDispatchAdmissionStore_ReserveAdmissionRequiresLivenessStores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))

	_, err := s.ReserveAdmission(ctx, dispatchAdmissionRequest("queue-a", "attempt-key-a", "attempt-a", 1))

	require.ErrorIs(t, err, exec.ErrDispatchAdmissionLivenessNotConfigured)
}

type admissionReserveResult struct {
	decision *exec.DispatchAdmissionDecision
	err      error
}

type dispatchAdmissionTestStores struct {
	first       *store.DispatchTaskStore
	second      *store.DispatchTaskStore
	leaseStore  *store.DAGRunLeaseStore
	activeStore *store.ActiveDistributedRunStore
}

func newDispatchAdmissionTestStores(t *testing.T) dispatchAdmissionTestStores {
	t.Helper()

	backend := testutil.NewMemoryBackend()
	dispatchCol := backend.Collection("dispatch_tasks")
	leaseStore := store.NewDAGRunLeaseStore(backend.Collection("dag_run_leases"))
	activeStore := store.NewActiveDistributedRunStore(backend.Collection("active_runs"))
	first := store.NewDispatchTaskStore(dispatchCol, store.WithDispatchAdmissionLiveness(leaseStore, activeStore))
	second := store.NewDispatchTaskStore(dispatchCol, store.WithDispatchAdmissionLiveness(leaseStore, activeStore))
	return dispatchAdmissionTestStores{
		first:       first,
		second:      second,
		leaseStore:  leaseStore,
		activeStore: activeStore,
	}
}

func dispatchAdmissionRequest(queueName, attemptKey, attemptID string, maxConcurrency int) exec.DispatchAdmissionRequest {
	return exec.DispatchAdmissionRequest{
		QueueName:      queueName,
		MaxConcurrency: maxConcurrency,
		AttemptKey:     attemptKey,
		AttemptID:      attemptID,
		DAGRun:         exec.NewDAGRunRef("dag-a", "run-"+attemptID),
		StaleThreshold: time.Minute,
	}
}

func dispatchAdmissionTask(queueName, attemptKey, attemptID string) *exec.DispatchTask {
	return &exec.DispatchTask{
		Operation:  exec.DispatchOperationRetry,
		DAGRunID:   "run-" + attemptID,
		Target:     "dag-a",
		Definition: "steps:\n- command: echo ok\n",
		AttemptID:  attemptID,
		AttemptKey: attemptKey,
		QueueName:  queueName,
	}
}

func reserveAdmission(ctx context.Context, s exec.DispatchAdmissionStore, req exec.DispatchAdmissionRequest) admissionReserveResult {
	decision, err := s.ReserveAdmission(ctx, req)
	return admissionReserveResult{decision: decision, err: err}
}

func reserveAdmissionConcurrently(ctx context.Context, req exec.DispatchAdmissionRequest, stores ...exec.DispatchAdmissionStore) []admissionReserveResult {
	results := make([]admissionReserveResult, len(stores))
	var wg sync.WaitGroup
	for i, s := range stores {
		wg.Add(1)
		go func(idx int, admissionStore exec.DispatchAdmissionStore) {
			defer wg.Done()
			results[idx] = reserveAdmission(ctx, admissionStore, req)
		}(i, s)
	}
	wg.Wait()
	return results
}

func countReservedDecisions(results []admissionReserveResult) int {
	var count int
	for _, result := range results {
		if result.err == nil && result.decision != nil && result.decision.Reserved {
			count++
		}
	}
	return count
}

func countRejectReason(results []admissionReserveResult, reason exec.DispatchAdmissionRejectReason) int {
	var count int
	for _, result := range results {
		if result.err == nil && result.decision != nil && !result.decision.Reserved && result.decision.Reason == reason {
			count++
		}
	}
	return count
}
