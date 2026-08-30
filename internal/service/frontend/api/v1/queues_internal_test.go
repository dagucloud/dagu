// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDAGRunLeaseStore(distributedDir string) *store.DAGRunLeaseStore {
	return store.NewDAGRunLeaseStore(file.NewCollection(filepath.Join(distributedDir, "leases")))
}

func TestGetQueueFiltersDistributedRunsByLeaseFreshness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	createDistributedQueueRun(t, ctx, dagRunRepository, leaseStore, "lease-q", "fresh-run", "lease-q", time.Now())
	createDistributedQueueRun(t, ctx, dagRunRepository, leaseStore, "lease-q", "stale-run", "lease-q", time.Now().Add(-2*time.Minute))

	a := &API{
		dagRunRepository:    dagRunRepository,
		dagRunLeaseStore:    leaseStore,
		procRepository:      procRepository,
		config:              &config.Config{},
		leaseStaleThreshold: time.Minute,
	}

	resp, err := a.GetQueue(ctx, openapiv1.GetQueueRequestObject{
		Name: "lease-q",
	})
	require.NoError(t, err)

	queueResp, ok := resp.(openapiv1.GetQueue200JSONResponse)
	require.True(t, ok)
	require.Len(t, queueResp.Running, 1)
	assert.Equal(t, "fresh-run", queueResp.Running[0].DagRunId)
	assert.Equal(t, openapiv1.StatusRunning, queueResp.Running[0].Status)
}

func TestGetQueueFallsBackToDAGNameWhenLeaseQueueIsEmpty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	createDistributedQueueRun(t, ctx, dagRunRepository, leaseStore, "fallback-q", "fresh-run", "", time.Now())

	a := &API{
		dagRunRepository:    dagRunRepository,
		dagRunLeaseStore:    leaseStore,
		procRepository:      procRepository,
		config:              &config.Config{},
		leaseStaleThreshold: time.Minute,
	}

	resp, err := a.GetQueue(ctx, openapiv1.GetQueueRequestObject{
		Name: "fallback-q",
	})
	require.NoError(t, err)

	queueResp, ok := resp.(openapiv1.GetQueue200JSONResponse)
	require.True(t, ok)
	require.Len(t, queueResp.Running, 1)
	assert.Equal(t, "fresh-run", queueResp.Running[0].DagRunId)
}

func TestGetQueueCountsFreshLeaseForClaimedAttemptAsRunning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status ir.Status
	}{
		{name: "Queued", status: ir.Queued},
		{name: "NotStarted", status: ir.NotStarted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			tmpDir := t.TempDir()
			dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
			leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
			procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

			createDistributedQueueRunWithStatus(t, ctx, dagRunRepository, leaseStore, "lease-q", "claimed-run", "lease-q", time.Now(), tt.status)

			a := &API{
				dagRunRepository:    dagRunRepository,
				dagRunLeaseStore:    leaseStore,
				procRepository:      procRepository,
				config:              &config.Config{},
				leaseStaleThreshold: time.Minute,
			}

			resp, err := a.GetQueue(ctx, openapiv1.GetQueueRequestObject{
				Name: "lease-q",
			})
			require.NoError(t, err)

			queueResp, ok := resp.(openapiv1.GetQueue200JSONResponse)
			require.True(t, ok)
			require.Len(t, queueResp.Running, 1)
			assert.Equal(t, 1, queueResp.RunningCount)
			assert.Equal(t, "claimed-run", queueResp.Running[0].DagRunId)
			assert.Equal(t, openapiv1.StatusRunning, queueResp.Running[0].Status)
			assert.Equal(t, openapiv1.StatusLabelRunning, queueResp.Running[0].StatusLabel)
			assert.Nil(t, queueResp.Running[0].Conditions)
		})
	}
}

