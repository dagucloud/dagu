// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/dispatch"
	"github.com/dagucloud/dagu/internal/launcher"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/history"
)

type editRetryOptions struct {
	specContent  string
	nameOverride string
	newDAGRunID  string
	skipSteps    *[]string
}

type editRetryPlan struct {
	sourceAttempt   exec.DAGRunAttempt
	sourceDAGRunID  string
	sourceStatus    *exec.DAGRunStatus
	editedDAG       *core.DAG
	targetWorkspace string
	newDAGRunID     string
	profileName     string
	params          string
	skippedSteps    []string
	runnableSteps   []string
	ineligible      []editRetryIneligibleStep
	warnings        []string
}

type editRetryIneligibleStep struct {
	name   string
	reason string
}

func (a *API) PreviewEditRetryDAGRun(ctx context.Context, request api.PreviewEditRetryDAGRunRequestObject) (api.PreviewEditRetryDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	opts, err := previewEditRetryOptions(request.Body)
	if err != nil {
		return nil, err
	}

	if err := a.requireEditRetryPrePlanPermissions(ctx, request.Name, request.DagRunId, opts.specContent); err != nil {
		return nil, err
	}

	plan, validationErrors, err := a.buildEditRetryPlan(ctx, request.Name, request.DagRunId, opts)
	if err != nil {
		return nil, err
	}
	if plan != nil {
		if err := a.authorizeEditRetryPlan(ctx, plan); err != nil {
			return nil, err
		}
	}

	dagName := request.Name
	skippedSteps := []string{}
	runnableSteps := []string{}
	steps := []api.Step{}
	ineligible := []struct {
		Reason   string `json:"reason"`
		StepName string `json:"stepName"`
	}{}
	warnings := []string{}
	if plan != nil {
		dagName = plan.editedDAG.Name
		skippedSteps = nonNilEditRetryStrings(plan.skippedSteps)
		runnableSteps = nonNilEditRetryStrings(plan.runnableSteps)
		steps = editRetryPreviewSteps(plan.editedDAG)
		ineligible = ineligibleStepsToAPI(plan.ineligible)
		if ineligible == nil {
			ineligible = []struct {
				Reason   string `json:"reason"`
				StepName string `json:"stepName"`
			}{}
		}
		warnings = nonNilEditRetryStrings(plan.warnings)
	}

	return api.PreviewEditRetryDAGRun200JSONResponse{
		DagName:         dagName,
		Errors:          nonNilEditRetryStrings(validationErrors),
		IneligibleSteps: ineligible,
		RunnableSteps:   runnableSteps,
		SkippedSteps:    skippedSteps,
		Steps:           steps,
		Warnings:        warnings,
	}, nil
}

func (a *API) EditRetryDAGRun(ctx context.Context, request api.EditRetryDAGRunRequestObject) (api.EditRetryDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	opts, err := editRetryOptionsFromBody(request.Body)
	if err != nil {
		return nil, err
	}

	if err := a.requireEditRetryPrePlanPermissions(ctx, request.Name, request.DagRunId, opts.specContent); err != nil {
		return nil, err
	}

	plan, validationErrors, err := a.buildEditRetryPlan(ctx, request.Name, request.DagRunId, opts)
	if err != nil {
		return nil, err
	}
	if plan != nil {
		if err := a.authorizeEditRetryPlan(ctx, plan); err != nil {
			return nil, err
		}
	}
	if len(validationErrors) > 0 {
		return nil, badEditRetryRequest(strings.Join(validationErrors, "; "))
	}

	if plan.newDAGRunID == "" {
		id, genErr := a.dagRunMgr.GenDAGRunID(ctx)
		if genErr != nil {
			return nil, fmt.Errorf("error generating dag-run ID: %w", genErr)
		}
		plan.newDAGRunID = id
	}
	if err := validateDAGRunID(plan.newDAGRunID); err != nil {
		return nil, err
	}
	if err := a.ensureDAGRunIDUnique(ctx, plan.editedDAG, plan.newDAGRunID); err != nil {
		return nil, err
	}

	queued, err := a.launchEditRetryDAGRun(ctx, plan)
	if err != nil {
		return nil, err
	}

	a.logEditRetryAudit(ctx, request.Name, plan, queued)

	return api.EditRetryDAGRun200JSONResponse{
		DagRunId:     api.DAGRunId(plan.newDAGRunID),
		Queued:       queued,
		SkippedSteps: nonNilEditRetryStrings(plan.skippedSteps),
		StartedSteps: nonNilEditRetryStrings(plan.runnableSteps),
	}, nil
}

