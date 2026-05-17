// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/spf13/cobra"
)

func Retry() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "retry [flags] <DAG name or file>",
			Short: "Retry a previously executed DAG-run with the same run ID",
			Long: `Create a new run for a previously executed DAG-run using the same DAG-run ID.

Flags:
  --run-id string (required) Unique identifier of the DAG-run to retry.
  --step string (optional) Retry only the specified step.

Examples:
  dagu retry --run-id=abc123 my_dag
  dagu retry --run-id=abc123 my_dag.yaml
`,
			Args: cobra.ExactArgs(1),
		}, retryFlags, runRetry,
	)
}

var retryFlags = []commandLineFlag{
	dagRunIDFlagRetry,
	stepNameForRetry,
	rootDAGRunFlag,
	defaultWorkingDirFlag,
	retryWorkerIDFlag,
	attemptIDFlag,
}

var retryWorkerIDFlag = commandLineFlag{
	name:  "worker-id",
	usage: "Worker ID executing this DAG run (auto-set in distributed mode, defaults to 'local')",
}

func runRetry(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		for _, flag := range []commandLineFlag{
			rootDAGRunFlag,
			defaultWorkingDirFlag,
			retryWorkerIDFlag,
			attemptIDFlag,
		} {
			if ctx.Command.Flags().Changed(flag.name) {
				return fmt.Errorf("--%s is not supported with --context", flag.name)
			}
		}
		return remoteRunRetry(ctx, args)
	}
	dagRunID, _ := ctx.StringParam("run-id")
	stepName, _ := ctx.StringParam("step")
	rootRefStr, _ := ctx.StringParam("root")
	workerID := getWorkerID(ctx)
	attemptID, err := requireWorkerAttemptID(ctx, workerID)
	if err != nil {
		return err
	}

	var rootRun exec.DAGRunRef
	if rootRefStr != "" {
		var err error
		rootRun, err = exec.ParseDAGRunRef(rootRefStr)
		if err != nil {
			return fmt.Errorf("failed to parse root dag-run reference: %w", err)
		}
	}

	name, err := extractDAGName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}

	ref := exec.NewDAGRunRef(name, dagRunID)
	queueDispatchRetry := queueDispatchRetryRequested()
	attempt, err := findRetryAttempt(ctx, ctx.DAGRunStore, ref, rootRun)
	if queueDispatchRetry {
		err = normalizeQueueDispatchRetryLookupError(err)
	}
	if err != nil {
		if queueDispatchRetry {
			return err
		}
		if rootRun.Zero() || rootRun.ID == ref.ID {
			return fmt.Errorf("failed to find the record for dag-run ID %s: %w", dagRunID, err)
		}
		return fmt.Errorf("failed to find the sub DAG record for dag-run ID %s under root %s: %w", dagRunID, rootRun, err)
	}

	status, err := attempt.ReadStatus(ctx)
	if queueDispatchRetry {
		err = normalizeQueueDispatchRetryLookupError(err)
	}
	if err != nil {
		if queueDispatchRetry {
			return err
		}
		return fmt.Errorf("failed to read status: %w", err)
	}
	if queueDispatchRetry && (status == nil || status.Status != core.Queued) {
		return newQueueDispatchNotQueuedError(status)
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("failed to read DAG from record: %w", err)
	}

	dag, err = restoreDAGFromStatus(ctx.Context, dag, status)
	if err != nil {
		return fmt.Errorf("failed to restore DAG from status: %w", err)
	}
	restoreRetryExecutionContext(dag, status, attempt)
	if err := applyRetryDefaultWorkingDir(ctx, dag, status); err != nil {
		return err
	}

	if err := prepareQueuedCatchupRetry(ctx, attempt, dag, status); err != nil {
		return err
	}

	if rootRun.Zero() {
		rootRun = status.Root
		if rootRun.Zero() {
			rootRun = status.DAGRun()
		}
	}
	status.Root = rootRun

	// Block retry via CLI for DAGs with workerSelector, UNLESS this is a distributed worker execution
	// (indicated by --worker-id being set to something other than "local")
	if len(dag.WorkerSelector) > 0 && workerID == "local" {
		return fmt.Errorf("cannot retry DAG %q with workerSelector via CLI; use 'dagu enqueue' for distributed execution", dag.Name)
	}

	// For DAGs using a global queue: when invoked by the user, enqueue the retry
	// so it respects queue capacity. When status is already Queued, we're being
	// invoked by the queue processor to run the item—execute directly.
	// Step retry is not supported via queue (queue processor does not pass step name).
	queueConfig := ctx.Config.FindQueueConfig(dag.ProcGroup())
	if stepName == "" && queueConfig != nil && status.Status != core.Queued {
		return enqueueRetry(ctx, attempt, dag, status, dagRunID)
	}

	ctx.Context = logger.WithValues(ctx.Context, tag.DAG(dag.Name), tag.RunID(dagRunID))

	if workerID == "local" {
		return withPreparedLocalExecution(
			ctx,
			dag,
			dagRunID,
			rootRun,
			status.Parent,
			exec.PreservedQueueTriggerType(status),
			status.ScheduleTime,
			func(execCtx context.Context) (exec.DAGRunAttempt, error) {
				if queueDispatchRetry {
					if err := ensureQueueDispatchRetryTarget(execCtx, ctx.DAGRunStore, ref, rootRun); err != nil {
						return nil, err
					}
				}
				opts := exec.NewDAGRunAttemptOptions{Retry: true}
				if !rootRun.Zero() && rootRun.ID != dagRunID {
					opts.RootDAGRun = &rootRun
				}
				return ctx.DAGRunStore.CreateAttempt(execCtx, dag, time.Now(), dagRunID, opts)
			},
			func(preparedAttempt exec.DAGRunAttempt) error {
				return executeRetry(ctx, dag, status, rootRun, stepName, workerID, attemptID, preparedAttempt)
			},
		)
	}

	if ctx.DAGRunStore == nil {
		return executeRetry(ctx, dag, status, rootRun, stepName, workerID, attemptID, nil)
	}

	if err := validateWorkerAttemptBinding(dagRunID, attemptID, attempt, status); err != nil {
		return err
	}

	return withPreparedLocalExecution(
		ctx,
		dag,
		dagRunID,
		rootRun,
		status.Parent,
		exec.PreservedQueueTriggerType(status),
		status.ScheduleTime,
		func(execCtx context.Context) (exec.DAGRunAttempt, error) {
			if queueDispatchRetry {
				if err := ensureQueueDispatchRetryTarget(execCtx, ctx.DAGRunStore, ref, rootRun); err != nil {
					return nil, err
				}
			}
			return attempt, nil
		},
		func(preparedAttempt exec.DAGRunAttempt) error {
			return executeRetry(ctx, dag, status, rootRun, stepName, workerID, attemptID, preparedAttempt)
		},
	)
}

