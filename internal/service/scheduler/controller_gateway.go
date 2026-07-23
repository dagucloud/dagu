// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/dagucloud/dagu/internal/controller"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/dagrun/intake"
	"github.com/dagucloud/dagu/internal/runtime"
)

type controllerChildRunGateway struct {
	scheduler *Scheduler
	dagStore  exec.DAGStore
	manager   *runtime.Manager
}

// NewControllerChildRunGateway creates the Controller adapter for the existing
// DAG enqueue, observation, and cancellation paths.
func (s *Scheduler) NewControllerChildRunGateway(
	dagStore exec.DAGStore,
	manager *runtime.Manager,
) controller.ChildRunGateway {
	if s == nil {
		return nil
	}
	return &controllerChildRunGateway{
		scheduler: s,
		dagStore:  dagStore,
		manager:   manager,
	}
}

func (g *controllerChildRunGateway) EnsureEnqueued(ctx context.Context, request controller.ChildRunRequest) error {
	if g == nil || g.scheduler == nil || g.scheduler.config == nil {
		return fmt.Errorf("controller child enqueue is not configured")
	}
	if !g.scheduler.config.Queues.Enabled {
		return fmt.Errorf("queues are disabled in configuration")
	}

	dagRun := exec.NewDAGRunRef(request.DAG, request.DAGRunID)
	attempt, err := g.scheduler.dagRunStore.FindAttempt(ctx, dagRun)
	switch {
	case err == nil:
		return g.republishQueuedAttempt(ctx, dagRun, attempt)
	case errors.Is(err, exec.ErrDAGRunIDNotFound):
		return g.enqueueNewAttempt(ctx, request)
	default:
		return fmt.Errorf("inspect Controller child DAG run: %w", err)
	}
}

func (g *controllerChildRunGateway) republishQueuedAttempt(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	attempt exec.DAGRunAttempt,
) error {
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read Controller child DAG run status: %w", err)
	}
	if status == nil {
		return fmt.Errorf("read Controller child DAG run status: %w", exec.ErrNoStatusData)
	}
	if status.Status != core.Queued {
		return nil
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("read queued Controller child DAG: %w", err)
	}
	if err := g.scheduler.queueStore.Enqueue(ctx, dag.ProcGroup(), exec.QueuePriorityLow, dagRun); err != nil {
		return fmt.Errorf("republish Controller child DAG run: %w", err)
	}
	return nil
}

func (g *controllerChildRunGateway) enqueueNewAttempt(ctx context.Context, request controller.ChildRunRequest) error {
	dag, err := g.dagStore.GetDetails(ctx, request.DAG, spec.WithAllowBuildErrors(), spec.WithoutEval())
	if err != nil {
		return fmt.Errorf("load Controller child DAG %q: %w", request.DAG, err)
	}
	if dag == nil || dag.FileName() != request.DAG || dag.Name != request.DAG {
		return fmt.Errorf("controller child DAG %q has inconsistent identity", request.DAG)
	}
	workspaceName, err := dagWorkspaceName(dag)
	if err != nil {
		return fmt.Errorf("resolve Controller child DAG workspace: %w", err)
	}
	if workspaceName != request.Workspace {
		return fmt.Errorf("controller child DAG %q is in a different workspace", request.DAG)
	}
	if len(dag.BuildErrors) > 0 {
		return fmt.Errorf("controller child DAG %q is invalid: %w", request.DAG, core.ErrorList(dag.BuildErrors))
	}

	params := string(request.Params)
	resolved, err := spec.ResolveRuntimeParams(ctx, dag, params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: g.scheduler.config.Paths.BaseConfig,
	})
	if err != nil {
		return fmt.Errorf("resolve Controller child DAG params: %w", err)
	}
	if resolved == nil {
		return fmt.Errorf("resolve Controller child DAG params: DAG is nil")
	}
	if len(resolved.BuildErrors) > 0 {
		return fmt.Errorf("controller child DAG %q is invalid: %w", request.DAG, core.ErrorList(resolved.BuildErrors))
	}
	if err := core.ValidateStartParams(resolved.DefaultParams, core.StartParamInput{RawParams: params}); err != nil {
		return fmt.Errorf("validate Controller child DAG params: %w", err)
	}

	profileName := ""
	if g.scheduler.dagExecutor != nil {
		profileName, err = g.scheduler.dagExecutor.defaultProfileName(ctx, resolved)
		if err != nil {
			return fmt.Errorf("resolve Controller child DAG profile: %w", err)
		}
	}
	resolved.Location = ""
	_, err = intake.EnqueueRun(ctx, intake.QueueRequest{
		DAGRunStore:     g.scheduler.dagRunStore,
		QueueStore:      g.scheduler.queueStore,
		DAG:             resolved,
		DAGRunID:        request.DAGRunID,
		LogBaseDir:      g.scheduler.config.Paths.LogDir,
		ArtifactBaseDir: g.scheduler.config.Paths.ArtifactDir,
		TriggerType:     core.TriggerTypeManual,
		ProfileName:     profileName,
	})
	if err != nil {
		return fmt.Errorf("enqueue Controller child DAG run: %w", err)
	}
	return nil
}

