// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestSingleton(t *testing.T) {
	server := test.SetupServer(t)

	t.Run("ExecuteDAGConflict", func(t *testing.T) {
		release := newHoldFile(t)
		spec := fmt.Sprintf(`
steps:
  - name: sleep
    run: |
%s`, indentCommandBlock(holdUntilFileExistsCommand(release), 6))
		// Create a new DAG
		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: "singleton_exec_dag",
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		// Execute the DAG
		singleton := true
		resp := server.Client().Post("/api/v1/dags/singleton_exec_dag/start", api.ExecuteDAGJSONRequestBody{
			Singleton: &singleton,
		}).ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)
		require.NotEmpty(t, execResp.DagRunId)

		// Wait for it to be running
		require.Eventually(t, func() bool {
			url := fmt.Sprintf("/api/v1/dags/singleton_exec_dag/dag-runs/%s", execResp.DagRunId)
			statusResp := server.Client().Get(url).ExpectStatus(http.StatusOK).Send(t)
			var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
			statusResp.Unmarshal(t, &dagRunStatus)
			return dagRunStatus.DagRun.Status == api.Status(ir.Running)
		}, 5*time.Second, 100*time.Millisecond)

		// Try to execute it again with singleton: true - should conflict
		server.Client().Post("/api/v1/dags/singleton_exec_dag/start", api.ExecuteDAGJSONRequestBody{
			Singleton: &singleton,
		}).ExpectStatus(http.StatusConflict).Send(t)

		releaseHoldFile(t, release)
		waitForDAGRunStatus(t, server, "singleton_exec_dag", execResp.DagRunId, 5*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})

		// Clean up (deleting the DAG will eventually stop the run)
		_ = server.Client().Delete("/api/v1/dags/singleton_exec_dag").ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("EnqueueDAGConflict_Running", func(t *testing.T) {
		release := newHoldFile(t)
		spec := fmt.Sprintf(`
steps:
  - name: sleep
    run: |
%s`, indentCommandBlock(holdUntilFileExistsCommand(release), 6))
		// Create a new DAG
		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: "singleton_enq_run_dag",
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		// Execute the DAG
		resp := server.Client().Post("/api/v1/dags/singleton_enq_run_dag/start", api.ExecuteDAGJSONRequestBody{}).
			ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)

		// Wait for it to be running
		require.Eventually(t, func() bool {
			url := fmt.Sprintf("/api/v1/dags/singleton_enq_run_dag/dag-runs/%s", execResp.DagRunId)
			statusResp := server.Client().Get(url).ExpectStatus(http.StatusOK).Send(t)
			var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
			statusResp.Unmarshal(t, &dagRunStatus)
			return dagRunStatus.DagRun.Status == api.Status(ir.Running)
		}, 5*time.Second, 100*time.Millisecond)

		// Try to enqueue it with singleton: true - should conflict because it's running
		singleton := true
		server.Client().Post("/api/v1/dags/singleton_enq_run_dag/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			Singleton: &singleton,
		}).ExpectStatus(http.StatusConflict).Send(t)

		releaseHoldFile(t, release)
		waitForDAGRunStatus(t, server, "singleton_enq_run_dag", execResp.DagRunId, 5*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})

		// Clean up
		_ = server.Client().Delete("/api/v1/dags/singleton_enq_run_dag").ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("EnqueueDAGConflict_Queued", func(t *testing.T) {
		spec := `
steps:
  - name: sleep
    run: sleep 10
`
		// Create a new DAG
		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: "singleton_enq_q_dag",
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		// Enqueue the DAG
		resp := server.Client().Post("/api/v1/dags/singleton_enq_q_dag/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{}).
			ExpectStatus(http.StatusOK).Send(t)

		var enqueueResp api.EnqueueDAGDAGRun200JSONResponse
		resp.Unmarshal(t, &enqueueResp)

		// Try to enqueue it again with singleton: true - should conflict because it's already queued
		singleton := true
		server.Client().Post("/api/v1/dags/singleton_enq_q_dag/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			Singleton: &singleton,
		}).ExpectStatus(http.StatusConflict).Send(t)

		// Clean up
		_ = server.Client().Delete("/api/v1/dags/singleton_enq_q_dag").ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("EnqueueDAGRunFromSpecConflict", func(t *testing.T) {
		spec := `
name: singleton_inline_enq_dag
steps:
  - name: sleep
    run: sleep 10
`
		name := "singleton_inline_enq_dag"
		resp := server.Client().Post("/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{
			Spec: spec,
			Name: &name,
		}).ExpectStatus(http.StatusOK).Send(t)

		var enqueueResp api.EnqueueDAGRunFromSpec200JSONResponse
		resp.Unmarshal(t, &enqueueResp)
		require.NotEmpty(t, enqueueResp.DagRunId)

		singleton := true
		server.Client().Post("/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{
			Spec:      spec,
			Name:      &name,
			Singleton: &singleton,
		}).ExpectStatus(http.StatusConflict).Send(t)
	})
}

