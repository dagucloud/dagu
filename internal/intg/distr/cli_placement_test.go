// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// selectorDAG matches the labels of the fixture's worker, worker-1.
const selectorDAG = `
worker_selector:
  test: "true"
steps:
  - id: note
    action: log.write
    with:
      message: placed
`

// runCommand runs spec and waits for it to exit.
func (f *testFixture) runCommand(spec launcher.CmdSpec) error {
	f.t.Helper()
	return launcher.Run(f.coord.Context, spec)
}

// requireRanOn requires that the latest run of the fixture's DAG ended as
// want on the worker workerID.
func (f *testFixture) requireRanOn(want ir.Status, workerID string) ir.DAGRunStatus {
	f.t.Helper()
	status, err := f.latestStoredStatus()
	require.NoError(f.t, err)
	require.Equal(f.t, want, status.Status)
	require.Equal(f.t, workerID, status.WorkerID)
	return status
}

// dagu start sends a DAG with a worker_selector to a matching worker, as the
// start request and the scheduler do, and waits for the run to end.
func TestCLIPlacement_StartWorkerSelector(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, selectorDAG, withCoordinatorEnabled())
	builder := launcher.NewSubCmdBuilder(f.coord.Config)

	require.NoError(t, f.runCommand(asUser(builder.Start(f.dagWrapper.DAG, launcher.StartOptions{Quiet: true}))))
	f.requireRanOn(ir.Succeeded, "worker-1")
}

// dagu start --worker-label routes the run to the worker with that label,
// though the DAG sets no worker_selector.
func TestCLIPlacement_StartWorkerLabel(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
steps:
  - id: note
    action: log.write
    with:
      message: placed
`, withCoordinatorEnabled())
	f.workers = append(f.workers, f.setupWorker("gpu-1", map[string]string{"gpu": "true"}, ""))
	builder := launcher.NewSubCmdBuilder(f.coord.Config)

	require.NoError(t, f.runCommand(asUser(builder.Start(f.dagWrapper.DAG, launcher.StartOptions{
		Quiet: true, WorkerSelector: map[string]string{"gpu": "true"},
	}))))
	f.requireRanOn(ir.Succeeded, "gpu-1")
}

// With coordinator.enabled false, dagu start runs a DAG with a
// worker_selector on this host, as the start request does.
func TestCLIPlacement_StartWithoutCoordinator(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, selectorDAG)
	builder := launcher.NewSubCmdBuilder(f.coord.Config)

	require.NoError(t, f.runCommand(asUser(builder.Start(f.dagWrapper.DAG, launcher.StartOptions{Quiet: true}))))
	f.requireRanOn(ir.Succeeded, "local")
}

// A start or retry that a Dagu process launches runs on this host: the
// process placed the run already, so the run is not dispatched again,
// though the rules would dispatch it.
func TestCLIPlacement_LaunchedRunStaysLocal(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  test: "true"
steps:
  - id: fail
    run: exit 1
`, withCoordinatorEnabled())
	builder := launcher.NewSubCmdBuilder(f.coord.Config)

	require.Error(t, f.runCommand(builder.Start(f.dagWrapper.DAG, launcher.StartOptions{Quiet: true})))
	first := f.requireRanOn(ir.Failed, "local")

	require.Error(t, f.runCommand(builder.Retry(f.dagWrapper.DAG, launcher.RetryOptions{DAGRunID: first.DAGRunID})))
	retried := f.requireRanOn(ir.Failed, "local")
	require.NotEqual(t, first.AttemptID, retried.AttemptID, "the retry runs in a new attempt")
}

// dagu retry sends a retry of a run with a worker_selector to a matching
// worker, in a new attempt. The step fails each time, so the test reads
// where each attempt ran.
func TestCLIPlacement_RetryWorkerSelector(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  test: "true"
steps:
  - id: fail
    run: exit 1
`, withCoordinatorEnabled())
	builder := launcher.NewSubCmdBuilder(f.coord.Config)

	require.Error(t, f.runCommand(asUser(builder.Start(f.dagWrapper.DAG, launcher.StartOptions{Quiet: true}))))
	first := f.requireRanOn(ir.Failed, "worker-1")
	f.waitForRunReleasedFromWorkers(first.DAGRunID, 10*time.Second)

	require.Error(t, f.runCommand(asUser(builder.Retry(f.dagWrapper.DAG, launcher.RetryOptions{DAGRunID: first.DAGRunID}))))
	retried := f.requireRanOn(ir.Failed, "worker-1")
	require.Equal(t, first.DAGRunID, retried.DAGRunID)
	require.NotEqual(t, first.AttemptID, retried.AttemptID, "the retry runs in a new attempt")
}

// dagu restart stops a running run with a worker_selector and starts the
// new run on a matching worker.
func TestCLIPlacement_RestartWorkerSelector(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, fmt.Sprintf(`
worker_selector:
  test: "true"
steps:
  - id: wait
    run: %s
`, test.ShellQuote(test.Sleep(60*time.Second))), withCoordinatorEnabled())
	builder := launcher.NewSubCmdBuilder(f.coord.Config)

	require.NoError(t, launcher.Start(f.coord.Context, asUser(builder.Start(f.dagWrapper.DAG, launcher.StartOptions{Quiet: true}))))
	first := f.waitForStatus(ir.Running, executionStatusTimeout())
	require.Equal(t, "worker-1", first.WorkerID)

	require.NoError(t, launcher.Start(f.coord.Context, builder.Restart(f.dagWrapper.DAG, launcher.RestartOptions{Quiet: true})))
	var restarted ir.DAGRunStatus
	f.h.Wait.EventuallyEveryWithin("the new run runs", executionStatusTimeout(), 100*time.Millisecond, func() bool {
		var err error
		restarted, err = f.latestStoredStatus()
		return err == nil && restarted.DAGRunID != first.DAGRunID && restarted.Status == ir.Running
	})
	require.Equal(t, "worker-1", restarted.WorkerID, "the new run runs on the worker")
	require.NoError(t, f.stop(restarted.DAGRunID))
	f.waitForStatusIn([]ir.Status{ir.Aborted, ir.Failed}, executionStatusTimeout())
}