func (g *controllerChildRunGateway) Observe(
	ctx context.Context,
	request controller.ChildRunRequest,
) (controller.ChildRunObservation, error) {
	dagRun := exec.NewDAGRunRef(request.DAG, request.DAGRunID)
	attempt, err := g.scheduler.dagRunStore.FindAttempt(ctx, dagRun)
	if errors.Is(err, exec.ErrDAGRunIDNotFound) {
		return controller.ChildRunObservation{}, nil
	}
	if err != nil {
		return controller.ChildRunObservation{}, fmt.Errorf("find Controller child DAG run: %w", err)
	}
	observation := controller.ChildRunObservation{Exists: true}

	var status *exec.DAGRunStatus
	if g.manager != nil {
		status, err = g.manager.GetSavedStatus(ctx, dagRun)
	} else {
		status, err = attempt.ReadStatus(ctx)
	}
	if err != nil {
		return observation, fmt.Errorf("read Controller child DAG run status: %w", err)
	}
	if status == nil {
		return observation, fmt.Errorf("read Controller child DAG run status: %w", exec.ErrNoStatusData)
	}

	observation.Status = status.Status
	if !controllerChildStatusTerminal(status.Status) {
		return observation, nil
	}
	if g.manager != nil {
		attempt, err = g.scheduler.dagRunStore.FindAttempt(ctx, dagRun)
		if err != nil {
			return observation, fmt.Errorf("find Controller child DAG run outputs: %w", err)
		}
	}
	if attempt.ID() != status.AttemptID {
		if g.manager == nil {
			return observation, fmt.Errorf(
				"controller child DAG run status attempt %q does not match stored attempt %q",
				status.AttemptID,
				attempt.ID(),
			)
		}
		observation.Status = core.Running
		return observation, nil
	}
	pendingRetryStatus, err := g.pendingAutoRetryStatus(ctx, status, attempt)
	if err != nil {
		return observation, fmt.Errorf("inspect Controller child DAG auto-retry: %w", err)
	}
	if pendingRetryStatus != nil {
		observation.Status = core.Running
		return observation, nil
	}
	outputs, err := attempt.ReadOutputs(ctx)
	if err != nil {
		return observation, fmt.Errorf("read Controller child DAG run outputs: %w", err)
	}
	observation.Outputs = make(map[string]string)
	if outputs != nil {
		maps.Copy(observation.Outputs, outputs.Outputs)
	}
	if status.Status != core.Succeeded && status.Status != core.PartiallySucceeded {
		observation.ErrorCategory = status.Status.String()
	}
	return observation, nil
}

