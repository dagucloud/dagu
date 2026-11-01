// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The snapshot a run is persisted with carries the per-run selector, which
// is what retries, reschedules, and queued dispatch read later.
func TestWorkerLabelFlag(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `worker_selector: local
steps:
  - name: "1"
    run: "true"
`)

	t.Run("EnqueueReplacesTheDAGSelector", func(t *testing.T) {
		th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
			Args:        []string{"enqueue", "--run-id=wl-enqueue", "--worker-label=zone=b", "--worker-label", "gpu=yes", dag.Location},
			ExpectedOut: []string{"Enqueued"},
		})

		snapshot := snapshotDAG(t, th, dag.Name, "wl-enqueue")
		assert.Equal(t, map[string]string{"zone": "b", "gpu": "yes"}, snapshot.WorkerSelector)
		assert.False(t, snapshot.ForceLocal)
	})

	t.Run("StartReplacesTheDAGSelector", func(t *testing.T) {
		// Without a coordinator the run still executes locally, as the YAML key would.
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id=wl-start", "--worker-label=zone=b", dag.Location},
		})

		snapshot := snapshotDAG(t, th, dag.Name, "wl-start")
		assert.Equal(t, map[string]string{"zone": "b"}, snapshot.WorkerSelector)
		assert.False(t, snapshot.ForceLocal)
	})

	t.Run("WithoutTheFlagTheDAGSelectorStays", func(t *testing.T) {
		th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
			Args:        []string{"enqueue", "--run-id=wl-plain", dag.Location},
			ExpectedOut: []string{"Enqueued"},
		})

		snapshot := snapshotDAG(t, th, dag.Name, "wl-plain")
		assert.Empty(t, snapshot.WorkerSelector)
		assert.True(t, snapshot.ForceLocal)
	})

	t.Run("InvalidLabelsAreRefused", func(t *testing.T) {
		for _, label := range []string{"zone", "zone=", "=b", "zo ne=b", "zone=b c"} {
			err := th.ExecuteCommand(cmd.Enqueue(), test.CmdTest{
				Args: []string{"enqueue", "--worker-label", label, dag.Location},
			})
			require.Error(t, err, label)
		}
	})

	t.Run("StartRefusesTheFlagOnAWorker", func(t *testing.T) {
		err := th.ExecuteCommand(cmd.Start(), test.CmdTest{
			Args: []string{"start", "--worker-id=worker-1", "--attempt-id=att", "--worker-label=zone=b", dag.Location},
		})
		require.ErrorContains(t, err, "--worker-label cannot be combined with --worker-id")
	})
}

func snapshotDAG(t *testing.T, th test.Command, name, runID string) *ir.DAG {
	t.Helper()
	attempt, err := th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(name, runID))
	require.NoError(t, err)
	dag, err := attempt.ReadDAG(th.Context)
	require.NoError(t, err)
	return dag
}
