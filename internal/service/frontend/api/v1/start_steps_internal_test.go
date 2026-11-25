// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
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

	_, err = api.startSelectedSteps(ctx, dag, selectedStepsStart{
		// The edited DAG changed build, which the request consents to reuse.
		selection: stepSelection{steps: []string{"consume"}, outputsFrom: "source-run", allowChanged: []string{"build"}},
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
	// The edit changed build and added a worker selector.
	require.Equal(t, []ir.StepChange{ir.StepChangeStep, ir.StepChangeSettings}, status.ReusedInputs[0].Changes)
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

	_, err = api.startSelectedSteps(ctx, dag, selectedStepsStart{
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
			_, err := api.startSelectedSteps(ctx, dag, tt.req)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tt.wantStatus, apiErr.HTTPStatus)
			require.Contains(t, apiErr.Message, tt.wantMsg)
		})
	}

	t.Run("OutputsFromNeedsSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, ptrOf("source"), nil, nil, nil, nil, nil, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputsFromRunId requires steps")
	})

	t.Run("OutputsFromRunsNeedSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, nil, &map[string]string{"build": "run-1"}, nil, nil, nil, nil, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputsFromRuns requires steps")
	})

	t.Run("OutputsFromRunsRunIDs", func(t *testing.T) {
		steps := []string{"consume"}
		for name, runID := range map[string]string{"Empty": " ", "BadID": "bad/id"} {
			_, err := selectedStepsFromBody(&steps, nil, &map[string]string{"build": runID}, nil, nil, nil, nil, nil)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr, name)
			require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, name)
		}
		got, err := selectedStepsFromBody(&steps, nil, &map[string]string{"build": " run-1 "}, nil, nil, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"build": "run-1"}, got.outputsFromRuns)
	})

	t.Run("OutputsSourcesNeedSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, nil, nil, nil, &map[string]gen.ReusedOutputSource{"build": {RunId: "run-1"}}, nil, nil, nil)
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
			_, err := selectedStepsFromBody(&steps, nil, nil, nil, &map[string]gen.ReusedOutputSource{"build": source}, nil, nil, nil)
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr, name)
			require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, name)
		}

		// A run that does not exist is a valid source.
		got, err := selectedStepsFromBody(&steps, nil, nil, nil, &map[string]gen.ReusedOutputSource{
			"build": {RunId: "gone-run", CapturedAt: ptrOf("2026-10-26T12:00:00+02:00")},
		}, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, intake.OutputSource{RunID: "gone-run", CapturedAt: "2026-10-26T12:00:00+02:00"}, got.outputsSources["build"])
	})

	t.Run("EphemeralNeedsSteps", func(t *testing.T) {
		yes := true
		_, err := selectedStepsFromBody(nil, nil, nil, nil, nil, &yes, nil, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "ephemeral requires steps")

		no := false
		_, err = selectedStepsFromBody(nil, nil, nil, nil, nil, &no, nil, nil)
		require.NoError(t, err)
	})

	t.Run("OutputsNeedSteps", func(t *testing.T) {
		_, err := selectedStepsFromBody(nil, nil, nil, &map[string]map[string]string{"build": {"RESULT": "x"}}, nil, nil, nil, nil)
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
		require.Contains(t, apiErr.Message, "outputs requires steps")
	})

	t.Run("AllowChangedSteps", func(t *testing.T) {
		steps := []string{"consume"}
		got, err := selectedStepsFromBody(&steps, nil, nil, nil, nil, nil, nil, &[]string{" build "})
		require.NoError(t, err)
		require.Equal(t, []string{"build"}, got.allowChanged)

		for name, tc := range map[string]struct {
			steps   *[]string
			reuse   *string
			allowed []string
			wantMsg string
		}{
			"NeedsSteps":   {allowed: []string{"build"}, wantMsg: "allowChangedSteps requires steps"},
			"NotWithReuse": {reuse: ptrOf("earlier"), allowed: []string{"build"}, wantMsg: "send it without steps, outputsFromRunId, outputsFromRuns, outputs, outputsSources, or allowChangedSteps"},
			"NamesAStep":   {steps: &steps, allowed: []string{" "}, wantMsg: "allowChangedSteps must name steps"},
		} {
			t.Run(name, func(t *testing.T) {
				_, err := selectedStepsFromBody(tc.steps, nil, nil, nil, nil, nil, tc.reuse, &tc.allowed)
				var apiErr *Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
				require.Contains(t, apiErr.Message, tc.wantMsg)
			})
		}
	})
}

