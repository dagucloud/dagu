// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
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

func TestResumeAdmissionDispatch(t *testing.T) {
	for _, scenario := range []string{"pending", "acknowledged", "dispatch error", "superseded", "ack error"} {
		t.Run(scenario, func(t *testing.T) {
			f := newQueueFixture(t).withDAG("resume-dispatch", 1).
				withProcessor(config.Queues{}).simulateQueue(1, false)
			attempt, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), "run", persis.DAGRunCreateAttemptOptions{})
			require.NoError(t, err)
			status := ir.InitialStatus(f.dag)
			status.DAGRunID, status.AttemptID, status.Status = "run", attempt.ID(), ir.Queued
			status.Resume, status.QueuedAt = true, time.Now().UTC().Format(time.RFC3339Nano)
			status.ResumeAdmission = &ir.ResumeAdmission{ID: "resume-admission", Admitted: scenario == "acknowledged"}
			require.NoError(t, attempt.Open(f.ctx))
			require.NoError(t, attempt.Write(f.ctx, status))
			require.NoError(t, attempt.Close(f.ctx))
			identity := queuedomain.RetryAdmissionIdentity{ID: status.ResumeAdmission.ID, AttemptID: status.AttemptID,
				AttemptKey: status.AttemptKey, QueuedAt: status.QueuedAt, Root: status.Root}
			created, err := f.queueStore.EnsureRetryAdmission(f.ctx, f.dag.Name, queuedomain.QueuePriorityLow, status.DAGRun(), identity)
			require.NoError(t, err)
			require.True(t, created)
			dispatcher := &mockDispatcher{}
			if scenario == "dispatch error" {
				dispatcher.errFunc = func(int32) error { return errors.New("dispatch unavailable") }
			}
			f.processor.dagExecutor = NewDAGExecutor(dispatcher, nil, config.ExecutionModeDistributed, "")
			activeStore := store.NewActiveDistributedRunStore(file.NewCollection(filepath.Join(f.distributedDir, "active-runs")))
			dispatchStore := store.NewDispatchTaskStore(file.NewCollection(f.distributedDir), store.WithDispatchAdmissionLiveness(f.leaseStore, activeStore))
			f.processor.dispatchTaskStore, f.processor.dispatchAdmissionStore = dispatchStore, dispatchStore
			if scenario == "superseded" {
				_, swapped, err := f.dagRunRepository.CompareAndSwapLatestAttemptStatus(f.ctx, status.DAGRun(), status.AttemptID, ir.Queued,
					func(latest *ir.DAGRunStatus) error {
						latest.ResumeAdmission = &ir.ResumeAdmission{ID: "new-generation"}
						return nil
					}, persis.DAGRunCompareAndSwapOptions{})
				require.NoError(t, err)
				require.True(t, swapped)
			}
			if scenario == "ack error" {
				items, err := f.queueStore.List(f.ctx, f.dag.Name)
				require.NoError(t, err)
				d := f.processor.newQueueDispatcher()
				d.dagRunRepository = persis.NewDAGRunRepository(&failedAdmissionAck{}, nil, persis.DAGRunRepositoryOptions{})
				assert.False(t, d.dispatchQueuedItem(f.ctx, items[0], f.dag.Name, queueDispatchBatch{maxConcurrency: 1}, func() {}, func() {}))
			} else {
				f.processor.ProcessQueueItems(f.ctx, f.dag.Name)
			}
			latest, err := attempt.ReadStatusUncached(f.ctx)
			require.NoError(t, err)
			items, err := f.queueStore.List(f.ctx, f.dag.Name)
			require.NoError(t, err)
			switch scenario {
			case "ack error":
				assert.Zero(t, dispatcher.callCount.Load())
				assert.False(t, latest.ResumeAdmission.Admitted)
				require.Len(t, items, 1)
			case "dispatch error":
				assert.EqualValues(t, 1, dispatcher.callCount.Load())
				assert.True(t, latest.ResumeAdmission.Admitted)
				require.Len(t, items, 1)
				assert.True(t, queuedomain.MatchesRetryAdmission(items[0], latest))
			case "superseded":
				assert.Zero(t, dispatcher.callCount.Load())
				assert.Equal(t, "new-generation", latest.ResumeAdmission.ID)
				assert.False(t, latest.ResumeAdmission.Admitted)
				assert.Empty(t, items)
			default:
				assert.EqualValues(t, 1, dispatcher.callCount.Load())
				assert.NotEmpty(t, dispatcher.LastRequest().AdmissionReservationToken)
				assert.Equal(t, ir.Queued, latest.Status)
				assert.True(t, latest.ResumeAdmission.Admitted)
				assert.Empty(t, items)
				created, err := queuedomain.EnqueueRetry(f.ctx, f.dagRunRepository, f.queueStore, f.dag, &status, queuedomain.EnqueueRetryOptions{Resume: true})
				require.NoError(t, err)
				assert.False(t, created)
			}
		})
	}
}

type failedAdmissionAck struct{ testutil.DAGRunStoreStub }

func (*failedAdmissionAck) CompareAndSwapLatestAttemptStatus(context.Context, persis.DAGRunCompareAndSwapStatusRequest) (*ir.DAGRunStatus, bool, error) {
	return nil, false, errors.New("acknowledgement unavailable")
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
