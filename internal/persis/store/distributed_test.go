// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

func TestDAGRunLeaseStore_UpsertTouchListAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("dag_run_leases"))

	claimedAt := time.Now().Add(-time.Minute).UTC()
	require.NoError(t, s.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      "attempt-key-1",
		DAGRun:          exec.NewDAGRunRef("dag-a", "run-1"),
		Root:            exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:       "attempt-1",
		QueueName:       "queue-a",
		WorkerID:        "worker-1",
		ClaimedAt:       claimedAt.UnixMilli(),
		LastHeartbeatAt: claimedAt.UnixMilli(),
	}))
	require.NoError(t, s.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      "attempt-key-2",
		DAGRun:          exec.NewDAGRunRef("dag-b", "run-2"),
		Root:            exec.NewDAGRunRef("dag-b", "run-2"),
		AttemptID:       "attempt-2",
		QueueName:       "queue-b",
		WorkerID:        "worker-2",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	leases, err := s.ListByQueue(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, leases, 1)
	assert.Equal(t, "attempt-key-1", leases[0].AttemptKey)

	touchedAt := time.Now().UTC()
	require.NoError(t, s.Touch(ctx, "attempt-key-1", touchedAt))

	lease, err := s.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, claimedAt.UnixMilli(), lease.ClaimedAt)
	assert.GreaterOrEqual(t, lease.LastHeartbeatAt, touchedAt.UnixMilli())

	require.NoError(t, s.Delete(ctx, "attempt-key-1"))
	_, err = s.Get(ctx, "attempt-key-1")
	assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)
}

func TestDAGRunLeaseStore_ConcurrentTouchPreservesLatestHeartbeat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("dag_run_leases"))

	require.NoError(t, s.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      "attempt-key-concurrent",
		DAGRun:          exec.NewDAGRunRef("dag-a", "run-1"),
		Root:            exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:       "attempt-1",
		QueueName:       "queue-a",
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().Add(-time.Minute).UTC().UnixMilli(),
	}))

	latest := time.Now().Add(time.Second).UTC()
	var wg sync.WaitGroup
	errCh := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			errCh <- s.Touch(ctx, "attempt-key-concurrent", latest)
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	lease, err := s.Get(ctx, "attempt-key-concurrent")
	require.NoError(t, err)
	require.NotNil(t, lease)
	assert.Equal(t, latest.UnixMilli(), lease.LastHeartbeatAt)
}

func TestDAGRunLeaseStore_RequiresDistributedCollectionLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := testutil.NewMemoryBackend().Collection("dag_run_leases")
	s := store.NewDAGRunLeaseStore(locklessCollection{Collection: base})

	err := s.Upsert(ctx, exec.DAGRunLease{AttemptKey: "attempt-key-lock-required"})
	require.ErrorContains(t, err, "WithLockOptions support")
}

func TestDAGRunLeaseStore_ListAllSurfacesCorruptRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, legacyHash("corrupt-lease")+".json"), []byte("{"), 0o600))

	s := store.NewDAGRunLeaseStore(file.NewCollection(dir))
	_, err := s.ListAll(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt")
}

func TestDAGRunLeaseStore_UsesLegacyLockPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	distributedDir := t.TempDir()
	attemptKey := "attempt-key-lock-compat"
	legacyLock := dirlock.New(filepath.Join(distributedDir, "locks", legacyHash(attemptKey)), &dirlock.LockOptions{
		StaleThreshold: time.Hour,
		RetryInterval:  time.Millisecond,
	})
	require.NoError(t, legacyLock.Lock(ctx))
	defer func() { _ = legacyLock.Unlock() }()

	s := store.NewDAGRunLeaseStore(file.NewCollectionWithLockRoot(filepath.Join(distributedDir, "leases"), distributedDir))
	lockCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	err := s.Upsert(lockCtx, exec.DAGRunLease{AttemptKey: attemptKey})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, legacyLock.Unlock())
	require.NoError(t, s.Upsert(ctx, exec.DAGRunLease{AttemptKey: attemptKey}))
}

