// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// selectedStepsStart is a start request that runs only some steps of a DAG.
type selectedStepsStart struct {
	selection   stepSelection
	params      string
	dagRunID    string
	labels      string
	profileName string
	noReuse     bool
	// inline marks a DAG loaded from a submitted spec rather than a DAG file.
	inline bool
	// workerSelector is the request's per-run selector; the seeded run is
	// persisted from the resolved DAG, which must carry it.
	workerSelector map[string]string
}

// stepSelection is the step selection of a start request body.
type stepSelection struct {
	steps       []string
	outputsFrom string
	// outputsFromRuns names, by step, the run that the step carries its
	// outputs from instead of outputsFrom.
	outputsFromRuns map[string]string
	// outputs are outputs of skipped steps, by step and then output name.
	outputs map[string]map[string]string
	// outputsSources names, by step, the run each step of outputs was captured from.
	outputsSources map[string]intake.OutputSource
	// ephemeral runs leave no trace: the run is stored apart from the history,
	// listed nowhere, and removed shortly after it ends.
	ephemeral bool
}

// selectedStepsFromBody validates the step selection fields of a start
// request body. It returns no steps when the request runs the whole DAG.
func selectedStepsFromBody(steps *[]string, outputsFromRunID *string, outputsFromRuns *map[string]string, outputs *map[string]map[string]string, outputsSources *map[string]api.ReusedOutputSource, ephemeral *bool) (stepSelection, error) {
	outputsFrom := strings.TrimSpace(valueOf(outputsFromRunID))
	if steps == nil {
		if valueOf(ephemeral) {
			return stepSelection{}, badSelectedStepsRequest("ephemeral requires steps: a run that leaves no trace is for steps")
		}
		if outputsFrom != "" {
			return stepSelection{}, badSelectedStepsRequest("outputsFromRunId requires steps")
		}
		if outputsFromRuns != nil {
			return stepSelection{}, badSelectedStepsRequest("outputsFromRuns requires steps")
		}
		if outputs != nil {
			return stepSelection{}, badSelectedStepsRequest("outputs requires steps")
		}
		if outputsSources != nil {
			return stepSelection{}, badSelectedStepsRequest("outputsSources requires steps")
		}
		return stepSelection{}, nil
	}
	if len(*steps) == 0 {
		return stepSelection{}, badSelectedStepsRequest("steps must name at least one step")
	}
	if err := validateDAGRunID(outputsFrom); err != nil {
		return stepSelection{}, err
	}
	fromRuns := make(map[string]string, len(valueOf(outputsFromRuns)))
	for step, runID := range valueOf(outputsFromRuns) {
		runID = strings.TrimSpace(runID)
		if runID == "" {
			return stepSelection{}, badSelectedStepsRequest(fmt.Sprintf("outputsFromRuns for step %s must name a run", step))
		}
		if err := validateDAGRunID(runID); err != nil {
			return stepSelection{}, err
		}
		fromRuns[step] = runID
	}
	sources, err := outputSourcesFromBody(valueOf(outputsSources))
	if err != nil {
		return stepSelection{}, err
	}
	return stepSelection{steps: *steps, outputsFrom: outputsFrom, outputsFromRuns: fromRuns, outputs: valueOf(outputs), outputsSources: sources, ephemeral: valueOf(ephemeral)}, nil
}

// outputSourcesFromBody validates the run IDs and times of outputsSources. A
// source run need not exist: the outputs may come from a run that is gone.
func outputSourcesFromBody(body map[string]api.ReusedOutputSource) (map[string]intake.OutputSource, error) {
	if len(body) == 0 {
		return nil, nil
	}
	sources := make(map[string]intake.OutputSource, len(body))
	for step, source := range body {
		runID := strings.TrimSpace(source.RunId)
		if runID == "" {
			return nil, badSelectedStepsRequest(fmt.Sprintf("outputsSources for step %s must name a runId", step))
		}
		if err := validateDAGRunID(runID); err != nil {
			return nil, err
		}
		entry := intake.OutputSource{RunID: runID}
		if capturedAt := strings.TrimSpace(valueOf(source.CapturedAt)); capturedAt != "" {
			t, err := time.Parse(time.RFC3339, capturedAt)
			if err != nil {
				return nil, badSelectedStepsRequest(fmt.Sprintf("outputsSources for step %s: capturedAt must be an RFC 3339 timestamp", step))
			}
			entry.CapturedAt = t.Format(time.RFC3339)
		}
		sources[step] = entry
	}
	return sources, nil
}

