// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/spec"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/spf13/cobra"
)

func Restart() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "restart [flags] <DAG name>",
			Short: "Restart a running DAG-run with a new ID",
			Long: `Stop a currently running DAG-run and immediately restart it with the same configuration but with a new DAG-run ID.

It first gracefully stops the active DAG-run, ensuring all resources are properly released, then
initiates a new DAG-run with identical parameters.

Flags:
  --run-id string (optional) Unique identifier of the DAG-run to restart. If not provided,
                             the command will find the current running DAG-run by the given DAG name.

Example:
  dagu restart --run-id=abc123 my_dag
`,
			Args: cobra.ExactArgs(1),
		}, restartFlags, runRestart,
	)
}

var restartFlags = []commandLineFlag{dagRunIDFlagRestart, scheduleTimeFlag, scheduledAtFlag}

func runRestart(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunRestart(ctx, args)
	}
	dagRunID, err := ctx.StringParam("run-id")
	if err != nil {
		return fmt.Errorf("failed to get dag-run ID: %w", err)
	}
	name := args[0]

	// The scheduler follows the current definition, so it decides whether the
	// DAG is a service. The snapshot of a run decides only once the definition
	// is gone.
	current, loadErr := spec.Load(ctx, args[0], ctx.projectLoadOptions()...)
	if loadErr == nil && current.RestartPolicy.Enabled() {
		return errRestartServiceDAG(current.Name, current.RestartPolicy)
	}

	var attempt dagrun.Attempt
	if dagRunID != "" {
		// Retrieve the previous run for the specified dag-run ID.
		dagRunRef := ir.NewDAGRunRef(name, dagRunID)
		attempt, err = ctx.Persistence.DAGRunRepository.FindAttempt(ctx, dagRunRef)
		if err != nil {
			return fmt.Errorf("failed to find the run for dag-run ID %s: %w", dagRunID, err)
		}
	} else {
		attempt, err = ctx.Persistence.DAGRunRepository.LatestAttempt(ctx, name, persis.DAGRunLatestAttemptOptions{})
		if err != nil {
			return fmt.Errorf("failed to find the latest execution history for DAG %s: %w", name, err)
		}
	}

	dagStatus, err := attempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to read status: %w", err)
	}

	dag, readErr := attempt.ReadDAG(ctx)
	if readErr == nil && loadErr != nil && dag.RestartPolicy.Enabled() {
		return errRestartServiceDAG(dag.Name, dag.RestartPolicy)
	}

	if dagStatus.Status != ir.Running {
		return fmt.Errorf("DAG %s is not running, current status: %s", name, dagStatus.Status)
	}
	if readErr != nil {
		return fmt.Errorf("failed to read DAG from execution history: %w", readErr)
	}

	dag, err = ctx.restoreRunDAG(dag, dagStatus)
	if err != nil {
		return fmt.Errorf("failed to restore DAG from status: %w", err)
	}

	slot, err := runSlotParams(ctx, dag, dagStatus.ProfileName, false)
	if err != nil {
		return err
	}
	// The restarted run is for the same slot unless the flags name another.
	if slot.scheduleTime == "" {
		slot = runSlot{scheduleTime: dagStatus.ScheduleTime, window: dagStatus.ScheduleWindow()}
	}
	// Without a slot, the restarted run settles its slot and dates when it
	// starts, after the restart wait. They are checked now, so a restart
	// whose dates cannot resolve is refused before the original run stops.
	dates, err := slotDates(ctx, dag, dagStatus.ProfileName, withDefaultSlot(ctx, dag, dagStatus.ProfileName, slot))
	if err != nil {
		return err
	}
	if slot.scheduleTime == "" {
		dates = nil
	}
	opts := runOptions{
		triggerType:    ir.TriggerTypeUnknown,
		scheduleTime:   slot.scheduleTime,
		scheduleWindow: slot.window,
		dates:          dates,
		profileName:    dagStatus.ProfileName,
		definitionID:   dagStatus.DAGDefinitionID(),
		noReuse:        dagStatus.NoReuse,
		targetNodes:    dagStatus.TargetNodes,
	}
	// A slot an operator names makes the run manual, so the scheduler reads
	// it by when it ran and it cannot stand in for the scheduler's run.
	if slot.operatorChosen {
		opts.triggerType = ir.TriggerTypeManual
	}
	// The run it read is stopped by its ID, which reaches a run on a worker
	// too, where no local process holds it.
	if err := handleRestartProcess(ctx, dag, dagStatus.DAGRunID, dagStatus.BoundWorker, opts); err != nil {
		return fmt.Errorf("restart process failed for DAG %s: %w", dag.Name, err)
	}

	return nil
}

