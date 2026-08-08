// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
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
	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
	leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
	procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))

	createDistributedQueueRun(t, ctx, dagRunStore, leaseStore, "lease-q", "fresh-run", "lease-q", time.Now())
	createDistributedQueueRun(t, ctx, dagRunStore, leaseStore, "lease-q", "stale-run", "lease-q", time.Now().Add(-2*time.Minute))

	a := &API{
		dagRunStore:         dagRunStore,
		dagRunLeaseStore:    leaseStore,
		procStore:           procStore,
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
	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
	leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
	procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))

	createDistributedQueueRun(t, ctx, dagRunStore, leaseStore, "fallback-q", "fresh-run", "", time.Now())

	a := &API{
		dagRunStore:         dagRunStore,
		dagRunLeaseStore:    leaseStore,
		procStore:           procStore,
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
			dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
			leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
			procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))

			createDistributedQueueRunWithStatus(t, ctx, dagRunStore, leaseStore, "lease-q", "claimed-run", "lease-q", time.Now(), tt.status)

			a := &API{
				dagRunStore:         dagRunStore,
				dagRunLeaseStore:    leaseStore,
				procStore:           procStore,
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
	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
	leaseStore := newTestDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))

	createDistributedQueueRun(t, ctx, dagRunStore, leaseStore, "mixed-q", "running-run", "mixed-q", time.Now())
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "mixed-q", "queued-run", ir.Queued)

	a := &API{
		dagRunStore:         dagRunStore,
		dagRunLeaseStore:    leaseStore,
		queueStore:          queueStore,
		procStore:           procStore,
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
	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))

	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-1", ir.Queued)
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-2", ir.Running)
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-3", ir.Queued)
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-4", ir.Queued)

	a := &API{
		dagRunStore: dagRunStore,
		queueStore:  queueStore,
		procStore:   procStore,
		config:      &config.Config{},
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
	store dagrun.DAGRunStore,
	leaseStore exec.DAGRunLeaseStore,
	name string,
	dagRunID string,
	leaseQueueName string,
	lastHeartbeatAt time.Time,
) {
	t.Helper()
	createDistributedQueueRunWithStatus(t, ctx, store, leaseStore, name, dagRunID, leaseQueueName, lastHeartbeatAt, ir.Running)
}

func createDistributedQueueRunWithStatus(
	t *testing.T,
	ctx context.Context,
	store dagrun.DAGRunStore,
	leaseStore exec.DAGRunLeaseStore,
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

	attempt, err := store.CreateAttempt(ctx, dag, time.Now().UTC(), dagRunID, dagrun.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		require.NoError(t, attempt.Close(ctx))
	}()

	runStatus := dagrun.InitialStatus(dag)
	runStatus.Status = status
	runStatus.DAGRunID = dagRunID
	runStatus.AttemptID = attempt.ID()
	runStatus.ProcGroup = name
	runStatus.WorkerID = "worker-1"
	if status == ir.Queued {
		runStatus.Conditions = []dagrun.DAGRunCondition{
			dagrun.NewDAGRunCondition(
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
	require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      dagrun.GenerateAttemptKey(name, dagRunID, name, dagRunID, attempt.ID()),
		DAGRun:          dagrun.NewDAGRunRef(name, dagRunID),
		Root:            dagrun.NewDAGRunRef(name, dagRunID),
		AttemptID:       attempt.ID(),
		QueueName:       leaseQueueName,
		WorkerID:        "worker-1",
		LastHeartbeatAt: lastHeartbeatAt.UTC().UnixMilli(),
	}))
}

func createQueuedQueueRun(
	t *testing.T,
	ctx context.Context,
	store dagrun.DAGRunStore,
	queueStore exec.QueueStore,
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

	attempt, err := store.CreateAttempt(ctx, dag, time.Now().UTC(), dagRunID, dagrun.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		require.NoError(t, attempt.Close(ctx))
	}()

	runStatus := dagrun.InitialStatus(dag)
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
	require.NoError(t, queueStore.Enqueue(ctx, name, exec.QueuePriorityLow, dagrun.NewDAGRunRef(name, dagRunID)))
}

func queueListLimitPtr(v int) *openapiv1.QueueListLimit {
	limit := openapiv1.QueueListLimit(v)
	return &limit
}
