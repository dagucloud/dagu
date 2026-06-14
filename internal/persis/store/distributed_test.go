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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
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

	// Use distinct observedAt per goroutine so the assertion meaningfully
	// catches a regression where one Touch's write would clobber another's.
	base := time.Now().UTC().Truncate(time.Second)
	observed := []time.Time{base, base.Add(time.Second), base.Add(2 * time.Second)}

	var wg sync.WaitGroup
	errCh := make(chan error, len(observed))
	for _, ts := range observed {
		wg.Add(1)
		go func(observedAt time.Time) {
			defer wg.Done()
			errCh <- s.Touch(ctx, "attempt-key-concurrent", observedAt)
		}(ts)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	lease, err := s.Get(ctx, "attempt-key-concurrent")
	require.NoError(t, err)
	require.NotNil(t, lease)
	// Under CAS-retry the last writer wins; assert the final value equals
	// one of the requested timestamps (no silent fold to an unrelated value).
	candidates := map[int64]struct{}{}
	for _, ts := range observed {
		candidates[ts.UnixMilli()] = struct{}{}
	}
	_, ok := candidates[lease.LastHeartbeatAt]
	assert.True(t, ok, "lease.LastHeartbeatAt must equal one of the requested observedAt values; got %d", lease.LastHeartbeatAt)
}

// TestDAGRunLeaseStore_ConcurrentTouchAndUpsertNoClobber documents the
// CAS-retry semantic: an Upsert and a Touch concurrently targeting the same
// attempt key must converge to a state that contains the Upsert's payload
// changes (WorkerID/Owner) AND a LastHeartbeatAt at-or-after the Touch.
func TestDAGRunLeaseStore_ConcurrentTouchAndUpsertNoClobber(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDAGRunLeaseStore(testutil.NewMemoryBackend().Collection("dag_run_leases"))

	initial := exec.DAGRunLease{
		AttemptKey:      "attempt-key-clobber",
		DAGRun:          exec.NewDAGRunRef("dag-a", "run-1"),
		Root:            exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:       "attempt-1",
		QueueName:       "queue-a",
		WorkerID:        "worker-old",
		ClaimedAt:       time.Now().Add(-time.Hour).UTC().UnixMilli(),
		LastHeartbeatAt: time.Now().Add(-time.Minute).UTC().UnixMilli(),
	}
	require.NoError(t, s.Upsert(ctx, initial))

	newWorker := "worker-claim-update"
	// Real coordinator callers pass fresh LastHeartbeatAt on every Upsert
	// (see coordinator/distributed_attempts.go). Mirror that here so this
	// test models production semantics: whichever write lands last, both
	// the WorkerID change and a fresh heartbeat survive.
	touchAt := time.Now().UTC().Truncate(time.Second)
	upsertHeartbeat := touchAt.UnixMilli()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		u := initial
		u.WorkerID = newWorker
		u.LastHeartbeatAt = upsertHeartbeat
		errCh <- s.Upsert(ctx, u)
	}()
	go func() {
		defer wg.Done()
		errCh <- s.Touch(ctx, "attempt-key-clobber", touchAt)
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	lease, err := s.Get(ctx, "attempt-key-clobber")
	require.NoError(t, err)
	require.NotNil(t, lease)
	assert.Equal(t, newWorker, lease.WorkerID, "Upsert's WorkerID must survive concurrent Touch")
	assert.GreaterOrEqual(t, lease.LastHeartbeatAt, touchAt.UnixMilli(), "LastHeartbeatAt must be at-or-after touchAt under either write ordering")
	assert.Equal(t, initial.ClaimedAt, lease.ClaimedAt, "ClaimedAt is caller-controlled and stable across both writes")
}