func TestGetQueueCountsQueuedItemsSeparatelyFromRunningItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	createDistributedQueueRun(t, ctx, dagRunRepository, leaseStore, "mixed-q", "running-run", "mixed-q", time.Now())
	createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "mixed-q", "queued-run", ir.Queued)

	a := &API{
		dagRunRepository:    dagRunRepository,
		dagRunLeaseStore:    leaseStore,
		queueStore:          queueStore,
		procRepository:      procRepository,
		config:              &config.Config{},
		leaseStaleThreshold: time.Minute,
	}

	resp, err := a.GetQueue(ctx, openapiv1.GetQueueRequestObject{
		Name: "mixed-q",
	})
	require.NoError(t, err)

	queueResp, ok := resp.(openapiv1.GetQueue200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 1, queueResp.RunningCount)
	assert.Equal(t, 1, queueResp.QueuedCount)
}

func TestListQueueItemsUsesCursorPaginationAndSkipsRunningEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "cursor-q", "run-1", ir.Queued)
	createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "cursor-q", "run-2", ir.Running)
	createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "cursor-q", "run-3", ir.Queued)
	createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "cursor-q", "run-4", ir.Queued)

	a := &API{
		dagRunRepository: dagRunRepository,
		queueStore:       queueStore,
		procRepository:   procRepository,
		config:           &config.Config{},
	}

	firstResp, err := a.ListQueueItems(ctx, openapiv1.ListQueueItemsRequestObject{
		Name: "cursor-q",
		Params: openapiv1.ListQueueItemsParams{
			Limit: queueListLimitPtr(2),
		},
	})
	require.NoError(t, err)

	firstPage, ok := firstResp.(openapiv1.ListQueueItems200JSONResponse)
	require.True(t, ok)
	require.Len(t, firstPage.Items, 2)
	require.NotNil(t, firstPage.NextCursor)
	assert.Equal(t, "run-1", firstPage.Items[0].DagRunId)
	assert.Equal(t, "run-3", firstPage.Items[1].DagRunId)

	secondResp, err := a.ListQueueItems(ctx, openapiv1.ListQueueItemsRequestObject{
		Name: "cursor-q",
		Params: openapiv1.ListQueueItemsParams{
			Limit:  queueListLimitPtr(2),
			Cursor: firstPage.NextCursor,
		},
	})
	require.NoError(t, err)

	secondPage, ok := secondResp.(openapiv1.ListQueueItems200JSONResponse)
	require.True(t, ok)
	require.Len(t, secondPage.Items, 1)
	assert.Equal(t, "run-4", secondPage.Items[0].DagRunId)
	assert.Nil(t, secondPage.NextCursor)
}

func TestListQueuesReturnsDeterministicQueueOrder(t *testing.T) {
	t.Parallel()

	a := &API{
		config: &config.Config{
			Queues: config.Queues{
				Enabled: true,
				Config: []config.QueueConfig{
					{Name: "z-queue", MaxActiveRuns: 1},
					{Name: "a-queue", MaxActiveRuns: 1},
				},
			},
		},
	}

	resp, err := a.ListQueues(context.Background(), openapiv1.ListQueuesRequestObject{})
	require.NoError(t, err)

	queueResp, ok := resp.(openapiv1.ListQueues200JSONResponse)
	require.True(t, ok)
	require.Len(t, queueResp.Queues, 2)
	assert.Equal(t, "a-queue", queueResp.Queues[0].Name)
	assert.Equal(t, "z-queue", queueResp.Queues[1].Name)
}

func createDistributedQueueRun(
	t *testing.T,
	ctx context.Context,
	repository *persis.DAGRunRepository,
	leaseStore dispatch.DAGRunLeaseStore,
	name string,
	dagRunID string,
	leaseQueueName string,
	lastHeartbeatAt time.Time,
) {
	t.Helper()
	createDistributedQueueRunWithStatus(t, ctx, repository, leaseStore, name, dagRunID, leaseQueueName, lastHeartbeatAt, ir.Running)
}

