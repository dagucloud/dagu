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
	// slot is the schedule slot the run is for, or nil.
	slot *requestedSlot
}

// stepSelection is the step selection of a start request body.
type stepSelection struct {
	steps       []string
	outputsFrom string
	// reuseFrom is the run whose unchanged steps the run reuses, choosing
	// the steps to run itself.
	reuseFrom string
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
	// allowChanged names, by step, the carried steps that the request
	// consents to reuse although they changed since their run.
	allowChanged []string
}

// selectedStepsFromBody validates the step selection fields of a start
// request body. It returns no steps when the request runs the whole DAG.
func selectedStepsFromBody(steps *[]string, outputsFromRunID *string, outputsFromRuns *map[string]string, outputs *map[string]map[string]string, outputsSources *map[string]api.ReusedOutputSource, ephemeral *bool, reuseFrom *string, allowChangedSteps *[]string) (stepSelection, error) {
	outputsFrom := strings.TrimSpace(valueOf(outputsFromRunID))
	if reuse := strings.TrimSpace(valueOf(reuseFrom)); reuseFrom != nil {
		if steps != nil || outputsFrom != "" || outputsFromRuns != nil || outputs != nil || outputsSources != nil || allowChangedSteps != nil {
			return stepSelection{}, badSelectedStepsRequest("reuseFrom chooses the steps to run itself; send it without steps, outputsFromRunId, outputsFromRuns, outputs, outputsSources, or allowChangedSteps")
		}
		if reuse == "" {
			return stepSelection{}, badSelectedStepsRequest("reuseFrom must name a run")
		}
		if err := validateDAGRunID(reuse); err != nil {
			return stepSelection{}, err
		}
		return stepSelection{reuseFrom: reuse, ephemeral: valueOf(ephemeral)}, nil
	}
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
		if allowChangedSteps != nil {
			return stepSelection{}, badSelectedStepsRequest("allowChangedSteps requires steps")
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
	allowed := make([]string, 0, len(valueOf(allowChangedSteps)))
	for _, step := range valueOf(allowChangedSteps) {
		step = strings.TrimSpace(step)
		if step == "" {
			return stepSelection{}, badSelectedStepsRequest("allowChangedSteps must name steps")
		}
		allowed = append(allowed, step)
	}
	return stepSelection{steps: *steps, outputsFrom: outputsFrom, outputsFromRuns: fromRuns, outputs: valueOf(outputs), outputsSources: sources, ephemeral: valueOf(ephemeral), allowChanged: allowed}, nil
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
// execute; every other step is recorded as skipped. A carried step that
// changed since its run is refused unless the request consents to it. It
// returns the changed steps that the run reuses with consent.
func (a *API) startSelectedSteps(ctx context.Context, dag *ir.DAG, req selectedStepsStart) ([]intake.CarriedStep, error) {
	planned, err := a.planSelectedSteps(ctx, dag, req)
	if err != nil {
		return nil, err
	}
	if refused := planned.plan.Refusal(); refused != nil {
		return nil, refusedStepsError(refused)
	}
	skipHandlers, err := intake.SelectionSkipsHandlers(planned.dag, planned.selection.steps)
	if err != nil {
		return nil, badSelectedStepsRequest(err.Error())
	}

	_, err = a.launchSeededDAGRun(ctx, seededRun{
		dag:          planned.dag,
		dagRunID:     req.dagRunID,
		nodes:        planned.plan.Nodes,
		reused:       planned.plan.Reused,
		skipHandlers: skipHandlers,
		source:       planned.source,
		params:       req.params,
		profileName:  req.profileName,
		labels:       req.labels,
		noReuse:      req.noReuse,
		ephemeral:    planned.selection.ephemeral,
		triggerType:  ir.TriggerTypeManual,
		slot:         req.slot,
	})
	if err != nil {
		return nil, err
	}
	var consented []intake.CarriedStep
	for _, step := range planned.plan.Carried {
		if step.Changed() {
			consented = append(consented, step)
		}
	}
	return consented, nil
}

// refusedStepsError is the 400 response of a start request that refuses
// carried steps, with the refused steps in its details.
func refusedStepsError(refused *intake.RefusedStepsError) *Error {
	steps := make([]api.CarriedStepCheck, 0, len(refused.Steps))
	for _, step := range refused.Steps {
		steps = append(steps, carriedStepCheck(step))
	}
	err := badSelectedStepsRequest(refused.Error())
	err.Details = map[string]any{"refusedSteps": steps}
	return err
}

// stepReuseCheck is the response of a preview of a selected-steps run.
func stepReuseCheck(plan *intake.SelectedStepsPlan) api.StepReuseCheck {
	carried := make([]api.CarriedStepCheck, 0, len(plan.Carried))
	for _, step := range plan.Carried {
		carried = append(carried, carriedStepCheck(step))
	}
	missing := plan.Missing
	if missing == nil {
		missing = []string{}
	}
	return api.StepReuseCheck{Carried: carried, Missing: missing}
}

// carriedStepCheck is a carried step of a selected-steps run as the API
// gives it.
func carriedStepCheck(step intake.CarriedStep) api.CarriedStepCheck {
	check := api.CarriedStepCheck{
		Step:       step.Step,
		RunId:      step.DAGRunID,
		Status:     api.CarriedStepCheckStatusUnchanged,
		Changes:    stepChangeKinds(step.Changes),
		StartedAt:  ptrOf(step.StartedAt),
		FinishedAt: ptrOf(step.FinishedAt),
	}
	switch {
	case step.Unavailable != "":
		check.Status = api.CarriedStepCheckStatusUnavailable
		check.Message = ptrOf(step.Message())
	case step.Changed():
		check.Status = api.CarriedStepCheckStatusChanged
		check.Message = ptrOf(step.Message())
	}
	return check
}

// stepChangeKinds converts what changed for a step to the API's kinds.
func stepChangeKinds(changes []ir.StepChange) []api.StepChangeKind {
	kinds := make([]api.StepChangeKind, 0, len(changes))
	for _, change := range changes {
		kinds = append(kinds, api.StepChangeKind(change))
	}
	return kinds
}

// selectedStepsPlan is a run of selected steps as a request asks for it,
// before it starts.
type selectedStepsPlan struct {
	// dag has the run's parameters resolved.
	dag       *ir.DAG
	selection stepSelection
	source    *ir.DAGRunStatus
	plan      *intake.SelectedStepsPlan
}

// planSelectedSteps resolves the parameters of the run, reads the runs that
// its skipped steps carry outputs from, and plans the run.
func (a *API) planSelectedSteps(ctx context.Context, dag *ir.DAG, req selectedStepsStart) (*selectedStepsPlan, error) {
	dag, err := spec.ResolveRuntimeParams(ctx, dag, req.params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: a.config.Paths.BaseConfig,
	})
	if err != nil {
		return nil, badSelectedStepsRequest(err.Error())
	}
	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return nil, err
	}
	if err := spec.ValidateStartParams(dag.DefaultParams, spec.StartParamInput{RawParams: req.params}); err != nil {
		return nil, badSelectedStepsRequest(err.Error())
	}
	if err := reapplyWorkerSelector(dag, req.workerSelector); err != nil {
		return nil, err
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

	selection := req.selection
	var (
		source    *ir.DAGRunStatus
		sourceDAG *ir.DAG
	)
	if selection.outputsFrom != "" {
		if source, sourceDAG, err = a.reuseSource(ctx, dag.Name, selection.outputsFrom); err != nil {
			return nil, err
		}
	}
	fromRuns, definitions, unavailable, err := a.selectedStepsFromRuns(ctx, dag.Name, selection.outputsFromRuns)
	if err != nil {
		return nil, err
	}
	if selection.reuseFrom != "" {
		reuse, err := a.reuseUnchangedSteps(ctx, dag, req.profileName, selection)
		if err != nil {
			return nil, err
		}
		selection, source, sourceDAG, fromRuns, definitions = reuse.selection, reuse.source, reuse.sourceDAG, reuse.fromRuns, reuse.definitions
	}
	if err := intake.CheckOutputSources(dag, selection.outputs, selection.outputsSources); err != nil {
		return nil, badSelectedStepsRequest(err.Error())
	}
	plan, err := intake.PlanSelectedSteps(dag, req.profileName, intake.StepSelection{
		Steps:            selection.steps,
		Source:           source,
		SourceDefinition: sourceDAG,
		FromRuns:         fromRuns,
		Definitions:      definitions,
		Unavailable:      unavailable,
		Outputs:          selection.outputs,
		Sources:          selection.outputsSources,
		AllowChanged:     selection.allowChanged,
	})
	if err != nil {
		return nil, badSelectedStepsRequest(err.Error())
	}
	return &selectedStepsPlan{dag: dag, selection: selection, source: source, plan: plan}, nil
}

// reusePlan is what a run reusing an earlier one runs, and the run, with
// the steps and its definition, it carries outputs from.
type reusePlan struct {
	selection   stepSelection
	source      *ir.DAGRunStatus
	sourceDAG   *ir.DAG
	fromRuns    intake.RunStepSources
	definitions intake.RunDefinitions
}

// reuseUnchangedSteps chooses the steps a run reusing selection.reuseFrom
// runs: the steps StepsToRun names, with every other step carrying its
// outputs from that run, whose work directory the run takes too. When no
// step can be reused, or none changed, every step runs, from nothing.
func (a *API) reuseUnchangedSteps(ctx context.Context, dag *ir.DAG, profile string, selection stepSelection) (reusePlan, error) {
	all := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		all = append(all, step.Name)
	}
	source, sourceDAG, err := a.reuseSource(ctx, dag.Name, selection.reuseFrom)
	if err != nil {
		return reusePlan{}, err
	}
	if source.Status.IsActive() || source.Status == ir.NotStarted {
		return reusePlan{}, badSelectedStepsRequest(fmt.Sprintf("dag-run %s is %s; steps are reused only from a finished run", source.DAGRunID, source.Status))
	}
	steps, err := intake.StepsToRun(dag, profile, sourceDAG, source)
	if err != nil || len(steps) == 0 || len(steps) == len(all) {
		selection.steps = all
		return reusePlan{selection: selection}, nil
	}
	fromRuns := make(intake.RunStepSources, len(all)-len(steps))
	for _, name := range all {
		if !slices.Contains(steps, name) {
			fromRuns[name] = source
		}
	}
	selection.steps = steps
	selection.outputsFrom = source.DAGRunID
	return reusePlan{
		selection:   selection,
		source:      source,
		sourceDAG:   sourceDAG,
		fromRuns:    fromRuns,
		definitions: intake.RunDefinitions{source.DAGRunID: sourceDAG},
	}, nil
}