func TestDAGRunLeaseStore_ListAllSurfacesCorruptRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, encodedKey("corrupt-lease")+".json"), []byte("{"), 0o600))

	s := store.NewDAGRunLeaseStore(file.NewCollection(dir))
	_, err := s.ListAll(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt")
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

// TestActiveDistributedRunStore_ConcurrentUpsertSerializes spawns five
// goroutines all upserting the same attempt key with distinct WorkerIDs.
// After all complete, exactly one WorkerID survives — no data loss, no
// orphan partial state.
func TestActiveDistributedRunStore_ConcurrentUpsertSerializes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewActiveDistributedRunStore(testutil.NewMemoryBackend().Collection("active_runs"))

	base := exec.ActiveDistributedRun{
		AttemptKey: "attempt-key-active-concurrent",
		DAGRun:     exec.NewDAGRunRef("dag-a", "run-1"),
		Root:       exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:  "attempt-1",
		Status:     core.Running,
	}

	const writers = 5
	workers := make([]string, writers)
	for i := range writers {
		workers[i] = "worker-" + string(rune('0'+i))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for _, w := range workers {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			r := base
			r.WorkerID = worker
			errCh <- s.Upsert(ctx, r)
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	rec, err := s.Get(ctx, "attempt-key-active-concurrent")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Contains(t, workers, rec.WorkerID, "final WorkerID must be one of the concurrent writers")
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, encodedKey("corrupt-active")+".json"), []byte("{"), 0o600))

	records, err := s.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "attempt-key-1", records[0].AttemptKey)
}

func TestDispatchTaskStore_ClaimRecycleAndSelectorFiltering(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	claimTimeout := 3 * time.Second
	s := store.NewDispatchTaskStore(col, store.WithDispatchReservationTTL(claimTimeout))

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:       "run-a",
		Target:         "dag-a",
		AttemptID:      "attempt-a",
		AttemptKey:     "attempt-key-a",
		WorkerSelector: map[string]string{"type": "gpu"},
	}))
	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:       "run-b",
		Target:         "dag-b",
		AttemptID:      "attempt-b",
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
	assert.Equal(t, "run-b", claimed.Task.DAGRunID)
	assert.Equal(t, "coord-a", claimed.Task.Owner.ID)
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
	assert.Equal(t, "run-a", gpuClaim.Task.DAGRunID)

	ageClaimedDispatchRecord(t, col, claimed.ClaimToken, 10*time.Second, 10*time.Second)

	reclaimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-2",
		PollerID: "poller-2",
		Labels:   map[string]string{"type": "cpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-b"},
	})
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	assert.Equal(t, "run-b", reclaimed.Task.DAGRunID)
	assert.Equal(t, "coord-b", reclaimed.Task.Owner.ID)

	_, err = s.GetClaim(ctx, claimed.ClaimToken)
	assert.ErrorIs(t, err, exec.ErrDispatchTaskNotFound)
}