func (a *API) authorizeEditRetryPlan(ctx context.Context, plan *editRetryPlan) error {
	if err := a.requireEditRetryPermissions(ctx, plan); err != nil {
		return err
	}
	profileName, err := a.inheritedRunProfileName(ctx, plan.sourceStatus.ProfileName)
	if err != nil {
		return err
	}
	plan.profileName = profileName
	return nil
}

func nonNilEditRetryStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (a *API) requireEditRetryPermissions(ctx context.Context, plan *editRetryPlan) error {
	if err := a.requireDAGRunStatusExecute(ctx, plan.sourceStatus); err != nil {
		return err
	}
	return a.requireDAGWriteForWorkspace(ctx, plan.targetWorkspace)
}

func (a *API) requireEditRetryPrePlanPermissions(ctx context.Context, dagName string, dagRunID string, specContent string) error {
	attempt, _, err := a.resolveAttemptForDAGRun(ctx, dagName, dagRunID)
	if err != nil {
		return err
	}
	sourceWorkspace, err := workspaceNameForAttempt(ctx, attempt)
	if err != nil {
		return err
	}
	if err := a.requireExecuteForWorkspace(ctx, sourceWorkspace); err != nil {
		return err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, sourceWorkspace); err != nil {
		return err
	}
	if strings.TrimSpace(specContent) == "" {
		return nil
	}
	return a.requireDAGWriteForWorkspace(ctx, submittedSpecRuntimeWorkspaceName(specContent, ""))
}

func editRetryRuntimeParams(status *exec.DAGRunStatus, preservedParams string) string {
	if status == nil {
		return preservedParams
	}
	if len(status.ParamsList) > 0 {
		return preservedParams
	}
	if status.Params != "" {
		return status.Params
	}
	return preservedParams
}

func previewEditRetryOptions(body *api.PreviewEditRetryDAGRunJSONRequestBody) (editRetryOptions, error) {
	if body == nil {
		return editRetryOptions{}, badEditRetryRequest("request body is required")
	}
	opts := editRetryOptions{
		specContent: strings.TrimSpace(body.Spec),
	}
	if body.DagName != nil {
		opts.nameOverride = strings.TrimSpace(*body.DagName)
	}
	return opts, nil
}

func editRetryOptionsFromBody(body *api.EditRetryDAGRunJSONRequestBody) (editRetryOptions, error) {
	if body == nil {
		return editRetryOptions{}, badEditRetryRequest("request body is required")
	}
	opts := editRetryOptions{
		specContent: strings.TrimSpace(body.Spec),
	}
	if body.DagName != nil {
		opts.nameOverride = strings.TrimSpace(*body.DagName)
	}
	if body.DagRunId != nil {
		opts.newDAGRunID = strings.TrimSpace(*body.DagRunId)
	}
	if body.SkipSteps != nil {
		skipSteps := append([]string(nil), (*body.SkipSteps)...)
		opts.skipSteps = &skipSteps
	}
	return opts, nil
}