// reuseSource reads the run whose outputs a run of selected steps carries,
// with the definition it ran, which is nil when it cannot be read.
func (a *API) reuseSource(ctx context.Context, dagName, dagRunID string) (*ir.DAGRunStatus, *ir.DAG, error) {
	attempt, err := a.dagRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dagName, dagRunID))
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return nil, nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("dag-run %s not found for DAG %s", dagRunID, dagName),
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find dag-run %s: %w", dagRunID, err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read status for dag-run %s: %w", dagRunID, err)
	}
	if err := a.requireDAGRunStatusExecute(ctx, status); err != nil {
		return nil, nil, err
	}
	sourceDAG, err := attempt.ReadDAG(ctx)
	if err != nil {
		return status, nil, nil
	}
	return status, sourceDAG, nil
}

// selectedStepsFromRuns reads the run that each step of outputsFromRuns carries
// its outputs from, and the definition that each run ran. The entries whose
// run does not exist for the DAG (it was removed, or belongs to another DAG)
// come back unavailable.
func (a *API) selectedStepsFromRuns(ctx context.Context, dagName string, refs map[string]string) (intake.RunStepSources, intake.RunDefinitions, []intake.CarriedStep, error) {
	return intake.LoadRunStepSources(ctx, a.dagRunRepository, dagName, refs, func(status *ir.DAGRunStatus) error {
		return a.requireDAGRunStatusExecute(ctx, status)
	})
}

// addSelectedStepsAudit records the step selection of a start request. Only
// the names of supplied outputs are recorded, since their values may be
// credentials. The request is recorded as ephemeral when the run leaves no
// trace, so the audit log stays the one record of it. Each changed step that
// the run reuses with consent is recorded with what changed.
func addSelectedStepsAudit(details map[string]any, selection stepSelection, consented []intake.CarriedStep) {
	if selection.reuseFrom != "" {
		details["reuse_from_dag_run_id"] = selection.reuseFrom
	}
	if selection.ephemeral {
		details["ephemeral"] = true
	}
	if len(selection.steps) == 0 {
		return
	}
	details["steps"] = selection.steps
	if selection.outputsFrom != "" {
		details["outputs_from_dag_run_id"] = selection.outputsFrom
	}
	if len(selection.outputsFromRuns) > 0 {
		details["outputs_from_runs"] = selection.outputsFromRuns
	}
	if len(consented) > 0 {
		allowed := make([]map[string]any, 0, len(consented))
		for _, step := range consented {
			allowed = append(allowed, map[string]any{"step": step.Step, "dag_run_id": step.DAGRunID, "changes": step.Changes})
		}
		details["allowed_changed_steps"] = allowed
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