func restoreRetryExecutionContext(dag *core.DAG, status *exec.DAGRunStatus, attempt exec.DAGRunAttempt) {
	// Most retry inputs are already restored before this point: attempt.ReadDAG
	// provides the original DAG snapshot, restoreDAGFromStatus restores runtime
	// params and JSON-excluded config, and retry nodes carry persisted state.
	// This backfills only metadata that older run histories did not record.
	backfillMissingRunWorkingDirSnapshot(dag, status, attempt)
}

func applyRetryDefaultWorkingDir(ctx *Context, dag *core.DAG, status *exec.DAGRunStatus) error {
	defaultWorkingDir, err := ctx.StringParam("default-working-dir")
	if err != nil {
		return fmt.Errorf("failed to get default-working-dir: %w", err)
	}
	if defaultWorkingDir == "" {
		return nil
	}
	cleanDir := filepath.Clean(defaultWorkingDir)
	dag.WorkingDir = cleanDir
	dag.WorkingDirExplicit = true
	if status != nil {
		status.WorkingDir = cleanDir
	}
	return nil
}

func backfillMissingRunWorkingDirSnapshot(dag *core.DAG, status *exec.DAGRunStatus, attempt exec.DAGRunAttempt) {
	if dag == nil || status == nil || status.WorkingDir != "" {
		return
	}

	if dag.WorkingDir != "" && (dag.WorkingDirExplicit || storedDAGHasNonDefaultWorkingDir(dag)) {
		dag.WorkingDirExplicit = true
		status.WorkingDir = dag.WorkingDir
		return
	}

	if attempt == nil {
		return
	}
	attemptWorkDir := attempt.WorkDir()
	if attemptWorkDir == "" {
		return
	}
	status.WorkingDir = attemptWorkDir
	dag.WorkingDir = attemptWorkDir
	dag.WorkingDirExplicit = true
}

