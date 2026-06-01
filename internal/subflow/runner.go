// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package subflow adapts Dagu child workflow execution to the runtime executor's
// child workflow interface.
package subflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/agentsnapshot"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
)

var (
	errRunIDNotSet     = errors.New("DAG run ID is not set")
	errRootRunNotSet   = errors.New("root DAG run ID is not set")
	errNoDispatcher    = errors.New("no dispatcher configured for child workflow execution")
	errMissingChildDAG = errors.New("child workflow DAG is required")
	errMissingDAGPath  = errors.New("child workflow DAG location is required")
	errNoRunDatabase   = errors.New("child workflow status database is not configured")
	errStepNameNotSet  = errors.New("retry step name is not set")
	errChildCancelled  = errors.New("sub DAG execution cancelled")
)

const (
	defaultPollInterval         = time.Second
	defaultLogInterval          = 15 * time.Second
	defaultCancellationTimeout  = 30 * time.Second
	defaultCancellationLogDelay = 5 * time.Second
	defaultMaxConsecutiveErrors = 10
)

var _ executor.SubWorkflowRunner = (*Runner)(nil)

// Runner executes child workflows through Dagu's distributed coordinator.
type Runner struct {
	dispatcher           exec.Dispatcher
	defaultMode          config.ExecutionMode
	pollInterval         time.Duration
	logInterval          time.Duration
	cancellationTimeout  time.Duration
	maxConsecutiveErrors int
}

// Option configures Runner.
type Option func(*Runner)

// WithPollInterval sets the status polling interval.
func WithPollInterval(interval time.Duration) Option {
	return func(r *Runner) {
		r.pollInterval = interval
	}
}

// WithLogInterval sets the waiting progress log interval.
func WithLogInterval(interval time.Duration) Option {
	return func(r *Runner) {
		r.logInterval = interval
	}
}

// WithCancellationTimeout sets how long cancellation waits for a terminal status.
func WithCancellationTimeout(timeout time.Duration) Option {
	return func(r *Runner) {
		r.cancellationTimeout = timeout
	}
}

