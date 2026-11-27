// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// A start or enqueue checks its node list before the run is launched, so a
// mistake in it is a 400 and no run starts (RFC 013 §Narrowing a run).
func TestTargetNodes(t *testing.T) {
	server := test.SetupServer(t)
	server.Client().Post("/api/v1/inventory-imports", []map[string]any{{
		"name": "fleet",
		"nodes": []map[string]any{
			{"name": "web01", "host": "10.0.1.11", "labels": []string{"web"}},
			{"name": "db01", "host": "10.0.1.21", "labels": []string{"db"}},
		},
	}}).ExpectStatus(http.StatusOK).Send(t)
	workers, err := store.NewWorkerNodeStore(server.Backend.Collection(persis.CollectionWorkerNodes), nil)
	require.NoError(t, err)
	require.NoError(t, workers.Record(server.Context, inventory.WorkerNode{
		Name: "tokyo-gw", Host: "gw.tokyo", Labels: ir.NewLabels([]string{"web"}), Online: true,
	}))

	createDAG := func(name, spec string) {
		server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{Name: name, Spec: &spec}).
			ExpectStatus(http.StatusCreated).Send(t)
	}
	createDAG("narrowed", `
steps:
  - id: restart
    run: echo restart
    nodes: web
`)
	createDAG("remote", `
worker_selector:
  gpu: "true"
steps:
  - id: restart
    run: echo restart
    nodes: web
`)
	createDAG("plain", `
steps:
  - id: echo
    run: echo hi
`)
	createDAG("bound", `
node: required
steps:
  - id: echo
    run: echo hi
`)
	nodes := func(names ...string) *api.TargetNodes {
		list := api.TargetNodes(names)
		return &list
	}
	// statusOf waits for the status of a run.
	statusOf := func(t *testing.T, name, runID string) ir.DAGRunStatus {
		t.Helper()
		var status ir.DAGRunStatus
		require.Eventually(t, func() bool {
			attempt, err := server.DAGRunRepository.FindAttempt(server.Context, ir.NewDAGRunRef(name, runID))
			if err != nil {
				return false
			}
			read, err := attempt.ReadStatus(server.Context)
			if err != nil {
				return false
			}
			status = *read
			return true
		}, 5*time.Second, 100*time.Millisecond)
		return status
	}
	enqueue := func(t *testing.T, dag string) string {
		t.Helper()
		var enqueued api.EnqueueDAGDAGRun200JSONResponse
		server.Client().Post("/api/v1/dags/"+dag+"/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			TargetNodes: nodes("WEB01"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &enqueued)
		return enqueued.DagRunId
	}
	enqueueNarrowed := func(t *testing.T) string {
		t.Helper()
		return enqueue(t, "narrowed")
	}

	t.Run("a queued run keeps its list", func(t *testing.T) {
		runID := enqueueNarrowed(t)
		require.Equal(t, []string{"web01"}, statusOf(t, "narrowed", runID).TargetNodes)
	})

	// Steps with nodes run on workers too, so a DAG that selects workers
	// takes a list.
	t.Run("a run that selects workers keeps its list", func(t *testing.T) {
		runID := enqueue(t, "remote")
		require.Equal(t, []string{"web01"}, statusOf(t, "remote", runID).TargetNodes)
		require.Equal(t, map[string]string{"gpu": "true"}, snapshotDAGOf(t, server, "remote", runID).WorkerSelector)
	})

	// A new run from a narrowed run keeps its list, whether it is built from
	// the run's snapshot or from the current file.
	t.Run("a rescheduled run keeps its list", func(t *testing.T) {
		source := enqueueNarrowed(t)
		statusOf(t, "narrowed", source)
		for _, currentFile := range []bool{false, true} {
			var resp api.RescheduleDAGRun200JSONResponse
			server.Client().Post(fmt.Sprintf("/api/v1/dag-runs/narrowed/%s/reschedule", source), api.RescheduleDAGRunJSONRequestBody{
				UseCurrentDagFile: &currentFile,
			}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resp)

			require.Equal(t, []string{"web01"}, statusOf(t, "narrowed", resp.DagRunId).TargetNodes, "current file: %v", currentFile)
		}
	})

	t.Run("a worker node is a node of the project", func(t *testing.T) {
		var enqueued api.EnqueueDAGDAGRun200JSONResponse
		server.Client().Post("/api/v1/dags/narrowed/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			TargetNodes: nodes("Tokyo-GW", "web01"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &enqueued)
		require.Equal(t, []string{"tokyo-gw", "web01"}, statusOf(t, "narrowed", enqueued.DagRunId).TargetNodes)
	})

	t.Run("details tell a node: required DAG", func(t *testing.T) {
		for dag, want := range map[string]bool{"bound": true, "plain": false} {
			var details api.GetDAGDetails200JSONResponse
			server.Client().Get("/api/v1/dags/"+dag).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
			require.NotNil(t, details.Dag)
			require.Equal(t, want, valueOf(details.Dag.NodeRequired), dag)
		}
	})

	for _, tc := range []struct {
		name    string
		dag     string
		body    api.ExecuteDAGJSONRequestBody
		message string
	}{
		{name: "unknown node", dag: "narrowed", body: api.ExecuteDAGJSONRequestBody{TargetNodes: nodes("web1")}, message: `node "web1" is not a node of the project`},
		{name: "nothing to narrow", dag: "plain", body: api.ExecuteDAGJSONRequestBody{TargetNodes: nodes("web01")}, message: "a node list narrows nothing"},
		{name: "node: required without a node", dag: "bound", message: "starts with exactly one node, not 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/start", "/enqueue"} {
				resp := server.Client().Post("/api/v1/dags/"+tc.dag+path, tc.body).
					ExpectStatus(http.StatusBadRequest).Send(t)
				var errResp api.Error
				resp.Unmarshal(t, &errResp)
				require.Contains(t, errResp.Message, tc.message, path)
			}
		})
	}

	// A run bound to a worker node is queued as any run is, and the
	// scheduler sends it to its worker.
	t.Run("node: required with a worker node", func(t *testing.T) {
		var enqueued api.EnqueueDAGDAGRun200JSONResponse
		server.Client().Post("/api/v1/dags/bound/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			TargetNodes: nodes("Tokyo-GW"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &enqueued)
		require.Equal(t, []string{"tokyo-gw"}, statusOf(t, "bound", enqueued.DagRunId).TargetNodes)
	})

	t.Run("selected steps take no list", func(t *testing.T) {
		steps := []string{"restart"}
		resp := server.Client().Post("/api/v1/dags/narrowed/start", api.ExecuteDAGJSONRequestBody{
			TargetNodes: nodes("web01"),
			Steps:       &steps,
		}).ExpectStatus(http.StatusBadRequest).Send(t)
		var errResp api.Error
		resp.Unmarshal(t, &errResp)
		require.Contains(t, errResp.Message, "targetNodes cannot be combined with selected steps")
	})
}
