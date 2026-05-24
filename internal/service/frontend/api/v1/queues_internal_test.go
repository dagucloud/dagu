// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/persis/filedistributed"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetQueueFiltersDistributedRunsByLeaseFreshness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
	leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
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
	leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
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
		status core.Status
	}{
		{name: "Queued", status: core.Queued},
		{name: "NotStarted", status: core.NotStarted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			tmpDir := t.TempDir()
			dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
			leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
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
		})
	}
}

func TestGetQueueCountsQueuedItemsSeparatelyFromRunningItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()
	dagRunStore := filedagrun.New(filepath.Join(tmpDir, "dag-runs"))
	leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(tmpDir, "distributed"))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	procStore := newTestProcStore(filepath.Join(tmpDir, "proc"))

	createDistributedQueueRun(t, ctx, dagRunStore, leaseStore, "mixed-q", "running-run", "mixed-q", time.Now())
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "mixed-q", "queued-run", core.Queued)

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

	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-1", core.Queued)
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-2", core.Running)
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-3", core.Queued)
	createQueuedQueueRun(t, ctx, dagRunStore, queueStore, "cursor-q", "run-4", core.Queued)

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
	store exec.DAGRunStore,
	leaseStore exec.DAGRunLeaseStore,
	name string,
	dagRunID string,
	leaseQueueName string,
	lastHeartbeatAt time.Time,
) {
	t.Helper()
	createDistributedQueueRunWithStatus(t, ctx, store, leaseStore, name, dagRunID, leaseQueueName, lastHeartbeatAt, core.Running)
}

func createDistributedQueueRunWithStatus(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	leaseStore exec.DAGRunLeaseStore,
	name string,
	dagRunID string,
	leaseQueueName string,
	lastHeartbeatAt time.Time,
	status core.Status,
) {
	t.Helper()

	dag := &core.DAG{
		Name: name,
		Steps: []core.Step{
			{Name: "step", Command: "echo hello"},
		},
	}

	attempt, err := store.CreateAttempt(ctx, dag, time.Now().UTC(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		require.NoError(t, attempt.Close(ctx))
	}()

	runStatus := exec.InitialStatus(dag)
	runStatus.Status = status
	runStatus.DAGRunID = dagRunID
	runStatus.AttemptID = attempt.ID()
	runStatus.ProcGroup = name
	runStatus.WorkerID = "worker-1"
	if status == core.Running {
		runStatus.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	runStatus.CreatedAt = time.Now().UnixMilli()

	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      exec.GenerateAttemptKey(name, dagRunID, name, dagRunID, attempt.ID()),
		DAGRun:          exec.NewDAGRunRef(name, dagRunID),
		Root:            exec.NewDAGRunRef(name, dagRunID),
		AttemptID:       attempt.ID(),
		QueueName:       leaseQueueName,
		WorkerID:        "worker-1",
		LastHeartbeatAt: lastHeartbeatAt.UTC().UnixMilli(),
	}))
}

func createQueuedQueueRun(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	queueStore exec.QueueStore,
	name string,
	dagRunID string,
	status core.Status,
) {
	t.Helper()

	dag := &core.DAG{
		Name: name,
		Steps: []core.Step{
			{Name: "step", Command: "echo hello"},
		},
	}

	attempt, err := store.CreateAttempt(ctx, dag, time.Now().UTC(), dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		require.NoError(t, attempt.Close(ctx))
	}()

	runStatus := exec.InitialStatus(dag)
	runStatus.Status = status
	runStatus.DAGRunID = dagRunID
	runStatus.AttemptID = attempt.ID()
	runStatus.ProcGroup = name
	runStatus.QueuedAt = time.Now().UTC().Format(time.RFC3339)
	runStatus.CreatedAt = time.Now().UnixMilli()
	if status == core.Running {
		runStatus.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}

	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, queueStore.Enqueue(ctx, name, exec.QueuePriorityLow, exec.NewDAGRunRef(name, dagRunID)))
}

func queueListLimitPtr(v int) *openapiv1.QueueListLimit {
	limit := openapiv1.QueueListLimit(v)
	return &limit
}
