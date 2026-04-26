// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agentsnapshot"
	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/audit"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

type editRetryOptions struct {
	specContent  string
	nameOverride string
	newDAGRunID  string
	skipSteps    *[]string
}

type editRetryPlan struct {
	sourceAttempt  exec.DAGRunAttempt
	sourceDAGRunID string
	sourceStatus   *exec.DAGRunStatus
	editedDAG      *core.DAG
	newDAGRunID    string
	params         string
	skippedSteps   []string
	runnableSteps  []string
	ineligible     []editRetryIneligibleStep
	warnings       []string
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

	plan, validationErrors, err := a.buildEditRetryPlan(ctx, request.Name, request.DagRunId, opts)
	if err != nil {
		return nil, err
	}
	if plan != nil {
		if err := a.requireDAGRunStatusExecute(ctx, plan.sourceStatus); err != nil {
			return nil, err
		}
		if err := a.requireExecuteForWorkspace(ctx, dagWorkspaceName(plan.editedDAG)); err != nil {
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

	plan, validationErrors, err := a.buildEditRetryPlan(ctx, request.Name, request.DagRunId, opts)
	if err != nil {
		return nil, err
	}
	if len(validationErrors) > 0 {
		return nil, badEditRetryRequest(strings.Join(validationErrors, "; "))
	}
	if plan != nil {
		if err := a.requireDAGRunStatusExecute(ctx, plan.sourceStatus); err != nil {
			return nil, err
		}
		if err := a.requireExecuteForWorkspace(ctx, dagWorkspaceName(plan.editedDAG)); err != nil {
			return nil, err
		}
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

func nonNilEditRetryStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
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
			sourceAttempt:  attempt,
			sourceDAGRunID: sourceDAGRunID,
			sourceStatus:   status,
			editedDAG:      &core.DAG{Name: dagName},
			newDAGRunID:    opts.newDAGRunID,
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
			sourceAttempt:  attempt,
			sourceDAGRunID: sourceDAGRunID,
			sourceStatus:   status,
			editedDAG:      &core.DAG{Name: editRetryFallbackDAGName(dagName, opts.nameOverride)},
			newDAGRunID:    opts.newDAGRunID,
			params:         params,
		}, validationErrors, nil
	}

	stepPlan := planEditRetrySteps(status, editedDAG, opts.skipSteps)
	validationErrors = append(validationErrors, stepPlan.validationErrors...)

	warnings := editRetryWarnings(stepPlan.skippedSteps, stepPlan.ineligible, stepPlan.reusableSourceSteps)
	return &editRetryPlan{
		sourceAttempt:  attempt,
		sourceDAGRunID: sourceDAGRunID,
		sourceStatus:   status,
		editedDAG:      editedDAG,
		newDAGRunID:    opts.newDAGRunID,
		params:         params,
		skippedSteps:   stepPlan.skippedSteps,
		runnableSteps:  stepPlan.runnableSteps,
		ineligible:     stepPlan.ineligible,
		warnings:       warnings,
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

	nodes := editRetrySeedNodes(plan.editedDAG, plan.sourceStatus, plan.skippedSteps)
	seedStatus, err := a.seedEditRetryAttempt(ctx, plan.editedDAG, plan.newDAGRunID, plan.params, nodes, plan.sourceAttempt.WorkDir())
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			a.markEditRetrySeedFailed(ctx, seedStatus, err)
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

	if core.ShouldDispatchToCoordinator(plan.editedDAG, a.coordinatorCli != nil, a.defaultExecMode) {
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
	if err := runtime.Start(ctx, retrySpec); err != nil {
		return false, fmt.Errorf("error starting edit retry DAG: %w", err)
	}

	return false, nil
}

func (a *API) markEditRetrySeedFailed(ctx context.Context, status *exec.DAGRunStatus, cause error) {
	if status == nil || cause == nil {
		return
	}
	_, _, err := a.dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		status.AttemptID,
		core.Queued,
		func(latest *exec.DAGRunStatus) error {
			latest.Status = core.Failed
			latest.FinishedAt = stringutil.FormatTime(time.Now())
			latest.Error = cause.Error()
			return nil
		},
	)
	if err != nil {
		logger.Warn(ctx, "Failed to mark edit retry seed as failed",
			tag.DAG(status.Name),
			tag.RunID(status.DAGRunID),
			tag.Error(err),
		)
	}
}

func (a *API) seedEditRetryAttempt(
	ctx context.Context,
	dag *core.DAG,
	dagRunID string,
	params string,
	nodes []runtime.NodeData,
	sourceWorkDir string,
) (*exec.DAGRunStatus, error) {
	now := time.Now()
	attempt, err := a.dagRunStore.CreateAttempt(ctx, dag, now, dagRunID, exec.NewDAGRunAttemptOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create edit retry attempt: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rmErr := a.dagRunStore.RemoveDAGRun(ctx, exec.NewDAGRunRef(dag.Name, dagRunID)); rmErr != nil {
			logger.Error(ctx, "Failed to rollback edit retry attempt",
				tag.DAG(dag.Name),
				tag.RunID(dagRunID),
				tag.Error(rmErr),
			)
		}
	}()

	logFile, err := logpath.Generate(ctx, a.config.Paths.LogDir, dag.LogDir, dag.Name, dagRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate edit retry log file: %w", err)
	}
	artifactDir, err := editRetryArtifactDir(ctx, a.config.Paths.ArtifactDir, dag, dagRunID)
	if err != nil {
		return nil, err
	}

	opts := []transform.StatusOption{
		transform.WithNodes(nodes),
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(artifactDir),
		transform.WithAttemptID(attempt.ID()),
		transform.WithQueuedAt(stringutil.FormatTime(now)),
		transform.WithPreconditions(dag.Preconditions),
		transform.WithHierarchyRefs(
			exec.NewDAGRunRef(dag.Name, dagRunID),
			exec.DAGRunRef{},
		),
		transform.WithTriggerType(core.TriggerTypeRetry),
	}
	status := transform.NewStatusBuilder(dag).Create(dagRunID, core.Queued, 0, time.Time{}, opts...)
	status.Params = params
	status.ParamsList = dag.Params

	if err := attempt.Open(ctx); err != nil {
		return nil, fmt.Errorf("failed to open edit retry attempt: %w", err)
	}
	if hasSkippedEditRetryNode(nodes) && sourceWorkDir != "" {
		newWorkDir := attempt.WorkDir()
		if err := copyEditRetryWorkDir(sourceWorkDir, newWorkDir); err != nil {
			_ = attempt.Close(ctx)
			return nil, fmt.Errorf("failed to copy edit retry work directory: %w", err)
		}
		remapEditRetryWorkDirOutputs(status.Nodes, sourceWorkDir, newWorkDir)
	}

	if err := attempt.Write(ctx, status); err != nil {
		_ = attempt.Close(ctx)
		return nil, fmt.Errorf("failed to save edit retry status: %w", err)
	}
	if err := attempt.Close(ctx); err != nil {
		return nil, fmt.Errorf("failed to close edit retry attempt: %w", err)
	}
	committed = true

	return &status, nil
}

func hasSkippedEditRetryNode(nodes []runtime.NodeData) bool {
	for _, node := range nodes {
		if node.State.SkippedByRetry {
			return true
		}
	}
	return false
}

func copyEditRetryWorkDir(sourceWorkDir, targetWorkDir string) error {
	sourceWorkDir = cleanEditRetryWorkDir(sourceWorkDir)
	targetWorkDir = cleanEditRetryWorkDir(targetWorkDir)
	if sourceWorkDir == "" || targetWorkDir == "" || sourceWorkDir == targetWorkDir {
		return nil
	}

	info, err := os.Stat(sourceWorkDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", sourceWorkDir)
	}
	if err := os.MkdirAll(targetWorkDir, 0o750); err != nil {
		return err
	}

	return filepath.WalkDir(sourceWorkDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceWorkDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		targetPath := filepath.Join(targetWorkDir, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case entry.IsDir():
			return os.MkdirAll(targetPath, mode.Perm())
		case mode.Type()&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
				return err
			}
			if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			return os.Symlink(linkTarget, targetPath)
		case mode.IsRegular():
			return copyEditRetryFile(path, targetPath, mode)
		default:
			return nil
		}
	})
}

