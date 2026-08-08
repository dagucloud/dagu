// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dagwarning"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// DAGExecutor handles both local and distributed DAG execution.
// It encapsulates the logic for deciding between local and distributed execution
// and dispatching DAGs accordingly.
//
// Architecture Overview:
//
// The DAGExecutor implements a persistence-first approach for distributed execution to ensure
// reliability and eventual execution even when the coordinator or workers are temporarily unavailable.
//
// Execution Flow:
//
// 1. Scheduled Jobs (from TickPlanner.DispatchRun):
//   - Operation: OPERATION_START
//   - Flow: TickPlanner.DispatchRun() → HandleJob() → EnqueueDAGRun() (for distributed)
//   - This creates a persisted record with status=QUEUED before any dispatch attempt
//   - Ensures the job is tracked and can be retried if coordinator/workers are down
//
// 2. Queue Processing (from Scheduler queue handler):
//   - Operation: OPERATION_RETRY (meaning "retry the dispatch", not "retry failed execution")
//   - Flow: Queue Handler → ExecuteDAG() → Dispatch to Coordinator
//   - The item has already been persisted (was enqueued in step 1)
//   - Directly dispatches to coordinator without enqueueing again
//
// This two-phase approach guarantees:
// - No lost jobs: All scheduled runs are persisted before dispatch
// - Automatic retry: If dispatch fails, the queue handler will retry
// - Idempotency: Queue items are never enqueued twice
// - Resilience: System continues to work even if coordinator is temporarily down
//
// Method Responsibilities:
// - HandleJob(): Entry point for new scheduled jobs (handles persistence)
// - ExecuteDAG(): Executes/dispatches already-persisted jobs (no persistence)
type DAGExecutor struct {
	coordinatorCli         dispatch.Dispatcher
	subCmdBuilder          *launcher.SubCmdBuilder
	defaultExecMode        config.ExecutionMode
	baseConfigPath         string
	workspaceBaseConfigDir string
	profileResolver        DAGProfileResolver
}

type DAGProfileResolver interface {
	ResolveProfile(ctx context.Context, dagName string, workspaceName string) (string, error)
}

type DAGExecutorOption func(*DAGExecutor)

func WithDAGExecutorProfileResolver(resolver DAGProfileResolver) DAGExecutorOption {
	return func(e *DAGExecutor) {
		e.profileResolver = resolver
	}
}

// WithDAGExecutorWorkspaceBaseConfigDir sets the directory used to resolve workspace base configs.
func WithDAGExecutorWorkspaceBaseConfigDir(dir string) DAGExecutorOption {
	return func(e *DAGExecutor) {
		e.workspaceBaseConfigDir = dir
	}
}

// NewDAGExecutor creates a new DAGExecutor instance.
func NewDAGExecutor(
	coordinatorCli dispatch.Dispatcher,
	subCmdBuilder *launcher.SubCmdBuilder,
	defaultExecMode config.ExecutionMode,
	baseConfigPath string,
	opts ...DAGExecutorOption,
) *DAGExecutor {
	executor := &DAGExecutor{
		coordinatorCli:  coordinatorCli,
		subCmdBuilder:   subCmdBuilder,
		defaultExecMode: defaultExecMode,
		baseConfigPath:  baseConfigPath,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(executor)
		}
	}
	return executor
}

// HandleJob is the entry point for new scheduled jobs (from DAGRunJob.Start).
// For distributed execution, it enqueues the DAG run to ensure persistence before dispatch.
// For local execution, it delegates to ExecuteDAG.
//
// This method implements the persistence-first approach:
// 1. Distributed: Enqueue → Queue Handler picks up → ExecuteDAG dispatches
// 2. Local: Direct execution via ExecuteDAG
//
// The enqueueing step ensures that:
// - The job is persisted with status=QUEUED before any execution attempt
// - The job can be retried if the coordinator or workers are unavailable
// - No jobs are lost due to temporary system failures
func (e *DAGExecutor) HandleJob(
	ctx context.Context,
	dag *ir.DAG,
	operation dispatch.DispatchOperation,
	runID string,
	triggerType ir.TriggerType,
	scheduleTime time.Time,
) error {
	profileName := ""
	if operation == dispatch.DispatchOperationStart {
		var err error
		profileName, err = e.defaultProfileName(ctx, dag)
		if err != nil {
			return fmt.Errorf("failed to resolve DAG profile: %w", err)
		}
	}

	// For distributed execution with START operation, enqueue for persistence
	if e.shouldUseDistributedExecution(dag) && operation == dispatch.DispatchOperationStart {
		if dag.Type == ir.TypeIncremental {
			return dispatch.ErrIncrementalRequiresLocal
		}
		ctx = logger.WithValues(ctx,
			tag.DAG(dag.Name),
			tag.RunID(runID),
		)
		dag, err := e.prepareDAGForSubprocess(ctx, dag, "")
		if err != nil {
			return fmt.Errorf("failed to prepare DAG env for enqueue: %w", err)
		}

		logger.Info(ctx, "Enqueueing DAG for distributed execution",
			slog.Any("worker-selector", dag.WorkerSelector),
		)

		spec := e.subCmdBuilder.Enqueue(dag, launcher.EnqueueOptions{
			DAGRunID:     runID,
			TriggerType:  triggerType.String(),
			ScheduleTime: stringutil.FormatTime(scheduleTime),
			ProfileName:  profileName,
		})
		if err := launcher.Run(ctx, spec); err != nil {
			return fmt.Errorf("failed to enqueue DAG run: %w", err)
		}
		return nil
	}

	// For all other cases (local execution or non-START operations), use ExecuteDAG
	return e.executeDAG(ctx, dag, operation, runID, nil, triggerType, stringutil.FormatTime(scheduleTime), profileName, "")
}