func (a *API) buildEditRetryPlan(
	ctx context.Context,
	dagName string,
	dagRunID string,
	opts editRetryOptions,
) (*editRetryPlan, []string, error) {
	attempt, sourceDAGRunID, err := a.resolveAttemptForDAGRun(ctx, dagName, dagRunID)
	if err != nil {
		return nil, nil, err
	}

	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read status: %w", err)
	}
	if status == nil {
		return nil, nil, fmt.Errorf("failed to read status: status data is nil")
	}
	if status.Status.IsActive() || status.Status == core.NotStarted {
		return nil, nil, badEditRetryRequest(fmt.Sprintf("dag-run %s is %s and cannot be edit-retried", sourceDAGRunID, status.Status.String()))
	}

	sourceDAG, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read DAG snapshot: %w", err)
	}
	if sourceDAG == nil {
		return nil, nil, fmt.Errorf("failed to read DAG snapshot: DAG data is nil")
	}

	var validationErrors []string
	if opts.specContent == "" {
		validationErrors = append(validationErrors, "spec is required")
	}
	if opts.nameOverride != "" {
		if err := core.ValidateDAGName(opts.nameOverride); err != nil {
			validationErrors = append(validationErrors, err.Error())
		}
	}
	if opts.newDAGRunID != "" {
		if err := validateDAGRunID(opts.newDAGRunID); err != nil {
			validationErrors = append(validationErrors, err.Error())
		}
	}
	if len(validationErrors) > 0 {
		return &editRetryPlan{
			sourceAttempt:   attempt,
			sourceDAGRunID:  sourceDAGRunID,
			sourceStatus:    status,
			editedDAG:       &core.DAG{Name: dagName},
			targetWorkspace: statusWorkspaceName(status),
			newDAGRunID:     opts.newDAGRunID,
		}, validationErrors, nil
	}

	_, preservedParams, err := restoreDAGRunSnapshot(ctx, sourceDAG, status)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to restore DAG snapshot: %w", err)
	}
	params := editRetryRuntimeParams(status, preservedParams)

	editedDAG, validationErrors, err := a.loadEditedRetryDAG(ctx, opts, params, sourceDAGRunID)
	if err != nil {
		return nil, nil, err
	}
	if editedDAG == nil {
		return &editRetryPlan{
			sourceAttempt:   attempt,
			sourceDAGRunID:  sourceDAGRunID,
			sourceStatus:    status,
			editedDAG:       &core.DAG{Name: editRetryFallbackDAGName(dagName, opts.nameOverride)},
			targetWorkspace: submittedSpecRuntimeWorkspaceName(opts.specContent, ""),
			newDAGRunID:     opts.newDAGRunID,
			params:          params,
		}, validationErrors, nil
	}

	stepPlan := planEditRetrySteps(status, editedDAG, opts.skipSteps)
	validationErrors = append(validationErrors, stepPlan.validationErrors...)

	warnings := editRetryWarnings(stepPlan.skippedSteps, stepPlan.ineligible, stepPlan.reusableSourceSteps)
	return &editRetryPlan{
		sourceAttempt:   attempt,
		sourceDAGRunID:  sourceDAGRunID,
		sourceStatus:    status,
		editedDAG:       editedDAG,
		targetWorkspace: dagWorkspaceName(editedDAG),
		newDAGRunID:     opts.newDAGRunID,
		params:          params,
		skippedSteps:    stepPlan.skippedSteps,
		runnableSteps:   stepPlan.runnableSteps,
		ineligible:      stepPlan.ineligible,
		warnings:        warnings,
	}, validationErrors, nil
}

func (a *API) loadEditedRetryDAG(
	ctx context.Context,
	opts editRetryOptions,
	params string,
	sourceDAGRunID string,
) (*core.DAG, []string, error) {
	loadName := opts.nameOverride

	var namePtr *string
	if loadName != "" {
		namePtr = &loadName
	}

	tempRunID := sourceDAGRunID + "-edit-retry"
	dag, cleanup, err := a.loadInlineDAG(ctx, opts.specContent, namePtr, tempRunID)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, []string{err.Error()}, nil
	}

	resolved, err := spec.ResolveRuntimeParams(ctx, dag, params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: a.config.Paths.BaseConfig,
	})
	if err != nil {
		return nil, []string{err.Error()}, nil
	}

	var validationErrors []string
	if apiErr := buildErrorsToAPIError(resolved.BuildErrors); apiErr != nil {
		validationErrors = append(validationErrors, apiErr.Message)
	}
	if err := core.ValidateStartParams(resolved.DefaultParams, core.StartParamInput{RawParams: params}); err != nil {
		validationErrors = append(validationErrors, err.Error())
	}

	resolved.Location = ""
	resolved.SourceFile = ""

	return resolved, validationErrors, nil
}

type editRetryStepPlan struct {
	skippedSteps        []string
	runnableSteps       []string
	ineligible          []editRetryIneligibleStep
	validationErrors    []string
	reusableSourceSteps int
}