func (g *controllerChildRunGateway) Stop(ctx context.Context, request controller.ChildRunRequest) error {
	dagRun := exec.NewDAGRunRef(request.DAG, request.DAGRunID)
	attempt, err := g.scheduler.dagRunStore.FindAttempt(ctx, dagRun)
	if errors.Is(err, exec.ErrDAGRunIDNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find Controller child DAG run to stop: %w", err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("read Controller child DAG run status to stop: %w", err)
	}
	if status == nil {
		return fmt.Errorf("read Controller child DAG run status to stop: %w", exec.ErrNoStatusData)
	}
	pendingRetryStatus, err := g.pendingAutoRetryStatus(ctx, status, attempt)
	if err != nil {
		return fmt.Errorf("inspect Controller child DAG auto-retry to stop: %w", err)
	}
	if pendingRetryStatus != nil {
		if err := exec.CancelFailedAutoRetryPendingRun(ctx, g.scheduler.dagRunStore, pendingRetryStatus); err != nil {
			return fmt.Errorf("cancel pending Controller child auto-retry: %w", err)
		}
		return nil
	}
	if controllerChildStatusTerminal(status.Status) {
		return nil
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("read Controller child DAG to stop: %w", err)
	}
	if status.Status == core.Queued {
		queueName := dag.ProcGroup()
		if err := g.scheduler.procStore.Lock(ctx, queueName); err != nil {
			return fmt.Errorf("lock Controller child queue %q: %w", queueName, err)
		}
		defer g.scheduler.procStore.Unlock(ctx, queueName)

		if err := exec.AbortQueuedDAGRun(ctx, g.scheduler.dagRunStore, dagRun); err != nil {
			return fmt.Errorf("abort queued Controller child DAG run: %w", err)
		}
		if _, err := g.scheduler.queueStore.DequeueByDAGRunID(ctx, queueName, dagRun); err != nil && !errors.Is(err, exec.ErrQueueItemNotFound) {
			return fmt.Errorf("dequeue Controller child DAG run: %w", err)
		}
		return nil
	}
	if status.Status != core.Running && status.Status != core.Waiting {
		return fmt.Errorf("controller child DAG run has invalid active status %s", status.Status)
	}
	if g.scheduler.dagExecutor != nil && g.scheduler.dagExecutor.IsDistributed(dag) {
		if g.scheduler.dagExecutor.coordinatorCli == nil {
			return fmt.Errorf("coordinator is not configured for Controller child cancellation")
		}
		if err := g.scheduler.dagExecutor.coordinatorCli.RequestCancel(ctx, request.DAG, request.DAGRunID, nil); err != nil {
			return fmt.Errorf("cancel distributed Controller child DAG run: %w", err)
		}
		return nil
	}
	if g.manager == nil {
		return fmt.Errorf("DAG run manager is not configured for Controller child cancellation")
	}
	if err := g.manager.Stop(ctx, dag, request.DAGRunID); err != nil {
		return fmt.Errorf("stop Controller child DAG run: %w", err)
	}
	return nil
}

func (g *controllerChildRunGateway) pendingAutoRetryStatus(
	ctx context.Context,
	status *exec.DAGRunStatus,
	attempt exec.DAGRunAttempt,
) (*exec.DAGRunStatus, error) {
	if g == nil || g.scheduler == nil || g.scheduler.retryScanner == nil {
		return nil, nil
	}
	scanner := g.scheduler.retryScanner
	if scanner.autoRetryPending(status) {
		return status, nil
	}
	if status == nil || status.Status != core.Failed || !status.Parent.Zero() {
		return nil, nil
	}
	if _, ok := retryMetadataFromStatus(status); ok {
		return nil, nil
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil, err
	}
	metadata, ok := retryMetadataFromDAG(dag)
	if !ok {
		return nil, nil
	}
	legacyStatus := *status
	legacyStatus.ProcGroup = dag.ProcGroup()
	legacyStatus.AutoRetryLimit = metadata.limit
	legacyStatus.AutoRetryInterval = metadata.interval
	legacyStatus.AutoRetryBackoff = metadata.backoff
	legacyStatus.AutoRetryMaxInterval = metadata.maxInterval
	if scanner.autoRetryPending(&legacyStatus) {
		return &legacyStatus, nil
	}
	return nil, nil
}

func controllerChildStatusTerminal(status core.Status) bool {
	switch status {
	case core.Succeeded, core.PartiallySucceeded, core.Failed, core.Aborted, core.Rejected:
		return true
	case core.NotStarted, core.Running, core.Queued, core.Waiting:
		return false
	}
	return false
}