// ExecuteDAG executes or dispatches an already-persisted DAG.
// This method is used by the queue handler for processing queued items.
// It NEVER enqueues - that's the responsibility of HandleJob.
//
// For distributed execution: Creates a task and dispatches to coordinator
// For local execution: Runs the DAG using the appropriate manager method
//
// Note: When called from the queue handler, operation is always OPERATION_RETRY,
// which means "retry the dispatch", not "retry a failed execution".
func (e *DAGExecutor) ExecuteDAG(
	ctx context.Context,
	dag *ir.DAG,
	operation dispatch.DispatchOperation,
	runID string,
	previousStatus *dagrun.DAGRunStatus,
	triggerType ir.TriggerType,
	scheduleTime string,
) error {
	return e.executeDAG(ctx, dag, operation, runID, previousStatus, triggerType, scheduleTime, "", "")
}

func (e *DAGExecutor) ExecuteDAGWithAdmission(
	ctx context.Context,
	dag *ir.DAG,
	operation dispatch.DispatchOperation,
	runID string,
	previousStatus *dagrun.DAGRunStatus,
	triggerType ir.TriggerType,
	scheduleTime string,
	admissionReservationToken string,
) error {
	return e.executeDAG(ctx, dag, operation, runID, previousStatus, triggerType, scheduleTime, "", admissionReservationToken)
}

func (e *DAGExecutor) executeDAG(
	ctx context.Context,
	dag *ir.DAG,
	operation dispatch.DispatchOperation,
	runID string,
	previousStatus *dagrun.DAGRunStatus,
	triggerType ir.TriggerType,
	scheduleTime string,
	defaultProfileName string,
	admissionReservationToken string,
) error {
	if err := validateDispatchOperation(operation); err != nil {
		return err
	}

	triggerActor := ""
	if previousStatus != nil {
		triggerActor = previousStatus.TriggerActor
	}

	if e.shouldUseDistributedExecution(dag) {
		if dag.Type == ir.TypeIncremental {
			return dispatch.ErrIncrementalRequiresLocal
		}
		// Distributed execution: dispatch to coordinator
		taskOpts := []executor.TaskOption{
			executor.WithWorkerSelector(dag.WorkerSelector),
			executor.WithPreviousStatus(previousStatus),
			executor.WithBaseConfig(executor.ResolveBaseConfig(dag.BaseConfigData, e.baseConfigPath)),
		}
		profileName := profileNameFromStatus(previousStatus)
		if profileName == "" {
			profileName = defaultProfileName
		}
		if profileName != "" {
			taskOpts = append(taskOpts, executor.WithProfileName(profileName))
		}
		if triggerActor != "" {
			taskOpts = append(taskOpts, executor.WithTriggerActor(triggerActor))
		}
		if previousStatus != nil && len(previousStatus.ParamsList) == 0 && previousStatus.Params != "" {
			taskOpts = append(taskOpts, executor.WithTaskParams(previousStatus.Params))
		}
		if dag.SourceFile != "" {
			taskOpts = append(taskOpts, executor.WithSourceFile(dag.SourceFile))
		}
		if scheduleTime != "" {
			taskOpts = append(taskOpts, executor.WithScheduleTime(scheduleTime))
		}
		task := executor.CreateTask(
			dag.Name,
			string(dag.YamlData),
			operation,
			runID,
			taskOpts...,
		)
		return e.dispatchToCoordinator(ctx, dispatch.DispatchRequest{
			Task:                      task,
			AdmissionReservationToken: admissionReservationToken,
		})
	}

	// Local execution
	var params any
	if previousStatus != nil {
		params = previousStatus.ParamsList
	}
	dag, err := e.prepareDAGForSubprocess(ctx, dag, params)
	if err != nil {
		return fmt.Errorf("failed to prepare DAG env for subprocess: %w", err)
	}

	switch operation {
	case dispatch.DispatchOperationUnspecified:
		return fmt.Errorf("operation not specified")

	case dispatch.DispatchOperationStart:
		spec := e.subCmdBuilder.Start(dag, launcher.StartOptions{
			DAGRunID:     runID,
			Quiet:        true,
			TriggerType:  triggerType.String(),
			TriggerActor: triggerActor,
			ScheduleTime: scheduleTime,
			ProfileName:  fallbackProfileName(profileNameFromStatus(previousStatus), defaultProfileName),
			NoReuse:      previousStatus != nil && previousStatus.NoReuse,
		})
		return launcher.Start(ctx, spec)

	case dispatch.DispatchOperationRetry:
		spec := e.subCmdBuilder.Retry(dag, launcher.RetryOptions{
			DAGRunID:      runID,
			TriggerActor:  triggerActor,
			QueueDispatch: true,
		})
		return launcher.Run(ctx, spec)

	default:
		return fmt.Errorf("unknown operation: %s", operation)
	}
}