// New creates a coordinator-backed child workflow runner.
func New(dispatcher exec.Dispatcher, defaultMode config.ExecutionMode, opts ...Option) *Runner {
	r := &Runner{
		dispatcher:           dispatcher,
		defaultMode:          defaultMode,
		pollInterval:         defaultPollInterval,
		logInterval:          defaultLogInterval,
		cancellationTimeout:  defaultCancellationTimeout,
		maxConsecutiveErrors: defaultMaxConsecutiveErrors,
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.pollInterval <= 0 {
		r.pollInterval = defaultPollInterval
	}
	if r.logInterval <= 0 {
		r.logInterval = defaultLogInterval
	}
	if r.cancellationTimeout <= 0 {
		r.cancellationTimeout = defaultCancellationTimeout
	}
	if r.maxConsecutiveErrors <= 0 {
		r.maxConsecutiveErrors = defaultMaxConsecutiveErrors
	}
	return r
}

// ShouldRun reports whether req should use the distributed child workflow path.
func (r *Runner) ShouldRun(_ context.Context, req executor.SubWorkflowRequest) bool {
	if r == nil || r.dispatcher == nil || req.DAG == nil {
		return false
	}
	if req.RunID == "" || req.RootDAGRun.Zero() {
		return false
	}
	if req.DAG.ForceLocal {
		return false
	}
	if len(req.WorkerSelector) > 0 {
		return true
	}
	return r.defaultMode == config.ExecutionModeDistributed
}

// Run starts a child workflow and waits for its result.
func (r *Runner) Run(ctx context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	if err := r.validate(req); err != nil {
		return nil, err
	}

	dispatchCtx := logger.WithValues(ctx,
		tag.RunID(req.RunID),
		tag.DAG(req.DAG.Name),
	)

	if err := r.dispatchStart(ctx, req); err != nil {
		logger.Error(dispatchCtx, "Distributed child workflow dispatch failed", tag.Error(err))
		return nil, fmt.Errorf("distributed execution failed: %w", err)
	}

	logger.Info(dispatchCtx, "Distributed child workflow dispatched; awaiting completion")
	return r.waitCompletion(ctx, req)
}

// Retry schedules a parent-managed retry for a child workflow step.
func (r *Runner) Retry(ctx context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	if err := r.validate(req.SubWorkflowRequest); err != nil {
		return nil, err
	}
	if req.StepName == "" {
		return nil, errStepNameNotSet
	}

	logger.Info(ctx, "Retrying child workflow via distributed execution", tag.Step(req.StepName))
	if err := r.dispatchRetry(ctx, req); err != nil {
		return nil, fmt.Errorf("distributed step retry failed: %w", err)
	}
	return r.waitCompletion(ctx, req.SubWorkflowRequest)
}

// Cancel requests cancellation for a child workflow run.
func (r *Runner) Cancel(ctx context.Context, req executor.SubWorkflowCancelRequest) error {
	if r == nil || r.dispatcher == nil {
		return errNoDispatcher
	}
	if req.DAG == nil {
		return errMissingChildDAG
	}
	if req.RunID == "" {
		return errRunIDNotSet
	}
	if req.RootDAGRun.Zero() {
		return errRootRunNotSet
	}
	return r.dispatcher.RequestCancel(ctx, req.DAG.Name, req.RunID, &req.RootDAGRun)
}

// Cleanup releases resources held by the underlying dispatcher.
func (r *Runner) Cleanup(ctx context.Context) error {
	if r == nil || r.dispatcher == nil {
		return nil
	}
	return r.dispatcher.Cleanup(ctx)
}

func (r *Runner) validate(req executor.SubWorkflowRequest) error {
	if r == nil || r.dispatcher == nil {
		return errNoDispatcher
	}
	if req.DAG == nil {
		return errMissingChildDAG
	}
	if req.RunID == "" {
		return errRunIDNotSet
	}
	if req.RootDAGRun.Zero() {
		return errRootRunNotSet
	}
	return nil
}

func (r *Runner) dispatchStart(ctx context.Context, req executor.SubWorkflowRequest) error {
	if req.Workspace != nil {
		client, ok := r.dispatcher.(workspacebundle.Client)
		if !ok {
			return fmt.Errorf("dispatcher does not support workspace bundles")
		}
		if err := client.PutWorkspaceBundle(ctx, req.Workspace.Descriptor, req.Workspace.Archive); err != nil {
			return fmt.Errorf("upload workspace bundle: %w", err)
		}
	}

	task, err := r.buildStartTask(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to build coordinator task: %w", err)
	}

	taskCtx := logger.WithValues(ctx,
		tag.RunID(task.DAGRunID),
		tag.Target(task.Target),
	)
	logger.Info(taskCtx, "Dispatching child workflow task",
		slog.Any("worker-selector", task.WorkerSelector),
	)

	if err := r.dispatcher.Dispatch(ctx, task); err != nil {
		return fmt.Errorf("failed to dispatch task: %w", err)
	}
	return nil
}

func (r *Runner) dispatchRetry(ctx context.Context, req executor.SubWorkflowRetryRequest) error {
	previousStatus, err := r.getFullStatus(ctx, req.SubWorkflowRequest)
	if err != nil {
		return fmt.Errorf("failed to load child workflow status for retry: %w", err)
	}

	task, err := r.buildRetryTask(ctx, req, previousStatus)
	if err != nil {
		return fmt.Errorf("failed to build retry coordinator task: %w", err)
	}

	taskCtx := logger.WithValues(ctx,
		tag.RunID(task.DAGRunID),
		tag.Target(task.Target),
		tag.Step(req.StepName),
	)
	logger.Info(taskCtx, "Dispatching child workflow retry task",
		slog.Any("worker-selector", task.WorkerSelector),
	)

	if err := r.dispatcher.Dispatch(ctx, task); err != nil {
		return fmt.Errorf("failed to dispatch retry task: %w", err)
	}
	return nil
}

func (r *Runner) buildStartTask(ctx context.Context, req executor.SubWorkflowRequest) (*exec.DispatchTask, error) {
	opts, err := r.taskOptions(ctx, req, executor.WithTaskParams(req.Params))
	if err != nil {
		return nil, err
	}
	return executor.CreateTask(
		req.DAG.Name,
		string(req.DAG.YamlData),
		exec.DispatchOperationStart,
		req.RunID,
		opts...,
	), nil
}

func (r *Runner) buildRetryTask(
	ctx context.Context,
	req executor.SubWorkflowRetryRequest,
	previousStatus *exec.DAGRunStatus,
) (*exec.DispatchTask, error) {
	opts, err := r.taskOptions(
		ctx,
		req.SubWorkflowRequest,
		executor.WithStep(req.StepName),
		executor.WithPreviousStatus(previousStatus),
	)
	if err != nil {
		return nil, err
	}
	return executor.CreateTask(
		req.DAG.Name,
		string(req.DAG.YamlData),
		exec.DispatchOperationRetry,
		req.RunID,
		opts...,
	), nil
}

func (r *Runner) taskOptions(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	extra ...executor.TaskOption,
) ([]executor.TaskOption, error) {
	baseConfig := string(req.DAG.BaseConfigData)
	if baseConfig == "" && req.ParentDAG != nil {
		baseConfig = string(req.ParentDAG.BaseConfigData)
	}

	options := []executor.TaskOption{
		executor.WithRootDagRun(req.RootDAGRun),
		executor.WithParentDagRun(req.ParentDAGRun),
		executor.WithWorkerSelector(cloneMap(req.WorkerSelector)),
	}
	if req.Workspace == nil {
		options = append(options, executor.WithBaseConfig(baseConfig))
	}
	if req.DAG.SourceFile != "" {
		options = append(options, executor.WithSourceFile(req.DAG.SourceFile))
	}
	if req.ExternalStepRetry {
		options = append(options, executor.WithExternalStepRetry(true))
	}

	snapshot, err := agentsnapshot.BuildFromContext(ctx, req.DAG)
	if err != nil {
		return nil, fmt.Errorf("build distributed agent snapshot: %w", err)
	}
	if len(snapshot) > 0 {
		options = append(options, executor.WithAgentSnapshot(snapshot))
	}
	if req.Workspace != nil {
		options = append(options, executor.WithWorkspaceBundle(req.Workspace.Descriptor))
	}

	options = append(options, extra...)
	return options, nil
}

func (r *Runner) waitCompletion(ctx context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	waitCtx := logger.WithValues(ctx,
		tag.RunID(req.RunID),
		tag.DAG(req.DAG.Name),
	)

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	logTicker := time.NewTicker(r.logInterval)
	defer logTicker.Stop()

	start := time.Now()
	var consecutiveErrors int

	for {
		select {
		case <-ctx.Done():
			cancelCtx := context.WithoutCancel(ctx)
			if err := r.Cancel(cancelCtx, cancelRequest(req)); err != nil {
				logger.Warn(waitCtx, "Failed to request child workflow cancellation",
					tag.Error(err),
				)
			}
			return r.waitForCancellation(cancelCtx, req, start)

		case <-ticker.C:
			result, err := r.getStatus(ctx, req)
			if err != nil {
				consecutiveErrors++
				logger.Warn(waitCtx, "Failed to get child workflow status",
					tag.Error(err),
					slog.Int("consecutive_errors", consecutiveErrors),
				)
				if consecutiveErrors >= r.maxConsecutiveErrors {
					return nil, fmt.Errorf("lost connection to coordinator after %d attempts: %w", consecutiveErrors, err)
				}
				continue
			}
			consecutiveErrors = 0

			if len(result.PendingStepRetries) > 0 {
				logger.Info(waitCtx, "Distributed child workflow returned pending step retry")
				return result, nil
			}

			if result.Status.IsActive() || result.Status == core.NotStarted {
				logger.Debug(waitCtx, "Child workflow run not completed yet")
				continue
			}

			logger.Info(waitCtx, "Distributed child workflow completed", tag.Name(result.Name))
			return result, nil

		case <-logTicker.C:
			logger.Info(waitCtx, "Waiting for distributed child workflow run to complete",
				tag.Duration(time.Since(start).Round(time.Second)),
			)
		}
	}
}

func (r *Runner) waitForCancellation(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	startTime time.Time,
) (*exec.RunStatus, error) {
	waitCtx := logger.WithValues(ctx,
		tag.RunID(req.RunID),
		tag.DAG(req.DAG.Name),
	)

	logger.Info(waitCtx, "Cancellation requested for distributed child workflow run; waiting for termination")

	timeout := time.After(r.cancellationTimeout)
	pollTicker := time.NewTicker(r.pollInterval)
	defer pollTicker.Stop()

	logTicker := time.NewTicker(defaultCancellationLogDelay)
	defer logTicker.Stop()

	var lastStatus *exec.RunStatus

	for {
		status, err := r.getStatus(ctx, req)
		if err != nil {
			logger.Warn(waitCtx, "Failed to get child workflow status during cancellation wait",
				tag.Error(err),
			)
			if shouldAbortCancellationWait(err) {
				return nil, errors.Join(errChildCancelled, err)
			}
		}
		lastStatus = status

		if status != nil && !status.Status.IsActive() && status.Status != core.NotStarted {
			return status, nil
		}

		select {
		case <-timeout:
			return nil, fmt.Errorf("distributed execution cancellation timed out for dag-run ID %s", req.RunID)

		case <-pollTicker.C:

		case <-logTicker.C:
			statusStr := "unknown"
			if lastStatus != nil {
				statusStr = lastStatus.Status.String()
			}
			logger.Info(waitCtx, "Still waiting for distributed child workflow run to terminate",
				tag.Duration(time.Since(startTime).Round(time.Second)),
				tag.Status(statusStr),
			)
		}
	}
}

func (r *Runner) getStatus(ctx context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	status, err := r.getFullStatus(ctx, req)
	if err != nil {
		return nil, err
	}
	return statusToRunStatus(status, req.RunID), nil
}

func (r *Runner) getFullStatus(
	ctx context.Context,
	req executor.SubWorkflowRequest,
) (*exec.DAGRunStatus, error) {
	result, err := r.dispatcher.GetDAGRunStatus(ctx, req.DAG.Name, req.RunID, &req.RootDAGRun)
	if err != nil {
		return nil, fmt.Errorf("failed to get DAG run status from coordinator: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("no response from coordinator")
	}
	if !result.Found {
		return nil, fmt.Errorf("DAG run not found in coordinator")
	}
	if result.Status == nil {
		return nil, fmt.Errorf("coordinator returned empty DAG run status")
	}
	return result.Status, nil
}

func statusToRunStatus(status *exec.DAGRunStatus, runID string) *exec.RunStatus {
	return &exec.RunStatus{
		Name:               status.Name,
		DAGRunID:           runID,
		Params:             status.Params,
		Outputs:            outputVariablesFromNodes(status.Nodes),
		OutputValues:       outputValuesFromNodes(status.Nodes),
		Status:             status.Status,
		PendingStepRetries: exec.PendingStepRetriesFromStatus(status),
	}
}

func outputVariablesFromNodes(nodes []*exec.Node) map[string]string {
	outputs := make(map[string]string)
	for _, node := range nodes {
		if node == nil || node.OutputVariables == nil {
			continue
		}
		node.OutputVariables.Range(func(key, value any) bool {
			k, ok := key.(string)
			if !ok {
				return true
			}
			v, ok := value.(string)
			if !ok {
				return true
			}
			if after, ok := strings.CutPrefix(v, k+"="); ok {
				outputs[k] = after
			} else {
				outputs[k] = v
			}
			return true
		})
	}
	return outputs
}

func outputValuesFromNodes(nodes []*exec.Node) map[string]any {
	outputs := make(map[string]any)
	for _, node := range nodes {
		if node == nil || node.OutputsValue == nil {
			continue
		}
		var values map[string]any
		if err := json.Unmarshal([]byte(*node.OutputsValue), &values); err != nil {
			continue
		}
		maps.Copy(outputs, values)
	}
	if len(outputs) == 0 {
		return nil
	}
	return outputs
}

func cancelRequest(req executor.SubWorkflowRequest) executor.SubWorkflowCancelRequest {
	return executor.SubWorkflowCancelRequest{
		DAG:        req.DAG,
		RootDAGRun: req.RootDAGRun,
		RunID:      req.RunID,
	}
}

func cloneMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func shouldAbortCancellationWait(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no coordinators available") ||
		strings.Contains(msg, "no coordinator client configured") ||
		strings.Contains(msg, "no available workers")
}
