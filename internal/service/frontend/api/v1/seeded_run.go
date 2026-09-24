// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/launcher"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// seededRun describes a new DAG-run whose node states are fixed before it
// starts, so only the steps left not started execute.
type seededRun struct {
	dag         *ir.DAG
	dagRunID    string
	nodes       []runtime.NodeData
	source      *ir.DAGRunStatus
	params      string
	profileName string
	labels      string
	noReuse     bool
	triggerType ir.TriggerType
	// enqueue sends the run to the DAG's queue when one is configured.
	enqueue bool
}

// launchSeededDAGRun persists run as a queued attempt and hands it to the
// queue, the coordinator, or a local retry process.
func (a *API) launchSeededDAGRun(ctx context.Context, run seededRun) (queued bool, err error) {
	queueConfigured := run.enqueue && a.config.FindQueueConfig(run.dag.ProcGroup()) != nil
	shouldDispatch := !queueConfigured && dispatch.ShouldDispatchToCoordinator(run.dag, a.coordinatorCli != nil, a.defaultExecMode)
	if shouldDispatch && run.dag.Type == ir.TypeBuild {
		return false, buildRequiresLocalAPIError()
	}

	_, seedStatus, err := intake.SeedRun(ctx, intake.SeedRequest{
		DAGRunRepository: a.dagRunRepository,
		DAG:              run.dag,
		DAGRunID:         run.dagRunID,
		Nodes:            run.nodes,
		Source:           run.source,
		Params:           run.params,
		TriggerType:      run.triggerType,
		TriggerActor:     triggerActorFromContext(ctx),
		ProfileName:      run.profileName,
		NoReuse:          run.noReuse,
		LogBaseDir:       a.config.Paths.LogDir,
		ArtifactBaseDir:  a.config.Paths.ArtifactDir,
	})
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			intake.MarkSeedFailed(ctx, a.dagRunRepository, seedStatus, err)
		}
	}()

	if queueConfigured {
		if a.queueStore == nil {
			return false, fmt.Errorf("queue store is not configured")
		}
		if err := a.queueStore.Enqueue(ctx, run.dag.ProcGroup(), queue.QueuePriorityLow, seedStatus.DAGRun()); err != nil {
			return false, fmt.Errorf("failed to enqueue seeded dag-run: %w", err)
		}
		return true, nil
	}

	if shouldDispatch {
		if err := a.dispatchSeededRun(ctx, run.dag, seedStatus, run.labels); err != nil {
			return false, err
		}
		return false, nil
	}

	prepared, err := a.prepareRetryDAGForSubprocess(ctx, run.dag, seedStatus)
	if err != nil {
		return false, fmt.Errorf("error preparing seeded DAG env: %w", err)
	}

	retrySpec := a.subCmdBuilder.Retry(prepared, launcher.RetryOptions{
		DAGRunID:      run.dagRunID,
		TriggerActor:  seedStatus.TriggerActor,
		QueueDispatch: true,
	})
	retrySpec.Env = append(retrySpec.Env, a.managedOpenCodeEnv(ctx, prepared)...)
	if err := launcher.Start(ctx, retrySpec); err != nil {
		return false, fmt.Errorf("error starting seeded DAG: %w", err)
	}

	return false, nil
}

// dispatchSeededRun sends a seeded run to the coordinator as a retry of its
// queued attempt.
func (a *API) dispatchSeededRun(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus, labels string) error {
	dag, err := a.refreshBaseSMTP(ctx, dag, status)
	if err != nil {
		return err
	}
	opts := []executor.TaskOption{
		executor.WithWorkerSelector(dag.WorkerSelector),
		executor.WithPreviousStatus(status),
		executor.WithBaseConfig(executor.ResolveBaseConfig(dag.BaseConfigData, a.config.Paths.BaseConfig), dag.BaseConfigWorkspace),
	}
	if dag.SourceFile != "" {
		opts = append(opts, executor.WithSourceFile(dag.SourceFile))
	}
	if labels != "" {
		opts = append(opts, executor.WithLabels(labels))
	}
	if status.ProfileName != "" {
		opts = append(opts, executor.WithProfileName(status.ProfileName))
	}
	if status.TriggerActor != "" {
		opts = append(opts, executor.WithTriggerActor(status.TriggerActor))
	}
	if status.ParallelItem != "" {
		opts = append(opts, executor.WithParallelItem(status.ParallelItem))
	}
	task := executor.CreateTask(
		dag.Name,
		string(dag.YamlData),
		dispatch.DispatchOperationRetry,
		status.DAGRunID,
		opts...,
	)
	if err := a.coordinatorCli.Dispatch(ctx, dispatch.DispatchRequest{Task: task}); err != nil {
		return fmt.Errorf("error dispatching seeded run to coordinator: %w", err)
	}
	return nil
}