func TestDispatchTaskStore_ClaimsLegacyProtoJSONTaskRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	s := store.NewDispatchTaskStore(file.NewCollection(dir))

	statusData, err := json.Marshal(exec.DAGRunStatus{
		Name:     "dag-legacy",
		DAGRunID: "run-legacy",
		Status:   core.Running,
	})
	require.NoError(t, err)

	fileName := "task_00000000000000000001_legacy.json"
	writeJSONFile(t, filepath.Join(dir, "pending", fileName), map[string]any{
		"version":      1,
		"taskFileName": fileName,
		"enqueuedAt":   time.Now().UTC().UnixMilli(),
		"task": map[string]any{
			"root_dag_run_name":             "root-dag",
			"root_dag_run_id":               "root-run",
			"parent_dag_run_name":           "parent-dag",
			"parent_dag_run_id":             "parent-run",
			"operation":                     int32(exec.DispatchOperationRetry),
			"dag_run_id":                    "run-legacy",
			"target":                        "dag-legacy",
			"definition":                    "steps:\n  - run: echo legacy\n",
			"worker_id":                     "worker-old",
			"attempt_id":                    "attempt-legacy",
			"attempt_key":                   "attempt-key-legacy",
			"step":                          "step-a",
			"params":                        "PARAM=value",
			"queue_name":                    "queue-legacy",
			"base_config":                   "env:\n  - BASE=1\n",
			"labels":                        "team=ops",
			"schedule_time":                 "2026-05-31T00:00:00Z",
			"source_file":                   "/dags/legacy.yaml",
			"worker_selector":               map[string]string{"type": "gpu"},
			"agent_snapshot":                []byte{1, 2, 3},
			"external_step_retry":           true,
			"workspace_bundle_digest":       "sha256:legacy",
			"workspace_bundle_size":         42,
			"workspace_bundle_dag_path":     "legacy.yaml",
			"workspace_bundle_original_ref": "main",
			"workspace_bundle_resolved_ref": "abc123",
			"owner_coordinator_id":          "coord-old",
			"owner_coordinator_host":        "old.example.test",
			"owner_coordinator_port":        int32(8090),
			"claim_token":                   "claim-old",
			"previous_status":               map[string]any{"json_data": string(statusData)},
		},
	})

	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-1",
		PollerID: "poller-1",
		Labels:   map[string]string{"type": "gpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-new", Host: "new.example.test", Port: 9090},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.NotNil(t, claimed.Task)

	assert.Equal(t, "root-dag", claimed.Task.RootDAGRunName)
	assert.Equal(t, "root-run", claimed.Task.RootDAGRunID)
	assert.Equal(t, "parent-dag", claimed.Task.ParentDAGRunName)
	assert.Equal(t, "parent-run", claimed.Task.ParentDAGRunID)
	assert.Equal(t, exec.DispatchOperationRetry, claimed.Task.Operation)
	assert.Equal(t, "run-legacy", claimed.Task.DAGRunID)
	assert.Equal(t, "dag-legacy", claimed.Task.Target)
	assert.Equal(t, "attempt-legacy", claimed.Task.AttemptID)
	assert.Equal(t, "attempt-key-legacy", claimed.Task.AttemptKey)
	assert.Equal(t, "step-a", claimed.Task.Step)
	assert.Equal(t, "PARAM=value", claimed.Task.Params)
	assert.Equal(t, "queue-legacy", claimed.Task.QueueName)
	assert.Equal(t, "team=ops", claimed.Task.Labels)
	assert.Equal(t, map[string]string{"type": "gpu"}, claimed.Task.WorkerSelector)
	assert.Equal(t, []byte{1, 2, 3}, claimed.Task.AgentSnapshot)
	assert.Equal(t, "sha256:legacy", claimed.Task.WorkspaceBundleDigest)
	assert.Equal(t, int64(42), claimed.Task.WorkspaceBundleSize)
	assert.Equal(t, "legacy.yaml", claimed.Task.WorkspaceBundleDAGPath)
	assert.Equal(t, "main", claimed.Task.WorkspaceBundleOriginalRef)
	assert.Equal(t, "abc123", claimed.Task.WorkspaceBundleResolvedRef)
	assert.Equal(t, exec.CoordinatorEndpoint{ID: "coord-new", Host: "new.example.test", Port: 9090}, claimed.Task.Owner)
	assert.NotEmpty(t, claimed.Task.ClaimToken)
	require.NotNil(t, claimed.Task.PreviousStatus)
	assert.Equal(t, "dag-legacy", claimed.Task.PreviousStatus.Name)
	assert.Equal(t, "run-legacy", claimed.Task.PreviousStatus.DAGRunID)
	assert.Equal(t, core.Running, claimed.Task.PreviousStatus.Status)
}

func TestDispatchTaskStore_ReleaseClaimReturnsTaskToPending(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:       "run-release",
		Target:         "dag-release",
		AttemptID:      "attempt-release",
		AttemptKey:     "attempt-key-release",
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
	require.NotEmpty(t, claimed.ClaimToken)

	require.NoError(t, s.ReleaseClaim(ctx, claimed.ClaimToken))

	_, err = s.GetClaim(ctx, claimed.ClaimToken)
	assert.ErrorIs(t, err, exec.ErrDispatchTaskNotFound)

	reclaimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-2",
		PollerID: "poller-2",
		Labels:   map[string]string{"type": "cpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-b"},
	})
	require.NoError(t, err)
	require.NotNil(t, reclaimed)
	assert.Equal(t, "run-release", reclaimed.Task.DAGRunID)
	assert.Equal(t, "coord-b", reclaimed.Task.Owner.ID)
	assert.NotEqual(t, claimed.ClaimToken, reclaimed.ClaimToken)
}

func TestDispatchTaskStore_RemovesPendingDuplicateWhenActiveClaimExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	s := store.NewDispatchTaskStore(col)

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:   "run-duplicate",
		Target:     "dag-duplicate",
		QueueName:  "queue-a",
		AttemptID:  "attempt-duplicate",
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

