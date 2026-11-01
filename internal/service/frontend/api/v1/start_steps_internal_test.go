// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	gen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/spec"
	runtestutil "github.com/dagucloud/dagu/v2/internal/testutil"
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

	// Only the skipped step that carried outputs has a provenance.
	require.Len(t, status.ReusedInputs, 1)
	require.Equal(t, "build", status.ReusedInputs[0].Step)
	require.Equal(t, "source-run", status.ReusedInputs[0].RunID)
	require.Equal(t, ir.ReusedInputViaRun, status.ReusedInputs[0].Via)
	require.Equal(t, status.Nodes[0].FinishedAt, status.ReusedInputs[0].CapturedAt)
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
			steps:          []string{"consume"},
			outputsFrom:    "source-run",
			outputs:        map[string]map[string]string{"build": {"RESULT": "given"}},
			outputsSources: map[string]intake.OutputSource{"build": {RunID: "captured-run", CapturedAt: "2026-10-26T10:00:00Z"}},
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

	// The supplied step names its run; the carried status keeps it.
	require.Equal(t, []ir.ReusedInput{
		{Step: "build", RunID: "captured-run", CapturedAt: "2026-10-26T10:00:00Z", Via: ir.ReusedInputViaCache},
	}, status.ReusedInputs)
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
			name:       "RunOfAStepIsGone",
			req:        selectedStepsStart{selection: stepSelection{steps: []string{"consume"}, outputsFromRuns: map[string]string{"build": "nope"}}, dagRunID: "run"},
			wantStatus: http.StatusBadRequest,
			wantMsg:    "step build cannot be reused from run nope: the run does not exist for DAG",
		},
		{
			name: "OutputOfSelectedStep",
			req: selectedStepsStart{selection: stepSelection{
				steps:          []string{"consume"},
				outputs:        map[string]map[string]string{"consume": {"RESULT": "x"}},
				outputsSources: map[string]intake.OutputSource{"consume": {RunID: "captured-run"}},
			}, dagRunID: "run"},
			wantStatus: http.StatusBadRequest,
			wantMsg:    `cannot set outputs of step "consume": it is selected to run`,
		},
		{
			name: "SourceWithoutOutput",
			req: selectedStepsStart{selection: stepSelection{
				steps:          []string{"consume"},
				outputsSources: map[string]intake.OutputSource{"build": {RunID: "captured-run"}},
			}, dagRunID: "run"},
			wantStatus: http.StatusBadRequest,
			wantMsg:    "outputsSources names step build, which is not in outputs",
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
		_, err := selectedStepsFromBody(nil, ptrOf("source"), nil, nil, nil, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputsFromRunId requires steps")
	})

	t.Run("OutputsFromRunsNeedSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, nil, &map[string]string{"build": "run-1"}, nil, nil, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputsFromRuns requires steps")
	})

	t.Run("OutputsFromRunsRunIDs", func(t *testing.T) {
		steps := []string{"consume"}
		for name, runID := range map[string]string{"Empty": " ", "BadID": "bad/id"} {
			_, err := selectedStepsFromBody(&steps, nil, &map[string]string{"build": runID}, nil, nil, nil)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr, name)
			require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, name)
		}
		got, err := selectedStepsFromBody(&steps, nil, &map[string]string{"build": " run-1 "}, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"build": "run-1"}, got.outputsFromRuns)
	})

	t.Run("OutputsSourcesNeedSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, nil, nil, nil, &map[string]gen.ReusedOutputSource{"build": {RunId: "run-1"}}, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputsSources requires steps")
	})

	t.Run("SourceRunID", func(t *testing.T) {
		steps := []string{"consume"}
		for name, source := range map[string]gen.ReusedOutputSource{
			"Empty":      {},
			"BadID":      {RunId: "bad/id"},
			"BadCapture": {RunId: "run-1", CapturedAt: ptrOf("yesterday")},
		} {
			_, err := selectedStepsFromBody(&steps, nil, nil, nil, &map[string]gen.ReusedOutputSource{"build": source}, nil)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr, name)
			require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, name)
		}

		// A run that does not exist is a valid source.
		got, err := selectedStepsFromBody(&steps, nil, nil, nil, &map[string]gen.ReusedOutputSource{
			"build": {RunId: "gone-run", CapturedAt: ptrOf("2026-10-26T12:00:00+02:00")},
		}, nil)
		require.NoError(t, err)
		require.Equal(t, intake.OutputSource{RunID: "gone-run", CapturedAt: "2026-10-26T12:00:00+02:00"}, got.outputsSources["build"])
	})

	t.Run("EphemeralNeedsSteps", func(t *testing.T) {
		yes := true
		_, err := selectedStepsFromBody(nil, nil, nil, nil, nil, &yes)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "ephemeral requires steps")

		no := false
		_, err = selectedStepsFromBody(nil, nil, nil, nil, nil, &no)
		require.NoError(t, err)
	})

	t.Run("OutputsNeedSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, nil, nil, &map[string]map[string]string{"build": {"RESULT": "x"}}, nil, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputs requires steps")
	})
}

