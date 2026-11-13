// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dispatchDirect sends the fixture's DAG straight to the coordinator, the
// way a start, a retry, or a sub-DAG reaches it without the scheduler's queue.
func (f *testFixture) dispatchDirect(opts ...executor.TaskOption) (string, error) {
	f.t.Helper()
	runID, err := ir.NewDAGRunID()
	if err != nil {
		return "", err
	}
	dag := f.dagWrapper.DAG
	opts = append(opts, executor.WithWorkerSelector(dag.WorkerSelector))
	task := executor.CreateTask(dag.Name, string(dag.YamlData), dispatch.DispatchOperationStart, runID, opts...)
	return runID, f.coordinatorClient.Dispatch(f.coord.Context, dispatch.DispatchRequest{Task: task})
}

// The worker records how a directly dispatched run was started.
func TestDispatch_RecordsTriggerType(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  zone: a
steps:
  - name: echo
    run: echo started
`, withWorkerCount(0))
	f.workers = append(f.workers, f.setupWorker("worker-a", map[string]string{"zone": "a"}, ""))

	_, err := f.dispatchDirect(executor.WithTriggerType(ir.TriggerTypeManual))
	require.NoError(t, err)

	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-a")
	assert.Equal(t, ir.TriggerTypeManual, status.TriggerType)
}

// A direct dispatch whose selector matches no worker is held by the
// coordinator: the run waits as Queued and runs when a matching worker
// registers.
func TestDispatch_HoldsUntilWorkerArrives(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  zone: b
steps:
  - name: echo
    run: echo held
`, withWorkerCount(0), withWorkerWaitTimeout(5*time.Minute))
	f.workers = append(f.workers, f.setupWorker("worker-a", map[string]string{"zone": "a"}, ""))

	_, err := f.dispatchDirect()
	require.NoError(t, err, "the dispatch is accepted, not refused")
	f.waitForNoMatchingWorker()

	f.workers = append(f.workers, f.setupWorker("worker-b", map[string]string{"zone": "b"}, ""))

	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-b")
}

// A worker that stops tells the coordinator before it exits. A dispatch right
// after the exit is held for the next matching worker, which runs it.
func TestDispatch_HoldsAfterWorkerStops(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  zone: b
steps:
  - name: echo
    run: echo held
`, withWorkerCount(0), withWorkerWaitTimeout(5*time.Minute))
	f.workers = append(f.workers, f.setupWorker("worker-a", map[string]string{"zone": "a"}, ""))
	gone := f.setupWorker("worker-b1", map[string]string{"zone": "b"}, "")
	stopCtx, cancel := context.WithTimeout(f.coord.Context, distrTestTimeout(10*time.Second))
	defer cancel()
	require.NoError(t, gone.Stop(stopCtx))

	_, err := f.dispatchDirect()
	require.NoError(t, err)
	f.waitForNoMatchingWorker()

	f.workers = append(f.workers, f.setupWorker("worker-b2", map[string]string{"zone": "b"}, ""))
	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-b2")
}

// A worker that exits without a word still counts as healthy until its
// heartbeat is stale, so the dispatch is not held. The task waits for a worker
// all the same: the next matching worker takes it, also after the reservation
// of the dispatch store.
func TestDispatch_NextWorkerTakesRunOfGoneWorker(t *testing.T) {
	t.Parallel()

	const lease = 3 * time.Second
	f := newTestFixture(t, `
worker_selector:
  zone: b
steps:
  - name: echo
    run: echo next
`, withWorkerCount(0), withWorkerWaitTimeout(5*time.Minute), withStaleThresholds(time.Minute, lease))
	require.NoError(t, f.coord.WorkerHeartbeatStore.Upsert(f.coord.Context, dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-gone", Labels: map[string]string{"zone": "b"}, LastHeartbeatAt: time.Now().UnixMilli(),
	}))

	_, err := f.dispatchDirect()
	require.NoError(t, err)
	// The reservation of the dispatch store is the lease threshold.
	time.Sleep(lease + time.Second)

	f.workers = append(f.workers, f.setupWorker("worker-b", map[string]string{"zone": "b"}, ""))
	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-b")
}

// When no matching worker registers within the wait, the run fails and its
// error names the selector it waited for.
func TestDispatch_HoldDeadlineFailsRun(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  zone: b
steps:
  - name: echo
    run: echo held
`, withWorkerCount(0), withWorkerWaitTimeout(2*time.Second), withZombieDetectionInterval(500*time.Millisecond))
	f.workers = append(f.workers, f.setupWorker("worker-a", map[string]string{"zone": "a"}, ""))

	_, err := f.dispatchDirect()
	require.NoError(t, err)
	f.waitForNoMatchingWorker()

	status := f.waitForStatus(ir.Failed, distrTestTimeout(30*time.Second))
	assert.Contains(t, status.Error, "no worker matched the selector {zone: b} within 2s")
}