func planEditRetrySteps(
	status *exec.DAGRunStatus,
	dag *core.DAG,
	requestedSkipSteps *[]string,
) editRetryStepPlan {
	var plan editRetryStepPlan
	editedSteps := make(map[string]core.Step, len(dag.Steps))
	editedOrder := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		editedSteps[step.Name] = step
		editedOrder = append(editedOrder, step.Name)
	}

	sourceNodes := make(map[string]*exec.Node, len(status.Nodes))
	ineligibleReasons := make(map[string]string)
	eligible := make(map[string]struct{})
	for _, node := range status.Nodes {
		if node == nil {
			continue
		}
		sourceNodes[node.Step.Name] = node
		if !isReusableEditRetrySourceNode(node) {
			continue
		}
		plan.reusableSourceSteps++
		editedStep, ok := editedSteps[node.Step.Name]
		if !ok {
			reason := "step does not exist in the edited DAG"
			plan.ineligible = append(plan.ineligible, editRetryIneligibleStep{name: node.Step.Name, reason: reason})
			ineligibleReasons[node.Step.Name] = reason
			continue
		}
		if reason := missingEditedRetryOutputReason(node, editedStep); reason != "" {
			plan.ineligible = append(plan.ineligible, editRetryIneligibleStep{name: node.Step.Name, reason: reason})
			ineligibleReasons[node.Step.Name] = reason
			continue
		}
		eligible[node.Step.Name] = struct{}{}
	}

	skipSet := make(map[string]struct{})
	if requestedSkipSteps == nil {
		for _, stepName := range editedOrder {
			if _, ok := eligible[stepName]; ok {
				skipSet[stepName] = struct{}{}
			}
		}
	} else {
		for _, raw := range *requestedSkipSteps {
			stepName := strings.TrimSpace(raw)
			if stepName == "" {
				continue
			}
			if _, seen := skipSet[stepName]; seen {
				continue
			}
			if _, ok := editedSteps[stepName]; !ok {
				plan.validationErrors = append(plan.validationErrors, fmt.Sprintf("skipSteps contains unknown step %q", stepName))
				continue
			}
			if _, ok := eligible[stepName]; !ok {
				reason := ineligibleReasons[stepName]
				if reason == "" {
					sourceNode := sourceNodes[stepName]
					if sourceNode == nil {
						reason = "step was not present in the source DAG-run"
					} else {
						reason = editRetrySourceStatusReason(sourceNode)
					}
				}
				plan.validationErrors = append(plan.validationErrors, fmt.Sprintf("skipSteps contains ineligible step %q: %s", stepName, reason))
				continue
			}
			skipSet[stepName] = struct{}{}
		}
	}

	for _, stepName := range editedOrder {
		if _, ok := skipSet[stepName]; ok {
			plan.skippedSteps = append(plan.skippedSteps, stepName)
			continue
		}
		plan.runnableSteps = append(plan.runnableSteps, stepName)
	}
	sortIneligibleSteps(plan.ineligible)
	return plan
}

func isReusableEditRetrySourceNode(node *exec.Node) bool {
	if node == nil {
		return false
	}
	return node.Status.IsSuccess() || (node.Status == core.NodeSkipped && node.SkippedByRetry)
}

func editRetrySourceStatusReason(node *exec.Node) string {
	if node == nil {
		return "step was not present in the source DAG-run"
	}
	if node.Status == core.NodeSkipped {
		if node.SkippedByRetry {
			return "source step was skipped by edit retry but is missing reusable output data"
		}
		return "source step was skipped by normal DAG execution, not by edit retry"
	}
	return fmt.Sprintf("source step status is %s, not reusable", node.Status.String())
}

func missingEditedRetryOutputReason(node *exec.Node, editedStep core.Step) string {
	if editedStep.Output == "" {
		return ""
	}
	if node.OutputVariables == nil {
		return fmt.Sprintf("previous output %q is not available", editedStep.Output)
	}
	raw, ok := node.OutputVariables.Load(editedStep.Output)
	if !ok {
		return fmt.Sprintf("previous output %q is not available", editedStep.Output)
	}
	if _, ok := raw.(string); !ok {
		return fmt.Sprintf("previous output %q is not a string", editedStep.Output)
	}
	return ""
}

func (a *API) launchEditRetryDAGRun(ctx context.Context, plan *editRetryPlan) (queued bool, err error) {
	if plan == nil || plan.editedDAG == nil || plan.sourceStatus == nil {
		return false, fmt.Errorf("edit retry plan is incomplete")
	}

	historySvc := history.New(history.Config{
		DAGRunStore:     a.dagRunStore,
		LogBaseDir:      a.config.Paths.LogDir,
		ArtifactBaseDir: a.config.Paths.ArtifactDir,
	})
	seeded, err := historySvc.SeedEditRetryRun(ctx, history.SeedEditRetryRunRequest{
		DAG:           plan.editedDAG,
		DAGRunID:      plan.newDAGRunID,
		Params:        plan.params,
		ProfileName:   plan.profileName,
		SourceStatus:  plan.sourceStatus,
		SkippedSteps:  plan.skippedSteps,
		SourceWorkDir: plan.sourceAttempt.WorkDir(),
	})
	if err != nil {
		return false, err
	}
	seedStatus := seeded.Status
	defer func() {
		if err != nil {
			_ = historySvc.MarkEditRetrySeedFailed(ctx, history.MarkEditRetrySeedFailedRequest{
				Status: seedStatus,
				Cause:  err,
			})
		}
	}()

	if a.config.FindQueueConfig(plan.editedDAG.ProcGroup()) != nil {
		if a.queueStore == nil {
			return false, fmt.Errorf("queue store is not configured")
		}
		if err := a.queueStore.Enqueue(ctx, plan.editedDAG.ProcGroup(), exec.QueuePriorityLow, seedStatus.DAGRun()); err != nil {
			return false, fmt.Errorf("failed to enqueue edit retry dag-run: %w", err)
		}
		return true, nil
	}

	if dispatch.ShouldDispatchToCoordinator(plan.editedDAG, a.coordinatorCli != nil, a.defaultExecMode) {
		if err := a.dispatchEditRetry(ctx, plan.editedDAG, seedStatus); err != nil {
			return false, err
		}
		return false, nil
	}

	prepared, err := a.prepareRetryDAGForSubprocess(ctx, plan.editedDAG, seedStatus)
	if err != nil {
		return false, fmt.Errorf("error preparing edit retry DAG env: %w", err)
	}

	retrySpec := a.subCmdBuilder.QueueDispatchRetry(prepared, plan.newDAGRunID, "")
	if err := launcher.Start(ctx, retrySpec); err != nil {
		return false, fmt.Errorf("error starting edit retry DAG: %w", err)
	}

	return false, nil
}

