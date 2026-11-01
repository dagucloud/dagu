// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotDAGOf reads the DAG a run was persisted with; the HTTP run details
// do not expose the selector, and the snapshot is what routing reads later.
func snapshotDAGOf(t *testing.T, server test.Server, name, runID string) *ir.DAG {
	t.Helper()
	var dag *ir.DAG
	require.Eventually(t, func() bool {
		attempt, err := server.DAGRunRepository.FindAttempt(server.Context, ir.NewDAGRunRef(name, runID))
		if err != nil {
			return false
		}
		dag, err = attempt.ReadDAG(server.Context)
		return err == nil && dag != nil
	}, 5*time.Second, 100*time.Millisecond)
	return dag
}

// Inline specs take the same per-run selector as DAG files. The start
// paths are covered by the internal tests, since a selector dispatches to
// the coordinator the test server has no running instance of.
func TestWorkerSelectorFromSpec(t *testing.T) {
	server := test.SetupServer(t)

	spec := `
worker_selector: local
steps:
  - name: echo
    run: echo "routed"
`
	selector := api.WorkerSelectorOverride{"zone": "b"}

	t.Run("Enqueue", func(t *testing.T) {
		name := "inline_ws_enqueue"
		resp := server.Client().Post("/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{
			Spec:           spec,
			Name:           &name,
			WorkerSelector: &selector,
		}).ExpectStatus(http.StatusOK).Send(t)

		var body api.EnqueueDAGRunFromSpec200JSONResponse
		resp.Unmarshal(t, &body)

		snapshot := snapshotDAGOf(t, server, name, body.DagRunId)
		assert.Equal(t, map[string]string{"zone": "b"}, snapshot.WorkerSelector)
		assert.False(t, snapshot.ForceLocal)
	})

	t.Run("InvalidIsRefused", func(t *testing.T) {
		bad := api.WorkerSelectorOverride{"zone": "b c"}
		for _, path := range []string{"/api/v1/dag-runs", "/api/v1/dag-runs/enqueue"} {
			name := "inline_ws_bad"
			resp := server.Client().Post(path, api.ExecuteDAGRunFromSpecJSONRequestBody{
				Spec:           spec,
				Name:           &name,
				WorkerSelector: &bad,
			}).ExpectStatus(http.StatusBadRequest).Send(t)

			var errResp api.Error
			resp.Unmarshal(t, &errResp)
			require.Contains(t, errResp.Message, "workerSelector", path)
		}
	})
}