func storedDAGHasNonDefaultWorkingDir(dag *core.DAG) bool {
	if dag.WorkingDir == "" || dag.Location == "" {
		return false
	}
	return filepath.Clean(dag.WorkingDir) != filepath.Clean(filepath.Dir(dag.Location))
}

func queueDispatchRetryRequested() bool {
	return os.Getenv(exec.EnvKeyQueueDispatchRetry) != ""
}

func ensureQueueDispatchRetryTarget(
	ctx context.Context,
	dagRunStore exec.DAGRunStore,
	ref exec.DAGRunRef,
	rootRun exec.DAGRunRef,
) error {
	if dagRunStore == nil {
		return nil
	}

	attempt, err := findRetryAttempt(ctx, dagRunStore, ref, rootRun)
	err = normalizeQueueDispatchRetryLookupError(err)
	if err != nil {
		return err
	}

	status, err := attempt.ReadStatus(ctx)
	err = normalizeQueueDispatchRetryLookupError(err)
	if err != nil {
		return err
	}
	if status == nil || status.Status != core.Queued {
		return newQueueDispatchNotQueuedError(status)
	}

	return nil
}

func normalizeQueueDispatchRetryLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData) {
		return newQueueDispatchNotQueuedError(nil)
	}
	return err
}

func findRetryAttempt(
	ctx context.Context,
	dagRunStore exec.DAGRunStore,
	ref exec.DAGRunRef,
	rootRun exec.DAGRunRef,
) (exec.DAGRunAttempt, error) {
	if rootRun.Zero() || rootRun.ID == ref.ID {
		return dagRunStore.FindAttempt(ctx, ref)
	}
	return dagRunStore.FindSubAttempt(ctx, rootRun, ref.ID)
}

func newQueueDispatchNotQueuedError(status *exec.DAGRunStatus) *exec.DAGRunNotQueuedError {
	if status == nil {
		return &exec.DAGRunNotQueuedError{}
	}
	return &exec.DAGRunNotQueuedError{Status: status.Status, HasStatus: true}
}

// enqueueRetry enqueues the retry and persists Queued status via exec.EnqueueRetry.
// Retries respect global queue capacity because the queue processor picks them up
// when capacity is available.
func enqueueRetry(ctx *Context, _ exec.DAGRunAttempt, dag *core.DAG, status *exec.DAGRunStatus, dagRunID string) error {
	if err := exec.EnqueueRetry(ctx.Context, ctx.DAGRunStore, ctx.QueueStore, dag, status, exec.EnqueueRetryOptions{}); err != nil {
		if errors.Is(err, exec.ErrRetryStaleLatest) {
			return fmt.Errorf("dag-run state changed before retry could be queued")
		}
		return err
	}
	logger.Info(ctx, "Enqueued retry; will run when queue capacity is available",
		tag.DAG(dag.Name),
		tag.RunID(dagRunID),
	)
	return nil
}

