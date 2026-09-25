// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

// A distributed selected-steps run is seeded as a queued manual run and
// dispatched as a retry of that attempt, carrying the source run's outputs.
func TestStartStepsDispatchesSeed(t *testing.T) {
	ctx := context.Background()
	api, sourceDAG := setupEditRetryAPI(t, t.TempDir(), editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, sourceDAG, "source-run")
	recorder := &retryCoordinatorRecorder{}
	api.coordinatorCli = recorder
	dag, err := spec.LoadYAML(ctx, []byte(editRetryEditedYAMLWithWorkerSelector()))
	require.NoError(t, err)

	err = api.startSelectedSteps(ctx, dag, selectedStepsStart{
		selection: stepSelection{steps: []string{"consume"}, outputsFrom: "source-run"},
		dagRunID:  "only-run",
		labels:    "team=qa",
	})
	require.NoError(t, err)

	require.Len(t, recorder.dispatched, 1)
	task := recorder.dispatched[0]
	require.Equal(t, dispatch.DispatchOperationRetry, task.Operation)
	require.Equal(t, "team=qa", task.Labels)
	require.NotNil(t, task.PreviousStatus)
	require.Equal(t, ir.Queued, task.PreviousStatus.Status)

	attempt, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "only-run"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Queued, status.Status)
	require.Equal(t, ir.TriggerTypeManual, status.TriggerType)
	require.Len(t, status.Nodes, 3)
	require.Equal(t, ir.NodeSkipped, status.Nodes[0].Status)
	require.True(t, status.Nodes[0].SkippedByRetry)
	raw, ok := status.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT=from-source", raw)
	require.Equal(t, ir.NodeNotStarted, status.Nodes[1].Status)
	require.Equal(t, ir.NodeSkipped, status.Nodes[2].Status)
}

// Supplied outputs are seeded onto the skipped step, replacing the value the
// source run recorded for the same output.
func TestStartStepsSetsOutputs(t *testing.T) {
	ctx := context.Background()
	api, sourceDAG := setupEditRetryAPI(t, t.TempDir(), editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, sourceDAG, "source-run")
	api.coordinatorCli = &retryCoordinatorRecorder{}
	dag, err := spec.LoadYAML(ctx, []byte(editRetryEditedYAMLWithWorkerSelector()))
	require.NoError(t, err)

	err = api.startSelectedSteps(ctx, dag, selectedStepsStart{
		selection: stepSelection{
			steps:       []string{"consume"},
			outputsFrom: "source-run",
			outputs:     map[string]map[string]string{"build": {"RESULT": "given"}},
		},
		dagRunID: "only-run",
	})
	require.NoError(t, err)

	attempt, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "only-run"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	raw, ok := status.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT=given", raw)
}

func TestStartStepsRejects(t *testing.T) {
	ctx := context.Background()
	api, dag := setupEditRetryAPI(t, t.TempDir(), editRetrySourceYAML())

	tests := []struct {
		name       string
		req        selectedStepsStart
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "UnknownStep",
			req:        selectedStepsStart{selection: stepSelection{steps: []string{"missing"}}, dagRunID: "run"},
			wantStatus: http.StatusBadRequest,
			wantMsg:    `unknown step "missing"`,
		},
		{
			name:       "MissingSource",
			req:        selectedStepsStart{selection: stepSelection{steps: []string{"consume"}, outputsFrom: "nope"}, dagRunID: "run"},
			wantStatus: http.StatusNotFound,
			wantMsg:    "dag-run nope not found",
		},
		{
			name: "OutputOfSelectedStep",
			req: selectedStepsStart{selection: stepSelection{
				steps:   []string{"consume"},
				outputs: map[string]map[string]string{"consume": {"RESULT": "x"}},
			}, dagRunID: "run"},
			wantStatus: http.StatusBadRequest,
			wantMsg:    `cannot set outputs of step "consume": it is selected to run`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := api.startSelectedSteps(ctx, dag, tt.req)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tt.wantStatus, apiErr.HTTPStatus)
			require.Contains(t, apiErr.Message, tt.wantMsg)
		})
	}

	t.Run("OutputsFromNeedsSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, ptrOf("source"), nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputsFromRunId requires steps")
	})

	t.Run("OutputsNeedSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, nil, &map[string]map[string]string{"build": {"RESULT": "x"}})
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputs requires steps")
	})
}