// errRestartServiceDAG rejects the restart of a DAG that sets restart_policy:
// the scheduler starts its runs, and a run started by the command next to them
// would race with the scheduler's own restart.
func errRestartServiceDAG(name string, policy ir.RestartPolicy) error {
	return fmt.Errorf(
		"DAG %s sets restart_policy %q, so the scheduler starts its runs: stop the running run instead "+
			"(with unless-stopped, start the DAG again after the stop)",
		name, policy,
	)
}

// handleRestartProcess stops the run oldDagRunID and starts a new one where
// the rules place it. A run started bound to the worker node admitted
// restarts on that worker.
func handleRestartProcess(
	ctx *Context,
	d *ir.DAG,
	oldDagRunID string,
	admitted string,
	opts runOptions,
) error {
	placement, err := placeRun(ctx, d, opts.targetNodes, admitted)
	if err != nil {
		return err
	}
	// The coordinator, and the worker of a bound run, are checked before the
	// run stops, so a restart that cannot dispatch stops nothing.
	if placement.dispatched() {
		if err := requireWorkerOnline(ctx, placement.client, placement.worker); err != nil {
			return err
		}
	}
	opts.boundWorker = placement.worker
	if err := stopDAGIfRunning(ctx, ctx.DAGRunMgr, d, oldDagRunID); err != nil {
		return err
	}

	if d.RestartWait > 0 {
		logger.Info(ctx, "Waiting for restart", tag.Duration(d.RestartWait))
		time.Sleep(d.RestartWait)
	}

	newDagRunID, err := genRunID()
	if err != nil {
		return fmt.Errorf("failed to generate dag-run ID: %w", err)
	}
	opts.root = ir.NewDAGRunRef(d.Name, newDagRunID)
	if placement.dispatched() {
		return dispatchToCoordinatorAndWait(ctx, d, newDagRunID, opts, placement.client)
	}

	return withPreparedLocalExecution(
		ctx,
		d,
		newDagRunID,
		opts,
		func(execCtx context.Context) (dagrun.Attempt, error) {
			return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, d, time.Now(), newDagRunID, persis.DAGRunCreateAttemptOptions{})
		},
		func(preparedAttempt dagrun.Attempt) error {
			return executeDAGWithRunID(ctx, ctx.DAGRunMgr, d, newDagRunID, opts, preparedAttempt)
		},
	)
}