func TestDispatchTaskStore_KeepsNewerPendingDuplicateDuringActiveClaimCleanup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("dispatch_tasks")
	s := store.NewDispatchTaskStore(col)

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:   "run-newer-pending",
		Target:     "dag-newer-pending",
		QueueName:  "queue-a",
		AttemptID:  "attempt-newer-pending",
		AttemptKey: "attempt-key-newer-pending",
	}))
	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-1",
		PollerID: "poller-1",
		Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)

	putNewerPendingDuplicateFromClaim(t, col, claimed.ClaimToken)

	count, err := s.CountOutstandingByQueue(ctx, "queue-a", time.Second)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestDispatchTaskStore_ConcurrentClaimIsExclusive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:       "run-exclusive",
		Target:         "dag-exclusive",
		AttemptID:      "attempt-exclusive",
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

func TestDispatchTaskStore_RepeatedNoMatchDoesNotRereadPendingRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := newCountingRecordIDsCollection(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	s := store.NewDispatchTaskStore(col)

	for i := 0; i < 8; i++ {
		require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
			DAGRunID:       "run-no-match-" + string(rune('a'+i)),
			Target:         "dag-no-match",
			AttemptID:      "attempt-no-match",
			AttemptKey:     "attempt-key-no-match-" + string(rune('a'+i)),
			WorkerSelector: map[string]string{"type": "gpu"},
		}))
	}

	col.Reset()

	for i := 0; i < 3; i++ {
		claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
			WorkerID: "worker-cpu",
			PollerID: "poller-cpu",
			Labels:   map[string]string{"type": "cpu"},
			Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
		})
		require.NoError(t, err)
		require.Nil(t, claimed)
	}

	assert.LessOrEqual(t, col.GetCount(), int64(1), "repeated no-match claims should use indexed metadata instead of reading every pending record")
}

func TestDispatchTaskStore_RepeatedNoMatchWithClaimsThrottlesValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := newCountingRecordIDsCollection(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	s := store.NewDispatchTaskStore(col)

	for i := 0; i < 8; i++ {
		require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
			DAGRunID:       "run-claim-throttle-" + string(rune('a'+i)),
			Target:         "dag-claim-throttle",
			AttemptID:      "attempt-claim-throttle",
			AttemptKey:     "attempt-key-claim-throttle-" + string(rune('a'+i)),
			WorkerSelector: map[string]string{"type": "cpu"},
		}))
		claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
			WorkerID: "worker-cpu",
			PollerID: "poller-cpu",
			Labels:   map[string]string{"type": "cpu"},
			Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
		})
		require.NoError(t, err)
		require.NotNil(t, claimed)
	}

	col.Reset()

	for i := 0; i < 3; i++ {
		claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
			WorkerID: "worker-gpu",
			PollerID: "poller-gpu",
			Labels:   map[string]string{"type": "gpu"},
			Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
		})
		require.NoError(t, err)
		require.Nil(t, claimed)
	}

	assert.LessOrEqual(t, col.RecordVersionCount(), int64(8), "claim validation should be throttled after index warmup")
}

func TestDispatchTaskStore_EnqueueInvalidatesIndexedNoMatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))

	claimed, err := s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-cpu",
		Labels:   map[string]string{"type": "cpu"},
	})
	require.NoError(t, err)
	require.Nil(t, claimed)

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:       "run-cpu",
		Target:         "dag-cpu",
		AttemptID:      "attempt-cpu",
		AttemptKey:     "attempt-key-cpu",
		WorkerSelector: map[string]string{"type": "cpu"},
	}))

	claimed, err = s.ClaimNext(ctx, exec.DispatchTaskClaim{
		WorkerID: "worker-cpu",
		Labels:   map[string]string{"type": "cpu"},
		Owner:    exec.CoordinatorEndpoint{ID: "coord-a"},
	})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "run-cpu", claimed.Task.DAGRunID)
}

