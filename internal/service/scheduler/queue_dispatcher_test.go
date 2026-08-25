// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testQueuedItem struct {
	id  string
	ref *ir.DAGRunRef
	err error
}

func (i testQueuedItem) ID() string {
	return i.id
}

func (i testQueuedItem) Data() (*ir.DAGRunRef, error) {
	if i.err != nil {
		return nil, i.err
	}
	return i.ref, nil
}

func TestQueueDispatcher_SelectRunnableQueueItemsSkipsOutstandingReservations(t *testing.T) {
	f := newQueueFixture(t).withDAG("dispatcher-select-dag", 2).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(freshDistributedTestThreshold)).
		simulateQueue(2, false)

	f.enqueueRuns(2)

	reservedRef := ir.NewDAGRunRef(f.dag.Name, "run-1")
	reservedAttempt, err := f.dagRunRepository.FindAttempt(f.ctx, reservedRef)
	require.NoError(t, err)
	reservedStatus, err := reservedAttempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &dispatch.DispatchTask{
		DAGRunID:   reservedRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptID:  reservedAttempt.ID(),
		AttemptKey: queueAttemptKey(reservedRef, reservedAttempt, reservedStatus),
	}))

	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunRepository:    f.dagRunRepository,
		dispatchTaskStore:   f.dispatchStore,
		leaseStaleThreshold: freshDistributedTestThreshold,
	})
	runnable, err := dispatcher.selectRunnableQueueItems(f.ctx, items, 1)
	require.NoError(t, err)
	require.Len(t, runnable, 1)

	selectedRef, err := runnable[0].Data()
	require.NoError(t, err)
	assert.Equal(t, "run-2", selectedRef.ID)
}

func TestQueueDispatcher_SelectRunnableQueueItemsSkipsInvalidItems(t *testing.T) {
	dispatcher := newQueueDispatcher(queueDispatchDeps{})
	validRef := ir.NewDAGRunRef("dag", "run-ok")

	runnable, retryScan, err := dispatcher.selectRunnableQueueItemsInQueue(t.Context(), "", []queuedomain.QueuedItemData{
		testQueuedItem{id: "bad", err: fmt.Errorf("invalid queued item")},
		testQueuedItem{id: "ok", ref: &validRef},
	}, 1, &workerHeartbeatSnapshot{})
	require.NoError(t, err)
	require.Len(t, runnable, 1)
	assert.Equal(t, "ok", runnable[0].ID())
	assert.True(t, retryScan)
}
