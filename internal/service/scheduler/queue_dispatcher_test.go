// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueueDispatcher_SelectRunnableQueueItemsSkipsOutstandingReservations(t *testing.T) {
	f := newQueueFixture(t).withDAG("dispatcher-select-dag", 2).
		withProcessor(config.Queues{}, WithLeaseStaleThreshold(5*time.Second)).
		simulateQueue(2, false)

	f.enqueueRuns(2)

	reservedRef := exec.NewDAGRunRef(f.dag.Name, "run-1")
	reservedAttempt, err := f.dagRunStore.FindAttempt(f.ctx, reservedRef)
	require.NoError(t, err)
	reservedStatus, err := reservedAttempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.dispatchStore.Enqueue(f.ctx, &coordinatorv1.Task{
		DagRunId:   reservedRef.ID,
		Target:     f.dag.Name,
		QueueName:  f.dag.Name,
		AttemptId:  reservedAttempt.ID(),
		AttemptKey: queueAttemptKey(reservedRef, reservedAttempt, reservedStatus),
	}))

	items, err := f.queueStore.List(f.ctx, f.dag.Name)
	require.NoError(t, err)

	dispatcher := newQueueDispatcher(queueDispatchDeps{
		dagRunStore:         f.dagRunStore,
		dispatchTaskStore:   f.dispatchStore,
		leaseStaleThreshold: 5 * time.Second,
	})
	runnable, err := dispatcher.selectRunnableQueueItems(f.ctx, items, 1)
	require.NoError(t, err)
	require.Len(t, runnable, 1)

	selectedRef, err := runnable[0].Data()
	require.NoError(t, err)
	assert.Equal(t, "run-2", selectedRef.ID)
}