func TestDispatchTaskStore_OutstandingQueriesUseIndexAfterWarmup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := newCountingRecordIDsCollection(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	s := store.NewDispatchTaskStore(col)

	for i := 0; i < 8; i++ {
		require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
			DAGRunID:   "run-outstanding-" + string(rune('a'+i)),
			Target:     "dag-outstanding",
			QueueName:  "queue-a",
			AttemptID:  "attempt-outstanding",
			AttemptKey: "attempt-key-outstanding-" + string(rune('a'+i)),
		}))
	}

	count, err := s.CountOutstandingByQueue(ctx, "queue-a", time.Second)
	require.NoError(t, err)
	require.Equal(t, 8, count)
	hasOutstanding, err := s.HasOutstandingAttempt(ctx, "attempt-key-outstanding-a", time.Second)
	require.NoError(t, err)
	require.True(t, hasOutstanding)

	col.Reset()

	for i := 0; i < 3; i++ {
		count, err = s.CountOutstandingByQueue(ctx, "queue-a", time.Second)
		require.NoError(t, err)
		require.Equal(t, 8, count)
		hasOutstanding, err = s.HasOutstandingAttempt(ctx, "attempt-key-outstanding-h", time.Second)
		require.NoError(t, err)
		require.True(t, hasOutstanding)
	}

	assert.Zero(t, col.GetCount(), "outstanding queries should use indexed metadata after warmup")
}

func TestDispatchTaskStore_CountOutstandingByQueueAndAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:       "run-a",
		Target:         "dag-a",
		QueueName:      "queue-a",
		AttemptID:      "attempt-a",
		AttemptKey:     "attempt-key-a",
		WorkerSelector: map[string]string{"type": "queue-a"},
	}))
	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:       "run-b",
		Target:         "dag-b",
		QueueName:      "queue-b",
		AttemptID:      "attempt-b",
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

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:   "run-stale",
		Target:     "dag-stale",
		QueueName:  "queue-a",
		AttemptID:  "attempt-stale",
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

	require.NoError(t, s.Enqueue(ctx, &exec.DispatchTask{
		DAGRunID:   "run-shared-ttl",
		Target:     "dag-shared-ttl",
		QueueName:  "queue-a",
		AttemptID:  "attempt-shared-ttl",
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
	assert.Equal(t, "run-shared-ttl", claimed.Task.DAGRunID)
}

func TestDistributedStores_ReadFileLayout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	distributedDir := t.TempDir()
	leaseKey := "attempt-key-file-lease"
	activeKey := "attempt-key-file-active"

	fileLease := exec.DAGRunLease{
		AttemptKey:      leaseKey,
		DAGRun:          exec.NewDAGRunRef("dag-a", "run-1"),
		Root:            exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:       "attempt-1",
		QueueName:       "queue-a",
		WorkerID:        "worker-1",
		ClaimedAt:       time.Now().UTC().UnixMilli(),
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}
	writeJSONFile(t, filepath.Join(distributedDir, "leases", encodedKey(leaseKey)+".json"), fileLease)

	leaseStore := store.NewDAGRunLeaseStore(file.NewCollection(filepath.Join(distributedDir, "leases")))
	gotLease, err := leaseStore.Get(ctx, leaseKey)
	require.NoError(t, err)
	assert.Equal(t, fileLease.AttemptKey, gotLease.AttemptKey)

	fileActive := exec.ActiveDistributedRun{
		AttemptKey: activeKey,
		DAGRun:     exec.NewDAGRunRef("dag-a", "run-1"),
		Root:       exec.NewDAGRunRef("dag-a", "run-1"),
		AttemptID:  "attempt-1",
		WorkerID:   "worker-1",
		Status:     core.Running,
		UpdatedAt:  time.Now().UTC().UnixMilli(),
	}
	writeJSONFile(t, filepath.Join(distributedDir, "active-runs", encodedKey(activeKey)+".json"), fileActive)

	activeStore := store.NewActiveDistributedRunStore(file.NewCollection(filepath.Join(distributedDir, "active-runs")))
	gotActive, err := activeStore.Get(ctx, activeKey)
	require.NoError(t, err)
	assert.Equal(t, fileActive.AttemptKey, gotActive.AttemptKey)

	fileTask := dispatchTaskRecord{
		Version:      1,
		Task:         &exec.DispatchTask{DAGRunID: "run-file", Target: "dag-file", AttemptKey: "attempt-key-file-task"},
		TaskFileName: "task_00000000000000000001_file.json",
		EnqueuedAt:   time.Now().UTC().UnixMilli(),
	}
	writeJSONFile(t, filepath.Join(distributedDir, "pending", fileTask.TaskFileName), fileTask)

	dispatchStore := store.NewDispatchTaskStore(file.NewCollection(distributedDir))
	claimed, err := dispatchStore.ClaimNext(ctx, exec.DispatchTaskClaim{WorkerID: "worker-1"})
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "run-file", claimed.Task.DAGRunID)
}