// An ephemeral run takes explicit outputs with no source run, is found by its
// ID, appears in no listing, and refuses retry, reschedule and edit.
func TestStartStepsEphemeral(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	api, _ := setupEditRetryAPI(t, tmp, editRetrySourceYAML())
	dag, err := spec.LoadYAML(ctx, []byte(editRetryEditedYAMLWithWorkerSelector()))
	require.NoError(t, err)
	api.dagRunRepository = runtestutil.NewFileDAGRunRepositoryWithEphemeral(
		filepath.Join(tmp, "dag-runs"),
		filepath.Join(tmp, "dag-runs-ephemeral"),
		persis.DAGRunRepositoryOptions{LatestStatusToday: true},
	)
	api.coordinatorCli = &retryCoordinatorRecorder{}

	err = api.startSelectedSteps(ctx, dag, selectedStepsStart{
		selection: stepSelection{
			steps:          []string{"consume"},
			outputs:        map[string]map[string]string{"build": {"RESULT": "given"}},
			outputsSources: map[string]intake.OutputSource{"build": {RunID: "captured-run"}},
			ephemeral:      true,
		},
		dagRunID: "trace-free",
	})
	require.NoError(t, err)

	attempt, err := api.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dag.Name, "trace-free"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.True(t, status.Ephemeral)
	require.Equal(t, []ir.ReusedInput{{Step: "build", RunID: "captured-run", Via: ir.ReusedInputViaCache}}, status.ReusedInputs)
	raw, ok := status.Nodes[0].OutputVariables.Load("RESULT")
	require.True(t, ok)
	require.Equal(t, "RESULT=given", raw)

	listed, err := api.dagRunRepository.ListStatuses(ctx, persis.DAGRunListOptions{AllHistory: true, Unbounded: true})
	require.NoError(t, err)
	require.Empty(t, listed)

	var apiErr *Error
	require.ErrorAs(t, rejectEphemeralRun(status, "retried"), &apiErr)
	require.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
	require.NoError(t, rejectEphemeralRun(&ir.DAGRunStatus{}, "retried"))

	_, err = api.retryDAGRun(ctx, dag.Name, "trace-free", "", "", "", false, false)
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
}

// The audit entry of a start request names the supplied outputs without their
// values and marks an ephemeral run.
func TestSelectedStepsAudit(t *testing.T) {
	details := map[string]any{}
	addSelectedStepsAudit(details, stepSelection{
		steps:     []string{"consume"},
		outputs:   map[string]map[string]string{"build": {"RESULT": "secret"}},
		ephemeral: true,
	})
	require.Equal(t, true, details["ephemeral"])
	require.Equal(t, []string{"build.RESULT"}, details["outputs"])

	details = map[string]any{}
	addSelectedStepsAudit(details, stepSelection{steps: []string{"consume"}})
	require.NotContains(t, details, "ephemeral")
}