// executeDAGWithRunID executes a DAG with a pre-generated run ID.
func executeDAGWithRunID(
	ctx *Context,
	cli runtime.Manager,
	dag *ir.DAG,
	dagRunID string,
	opts runOptions,
	preparedAttempt dagrun.Attempt,
) error {
	logFile, err := ctx.OpenLogFile(dag, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize log file: %w", err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	ctx.LogToFile(logFile)

	ctx.Context = logger.WithValues(ctx.Context, tag.DAG(dag.Name), tag.RunID(dagRunID))

	logger.Info(ctx, "Dag-run restart initiated", tag.File(logFile.Name()))

	artifactDir, err := ctx.GenArtifactDir(dag, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize artifact directory: %w", err)
	}

	dr, err := ctx.dagRepository(dagRepositoryConfig{
		SearchPaths: []string{filepath.Dir(dag.Location)},
	})
	if err != nil {
		return fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	as := ctx.runtimeStores()
	extraEnvs, err := prepareDAGTools(ctx, dag)
	if err != nil {
		return err
	}
	runState := withJobReports(ctx, persis.NewRunStateStore(ctx.Persistence.DAGRunRepository, preparedAttempt),
		jobRun{workerID: "local", keepTrigger: true})

	agentInstance := agent.New(
		dagRunID,
		dag,
		filepath.Dir(logFile.Name()),
		logFile.Name(),
		cli,
		dr,
		agent.Options{
			Dry:                      false,
			ExtraEnvs:                extraEnvs,
			AttemptID:                agentAttemptID("", preparedAttempt),
			RunStateStore:            runState,
			StateStore:               ctx.Persistence.StateStore,
			MaterializationStore:     as.MaterializationStore,
			NoReuse:                  opts.noReuse,
			DAGDefinitionID:          opts.definitionID,
			ProfileName:              opts.profileName,
			TriggerType:              opts.triggerType,
			ParallelItem:             parallelItemFromEnv(dag.Env),
			SecretStore:              as.SecretStore,
			SecretReferenceResolver:  as.SecretResolver,
			ConnectionChecker:        as.Connections,
			ProfileStore:             as.ProfileStore,
			ProfileResolver:          as.ProfileResolver,
			NodeSelector:             as.Nodes,
			HostKeys:                 as.HostKeys,
			TargetNodes:              opts.targetNodes,
			ServiceRegistry:          ctx.Persistence.ServiceRegistry,
			SubWorkflowRunnerFactory: ctx.SubWorkflowRunnerFactory("local"),
			RootDAGRun:               ir.NewDAGRunRef(dag.Name, dagRunID),
			PeerConfig:               ctx.Config.Core.Peer,
			DefaultExecMode:          ctx.Config.DefaultExecMode,
			ScheduleTime:             opts.scheduleTime,
			ScheduleWindow:           opts.scheduleWindow,
			Dates:                    opts.dates,
			ArtifactDir:              artifactDir,
			DAGRunLogDir:             ctx.projectPaths().LogDir,
			DAGRunArtifactDir:        ctx.projectPaths().ArtifactDir,
		})

	ctx.LogToFile(logFile)
	listenSignals(ctx, agentInstance)
	if err := agentInstance.Run(ctx); err != nil {
		if !ctx.Quiet {
			agentInstance.PrintSummary(ctx)
		}
		return fmt.Errorf("dag-run failed: %w", err)
	}

	return nil
}

func stopDAGIfRunning(ctx context.Context, cli runtime.Manager, dag *ir.DAG, dagRunID string) error {
	dagStatus, err := cli.GetCurrentStatus(ctx, dag, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to get current status: %w", err)
	}

	if dagStatus.Status == ir.Running {
		logger.Info(ctx, "Stopping DAG", tag.DAG(dag.Name))
		if err := stopRunningDAG(ctx, cli, dag, dagRunID); err != nil {
			return fmt.Errorf("failed to stop running DAG: %w", err)
		}
	}
	return nil
}

func stopRunningDAG(ctx context.Context, cli runtime.Manager, dag *ir.DAG, dagRunID string) error {
	const stopPollInterval = 100 * time.Millisecond
	for {
		dagStatus, err := cli.GetCurrentStatus(ctx, dag, dagRunID)
		if err != nil {
			return fmt.Errorf("failed to get current status: %w", err)
		}

		if dagStatus.Status != ir.Running {
			return nil
		}

		if err := cli.Stop(ctx, dag, dagRunID); err != nil {
			return err
		}

		time.Sleep(stopPollInterval)
	}
}

// requireWorkerOnline fails unless the coordinator answers and the worker
// sent a heartbeat recently enough for a dispatch to reach it. An empty
// worker requires only the coordinator. The worker can still be lost while
// the run stops and during restart_wait_sec; the dispatch then fails as
// offline.
func requireWorkerOnline(ctx context.Context, cli coordinator.Client, worker string) error {
	workers, err := cli.GetWorkers(ctx)
	switch {
	case err != nil && worker == "":
		return fmt.Errorf("failed to reach the coordinator: %w", err)
	case err != nil:
		return fmt.Errorf("failed to reach the coordinator of worker node %q: %w", worker, err)
	case worker == "":
		return nil
	}
	now := time.Now()
	if !slices.ContainsFunc(workers, func(w *coordinatorv1.WorkerInfo) bool {
		// The coordinator lists stale heartbeats until it sweeps them, so the
		// check applies the freshness rule its dispatch applies.
		return w.GetWorkerId() == worker && dispatch.WorkerHeartbeatFresh(dispatch.WorkerHeartbeatRecord{
			WorkerID:        w.GetWorkerId(),
			LastHeartbeatAt: time.Unix(w.GetLastHeartbeatAt(), 0).UnixMilli(),
		}, now, dispatch.DefaultStaleWorkerHeartbeatThreshold)
	}) {
		return fmt.Errorf("worker node %q is offline", worker)
	}
	return nil
}