func TestActiveDistributedRunStore_UpsertListGetAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewActiveDistributedRunStore(testutil.NewMemoryBackend().Collection("active_runs"))

	require.NoError(t, s.Upsert(ctx, exec.ActiveDistributedRun{
		AttemptKey: "attempt-key-1",
		DAGRun:     exec.NewDAGRunRef("dag-a", "run-1"),
		Root:       exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:  "attempt-1",
		WorkerID:   "worker-1",
		Status:     core.Running,
	}))
	require.NoError(t, s.Upsert(ctx, exec.ActiveDistributedRun{
		AttemptKey: "attempt-key-2",
		DAGRun:     exec.NewDAGRunRef("dag-b", "run-2"),
		Root:       exec.NewDAGRunRef("dag-b", "run-2"),
		AttemptID:  "attempt-2",
		WorkerID:   "worker-2",
		Status:     core.NotStarted,
	}))

	record, err := s.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "attempt-1", record.AttemptID)
	assert.Equal(t, "worker-1", record.WorkerID)
	assert.NotZero(t, record.UpdatedAt)

	records, err := s.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, records, 2)

	require.NoError(t, s.Delete(ctx, "attempt-key-1"))
	_, err = s.Get(ctx, "attempt-key-1")
	assert.ErrorIs(t, err, exec.ErrActiveRunNotFound)
}

func TestActiveDistributedRunStore_UpsertRefreshesUpdatedAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewActiveDistributedRunStore(testutil.NewMemoryBackend().Collection("active_runs"))

	staleUpdatedAt := time.Now().Add(-time.Hour).UTC().UnixMilli()
	require.NoError(t, s.Upsert(ctx, exec.ActiveDistributedRun{
		AttemptKey: "attempt-key-refresh",
		DAGRun:     exec.NewDAGRunRef("dag-a", "run-1"),
		Root:       exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:  "attempt-1",
		WorkerID:   "worker-1",
		Status:     core.Running,
		UpdatedAt:  staleUpdatedAt,
	}))

	record, err := s.Get(ctx, "attempt-key-refresh")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Greater(t, record.UpdatedAt, staleUpdatedAt)
}

func TestActiveDistributedRunStore_ListAllSkipsCorruptRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	s := store.NewActiveDistributedRunStore(file.NewCollection(dir))
	require.NoError(t, s.Upsert(ctx, exec.ActiveDistributedRun{
		AttemptKey: "attempt-key-1",
		DAGRun:     exec.NewDAGRunRef("dag-a", "run-1"),
		Root:       exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:  "attempt-1",
		WorkerID:   "worker-1",
		Status:     core.Running,
	}))
	require.NoError(t, os.WriteFile(filepath.Join(dir, legacyHash("corrupt-active")+".json"), []byte("{"), 0o600))

	records, err := s.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "attempt-key-1", records[0].AttemptKey)
}

func TestActiveDistributedRunStore_UsesLegacyLockPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	distributedDir := t.TempDir()
	attemptKey := "attempt-key-active-lock-compat"
	legacyLock := dirlock.New(filepath.Join(distributedDir, "locks", "active-run-"+legacyHash(attemptKey)), &dirlock.LockOptions{
		StaleThreshold: time.Hour,
		RetryInterval:  time.Millisecond,
	})
	require.NoError(t, legacyLock.Lock(ctx))
	defer func() { _ = legacyLock.Unlock() }()

	s := store.NewActiveDistributedRunStore(file.NewCollectionWithLockRoot(filepath.Join(distributedDir, "active-runs"), distributedDir))
	lockCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	err := s.Upsert(lockCtx, exec.ActiveDistributedRun{AttemptKey: attemptKey})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, legacyLock.Unlock())
	require.NoError(t, s.Upsert(ctx, exec.ActiveDistributedRun{AttemptKey: attemptKey}))
}