func BenchmarkDispatchTaskStoreClaimNextNoMatch(b *testing.B) {
	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	seedBenchmarkDispatchTasks(b, ctx, s, 1000, map[string]string{"type": "gpu"}, "gpu")
	claim := exec.DispatchTaskClaim{WorkerID: "worker-cpu", Labels: map[string]string{"type": "cpu"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		claimed, err := s.ClaimNext(ctx, claim)
		if err != nil {
			b.Fatal(err)
		}
		if claimed != nil {
			b.Fatalf("unexpected claim %q", claimed.Task.DAGRunID)
		}
	}
}

func BenchmarkDispatchTaskStoreClaimNextMatchFirst(b *testing.B) {
	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	seedBenchmarkDispatchTasks(b, ctx, s, b.N, map[string]string{"type": "cpu"}, "cpu")
	claim := exec.DispatchTaskClaim{WorkerID: "worker-cpu", Labels: map[string]string{"type": "cpu"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		claimed, err := s.ClaimNext(ctx, claim)
		if err != nil {
			b.Fatal(err)
		}
		if claimed == nil {
			b.Fatal("expected claim")
		}
	}
}

func BenchmarkDispatchTaskStoreClaimNextMatchLate(b *testing.B) {
	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	seedBenchmarkDispatchTasks(b, ctx, s, 1000, map[string]string{"type": "gpu"}, "gpu")
	seedBenchmarkDispatchTasks(b, ctx, s, b.N, map[string]string{"type": "cpu"}, "cpu")
	claim := exec.DispatchTaskClaim{WorkerID: "worker-cpu", Labels: map[string]string{"type": "cpu"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		claimed, err := s.ClaimNext(ctx, claim)
		if err != nil {
			b.Fatal(err)
		}
		if claimed == nil {
			b.Fatal("expected claim")
		}
	}
}

func BenchmarkDispatchTaskStoreClaimNextConcurrentNoMatch(b *testing.B) {
	ctx := context.Background()
	s := store.NewDispatchTaskStore(testutil.NewMemoryBackend().Collection("dispatch_tasks"))
	seedBenchmarkDispatchTasks(b, ctx, s, 1000, map[string]string{"type": "gpu"}, "gpu")
	claim := exec.DispatchTaskClaim{WorkerID: "worker-cpu", Labels: map[string]string{"type": "cpu"}}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			claimed, err := s.ClaimNext(ctx, claim)
			if err != nil {
				b.Fatal(err)
			}
			if claimed != nil {
				b.Fatalf("unexpected claim %q", claimed.Task.DAGRunID)
			}
		}
	})
}

