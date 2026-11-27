// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
)

// fixedNodes is a project of fixed nodes, read by name as the store reads
// them.
type fixedNodes []inventory.ResolvedNode

func (n fixedNodes) Select(context.Context, string) ([]inventory.ResolvedNode, error) {
	return nil, errors.New("not used")
}

func (n fixedNodes) Named(_ context.Context, names []string) ([]inventory.ResolvedNode, error) {
	return inventory.Named(n, names), nil
}

// enqueueBound queues run-1 of a node: required DAG bound to gw-1, recorded
// as started bound to the worker node boundWorker.
func (f *queueFixture) enqueueBound(boundWorker string) {
	f.t.Helper()
	f.dag.NodeRequired = true
	run, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, run.Open(f.ctx))
	st := ir.InitialStatus(f.dag)
	st.Status, st.DAGRunID, st.AttemptID = ir.Queued, "run-1", run.ID()
	st.TargetNodes = []string{"gw-1"}
	st.BoundWorker = boundWorker
	require.NoError(f.t, run.Write(f.ctx, st))
	require.NoError(f.t, run.Close(f.ctx))
	require.NoError(f.t, f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityHigh, ir.NewDAGRunRef(f.dag.Name, "run-1")))
}

// queuedRunStatus returns the status of run-1.
func (f *queueFixture) queuedRunStatus() *ir.DAGRunStatus {
	f.t.Helper()
	attempt, err := f.dagRunRepository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, "run-1"))
	require.NoError(f.t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(f.t, err)
	return status
}

// A queued run bound to a worker node goes to that worker, bound to it, in
// local mode too.
func TestQueueProcessor_DispatchesWorkerBoundRunToItsWorker(t *testing.T) {
	f := newQueueFixture(t).withDAG("deploy-one", 1).
		withProcessor(config.Queues{}).
		simulateQueue(1, false)
	dispatcher := &mockDispatcher{}
	f.processor.dagExecutor = NewDAGExecutor(dispatcher, nil, config.ExecutionModeLocal, "")
	f.processor.dagExecutor.nodes = fixedNodes{{Name: "gw-1", Kind: inventory.NodeKindWorker}}
	f.enqueueBound("gw-1")

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	require.Equal(t, int32(1), dispatcher.callCount.Load())
	task := dispatcher.lastReq.Task
	assert.Equal(t, "gw-1", task.TargetWorkerID)
	assert.Equal(t, "gw-1", task.BoundNode)
}

// A queued run bound to a worker node fails without a coordinator, rather
// than staying queued or running on the Dagu host.
func TestQueueProcessor_FailsWorkerBoundRunWithoutCoordinator(t *testing.T) {
	f := newQueueFixture(t).withDAG("deploy-one", 1).
		withProcessor(config.Queues{}).
		simulateQueue(1, false)
	f.processor.dagExecutor = NewDAGExecutor(nil, nil, config.ExecutionModeLocal, "")
	f.processor.dagExecutor.nodes = fixedNodes{{Name: "gw-1", Kind: inventory.NodeKindWorker}}
	f.enqueueBound("gw-1")

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	status := f.queuedRunStatus()
	assert.Equal(t, ir.Failed, status.Status)
	assert.Contains(t, status.Error, intake.ErrNoCoordinator.Error())
	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// A queued run started bound to worker node gw-1 fails once an inventory node
// takes the name, rather than running bound to that SSH node.
func TestQueueProcessor_FailsWorkerBoundRunWhenNodeChanged(t *testing.T) {
	f := newQueueFixture(t).withDAG("deploy-one", 1).
		withProcessor(config.Queues{}).
		simulateQueue(1, false)
	dispatcher := &mockDispatcher{}
	f.processor.dagExecutor = NewDAGExecutor(dispatcher, nil, config.ExecutionModeLocal, "")
	f.processor.dagExecutor.nodes = fixedNodes{{Name: "gw-1", Host: "10.0.0.5"}}
	f.enqueueBound("gw-1")

	f.processor.ProcessQueueItems(f.ctx, f.dag.Name)

	assert.Zero(t, dispatcher.callCount.Load())
	status := f.queuedRunStatus()
	assert.Equal(t, ir.Failed, status.Status)
	assert.Contains(t, status.Error, `node "gw-1": `+intake.ErrWorkerNodeChanged.Error())
}