func TestDispatchTaskStore_ClaimRecycleAndSelectorFiltering(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	claimTimeout := 3 * time.Second
	s := store.NewDispatchTaskStore(col, store.WithDispatchReservationTTL(claimTimeout))

	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:       "run-a",
		Target:         "dag-a",
		AttemptId:      "attempt-a",
		AttemptKey:     "attempt-key-a",
		WorkerSelector: map[string]string{"type": "gpu"},
	}))
	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:       "run-b",
		Target:         "dag-b",
		AttemptId:      "attempt-b",
		AttemptKey:     "attempt-key-b",
		WorkerSelector: map[string]string{"type": "cpu"},
	}))

	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-1",
		PollerID: "poller-1",
		Labels:   map[string]string{"type": "cpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "run-b", claimed.Task.DagRunId)
	assert.Equal(t, "coord-a", claimed.Task.OwnerCoordinatorId)
	assert.NotEmpty(t, claimed.Task.ClaimToken)

	secondClaim, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-2",
		PollerID: "poller-2",
		Labels:   map[string]string{"type": "cpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-b"},
	})
	require.NoError(t, err)
	assert.Nil(t, secondClaim)

	gpuClaim, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-3",
		PollerID: "poller-3",
		Labels:   map[string]string{"type": "gpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-c"},
	})
	require.NoError(t, err)
	require.NotNil(t, gpuClaim)
	assert.Equal(t, "run-a", gpuClaim.Task.DagRunId)

	ageClaimedDispatchRecord(t, col, claimed.ClaimToken, 10*time.Second, 10*time.Second)

	reclaimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-2",
		PollerID: "poller-2",
		Labels:   map[string]string{"type": "cpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-b"},
	})
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	assert.Equal(t, "run-b", reclaimed.Task.DagRunId)
	assert.Equal(t, "coord-b", reclaimed.Task.OwnerCoordinatorId)

	_, err = s.GetClaim(ctx, claimed.ClaimToken)
	assert.ErrorIs(t, err, exec.ErrDispatchTaskNotFound)
}

func TestDispatchTaskStore_RemovesPendingDuplicateWhenActiveClaimExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	s := store.NewDispatchTaskStore(col)

	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:   "run-duplicate",
		Target:     "dag-duplicate",
		QueueName:  "queue-a",
		AttemptId:  "attempt-duplicate",
		AttemptKey: "attempt-key-duplicate",
	}))
	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-1",
		PollerID: "poller-1",
		Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)

	putPendingDuplicateFromClaim(t, col, claimed.ClaimToken)

	count, err := s.CountOutstandingByQueue(ctx, "queue-a", time.Second)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	next, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{WorkerID: "worker-2"})
	require.NoError(t, err)
	assert.Nil(t, next)

	page, err := col.List(ctx, persis.ListQuery{Prefix: "pending/"})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
}

func TestDispatchTaskStore_ConcurrentClaimIsExclusive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:       "run-exclusive",
		Target:         "dag-exclusive",
		AttemptId:      "attempt-exclusive",
		AttemptKey:     "attempt-key-exclusive",
		WorkerSelector: map[string]string{"type": "cpu"},
	}))

	const pollers = 16
	results := make(chan *exec.ClaimedDispatchTask, pollers)
	errs := make(chan error, pollers)

	var wg sync.WaitGroup
	for i := range pollers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
				WorkerID: "worker-1",
				PollerID: "poller-" + string(rune('a'+idx)),
				Labels:   map[string]string{"type": "cpu"},
				Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
			})
			errs <- err
			results <- claimed
		}(i)
	}
	wg.Wait()
	close(errs)
	close(results)

	for err := range errs {
		require.NoError(t, err)
	}

	claimedCount := 0
	for claimed := range results {
		if claimed != nil {
			claimedCount++
		}
	}
	assert.Equal(t, 1, claimedCount)
}

func TestDispatchTaskStore_ClaimNextSurfacesCorruptPendingRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pending"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pending", "task_corrupt.json"), []byte("{"), 0o600))

	s := store.NewDispatchTaskStore(file.NewCollection(dir))
	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{WorkerID: "worker-1"})
	require.Error(t, err)
	assert.Nil(t, claimed)
	assert.Contains(t, err.Error(), "corrupt")
}

func TestDispatchTaskStore_CountOutstandingByQueueAndAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))

	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:       "run-a",
		Target:         "dag-a",
		QueueName:      "queue-a",
		AttemptId:      "attempt-a",
		AttemptKey:     "attempt-key-a",
		WorkerSelector: map[string]string{"type": "queue-a"},
	}))
	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:       "run-b",
		Target:         "dag-b",
		QueueName:      "queue-b",
		AttemptId:      "attempt-b",
		AttemptKey:     "attempt-key-b",
		WorkerSelector: map[string]string{"type": "queue-b"},
	}))

	count, err := s.CountOutstandingByQueue(ctx, "queue-a", time.Second)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	hasOutstanding, err := s.HasOutstandingAttempt(ctx, "attempt-key-a", time.Second)
	require.NoError(t, err)
	assert.True(t, hasOutstanding)

	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-1",
		PollerID: "poller-1",
		Labels:   map[string]string{"type": "queue-a"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)

	count, err = s.CountOutstandingByQueue(ctx, "queue-a", time.Second)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "claimed reservations must still count against queue admission")

	require.NoError(t, s.DeleteClaim(ctx, claimed.ClaimToken))

	count, err = s.CountOutstandingByQueue(ctx, "queue-a", time.Second)
	require.NoError(t, err)
	assert.Zero(t, count)

	hasOutstanding, err = s.HasOutstandingAttempt(ctx, "attempt-key-a", time.Second)
	require.NoError(t, err)
	assert.False(t, hasOutstanding)
}