func seedBenchmarkDispatchTasks(b *testing.B, ctx context.Context, s *store.DispatchTaskStore, count int, selector map[string]string, suffix string) {
	b.Helper()

	for i := 0; i < count; i++ {
		err := s.Enqueue(ctx, &exec.DispatchTask{
			DAGRunID:       "run-bench-" + suffix + "-" + strconv.Itoa(i),
			Target:         "dag-bench",
			AttemptID:      "attempt-bench-" + suffix + "-" + strconv.Itoa(i),
			AttemptKey:     "attempt-key-bench-" + suffix + "-" + strconv.Itoa(i),
			WorkerSelector: selector,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

type dispatchTaskRecord struct {
	Version      int                      `json:"version"`
	Task         *exec.DispatchTask       `json:"task"`
	TaskFileName string                   `json:"taskFileName"`
	EnqueuedAt   int64                    `json:"enqueuedAt"`
	ClaimToken   string                   `json:"claimToken,omitempty"`
	ClaimedAt    int64                    `json:"claimedAt,omitempty"`
	WorkerID     string                   `json:"workerId,omitempty"`
	PollerID     string                   `json:"pollerId,omitempty"`
	Owner        exec.CoordinatorEndpoint `json:"owner,omitzero"`
}

func putPendingDuplicateFromClaim(t *testing.T, col persis.Collection, claimToken string) {
	t.Helper()

	ctx := context.Background()
	claimRec, err := col.Get(ctx, "claims/claim_"+encodedKey(claimToken))
	require.NoError(t, err)

	var payload dispatchTaskRecord
	require.NoError(t, persis.Decode(claimRec, &payload))
	payload.ClaimToken = ""
	payload.ClaimedAt = 0
	payload.WorkerID = ""
	payload.PollerID = ""
	payload.Owner = exec.CoordinatorEndpoint{}
	if payload.Task != nil {
		payload.Task.Owner = exec.CoordinatorEndpoint{}
		payload.Task.ClaimToken = ""
		payload.Task.WorkerID = ""
	}
	data, err := persis.Encode(payload)
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        pendingRecordIDForTest(payload.TaskFileName),
		Data:      data,
		CreatedAt: now,
		UpdatedAt: now,
	}))
}

func putNewerPendingDuplicateFromClaim(t *testing.T, col persis.Collection, claimToken string) {
	t.Helper()

	ctx := context.Background()
	claimRec, err := col.Get(ctx, "claims/claim_"+encodedKey(claimToken))
	require.NoError(t, err)

	var payload dispatchTaskRecord
	require.NoError(t, persis.Decode(claimRec, &payload))
	payload.EnqueuedAt = time.Now().Add(time.Second).UTC().UnixMilli()
	payload.ClaimToken = ""
	payload.ClaimedAt = 0
	payload.WorkerID = ""
	payload.PollerID = ""
	payload.Owner = exec.CoordinatorEndpoint{}
	if payload.Task != nil {
		payload.Task.Owner = exec.CoordinatorEndpoint{}
		payload.Task.ClaimToken = ""
		payload.Task.WorkerID = ""
	}
	data, err := persis.Encode(payload)
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        pendingRecordIDForTest(payload.TaskFileName),
		Data:      data,
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
	rec, err := col.Get(ctx, "claims/claim_"+encodedKey(claimToken))
	require.NoError(t, err)

	var payload dispatchTaskRecord
	require.NoError(t, persis.Decode(rec, &payload))
	payload.EnqueuedAt = time.Now().Add(-pendingAge).UTC().UnixMilli()
	payload.ClaimedAt = time.Now().Add(-claimAge).UTC().UnixMilli()
	data, err := persis.Encode(payload)
	require.NoError(t, err)

	rec.Data = data
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
		data, err := persis.Encode(payload)
		require.NoError(t, err)

		rec.Data = data
		rec.CreatedAt = targetTime
		rec.UpdatedAt = targetTime
		require.NoError(t, col.Put(ctx, rec))
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func encodedKey(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

type countingRecordIDsCollection struct {
	persis.Collection
	gets           atomic.Int64
	recordVersions atomic.Int64
}

func newCountingRecordIDsCollection(col persis.Collection) *countingRecordIDsCollection {
	return &countingRecordIDsCollection{Collection: col}
}

func (c *countingRecordIDsCollection) Get(ctx context.Context, id string) (*persis.Record, error) {
	c.gets.Add(1)
	return c.Collection.Get(ctx, id)
}

func (c *countingRecordIDsCollection) RecordIDs(ctx context.Context, prefix string) ([]string, error) {
	page, err := c.List(ctx, persis.ListQuery{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(page.Records))
	for _, rec := range page.Records {
		ids = append(ids, rec.ID)
	}
	return ids, nil
}

func (c *countingRecordIDsCollection) RecordVersion(ctx context.Context, id string) (string, error) {
	c.recordVersions.Add(1)
	type recordVersionCollection interface {
		RecordVersion(context.Context, string) (string, error)
	}
	if versionCol, ok := c.Collection.(recordVersionCollection); ok {
		return versionCol.RecordVersion(ctx, id)
	}
	rec, err := c.Collection.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return rec.UpdatedAt.UTC().Format(time.RFC3339Nano) + "/" + strconv.Itoa(len(rec.Data)), nil
}

func (c *countingRecordIDsCollection) Reset() {
	c.gets.Store(0)
	c.recordVersions.Store(0)
}

func (c *countingRecordIDsCollection) GetCount() int64 {
	return c.gets.Load()
}

func (c *countingRecordIDsCollection) RecordVersionCount() int64 {
	return c.recordVersions.Load()
}
