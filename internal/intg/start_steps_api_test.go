// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestAPIStartSteps(t *testing.T) {
	server := test.SetupServer(t)
	dagName := "intg_start_steps"
	spec := startStepsSpec(dagName)
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	sourceRunID := "source-start-steps"
	server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{DagRunId: &sourceRunID},
	).ExpectStatus(http.StatusOK).Send(t)
	waitForEditRetryStoredStatus(t, server, dagName, sourceRunID, ir.Succeeded)

	runID := "only-consume"
	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{
			DagRunId:         &runID,
			Steps:            &[]string{"consume"},
			OutputsFromRunId: &sourceRunID,
		},
	).ExpectStatus(http.StatusOK).Send(t)
	var body api.ExecuteDAG200JSONResponse
	resp.Unmarshal(t, &body)
	require.Equal(t, api.DAGRunId(runID), body.DagRunId)

	status := waitForEditRetryStoredStatus(t, server, dagName, runID, ir.Succeeded)
	require.Equal(t, ir.TriggerTypeManual, status.TriggerType)
	require.Len(t, status.Nodes, 3)
	require.Equal(t, ir.NodeSkipped, status.Nodes[0].Status)
	require.Equal(t, ir.NodeSucceeded, status.Nodes[1].Status)
	require.Equal(t, ir.NodeSkipped, status.Nodes[2].Status)
	consumed, ok := status.Nodes[1].OutputVariables.Load("CONSUMED")
	require.True(t, ok)
	require.Equal(t, "CONSUMED=from-source", consumed)
}

// Supplied outputs stand in for a skipped step without any earlier run.
func TestAPIStartStepsSetsOutputs(t *testing.T) {
	server := test.SetupServer(t)
	dagName := "intg_start_steps_outputs"
	spec := startStepsSpec(dagName)
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	runID := "only-consume-given"
	server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{
			DagRunId: &runID,
			Steps:    &[]string{"consume"},
			Outputs:  &map[string]map[string]string{"build": {"RESULT": "given"}},
		},
	).ExpectStatus(http.StatusOK).Send(t)

	status := waitForEditRetryStoredStatus(t, server, dagName, runID, ir.Succeeded)
	require.Equal(t, ir.NodeSkipped, status.Nodes[0].Status)
	require.Equal(t, ir.NodeSucceeded, status.Nodes[1].Status)
	consumed, ok := status.Nodes[1].OutputVariables.Load("CONSUMED")
	require.True(t, ok)
	require.Equal(t, "CONSUMED=given", consumed)
}

func TestAPIStartStepsFromSpec(t *testing.T) {
	server := test.SetupServer(t)
	dagName := "intg_start_steps_inline"

	runID := "only-notify"
	server.Client().Post("/api/v1/dag-runs", api.ExecuteDAGRunFromSpecJSONRequestBody{
		Spec:     startStepsSpec(dagName),
		DagRunId: &runID,
		Steps:    &[]string{"notify"},
	}).ExpectStatus(http.StatusOK).Send(t)

	status := waitForEditRetryStoredStatus(t, server, dagName, runID, ir.Succeeded)
	require.Len(t, status.Nodes, 3)
	require.Equal(t, ir.NodeSkipped, status.Nodes[0].Status)
	require.Equal(t, ir.NodeSkipped, status.Nodes[1].Status)
	require.Equal(t, ir.NodeSucceeded, status.Nodes[2].Status)
}

func TestAPIStartStepsRejects(t *testing.T) {
	server := test.SetupServer(t)
	dagName := "intg_start_steps_rejects"
	spec := startStepsSpec(dagName)
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	source := "source"
	tests := []struct {
		name string
		body api.ExecuteDAGJSONRequestBody
	}{
		{name: "OutputsFromWithoutSteps", body: api.ExecuteDAGJSONRequestBody{OutputsFromRunId: &source}},
		{name: "OutputsWithoutSteps", body: api.ExecuteDAGJSONRequestBody{Outputs: &map[string]map[string]string{"build": {"RESULT": "x"}}}},
		{name: "EmptySteps", body: api.ExecuteDAGJSONRequestBody{Steps: &[]string{}}},
		{name: "UnknownStep", body: api.ExecuteDAGJSONRequestBody{Steps: &[]string{"missing"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server.Client().Post(
				fmt.Sprintf("/api/v1/dags/%s/start", dagName),
				tt.body,
			).ExpectStatus(http.StatusBadRequest).Send(t)
		})
	}
}

// startStepsSpec is a three-step chain; consume echoes the output build
// publishes, so a carried output shows up in consume's own output.
func startStepsSpec(dagName string) string {
	return fmt.Sprintf(`name: %s
type: graph
steps:
  - name: build
    run: %s
    output: RESULT
  - name: consume
    run: %s
    output: CONSUMED
    depends:
      - build
  - name: notify
    run: %s
    depends:
      - consume
`, dagName, editRetryEchoResultCommand(), editRetryEchoConsumedCommand(), editRetryEchoDoneCommand())
}
