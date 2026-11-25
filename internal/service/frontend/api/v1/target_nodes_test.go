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
	createDAG("plain", `
steps:
  - id: echo
    run: echo hi
`)
	nodes := func(names ...string) *api.TargetNodes {
		list := api.TargetNodes(names)
		return &list
	}

	t.Run("a queued run keeps its list and runs on the Dagu host", func(t *testing.T) {
		resp := server.Client().Post("/api/v1/dags/narrowed/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			TargetNodes: nodes("WEB01"),
		}).ExpectStatus(http.StatusOK).Send(t)
		var enqueued api.EnqueueDAGDAGRun200JSONResponse
		resp.Unmarshal(t, &enqueued)

		var status ir.DAGRunStatus
		require.Eventually(t, func() bool {
			attempt, err := server.DAGRunRepository.FindAttempt(server.Context, ir.NewDAGRunRef("narrowed", enqueued.DagRunId))
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
		require.Equal(t, []string{"web01"}, status.TargetNodes)
		require.True(t, snapshotDAGOf(t, server, "narrowed", enqueued.DagRunId).ForceLocal)
	})

	for _, tc := range []struct {
		name    string
		dag     string
		body    api.ExecuteDAGJSONRequestBody
		message string
	}{
		{name: "unknown node", dag: "narrowed", body: api.ExecuteDAGJSONRequestBody{TargetNodes: nodes("web1")}, message: `node "web1" is not a node of the project`},
		{name: "nothing to narrow", dag: "plain", body: api.ExecuteDAGJSONRequestBody{TargetNodes: nodes("web01")}, message: "a node list narrows nothing"},
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