// A step of outputsFromRuns carries outputs only from a run in which it was as
// it is now. The parameters count: a default from outside the DAG file, such
// as the base config, changes them without changing the file.
func TestStartStepsRejectsAChangedStep(t *testing.T) {
	ctx := context.Background()
	api, dag := setupEditRetryAPI(t, t.TempDir(), editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "same-run")
	seedEditRetrySourceAttemptWithParams(t, ctx, api.dagRunRepository, dag, "params-run")
	edited, err := spec.LoadYAML(ctx, []byte(editRetryEditedYAML()))
	require.NoError(t, err)

	tests := []struct {
		name      string
		dag       *ir.DAG
		selection stepSelection
		wantMsg   string
	}{
		{
			name:      "OtherParameters",
			dag:       dag,
			selection: stepSelection{steps: []string{"consume"}, outputsFromRuns: map[string]string{"build": "params-run"}},
			wantMsg:   "step build cannot be reused from run params-run: the parameters changed since that run",
		},
		{
			name:      "ChangedStep",
			dag:       edited,
			selection: stepSelection{steps: []string{"consume"}, outputsFromRuns: map[string]string{"build": "same-run"}},
			wantMsg:   "step build cannot be reused from run same-run: the step changed since that run",
		},
		{
			name:      "ChangedStepOfTheSource",
			dag:       edited,
			selection: stepSelection{steps: []string{"consume"}, outputsFrom: "same-run"},
			wantMsg:   "step build cannot be reused from run same-run: the step changed since that run",
		},
		{
			name:      "GoneRun",
			dag:       dag,
			selection: stepSelection{steps: []string{"consume"}, outputsFromRuns: map[string]string{"build": "gone-run"}},
			wantMsg:   "step build cannot be reused from run gone-run: the run does not exist for DAG",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := api.startSelectedSteps(ctx, tt.dag, selectedStepsStart{
				selection: tt.selection,
				dagRunID:  "run-" + strings.ToLower(tt.name),
			})
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
			require.Contains(t, apiErr.Message, tt.wantMsg)
			refused, ok := apiErr.Details["refusedSteps"].([]gen.CarriedStepCheck)
			require.True(t, ok)
			require.Len(t, refused, 1)
			require.Equal(t, "build", refused[0].Step)
			require.Contains(t, *refused[0].Message, tt.wantMsg)
		})
	}
}

// A preview plans a run as its start would and reports each carried step.
func TestPreviewSelectedSteps(t *testing.T) {
	ctx := context.Background()
	api, dag := setupEditRetryAPI(t, t.TempDir(), editRetrySourceYAML())
	seedEditRetrySourceAttempt(t, ctx, api.dagRunRepository, dag, "same-run")

	resp, err := api.PreviewExecuteDAGRunFromSpec(ctx, gen.PreviewExecuteDAGRunFromSpecRequestObject{
		Body: &gen.PreviewExecuteDAGRunFromSpecJSONRequestBody{
			Spec:             editRetryEditedYAML(),
			Steps:            []string{"notify"},
			OutputsFromRunId: ptrOf("same-run"),
		},
	})
	require.NoError(t, err)
	check, ok := resp.(gen.PreviewExecuteDAGRunFromSpec200JSONResponse)
	require.True(t, ok)
	require.Len(t, check.Carried, 1)
	build := check.Carried[0]
	require.Equal(t, "build", build.Step)
	require.Equal(t, "same-run", build.RunId)
	require.Equal(t, gen.CarriedStepCheckStatusChanged, build.Status)
	require.Equal(t, []gen.StepChangeKind{gen.StepChangeKindStep}, build.Changes)
	require.Equal(t, "step build cannot be reused from run same-run: the step changed since that run", *build.Message)
	// consume is new: no run gives it.
	require.Equal(t, []string{"consume"}, check.Missing)
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

	_, err = api.startSelectedSteps(ctx, dag, selectedStepsStart{
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
	}, nil)
	require.Equal(t, true, details["ephemeral"])
	require.Equal(t, []string{"build.RESULT"}, details["outputs"])

	details = map[string]any{}
	addSelectedStepsAudit(details, stepSelection{steps: []string{"consume"}}, nil)
	require.NotContains(t, details, "ephemeral")
	require.NotContains(t, details, "allowed_changed_steps")

	// A consent to reuse a changed step is recorded with what changed.
	details = map[string]any{}
	addSelectedStepsAudit(details, stepSelection{steps: []string{"consume"}}, []intake.CarriedStep{
		{Step: "build", DAGRunID: "earlier", Changes: []ir.StepChange{ir.StepChangeParams}},
	})
	require.Equal(t, []map[string]any{{"step": "build", "dag_run_id": "earlier", "changes": []ir.StepChange{ir.StepChangeParams}}}, details["allowed_changed_steps"])

	// A run that reuses another records it, and is marked ephemeral before
	// its steps are chosen.
	details = map[string]any{}
	addSelectedStepsAudit(details, stepSelection{reuseFrom: "earlier", ephemeral: true}, nil)
	require.Equal(t, "earlier", details["reuse_from_dag_run_id"])
	require.Equal(t, true, details["ephemeral"])
}