func createDistributedQueueRunWithStatus(
	t *testing.T,
	ctx context.Context,
	repository *persis.DAGRunRepository,
	leaseStore dispatch.DAGRunLeaseStore,
	name string,
	dagRunID string,
	leaseQueueName string,
	lastHeartbeatAt time.Time,
	status ir.Status,
) {
	t.Helper()

	dag := &ir.DAG{
		Name: name,
		Steps: []ir.Step{
			{Name: "step", Command: "echo hello"},
		},
	}

	attempt, err := repository.CreateAttempt(ctx, dag, time.Now().UTC(), dagRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		require.NoError(t, attempt.Close(ctx))
	}()

	runStatus := ir.InitialStatus(dag)
	runStatus.Status = status
	runStatus.DAGRunID = dagRunID
	runStatus.AttemptID = attempt.ID()
	runStatus.ProcGroup = name
	runStatus.WorkerID = "worker-1"
	if status == ir.Queued {
		runStatus.Conditions = []ir.DAGRunCondition{
			ir.NewDAGRunCondition(
				"Runnable",
				"False",
				"MaxConcurrencyReached",
				"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
				time.Now().UTC(),
			),
		}
	}
	if status == ir.Running {
		runStatus.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	runStatus.CreatedAt = time.Now().UnixMilli()

	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey:      ir.GenerateAttemptKey(name, dagRunID, name, dagRunID, attempt.ID()),
		DAGRun:          ir.NewDAGRunRef(name, dagRunID),
		Root:            ir.NewDAGRunRef(name, dagRunID),
		AttemptID:       attempt.ID(),
		QueueName:       leaseQueueName,
		WorkerID:        "worker-1",
		LastHeartbeatAt: lastHeartbeatAt.UTC().UnixMilli(),
	}))
}

func createQueuedQueueRun(
	t *testing.T,
	ctx context.Context,
	repository *persis.DAGRunRepository,
	queueStore queue.QueueStore,
	name string,
	dagRunID string,
	status ir.Status,
) {
	t.Helper()

	dag := &ir.DAG{
		Name: name,
		Steps: []ir.Step{
			{Name: "step", Command: "echo hello"},
		},
	}

	attempt, err := repository.CreateAttempt(ctx, dag, time.Now().UTC(), dagRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		require.NoError(t, attempt.Close(ctx))
	}()

	runStatus := ir.InitialStatus(dag)
	runStatus.Status = status
	runStatus.DAGRunID = dagRunID
	runStatus.AttemptID = attempt.ID()
	runStatus.ProcGroup = name
	runStatus.QueuedAt = time.Now().UTC().Format(time.RFC3339)
	runStatus.CreatedAt = time.Now().UnixMilli()
	if status == ir.Running {
		runStatus.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}

	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, queueStore.Enqueue(ctx, name, queue.QueuePriorityLow, ir.NewDAGRunRef(name, dagRunID)))
}

func queueListLimitPtr(v int) *openapiv1.QueueListLimit {
	limit := openapiv1.QueueListLimit(v)
	return &limit
}

func TestGetQueueCapsQueuedCountAndFlagsItAsLowerBound(t *testing.T) {
	// Not parallel: this test temporarily lowers the package-level scan cap.
	originalCap := queuedCountScanCap
	queuedCountScanCap = 3
	t.Cleanup(func() { queuedCountScanCap = originalCap })

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	for i := range 5 {
		createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "deep-q", fmt.Sprintf("queued-run-%d", i), ir.Queued)
	}

	a := &API{
		dagRunRepository:    dagRunRepository,
		queueStore:          queueStore,
		procRepository:      procRepository,
		config:              &config.Config{},
		leaseStaleThreshold: time.Minute,
	}

	resp, err := a.GetQueue(ctx, openapiv1.GetQueueRequestObject{Name: "deep-q"})
	require.NoError(t, err)

	queueResp, ok := resp.(openapiv1.GetQueue200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 3, queueResp.QueuedCount, "count stops at the cap")
	require.NotNil(t, queueResp.QueuedCountCapped)
	assert.True(t, *queueResp.QueuedCountCapped, "capped count is flagged as a lower bound")
}

