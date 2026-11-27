// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const boundDAGSpec = `name: deploy-one
node: required
steps:
  - name: where
    run: echo ok
`

// withWorkerNode gives a the nodes of an installation with one worker node,
// gw-1, and no inventory node.
func withWorkerNode(t *testing.T, a *API) {
	t.Helper()
	backend := file.NewBackend(config.PathsConfig{DataDir: t.TempDir()})
	inventories, err := store.NewInventoryStore(backend.Collection(persis.CollectionInventories), backend.Lock(persis.CollectionInventories), nil)
	require.NoError(t, err)
	workers, err := store.NewWorkerNodeStore(backend.Collection(persis.CollectionWorkerNodes), nil)
	require.NoError(t, err)
	require.NoError(t, workers.Record(t.Context(), inventory.WorkerNode{Name: "gw-1", Host: "gw.example", Online: true}))
	a.inventoryStore = inventories
	a.workerNodeStore = workers
}

// A start of a run bound to a worker node goes to that worker in local mode
// too, bound to the node, so the coordinator refuses it while the worker is
// offline.
func TestStartBoundToWorkerNodeDispatchesToWorker(t *testing.T) {
	t.Parallel()

	a, dag := setupEditRetryAPI(t, t.TempDir(), boundDAGSpec)
	withWorkerNode(t, a)
	stop := errors.New("stop after dispatch")
	recorder := &retryCoordinatorRecorder{dispatchErr: stop}
	a.coordinatorCli = recorder

	_, err := a.startDAGRun(context.Background(), dag, manualStart{dagRunID: "run-1", targetNodes: []string{"gw-1"}})
	require.ErrorIs(t, err, stop)
	require.Len(t, recorder.dispatched, 1)
	task := recorder.dispatched[0]
	assert.Equal(t, dispatch.DispatchOperationStart, task.Operation)
	assert.Equal(t, "gw-1", task.TargetWorkerID)
	assert.Equal(t, "gw-1", task.BoundNode)
	assert.Equal(t, []string{"gw-1"}, task.TargetNodes)
}

// Without a coordinator, a start of a run bound to a worker node fails
// before any run exists.
func TestStartBoundToWorkerNodeWithoutCoordinator(t *testing.T) {
	t.Parallel()

	a, dag := setupEditRetryAPI(t, t.TempDir(), boundDAGSpec)
	withWorkerNode(t, a)

	_, err := a.startDAGRun(context.Background(), dag, manualStart{dagRunID: "run-1", targetNodes: []string{"gw-1"}})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.HTTPStatus)
	assert.Contains(t, apiErr.Message, `node "gw-1" is a worker node`)
}

// A root run bound to a worker node resumes on that worker.
func TestResumeManagedAttemptBoundToWorkerNode(t *testing.T) {
	t.Parallel()

	a, dag := setupEditRetryAPI(t, t.TempDir(), boundDAGSpec)
	withWorkerNode(t, a)
	recorder := &retryCoordinatorRecorder{}
	a.coordinatorCli = recorder
	status := &ir.DAGRunStatus{Name: dag.Name, DAGRunID: "run-1", Status: ir.Waiting, TargetNodes: []string{"gw-1"}}

	require.NoError(t, a.resumeManagedAttempt(t.Context(), dag, status, status.DAGRunID, nil, dagrun.RetryPath{}))
	require.Len(t, recorder.dispatched, 1)
	task := recorder.dispatched[0]
	assert.Equal(t, dispatch.DispatchOperationRetry, task.Operation)
	assert.Equal(t, "gw-1", task.TargetWorkerID)
	assert.Equal(t, "gw-1", task.BoundNode)
}

// A retry of a run bound to a worker node goes to that worker in local mode
// too, as a retry of the run.
func TestRetryBoundToWorkerNodeDispatchesToWorker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, dag := setupEditRetryAPI(t, t.TempDir(), boundDAGSpec)
	withWorkerNode(t, a)
	recorder := &retryCoordinatorRecorder{}
	a.coordinatorCli = recorder

	attempt, err := a.dagRunRepository.CreateAttempt(ctx, dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	failed := ir.NewStatusBuilder(dag).Create("run-1", ir.Failed, 0, time.Now(),
		ir.WithAttemptID(attempt.ID()), ir.WithTargetNodes([]string{"gw-1"}), ir.WithBoundNode("gw-1"))
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, failed))
	require.NoError(t, attempt.Close(ctx))

	_, err = a.retryDAGRun(ctx, dag.Name, "run-1", "", "", "", false, false)
	require.NoError(t, err)
	require.Len(t, recorder.dispatched, 1)
	task := recorder.dispatched[0]
	assert.Equal(t, dispatch.DispatchOperationRetry, task.Operation)
	assert.Equal(t, "gw-1", task.TargetWorkerID)
	assert.Equal(t, "gw-1", task.BoundNode)
	assert.Equal(t, []string{"gw-1"}, task.TargetNodes)
}
