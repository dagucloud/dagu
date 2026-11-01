// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
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
func (f *testFixture) dispatchDirect() (string, error) {
	f.t.Helper()
	runID, err := ir.NewDAGRunID()
	if err != nil {
		return "", err
	}
	dag := f.dagWrapper.DAG
	task := executor.CreateTask(dag.Name, string(dag.YamlData), dispatch.DispatchOperationStart, runID,
		executor.WithWorkerSelector(dag.WorkerSelector))
	return runID, f.coordinatorClient.Dispatch(f.coord.Context, dispatch.DispatchRequest{Task: task})
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