func (a *API) dispatchEditRetry(ctx context.Context, dag *core.DAG, status *exec.DAGRunStatus) error {
	opts := []executor.TaskOption{
		executor.WithWorkerSelector(dag.WorkerSelector),
		executor.WithPreviousStatus(status),
		executor.WithBaseConfig(executor.ResolveBaseConfig(dag.BaseConfigData, a.config.Paths.BaseConfig)),
	}
	if dag.SourceFile != "" {
		opts = append(opts, executor.WithSourceFile(dag.SourceFile))
	}
	if status.ProfileName != "" {
		opts = append(opts, executor.WithProfileName(status.ProfileName))
	}
	task := executor.CreateTask(
		dag.Name,
		string(dag.YamlData),
		exec.DispatchOperationRetry,
		status.DAGRunID,
		opts...,
	)
	if err := a.coordinatorCli.Dispatch(ctx, exec.DispatchRequest{Task: task}); err != nil {
		return fmt.Errorf("error dispatching edit retry to coordinator: %w", err)
	}
	return nil
}

func editRetryPreviewSteps(dag *core.DAG) []api.Step {
	if dag == nil || len(dag.Steps) == 0 {
		return []api.Step{}
	}
	steps := make([]api.Step, len(dag.Steps))
	for i, step := range dag.Steps {
		steps[i] = toStep(step)
	}
	return steps
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func editRetryWarnings(skipped []string, ineligible []editRetryIneligibleStep, reusableSourceSteps int) []string {
	var warnings []string
	if len(skipped) == 0 && reusableSourceSteps > 0 {
		warnings = append(warnings, "no previously completed steps are eligible to reuse")
	}
	if len(ineligible) > 0 {
		warnings = append(warnings, "some previously completed steps cannot be reused with the edited DAG")
	}
	return warnings
}

func sortIneligibleSteps(steps []editRetryIneligibleStep) {
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].name < steps[j].name
	})
}

func ineligibleStepsToAPI(steps []editRetryIneligibleStep) []struct {
	Reason   string `json:"reason"`
	StepName string `json:"stepName"`
} {
	if len(steps) == 0 {
		return nil
	}
	ret := make([]struct {
		Reason   string `json:"reason"`
		StepName string `json:"stepName"`
	}, len(steps))
	for i, step := range steps {
		ret[i] = struct {
			Reason   string `json:"reason"`
			StepName string `json:"stepName"`
		}{
			Reason:   step.reason,
			StepName: step.name,
		}
	}
	return ret
}

func editRetryFallbackDAGName(pathName, override string) string {
	if override != "" {
		return override
	}
	if pathName != "" {
		return pathName
	}
	return "unknown"
}

func badEditRetryRequest(message string) *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    message,
	}
}

func (a *API) logEditRetryAudit(ctx context.Context, requestDAGName string, plan *editRetryPlan, queued bool) {
	details := map[string]any{
		"dag_name":          requestDAGName,
		"from_dag_run_id":   plan.sourceDAGRunID,
		"new_dag_name":      plan.editedDAG.Name,
		"new_dag_run_id":    plan.newDAGRunID,
		"skipped_steps":     plan.skippedSteps,
		"runnable_steps":    plan.runnableSteps,
		"queued":            queued,
		"trigger_type":      core.TriggerTypeRetry.String(),
		"source_attempt_id": plan.sourceAttempt.ID(),
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_edit_retry", details)
	logger.Info(ctx, "Edit retry dag-run launched",
		tag.DAG(plan.editedDAG.Name),
		tag.RunID(plan.newDAGRunID),
		tag.Status(core.Queued.String()),
	)
}