func TestGetQueueReportsExactQueuedCountBelowCap(t *testing.T) {
	// Not parallel: this test temporarily lowers the package-level scan cap.
	originalCap := queuedCountScanCap
	queuedCountScanCap = 10
	t.Cleanup(func() { queuedCountScanCap = originalCap })

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	for i := range 4 {
		createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "shallow-q", fmt.Sprintf("queued-run-%d", i), ir.Queued)
	}

	a := &API{
		dagRunRepository:    dagRunRepository,
		queueStore:          queueStore,
		procRepository:      procRepository,
		config:              &config.Config{},
		leaseStaleThreshold: time.Minute,
	}

	resp, err := a.GetQueue(ctx, openapiv1.GetQueueRequestObject{Name: "shallow-q"})
	require.NoError(t, err)

	queueResp, ok := resp.(openapiv1.GetQueue200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 4, queueResp.QueuedCount)
	assert.Nil(t, queueResp.QueuedCountCapped, "an exact count sets no capped flag")
}

func TestGetQueueCapsScanWhenRunningEntriesPrecedeQueuedEntries(t *testing.T) {
	// Not parallel: this test temporarily lowers the package-level scan cap.
	originalCap := queuedCountScanCap
	queuedCountScanCap = 3
	t.Cleanup(func() { queuedCountScanCap = originalCap })

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	// More running entries than the cap, ahead of any visible queued entry.
	// These are invisible to the count but still cost a status read each, so the
	// scan must stop on them rather than running to the end of the queue.
	for i := range 4 {
		createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "running-heavy-q", fmt.Sprintf("running-run-%d", i), ir.Running)
	}
	for i := range 2 {
		createQueuedQueueRun(t, ctx, dagRunRepository, queueStore, "running-heavy-q", fmt.Sprintf("queued-run-%d", i), ir.Queued)
	}

	a := &API{
		dagRunRepository:    dagRunRepository,
		queueStore:          queueStore,
		procRepository:      procRepository,
		config:              &config.Config{},
		leaseStaleThreshold: time.Minute,
	}

	resp, err := a.GetQueue(ctx, openapiv1.GetQueueRequestObject{Name: "running-heavy-q"})
	require.NoError(t, err)

	queueResp, ok := resp.(openapiv1.GetQueue200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 0, queueResp.QueuedCount, "scan stopped inside the running entries, before any visible one")
	require.NotNil(t, queueResp.QueuedCountCapped)
	assert.True(t, *queueResp.QueuedCountCapped, "a truncated scan is reported as a lower bound")
}

func TestListQueuesBoundsTotalScanAcrossQueues(t *testing.T) {
	// Not parallel: this test temporarily lowers the package-level scan caps.
	originalQueueCap, originalRequestCap := queuedCountScanCap, queuedCountRequestScanCap
	queuedCountScanCap = 100      // effectively disabled, so only the request budget bites
	queuedCountRequestScanCap = 5 // total across all queues
	t.Cleanup(func() {
		queuedCountScanCap = originalQueueCap
		queuedCountRequestScanCap = originalRequestCap
	})

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunRepository := testutil.NewFileDAGRunRepository(filepath.Join(tmpDir, "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procRepository := newTestProcRepository(filepath.Join(tmpDir, "proc"))

	// Four queues of four entries each: 16 entries, far above the request budget.
	// The per-queue cap alone would not bound this, since each queue is small.
	for q := range 4 {
		for i := range 4 {
			createQueuedQueueRun(t, ctx, dagRunRepository, queueStore,
				fmt.Sprintf("budget-q-%d", q), fmt.Sprintf("q%d-run-%d", q, i), ir.Queued)
		}
	}

	a := &API{
		dagRunRepository:    dagRunRepository,
		queueStore:          queueStore,
		procRepository:      procRepository,
		config:              &config.Config{},
		leaseStaleThreshold: time.Minute,
	}

	resp, err := a.ListQueues(ctx, openapiv1.ListQueuesRequestObject{})
	require.NoError(t, err)

	listResp, ok := resp.(openapiv1.ListQueues200JSONResponse)
	require.True(t, ok)

	assert.LessOrEqual(t, listResp.Summary.TotalQueued, queuedCountRequestScanCap,
		"the whole request must not count more entries than the shared budget allows")
	require.NotNil(t, listResp.Summary.TotalQueuedCapped)
	assert.True(t, *listResp.Summary.TotalQueuedCapped,
		"exhausting the request budget marks the total as a lower bound")
}