func TestDispatchTaskStore_StalePendingReservationsExpire(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	s := store.NewDispatchTaskStore(col, store.WithDispatchReservationTTL(500*time.Millisecond))

	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:   "run-stale",
		Target:     "dag-stale",
		QueueName:  "queue-a",
		AttemptId:  "attempt-stale",
		AttemptKey: "attempt-key-stale",
	}))
	agePendingDispatchRecords(t, col, 2*time.Second)

	count, err := s.CountOutstandingByQueue(ctx, "queue-a", time.Millisecond)
	require.NoError(t, err)
	assert.Zero(t, count)

	hasOutstanding, err := s.HasOutstandingAttempt(ctx, "attempt-key-stale", time.Millisecond)
	require.NoError(t, err)
	assert.False(t, hasOutstanding)

	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{WorkerID: "worker-1"})
	require.NoError(t, err)
	assert.Nil(t, claimed)

	page, err := col.List(ctx, persis.ListQuery{Prefix: "pending/"})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
}

func TestDispatchTaskStore_UsesStoreReservationTTLForCleanup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	s := store.NewDispatchTaskStore(col, store.WithDispatchReservationTTL(5*time.Second))

	require.NoError(t, s.Enqueue(ctx, &coordinatorv1.Task{
		DagRunId:   "run-shared-ttl",
		Target:     "dag-shared-ttl",
		QueueName:  "queue-a",
		AttemptId:  "attempt-shared-ttl",
		AttemptKey: "attempt-key-shared-ttl",
	}))
	agePendingDispatchRecords(t, col, 2*time.Second)

	count, err := s.CountOutstandingByQueue(ctx, "queue-a", time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	hasOutstanding, err := s.HasOutstandingAttempt(ctx, "attempt-key-shared-ttl", time.Millisecond)
	require.NoError(t, err)
	assert.True(t, hasOutstanding)

	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{WorkerID: "worker-1"})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "run-shared-ttl", claimed.Task.DagRunId)
}

func TestDistributedStores_ReadLegacyFileLayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	distributedDir := t.TempDir()
	leaseKey := "attempt-key-legacy-lease"
	activeKey := "attempt-key-legacy-active"

	legacyLease := exec.DAGRunLease{
		AttemptKey:      leaseKey,
		DAGRun:          exec.NewDAGRunRef("dag-a", "run-1"),
		Root:            exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:       "attempt-1",
		QueueName:       "queue-a",
		WorkerID:        "worker-1",
		ClaimedAt:       time.Now().UTC().UnixMilli(),
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}
	writeLegacyJSON(t, filepath.Join(distributedDir, "leases", legacyHash(leaseKey)+".json"), legacyLease)

	leaseStore := store.NewDAGRunLeaseStore(file.NewCollectionWithLockRoot(filepath.Join(distributedDir, "leases"), distributedDir))
	gotLease, err := leaseStore.Get(ctx, leaseKey)
	require.NoError(t, err)
	assert.Equal(t, legacyLease.AttemptKey, gotLease.AttemptKey)

	legacyActive := exec.ActiveDistributedRun{
		AttemptKey: activeKey,
		DAGRun:     exec.NewDAGRunRef("dag-a", "run-1"),
		Root:       exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:  "attempt-1",
		WorkerID:   "worker-1",
		Status:     core.Running,
		UpdatedAt:  time.Now().UTC().UnixMilli(),
	}
	writeLegacyJSON(t, filepath.Join(distributedDir, "active-runs", legacyHash(activeKey)+".json"), legacyActive)

	activeStore := store.NewActiveDistributedRunStore(file.NewCollectionWithLockRoot(filepath.Join(distributedDir, "active-runs"), distributedDir))
	gotActive, err := activeStore.Get(ctx, activeKey)
	require.NoError(t, err)
	assert.Equal(t, legacyActive.AttemptKey, gotActive.AttemptKey)

	legacyTask := dispatchTaskRecord{
		Version:      1,
		Task:         &coordinatorv1.Task{DagRunId: "run-legacy", Target: "dag-legacy", AttemptKey: "attempt-key-legacy-task"},
		TaskFileName: "task_00000000000000000001_legacy.json",
		EnqueuedAt:   time.Now().UTC().UnixMilli(),
	}
	writeLegacyJSON(t, filepath.Join(distributedDir, "pending", legacyTask.TaskFileName), legacyTask)

	dispatchStore := store.NewDispatchTaskStore(file.NewCollection(distributedDir))
	claimed, err := dispatchStore.ClaimNext(ctx, exec.DispatchTaskClaim{WorkerID: "worker-1"})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "run-legacy", claimed.Task.DagRunId)
}

