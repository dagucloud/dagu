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
		steps:       []string{"consume"},
		outputsFrom: "source-run",
		dagRunID:    "only-run",
		labels:      "team=qa",
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
			req:        selectedStepsStart{steps: []string{"missing"}, dagRunID: "run"},
			wantStatus: http.StatusBadRequest,
			wantMsg:    `unknown step "missing"`,
		},
		{
			name:       "MissingSource",
			req:        selectedStepsStart{steps: []string{"consume"}, outputsFrom: "nope", dagRunID: "run"},
			wantStatus: http.StatusNotFound,
			wantMsg:    "dag-run nope not found",
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
		_, _, err := selectedStepsFromBody(nil, ptrOf("source"))
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputsFromRunId requires steps")
	})
}