// startSelectedSteps starts a new run of dag in which only the selected steps
// execute; every other step is recorded as skipped.
func (a *API) startSelectedSteps(ctx context.Context, dag *ir.DAG, req selectedStepsStart) error {
	dag, err := spec.ResolveRuntimeParams(ctx, dag, req.params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: a.config.Paths.BaseConfig,
	})
	if err != nil {
		return badSelectedStepsRequest(err.Error())
	}
	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return err
	}
	if err := spec.ValidateStartParams(dag.DefaultParams, spec.StartParamInput{RawParams: req.params}); err != nil {
		return badSelectedStepsRequest(err.Error())
	}
	if err := reapplyWorkerSelector(dag, req.workerSelector); err != nil {
		return err
	}
	if req.inline {
		// The run executes from its stored snapshot, not the temporary spec file.
		if !dag.WorkingDirExplicit {
			dag.WorkingDir = ""
		}
		dag.Location = ""
		dag.SourceFile = ""
	}
	if req.labels != "" {
		dag.Labels = append(dag.Labels, ir.NewLabels(strings.Split(req.labels, ","))...)
	}

	var source *ir.DAGRunStatus
	if req.selection.outputsFrom != "" {
		source, err = a.selectedStepsSource(ctx, dag.Name, req.selection.outputsFrom)
		if err != nil {
			return err
		}
	}
	fromRuns, err := a.selectedStepsFromRuns(ctx, dag.Name, req.selection.outputsFromRuns)
	if err != nil {
		return err
	}
	if err := intake.CheckOutputSources(dag, req.selection.outputs, req.selection.outputsSources); err != nil {
		return badSelectedStepsRequest(err.Error())
	}
	nodes, reused, err := intake.SelectedStepNodes(dag, req.selection.steps, source, fromRuns, req.selection.outputs, req.selection.outputsSources)
	if err != nil {
		return badSelectedStepsRequest(err.Error())
	}

	skipHandlers, err := intake.SelectionSkipsHandlers(dag, req.selection.steps)
	if err != nil {
		return badSelectedStepsRequest(err.Error())
	}

	_, err = a.launchSeededDAGRun(ctx, seededRun{
		dag:          dag,
		dagRunID:     req.dagRunID,
		nodes:        nodes,
		reused:       reused,
		skipHandlers: skipHandlers,
		source:       source,
		params:       req.params,
		profileName:  req.profileName,
		labels:       req.labels,
		noReuse:      req.noReuse,
		ephemeral:    req.selection.ephemeral,
		triggerType:  ir.TriggerTypeManual,
	})
	return err
}

// selectedStepsSource reads the run whose outputs a selected-steps run reuses.
func (a *API) selectedStepsSource(ctx context.Context, dagName, dagRunID string) (*ir.DAGRunStatus, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dagName, dagRunID))
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("dag-run %s not found for DAG %s", dagRunID, dagName),
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find dag-run %s: %w", dagRunID, err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read status for dag-run %s: %w", dagRunID, err)
	}
	if err := a.requireDAGRunStatusExecute(ctx, status); err != nil {
		return nil, err
	}
	return status, nil
}

// selectedStepsFromRuns reads the run that each step of outputsFromRuns carries
// its outputs from. A run that does not exist for the DAG (it was removed, or
// belongs to another DAG) is a bad request that names the step and the run.
func (a *API) selectedStepsFromRuns(ctx context.Context, dagName string, refs map[string]string) (intake.RunStepSources, error) {
	runs, err := intake.LoadRunStepSources(ctx, a.dagRunRepository, dagName, refs, func(status *ir.DAGRunStatus) error {
		return a.requireDAGRunStatusExecute(ctx, status)
	})
	if sourceErr, ok := errors.AsType[*intake.RunStepSourceError](err); ok {
		return nil, badSelectedStepsRequest(sourceErr.Message)
	}
	return runs, err
}

// addSelectedStepsAudit records the step selection of a start request. Only
// the names of supplied outputs are recorded, since their values may be
// credentials. The request is recorded as ephemeral when the run leaves no
// trace, so the audit log stays the one record of it.
func addSelectedStepsAudit(details map[string]any, selection stepSelection) {
	if len(selection.steps) == 0 {
		return
	}
	details["steps"] = selection.steps
	if selection.ephemeral {
		details["ephemeral"] = true
	}
	if selection.outputsFrom != "" {
		details["outputs_from_dag_run_id"] = selection.outputsFrom
	}
	if len(selection.outputsFromRuns) > 0 {
		details["outputs_from_runs"] = selection.outputsFromRuns
	}
	if len(selection.outputsSources) > 0 {
		sources := make(map[string]string, len(selection.outputsSources))
		for step, source := range selection.outputsSources {
			sources[step] = source.RunID
		}
		details["outputs_sources"] = sources
	}
	if len(selection.outputs) > 0 {
		var names []string
		for step, values := range selection.outputs {
			for name := range values {
				names = append(names, step+"."+name)
			}
		}
		slices.Sort(names)
		details["outputs"] = names
	}
}

func badSelectedStepsRequest(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
	}
}

// rejectEphemeralRun refuses an action that would carry an ephemeral run into
// the history or run it again.
func rejectEphemeralRun(status *ir.DAGRunStatus, action string) error {
	if status == nil || !status.Ephemeral {
		return nil
	}
	return &Error{
		HTTPStatus: http.StatusConflict,
		Code:       api.ErrorCodeConflict,
		Message:    fmt.Sprintf("dag-run %s is ephemeral and cannot be %s", status.DAGRun(), action),
	}
}