// A DAG with a restart_policy has one run at a time: no request starts a run
// next to the active one. Once none is active, a manual start works again.
func TestAlwaysOnDAGRunsOneAtATime(t *testing.T) {
	server := test.SetupServer(t)

	const name = "service_dag"
	spec := fmt.Sprintf(`name: %s
restart_policy: always
restart_wait_sec: 1
steps:
  - name: main
    run: echo ok
`, name)
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{Name: name, Spec: &spec}).
		ExpectStatus(http.StatusCreated).Send(t)

	start := func() string {
		var started api.ExecuteDAG200JSONResponse
		server.Client().Post("/api/v1/dags/"+name+"/start", api.ExecuteDAGJSONRequestBody{}).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)
		waitForDAGRunStatus(t, server, name, started.DagRunId, 10*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})
		return started.DagRunId
	}
	start()
	ended := start()

	server.Client().Post("/api/v1/dags/"+name+"/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	specName := name
	for _, tt := range []struct {
		name string
		path string
		body any
	}{
		{"Start", "/api/v1/dags/" + name + "/start", api.ExecuteDAGJSONRequestBody{}},
		{"StartSteps", "/api/v1/dags/" + name + "/start", api.ExecuteDAGJSONRequestBody{Steps: &[]string{"main"}}},
		{"StartSync", "/api/v1/dags/" + name + "/start-sync", api.ExecuteDAGSyncJSONRequestBody{Timeout: 5}},
		{"Enqueue", "/api/v1/dags/" + name + "/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{}},
		{"StartFromSpec", "/api/v1/dag-runs", api.ExecuteDAGRunFromSpecJSONRequestBody{Spec: spec, Name: &specName}},
		{"EnqueueFromSpec", "/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{Spec: spec, Name: &specName}},
		{"Retry", "/api/v1/dag-runs/" + name + "/" + ended + "/retry", api.RetryDAGRunJSONRequestBody{DagRunId: ended}},
		{"Reschedule", "/api/v1/dag-runs/" + name + "/" + ended + "/reschedule", api.RescheduleDAGRunJSONRequestBody{}},
		{"EditRetry", "/api/v1/dag-runs/" + name + "/" + ended + "/edit-retry", api.EditRetryDAGRunJSONRequestBody{Spec: spec}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server.Client().Post(tt.path, tt.body).ExpectStatus(http.StatusConflict).Send(t)
		})
	}
}

// The scheduler follows the current definition, so neither a run whose snapshot
// predates the restart_policy nor a spec in the request gets around it.
func TestAlwaysOnDAGFollowsCurrentDefinition(t *testing.T) {
	server := test.SetupServer(t)

	const name = "service_dag_later"
	const steps = `steps:
  - name: main
    run: echo ok
`
	plain := steps
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{Name: name, Spec: &plain}).
		ExpectStatus(http.StatusCreated).Send(t)

	var started api.ExecuteDAG200JSONResponse
	server.Client().Post("/api/v1/dags/"+name+"/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)
	ended := started.DagRunId
	waitForDAGRunStatus(t, server, name, ended, 10*time.Second, func(status *ir.DAGRunStatus) bool {
		return status.Status == ir.Succeeded
	})

	server.Client().Put("/api/v1/dags/"+name+"/spec", api.UpdateDAGSpecJSONRequestBody{
		Spec: "restart_policy: always\nrestart_wait_sec: 1\n" + steps,
	}).ExpectStatus(http.StatusOK).Send(t)
	server.Client().Post("/api/v1/dags/"+name+"/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	specName := name
	for _, tt := range []struct {
		name string
		path string
		body any
	}{
		{"Retry", "/api/v1/dag-runs/" + name + "/" + ended + "/retry", api.RetryDAGRunJSONRequestBody{DagRunId: ended}},
		{"Reschedule", "/api/v1/dag-runs/" + name + "/" + ended + "/reschedule", api.RescheduleDAGRunJSONRequestBody{}},
		{"EditRetry", "/api/v1/dag-runs/" + name + "/" + ended + "/edit-retry", api.EditRetryDAGRunJSONRequestBody{Spec: "name: " + name + "\n" + steps}},
		{"StartFromSpec", "/api/v1/dag-runs", api.ExecuteDAGRunFromSpecJSONRequestBody{Spec: steps, Name: &specName}},
		{"EnqueueFromSpec", "/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{Spec: steps, Name: &specName}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server.Client().Post(tt.path, tt.body).ExpectStatus(http.StatusConflict).Send(t)
		})
	}
}