type dispatchTaskRecord struct {
	Version      int                      `json:"version"`
	Task         *coordinatorv1.Task      `json:"task"`
	TaskFileName string                   `json:"taskFileName"`
	EnqueuedAt   int64                    `json:"enqueuedAt"`
	ClaimToken   string                   `json:"claimToken,omitempty"`
	ClaimedAt    int64                    `json:"claimedAt,omitempty"`
	WorkerID     string                   `json:"workerId,omitempty"`
	PollerID     string                   `json:"pollerId,omitempty"`
	Owner        exec.CoordinatorEndpoint `json:"owner,omitzero"`
}

type locklessCollection struct {
	persis.Collection
}

func putPendingDuplicateFromClaim(t *testing.T, col persis.Collection, claimToken string) {
	t.Helper()

	ctx := context.Background()
	claimRec, err := col.Get(ctx, "claims/claim_"+legacyHash(claimToken))
	require.NoError(t, err)

	var payload dispatchTaskRecord
	require.NoError(t, persis.Decode(claimRec, &payload))
	payload.ClaimToken = ""
	payload.ClaimedAt = 0
	payload.WorkerID = ""
	payload.PollerID = ""
	payload.Owner = exec.CoordinatorEndpoint{}
	if payload.Task != nil {
		payload.Task.OwnerCoordinatorId = ""
		payload.Task.OwnerCoordinatorHost = ""
		payload.Task.OwnerCoordinatorPort = 0
		payload.Task.ClaimToken = ""
		payload.Task.WorkerId = ""
	}
	data, enc, err := persis.Encode(payload)
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        pendingRecordIDForTest(payload.TaskFileName),
		Data:      data,
		Encoding:  enc,
		CreatedAt: now,
		UpdatedAt: now,
	}))
}

func pendingRecordIDForTest(fileName string) string {
	return "pending/" + strings.TrimSuffix(filepath.Base(fileName), ".json")
}

func ageClaimedDispatchRecord(t *testing.T, col persis.Collection, claimToken string, pendingAge, claimAge time.Duration) {
	t.Helper()

	ctx := context.Background()
	rec, err := col.Get(ctx, "claims/claim_"+legacyHash(claimToken))
	require.NoError(t, err)

	var payload dispatchTaskRecord
	require.NoError(t, persis.Decode(rec, &payload))
	payload.EnqueuedAt = time.Now().Add(-pendingAge).UTC().UnixMilli()
	payload.ClaimedAt = time.Now().Add(-claimAge).UTC().UnixMilli()
	data, enc, err := persis.Encode(payload)
	require.NoError(t, err)

	rec.Data = data
	rec.Encoding = enc
	rec.UpdatedAt = time.Now().Add(-claimAge).UTC()
	require.NoError(t, col.Put(ctx, rec))
}

func agePendingDispatchRecords(t *testing.T, col persis.Collection, age time.Duration) {
	t.Helper()

	ctx := context.Background()
	page, err := col.List(ctx, persis.ListQuery{Prefix: "pending/"})
	require.NoError(t, err)
	require.NotEmpty(t, page.Records)

	targetTime := time.Now().Add(-age).UTC()
	for _, rec := range page.Records {
		var payload dispatchTaskRecord
		require.NoError(t, persis.Decode(rec, &payload))
		payload.EnqueuedAt = targetTime.UnixMilli()
		data, enc, err := persis.Encode(payload)
		require.NoError(t, err)

		rec.Data = data
		rec.Encoding = enc
		rec.CreatedAt = targetTime
		rec.UpdatedAt = targetTime
		require.NoError(t, col.Put(ctx, rec))
	}
}

func writeLegacyJSON(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func legacyHash(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
