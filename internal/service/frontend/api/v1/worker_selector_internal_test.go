// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	runtimepkg "github.com/dagucloud/dagu/v2/internal/runtime"
)

// Runtime-param resolution rebuilds the DAG from its YAML. The task handed to
// the coordinator must still carry the request's selector, not the YAML's.
func TestStartDispatchCarriesPerRunWorkerSelectorAfterResolution(t *testing.T) {
	api, dag := setupEditRetryAPI(t, t.TempDir(), `name: routed
params:
  - KEY: fallback
worker_selector:
  zone: a
steps:
  - name: print
    run: echo "${KEY}"
`)
	// The recorder refuses the dispatch after recording it, so the start
	// returns there instead of waiting for a run that never starts.
	recorder := &retryCoordinatorRecorder{dispatchErr: errors.New("recorded")}
	api.coordinatorCli = recorder
	require.NoError(t, dag.ApplyWorkerSelector(map[string]string{"zone": "b"}))

	_, err := api.startDAGRunWithOptions(t.Context(), dag, startDAGRunOptions{
		params:         "KEY=hello",
		dagRunID:       "run-routed",
		workerSelector: map[string]string{"zone": "b"},
	})
	require.ErrorContains(t, err, "recorded")

	require.Len(t, recorder.dispatched, 1)
	require.Equal(t, map[string]string{"zone": "b"}, recorder.dispatched[0].WorkerSelector)
}

// A run of selected steps is seeded from the resolved DAG, so the selector
// must be put back on it before the seed is persisted and dispatched.
func TestSelectedStepsCarryPerRunWorkerSelector(t *testing.T) {
	ctx := t.Context()
	api, dag := setupEditRetryAPI(t, t.TempDir(), `name: routed_steps
params:
  - KEY: fallback
steps:
  - name: first
    run: echo "${KEY}"
  - name: second
    run: echo second
`)
	recorder := &retryCoordinatorRecorder{dispatchErr: errors.New("recorded")}
	api.coordinatorCli = recorder
	require.NoError(t, dag.ApplyWorkerSelector(map[string]string{"zone": "b"}))

	err := api.startSelectedSteps(ctx, dag, selectedStepsStart{
		selection:      stepSelection{steps: []string{"second"}},
		params:         "KEY=hello",
		dagRunID:       "run-steps",
		workerSelector: map[string]string{"zone": "b"},
	})
	require.ErrorContains(t, err, "recorded")

	require.Len(t, recorder.dispatched, 1)
	require.Equal(t, map[string]string{"zone": "b"}, recorder.dispatched[0].WorkerSelector)

	attempt, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "run-steps"))
	require.NoError(t, err)
	snapshot, err := attempt.ReadDAG(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"zone": "b"}, snapshot.WorkerSelector)
}

// A reschedule from the snapshot keeps the selector the run was routed
// with. The snapshot's YAML alone would not: the selector never lived in it.
func TestRescheduleKeepsPerRunWorkerSelector(t *testing.T) {
	ctx := t.Context()
	tmpDir := t.TempDir()
	api, dag := setupEditRetryAPI(t, tmpDir, `name: routed_again
worker_selector: local
steps:
  - name: echo
    run: echo routed
`)
	api.config.Paths.DataDir = tmpDir
	api.config.Queues = config.Queues{Enabled: true}
	api.queueStore = store.NewQueueStore(file.NewCollection(filepath.Join(tmpDir, "queue")))
	api.dagRunMgr = runtimepkg.NewManager(api.dagRunRepository, nil, api.config)

	// The source run was routed with a per-run selector, which its snapshot
	// recorded; its YAML still says local.
	require.NoError(t, dag.ApplyWorkerSelector(map[string]string{"zone": "b"}))
	attempt, err := api.dagRunRepository.CreateAttempt(ctx, dag, time.Now().Add(-2*time.Minute), "source-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	status := ir.NewStatusBuilder(dag).Create("source-run", ir.Succeeded, 0, time.Now().Add(-2*time.Minute),
		ir.WithAttemptID(attempt.ID()), ir.WithFinishedAt(time.Now().Add(-time.Minute)))
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	result, err := api.rescheduleDAGRun(ctx, dag.Name, "source-run", rescheduleDAGRunOptions{})
	require.NoError(t, err)
	require.True(t, result.queued)

	rescheduled, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, result.newDagRunID))
	require.NoError(t, err)
	snapshot, err := rescheduled.ReadDAG(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"zone": "b"}, snapshot.WorkerSelector)
	require.False(t, snapshot.ForceLocal)
}
