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
}

// stepSelection is the step selection of a start request body.
type stepSelection struct {
	steps       []string
	outputsFrom string
	// outputs are outputs of skipped steps, by step and then output name.
	outputs map[string]map[string]string
}

// selectedStepsFromBody validates the step selection fields of a start
// request body. It returns no steps when the request runs the whole DAG.
func selectedStepsFromBody(steps *[]string, outputsFromRunID *string, outputs *map[string]map[string]string) (stepSelection, error) {
	outputsFrom := strings.TrimSpace(valueOf(outputsFromRunID))
	if steps == nil {
		if outputsFrom != "" {
			return stepSelection{}, badSelectedStepsRequest("outputsFromRunId requires steps")
		}
		if outputs != nil {
			return stepSelection{}, badSelectedStepsRequest("outputs requires steps")
		}
		return stepSelection{}, nil
	}
	if len(*steps) == 0 {
		return stepSelection{}, badSelectedStepsRequest("steps must name at least one step")
	}
	if err := validateDAGRunID(outputsFrom); err != nil {
		return stepSelection{}, err
	}
	return stepSelection{steps: *steps, outputsFrom: outputsFrom, outputs: valueOf(outputs)}, nil
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
	nodes, err := intake.SelectedStepNodes(dag, req.selection.steps, source, req.selection.outputs)
	if err != nil {
		return badSelectedStepsRequest(err.Error())
	}

	_, err = a.launchSeededDAGRun(ctx, seededRun{
		dag:         dag,
		dagRunID:    req.dagRunID,
		nodes:       nodes,
		source:      source,
		params:      req.params,
		profileName: req.profileName,
		labels:      req.labels,
		noReuse:     req.noReuse,
		triggerType: ir.TriggerTypeManual,
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

// addSelectedStepsAudit records the step selection of a start request. Only
// the names of supplied outputs are recorded, since their values may be
// credentials.
func addSelectedStepsAudit(details map[string]any, selection stepSelection) {
	if len(selection.steps) == 0 {
		return
	}
	details["steps"] = selection.steps
	if selection.outputsFrom != "" {
		details["outputs_from_dag_run_id"] = selection.outputsFrom
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