// prepareQueuedCatchupRetry repairs queued catchup records before they run
// through the retry path. The queue processor executes catchup items via
// `retry`, and executeRetry expects status.Log to already exist. Older or
// previously broken queued catchup statuses may have an empty log path, so
// this fills it in and persists the repaired status before execution.
func prepareQueuedCatchupRetry(ctx *Context, attempt exec.DAGRunAttempt, dag *core.DAG, status *exec.DAGRunStatus) error {
	if !exec.IsQueuedCatchup(status) || (status.Log != "" && (!dag.ArtifactsEnabled() || status.ArchiveDir != "")) {
		return nil
	}

	if status.Log == "" {
		logPath, err := ctx.GenLogFileName(dag, status.DAGRunID)
		if err != nil {
			return fmt.Errorf("failed to generate queued catchup log file: %w", err)
		}
		status.Log = logPath
	}
	if dag.ArtifactsEnabled() && status.ArchiveDir == "" {
		artifactDir, err := ctx.GenArtifactDir(dag, status.DAGRunID)
		if err != nil {
			return fmt.Errorf("failed to generate queued catchup artifact directory: %w", err)
		}
		status.ArchiveDir = artifactDir
	}

	if err := attempt.Open(ctx.Context); err != nil {
		return fmt.Errorf("failed to open queued catchup attempt: %w", err)
	}
	defer func() {
		_ = attempt.Close(ctx.Context)
	}()

	if err := attempt.Write(ctx.Context, *status); err != nil {
		return fmt.Errorf("failed to persist queued catchup log file path: %w", err)
	}

	return nil
}

// executeRetry runs a retry of a DAG run using the original run's log file.
// Queued catchup runs reuse this path but preserve their catchup trigger type.
func executeRetry(ctx *Context, dag *core.DAG, status *exec.DAGRunStatus, rootRun exec.DAGRunRef, stepName, workerID, attemptID string, preparedAttempt exec.DAGRunAttempt) error {
	if stepName != "" {
		ctx.Context = logger.WithValues(ctx.Context, tag.Step(stepName))
	}
	logger.Debug(ctx, "Executing dag-run retry")

	logFile, err := fileutil.OpenOrCreateFile(status.Log)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	logger.Info(ctx, "Dag-run retry initiated", tag.File(logFile.Name()))

	artifactDir := status.ArchiveDir
	if artifactDir == "" && dag.ArtifactsEnabled() {
		var err error
		artifactDir, err = ctx.GenArtifactDir(dag, status.DAGRunID)
		if err != nil {
			return fmt.Errorf("failed to initialize artifact directory: %w", err)
		}
	}

	dr, err := ctx.dagStore(dagStoreConfig{
		SearchPaths:           []string{filepath.Dir(dag.Location)},
		SkipDirectoryCreation: workerID != "local",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	as := ctx.agentStores()
	triggerType := exec.PreservedQueueTriggerType(status)
	if triggerType == core.TriggerTypeUnknown {
		triggerType = core.TriggerTypeRetry
	}
	extraEnvs, err := prepareDAGTools(ctx, dag)
	if err != nil {
		return err
	}

	agentInstance := agent.New(
		status.DAGRunID,
		dag,
		filepath.Dir(logFile.Name()),
		logFile.Name(),
		ctx.DAGRunMgr,
		dr,
		agent.Options{
			RetryTarget:                status,
			ParentDAGRun:               status.Parent,
			ProgressDisplay:            shouldEnableProgress(ctx),
			ExtraEnvs:                  extraEnvs,
			StepRetry:                  stepName,
			WorkerID:                   workerID,
			AttemptID:                  attemptID,
			PreparedAttempt:            preparedAttempt,
			DAGRunStore:                ctx.DAGRunStore,
			QueueStore:                 ctx.QueueStore,
			SecretStore:                as.SecretStore,
			ServiceRegistry:            ctx.ServiceRegistry,
			RootDAGRun:                 rootRun,
			PeerConfig:                 ctx.Config.Core.Peer,
			TriggerType:                triggerType,
			DefaultExecMode:            ctx.Config.DefaultExecMode,
			AgentConfigStore:           as.ConfigStore,
			AgentModelStore:            as.ModelStore,
			AgentMemoryStore:           as.MemoryStore,
			AgentSoulStore:             as.SoulStore,
			AgentOAuthManager:          as.OAuthManager,
			AgentRemoteContextResolver: as.ContextResolver,
			ArtifactDir:                artifactDir,
			DAGRunLogDir:               ctx.Config.Paths.LogDir,
			DAGRunArtifactDir:          ctx.Config.Paths.ArtifactDir,
		},
	)

	// Use the shared agent execution function
	return ExecuteAgent(ctx, agentInstance, dag, status.DAGRunID, logFile)
}