func fallbackProfileName(profileName, fallback string) string {
	if profileName != "" {
		return profileName
	}
	return fallback
}

func (e *DAGExecutor) defaultProfileName(ctx context.Context, dag *ir.DAG) (string, error) {
	if e.profileResolver == nil || dag == nil {
		return "", nil
	}
	fileName := dag.FileName()
	if fileName == "" {
		return "", fmt.Errorf("DAG file name is required to resolve default profile")
	}
	workspaceName, err := dagWorkspaceName(dag)
	if err != nil {
		return "", err
	}
	return e.profileResolver.ResolveProfile(ctx, fileName, workspaceName)
}

func profileNameFromStatus(status *dagrun.DAGRunStatus) string {
	if status == nil {
		return ""
	}
	return status.ProfileName
}

func validateDispatchOperation(operation dispatch.DispatchOperation) error {
	switch operation {
	case dispatch.DispatchOperationStart, dispatch.DispatchOperationRetry:
		return nil
	case dispatch.DispatchOperationUnspecified:
		return fmt.Errorf("operation not specified")
	default:
		return fmt.Errorf("unknown operation: %s", operation)
	}
}

// shouldUseDistributedExecution checks if distributed execution should be used.
// Delegates to dispatch.ShouldDispatchToCoordinator for consistent dispatch logic
// across all execution paths (API, CLI, scheduler, sub-DAG).
func (e *DAGExecutor) shouldUseDistributedExecution(dag *ir.DAG) bool {
	return dispatch.ShouldDispatchToCoordinator(dag, e.coordinatorCli != nil, e.defaultExecMode)
}

// IsDistributed returns whether the given DAG would use distributed execution.
func (e *DAGExecutor) IsDistributed(dag *ir.DAG) bool {
	return e.shouldUseDistributedExecution(dag)
}

// dispatchToCoordinator dispatches a task to the coordinator for distributed execution.
// This is called after the job has been persisted (for START operations via HandleJob)
// or when retrying dispatch (for RETRY operations from queue handler).
//
// The coordinator will:
// 1. Select an appropriate worker based on the task's workerSelector
// 2. Forward the task to the selected worker
// 3. Track the execution status
func (e *DAGExecutor) dispatchToCoordinator(ctx context.Context, req dispatch.DispatchRequest) error {
	task := req.Task
	ctx = logger.WithValues(ctx,
		tag.Target(task.Target),
		tag.RunID(task.DAGRunID),
	)

	if err := e.coordinatorCli.Dispatch(ctx, req); err != nil {
		logger.Error(ctx, "Failed to dispatch task to coordinator",
			tag.Error(err),
			slog.String("operation", task.Operation.String()),
		)
		return fmt.Errorf("failed to dispatch task: %w", err)
	}

	logger.Info(ctx, "Task dispatched to coordinator",
		slog.String("operation", task.Operation.String()),
	)

	return nil
}

// Restart restarts a DAG unconditionally.
func (e *DAGExecutor) Restart(ctx context.Context, dag *ir.DAG, scheduleTime time.Time) error {
	prepared, err := e.prepareDAGForSubprocess(ctx, dag, "")
	if err != nil {
		return fmt.Errorf("failed to prepare DAG env for restart: %w", err)
	}
	spec := e.subCmdBuilder.Restart(prepared, launcher.RestartOptions{
		Quiet:        true,
		ScheduleTime: stringutil.FormatTime(scheduleTime),
	})
	return launcher.Start(ctx, spec)
}

func (e *DAGExecutor) prepareDAGForSubprocess(ctx context.Context, dag *ir.DAG, params any) (*ir.DAG, error) {
	if dag == nil {
		return nil, nil
	}

	result, err := spec.ResolveEnvWithWarnings(ctx, dag, params, spec.ResolveEnvOptions{
		BaseConfig:             e.baseConfigPath,
		WorkspaceBaseConfigDir: e.workspaceBaseConfigDir,
	})
	if err != nil {
		return nil, err
	}
	dagwarning.Log(ctx, result.BuildWarnings)

	prepared := dag.Clone()
	prepared.Env = result.Env
	return prepared, nil
}

// Close closes any resources held by the DAGExecutor, including the coordinator client.
// Note: we intentionally do NOT nil out coordinatorCli here because Close is called
// from a goroutine in Stop while concurrent dispatchRun goroutines may still read
// coordinatorCli via shouldUseDistributedExecution.
func (e *DAGExecutor) Close(ctx context.Context) {
	if e.coordinatorCli != nil {
		if err := e.coordinatorCli.Cleanup(ctx); err != nil {
			logger.Error(ctx, "Failed to cleanup coordinator client", tag.Error(err))
		}
	}
}