func copyEditRetryFile(sourcePath, targetPath string, mode fs.FileMode) error {
	source, err := os.Open(sourcePath) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return err
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm()) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = target.Close()
	}()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return target.Chmod(mode.Perm())
}

func remapEditRetryWorkDirOutputs(nodes []*exec.Node, sourceWorkDir, targetWorkDir string) {
	sourceWorkDir = cleanEditRetryWorkDir(sourceWorkDir)
	targetWorkDir = cleanEditRetryWorkDir(targetWorkDir)
	if sourceWorkDir == "" || targetWorkDir == "" || sourceWorkDir == targetWorkDir {
		return
	}

	replacements := [][2]string{{sourceWorkDir, targetWorkDir}}
	sourceSlash := filepath.ToSlash(sourceWorkDir)
	targetSlash := filepath.ToSlash(targetWorkDir)
	if sourceSlash != sourceWorkDir || targetSlash != targetWorkDir {
		replacements = append(replacements, [2]string{sourceSlash, targetSlash})
	}

	for _, node := range nodes {
		if node == nil || !node.SkippedByRetry || node.OutputVariables == nil {
			continue
		}
		node.OutputVariables.Range(func(key, value any) bool {
			text, ok := value.(string)
			if !ok {
				return true
			}
			rewritten := text
			for _, replacement := range replacements {
				rewritten = strings.ReplaceAll(rewritten, replacement[0], replacement[1])
			}
			if rewritten != text {
				node.OutputVariables.Store(key, rewritten)
			}
			return true
		})
	}
}

func cleanEditRetryWorkDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
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
	if snapshot, err := agentsnapshot.BuildFromPaths(ctx, dag, a.config.Paths, a.dagStore); err != nil {
		return fmt.Errorf("build distributed agent snapshot: %w", err)
	} else if len(snapshot) > 0 {
		opts = append(opts, executor.WithAgentSnapshot(snapshot))
	}

	task := executor.CreateTask(
		dag.Name,
		string(dag.YamlData),
		coordinatorv1.Operation_OPERATION_RETRY,
		status.DAGRunID,
		opts...,
	)
	if err := a.coordinatorCli.Dispatch(ctx, task); err != nil {
		return fmt.Errorf("error dispatching edit retry to coordinator: %w", err)
	}
	return nil
}

func editRetrySeedNodes(dag *core.DAG, sourceStatus *exec.DAGRunStatus, skippedSteps []string) []runtime.NodeData {
	sourceNodes := make(map[string]*exec.Node, len(sourceStatus.Nodes))
	for _, node := range sourceStatus.Nodes {
		if node != nil {
			sourceNodes[node.Step.Name] = node
		}
	}
	skipSet := make(map[string]struct{}, len(skippedSteps))
	for _, stepName := range skippedSteps {
		skipSet[stepName] = struct{}{}
	}

	nodes := make([]runtime.NodeData, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		data := runtime.NodeData{
			Step: step,
			State: runtime.NodeState{
				Status: core.NodeNotStarted,
			},
		}
		if _, ok := skipSet[step.Name]; ok {
			data.State = skippedEditRetryNodeState(sourceNodes[step.Name])
		}
		nodes = append(nodes, data)
	}
	return nodes
}

func skippedEditRetryNodeState(source *exec.Node) runtime.NodeState {
	state := runtime.NodeState{
		Status:         core.NodeSkipped,
		SkippedByRetry: true,
	}
	if source == nil {
		return state
	}

	startedAt, _ := stringutil.ParseTime(source.StartedAt)
	finishedAt, _ := stringutil.ParseTime(source.FinishedAt)
	retriedAt, _ := stringutil.ParseTime(source.RetriedAt)
	state.Stdout = source.Stdout
	state.Stderr = source.Stderr
	state.StartedAt = startedAt
	state.FinishedAt = finishedAt
	state.RetriedAt = retriedAt
	state.RetryCount = source.RetryCount
	state.DoneCount = source.DoneCount
	state.Repeated = source.Repeated
	state.OutputVariables = cloneSyncMap(source.OutputVariables)
	state.ChatMessages = append([]exec.LLMMessage(nil), source.ChatMessages...)
	state.ToolDefinitions = append([]exec.ToolDefinition(nil), source.ToolDefinitions...)
	state.ApprovalInputs = cloneStringMap(source.ApprovalInputs)
	state.ApprovedAt = source.ApprovedAt
	state.ApprovedBy = source.ApprovedBy
	state.RejectedAt = source.RejectedAt
	state.RejectedBy = source.RejectedBy
	state.RejectionReason = source.RejectionReason
	state.ApprovalIteration = source.ApprovalIteration
	state.PushBackInputs = cloneStringMap(source.PushBackInputs)
	state.PushBackHistory = clonePushBackHistory(source.PushBackHistory)
	return state
}

func cloneSyncMap(src *collections.SyncMap) *collections.SyncMap {
	if src == nil {
		return nil
	}
	dst := &collections.SyncMap{}
	src.Range(func(key, value any) bool {
		dst.Store(key, value)
		return true
	})
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func clonePushBackHistory(src []exec.PushBackEntry) []exec.PushBackEntry {
	if len(src) == 0 {
		return nil
	}
	dst := make([]exec.PushBackEntry, len(src))
	for i, entry := range src {
		dst[i] = exec.PushBackEntry{
			Iteration: entry.Iteration,
			By:        entry.By,
			At:        entry.At,
			Inputs:    cloneStringMap(entry.Inputs),
		}
	}
	return dst
}

func editRetryArtifactDir(ctx context.Context, baseDir string, dag *core.DAG, dagRunID string) (string, error) {
	if dag == nil || !dag.ArtifactsEnabled() {
		return "", nil
	}
	dagArtifactDir := ""
	if dag.Artifacts != nil {
		dagArtifactDir = dag.Artifacts.Dir
	}
	artifactDir, err := logpath.GenerateDir(ctx, baseDir, dagArtifactDir, dag.Name, dagRunID)
	if err != nil {
		return "", fmt.Errorf("failed to generate edit retry artifact directory: %w", err)
	}
	return artifactDir, nil
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
