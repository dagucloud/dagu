// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/stretchr/testify/require"
)

func (f *testFixture) enqueueWithOptions(opts launcher.EnqueueOptions) error {
	f.t.Helper()
	opts.Quiet = true
	subCmdBuilder := launcher.NewSubCmdBuilder(f.coord.Config)
	return launcher.Run(f.coord.Context, subCmdBuilder.Enqueue(f.dagWrapper.DAG, opts))
}

func (f *testFixture) waitForNoMatchingWorker() {
	f.t.Helper()
	f.requireEventuallyNoSchedulerError(
		"DAG should stay queued while no worker matches its selector",
		executionStatusTimeout(),
		100*time.Millisecond,
		func() bool {
			status, err := f.latestStoredStatus()
			if err != nil || status.Status != ir.Queued {
				return false
			}
			for _, condition := range status.Conditions {
				if condition.Type == "WorkerReady" && condition.Status == "False" && condition.Reason == "NoMatchingWorker" {
					return true
				}
			}
			return false
		},
	)
}

// A run enqueued with its own selector goes to the workers that selector
// names, not to the ones the DAG file names.
func TestEnqueue_PerRunWorkerSelector(t *testing.T) {
	t.Parallel()

	const yaml = `
worker_selector:
  zone: a
steps:
  - name: echo
    run: echo routed
`

	t.Run("RoutesToTheSelectedWorker", func(t *testing.T) {
		t.Parallel()
		f := newTestFixture(t, yaml, withWorkerCount(0))
		f.workers = append(f.workers,
			f.setupWorker("worker-a", map[string]string{"zone": "a"}, ""),
			f.setupWorker("worker-b", map[string]string{"zone": "b"}, ""),
		)

		require.NoError(t, f.enqueueWithOptions(launcher.EnqueueOptions{
			WorkerSelector: map[string]string{"zone": "b"},
		}))
		f.waitForQueued()
		f.startScheduler(30 * time.Second)

		status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
		f.assertWorkerID(status, "worker-b")
	})

	t.Run("WaitsForAMatchingWorker", func(t *testing.T) {
		t.Parallel()
		f := newTestFixture(t, yaml, withWorkerCount(0))
		f.workers = append(f.workers, f.setupWorker("worker-a", map[string]string{"zone": "a"}, ""))

		require.NoError(t, f.enqueueWithOptions(launcher.EnqueueOptions{
			WorkerSelector: map[string]string{"zone": "b"},
		}))
		f.waitForQueued()
		f.startScheduler(30 * time.Second)
		f.waitForNoMatchingWorker()

		f.workers = append(f.workers, f.setupWorker("worker-b", map[string]string{"zone": "b"}, ""))

		status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
		f.assertWorkerID(status, "worker-b")
	})
}
