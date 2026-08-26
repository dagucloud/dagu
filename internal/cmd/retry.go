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

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/persis"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/humantask"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
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
  --downstream (optional) Also retry reachable descendants of --step.
  --sub-run-id string (optional) Retry the step in this persisted child DAG-run.

Examples:
  dagu retry --run-id=abc123 my_dag
  dagu retry --run-id=abc123 my_dag.yaml
  dagu retry --run-id=abc123 --step=build my_dag
  dagu retry --run-id=abc123 --step=build --downstream my_dag
  dagu retry --run-id=abc123 --sub-run-id=child123 --step=build my_dag
`,
			Args: cobra.ExactArgs(1),
		}, retryFlags, runRetry,
	)
}

var retryFlags = []commandLineFlag{
	dagRunIDFlagRetry,
	stepNameForRetry,
	downstreamForRetry,
	subDAGRunIDFlagStatus,
	rootDAGRunFlag,
	retryPathFlag,
	defaultWorkingDirFlag,
	retryWorkerIDFlag,
	attemptIDFlag,
	triggerActorFlag,
}

var retryWorkerIDFlag = commandLineFlag{
	name:  "worker-id",
	usage: "Worker ID executing this DAG run (auto-set in distributed mode, defaults to 'local')",
}

const (
	retrySourceReleaseTimeout      = 10 * time.Second
	retrySourceReleasePollInterval = 50 * time.Millisecond
)

func runRetry(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		for _, flag := range []commandLineFlag{
			rootDAGRunFlag,
			retryPathFlag,
			defaultWorkingDirFlag,
			retryWorkerIDFlag,
			attemptIDFlag,
			triggerActorFlag,
		} {
			if ctx.Command.Flags().Changed(flag.name) {
				return fmt.Errorf("--%s is not supported with --context", flag.name)
			}
		}
		return remoteRunRetry(ctx, args)
	}
	if ctx.Persistence.DAGRunRepository == nil {
		return fmt.Errorf("DAG-run repository is not available")
	}
	dagRunID, _ := ctx.StringParam("run-id")
	stepName, _ := ctx.StringParam("step")
	includeDownstream, err := ctx.Command.Flags().GetBool("downstream")
	if err != nil {
		return fmt.Errorf("failed to get --downstream: %w", err)
	}
	if includeDownstream && stepName == "" {
		return fmt.Errorf("--downstream requires --step")
	}
	subDAGRunID, _ := ctx.StringParam("sub-run-id")
	rootRefStr, _ := ctx.StringParam("root")
	retryPathValue, _ := ctx.StringParam("retry-path")
	retryPath, err := dagrun.ParseRetryPath(retryPathValue)
	if err != nil {
		return err
	}
	if subDAGRunID != "" && stepName == "" {
		return fmt.Errorf("--sub-run-id requires --step")
	}
	if subDAGRunID != "" && (rootRefStr != "" || len(retryPath.Hops) > 0) {
		return fmt.Errorf("--sub-run-id cannot be combined with internal child retry flags")
	}
	workerID := getWorkerID(ctx)
	triggerActor, err := ctx.StringParam("trigger-actor")
	if err != nil {
		return fmt.Errorf("failed to get trigger actor: %w", err)
	}
	attemptID, err := requireWorkerAttemptID(ctx, workerID)
	if err != nil {
		return err
	}

	var rootRun ir.DAGRunRef
	if rootRefStr != "" {
		var err error
		rootRun, err = ir.ParseDAGRunRef(rootRefStr)
		if err != nil {
			return fmt.Errorf("failed to parse root dag-run reference: %w", err)
		}
	}

	name, err := extractDAGName(ctx, args[0])
	if err != nil {
		return fmt.Errorf("failed to extract DAG name: %w", err)
	}

	ref := ir.NewDAGRunRef(name, dagRunID)
	queueDispatchRetry := queueDispatchRetryRequested()
	attempt, err := findRetryAttempt(ctx, ctx.Persistence.DAGRunRepository, ref, rootRun)
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
	if status == nil {
		if queueDispatchRetry {
			return newQueueDispatchNotQueuedError(status)
		}
		return fmt.Errorf("failed to read status: status data is nil")
	}
	if queueDispatchRetry && triggerActor == "" {
		triggerActor = status.TriggerActor
	}
	profileName := status.ProfileName
	if queueDispatchRetry && status.Status != ir.Queued {
		return newQueueDispatchNotQueuedError(status)
	}
	if subDAGRunID != "" {
		var targetStatus *ir.DAGRunStatus
		retryPath, targetStatus, err = ctx.Persistence.DAGRunRepository.ResolveRetryPath(ctx, ref, subDAGRunID, stepName)
		if err != nil {
			return err
		}
		if err := humantask.ValidateRetry(targetStatus, retryPath.Step); err != nil {
			return err
		}
		stepName = retryPath.RootStep()
	} else {
		if len(retryPath.Hops) > 0 {
			stepName = retryPath.RootStep()
		}
		if err := humantask.ValidateRetry(status, stepName); err != nil {
			return err
		}
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return fmt.Errorf("failed to read DAG from record: %w", err)
	}

	dag, err = restoreDAGFromStatus(ctx.Context, dag, status)
	if err != nil {
		return fmt.Errorf("failed to restore DAG from status: %w", err)
	}
	// Fresh queue dispatches stage dependencies from the source workspace.
	if !queueDispatchRetry || !shouldUseQueuedDispatchAttempt(status) {
		workDirRef := retryWorkDirRef(status, ref, rootRun)
		if err := restoreRetryExecutionContext(ctx.Context, ctx.Persistence.DAGRunRepository, dag, status, workDirRef); err != nil {
			return err
		}
	}
	if err := applyRetryDefaultWorkingDir(ctx, dag, status); err != nil {
		return err
	}
	if dag.Type == ir.TypeBuild && workerID != "local" {
		return dispatch.ErrBuildRequiresLocal
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
	if stepName == "" && queueConfig != nil && status.Status != ir.Queued {
		return enqueueRetry(ctx, dag, status, triggerActor)
	}

	if err := waitForRetrySourceRelease(ctx, dag, status); err != nil {
		return err
	}

	ctx.Context = logger.WithValues(ctx.Context, tag.DAG(dag.Name), tag.RunID(dagRunID))
	run := runOptions{
		root:              rootRun,
		parent:            status.Parent,
		workerID:          workerID,
		attemptID:         attemptID,
		triggerType:       queue.PreservedQueueTriggerType(status),
		triggerActor:      triggerActor,
		parallelItem:      status.ParallelItem,
		scheduleTime:      status.ScheduleTime,
		profileName:       profileName,
		definitionID:      status.DAGDefinitionID(),
		noReuse:           status.NoReuse,
		step:              stepName,
		includeDownstream: includeDownstream,
		retryPath:         retryPath,
	}

	if workerID == "local" {
		return withPreparedLocalExecution(
			ctx,
			dag,
			dagRunID,
			run,
			func(execCtx context.Context) (dagrun.Attempt, error) {
				if queueDispatchRetry {
					queuedAttempt, queuedStatus, err := queueDispatchRetryTarget(execCtx, ctx.Persistence.DAGRunRepository, ref, rootRun, attempt.ID())
					if err != nil {
						return nil, err
					}
					if shouldUseQueuedDispatchAttempt(queuedStatus) {
						return queuedAttempt, nil
					}
				}
				opts := persis.DAGRunCreateAttemptOptions{Retry: true}
				if !rootRun.Zero() && rootRun.ID != dagRunID {
					opts.RootDAGRun = rootRun
				}
				return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, dag, time.Now(), dagRunID, opts)
			},
			func(preparedAttempt dagrun.Attempt) error {
				prepared := run
				prepared.preparedAttempt = preparedAttempt
				return executeRetry(ctx, dag, status, prepared)
			},
		)
	}

	if err := validateWorkerAttemptBinding(dagRunID, attemptID, attempt, status); err != nil {
		return err
	}

	return withPreparedLocalExecution(
		ctx,
		dag,
		dagRunID,
		run,
		func(execCtx context.Context) (dagrun.Attempt, error) {
			if queueDispatchRetry {
				if err := ensureQueueDispatchRetryTarget(execCtx, ctx.Persistence.DAGRunRepository, ref, rootRun); err != nil {
					return nil, err
				}
			}
			return attempt, nil
		},
		func(preparedAttempt dagrun.Attempt) error {
			prepared := run
			prepared.preparedAttempt = preparedAttempt
			return executeRetry(ctx, dag, status, prepared)
		},
	)
}

func restoreRetryExecutionContext(
	ctx context.Context,
	repository *persis.DAGRunRepository,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	workDirRef dagrun.WorkDirRef,
) error {
	// Most retry inputs are already restored before this point: attempt.ReadDAG
	// provides the original DAG snapshot, restoreDAGFromStatus restores runtime
	// params and JSON-excluded config, and retry nodes carry persisted state.
	// This backfills only metadata that older run histories did not record.
	return backfillMissingRunWorkingDirSnapshot(ctx, repository, dag, status, workDirRef)
}

func retryWorkDirRef(status *ir.DAGRunStatus, defaultRef, defaultRoot ir.DAGRunRef) dagrun.WorkDirRef {
	ref := defaultRef
	root := defaultRoot
	if status != nil {
		if status.Name != "" {
			ref.Name = status.Name
		}
		if status.DAGRunID != "" {
			ref.ID = status.DAGRunID
		}
		if !status.Root.Zero() {
			root = status.Root
		}
	}
	if root.Zero() {
		root = ref
	}
	return dagrun.WorkDirRef{RootDAGRun: root, DAGRun: ref}
}

func applyRetryDefaultWorkingDir(ctx *Context, dag *ir.DAG, status *ir.DAGRunStatus) error {
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

func backfillMissingRunWorkingDirSnapshot(
	ctx context.Context,
	repository *persis.DAGRunRepository,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	workDirRef dagrun.WorkDirRef,
) error {
	if dag == nil || status == nil || status.WorkingDir != "" {
		return nil
	}

	if dag.WorkingDir != "" && (dag.WorkingDirExplicit || storedDAGHasNonDefaultWorkingDir(dag)) {
		dag.WorkingDirExplicit = true
		status.WorkingDir = dag.WorkingDir
		return nil
	}

	if repository == nil {
		return nil
	}
	workDir, err := repository.MaterializeWorkDir(ctx, workDirRef)
	if err != nil {
		return fmt.Errorf("failed to materialize retry work directory: %w", err)
	}
	if workDir == "" {
		return nil
	}
	status.WorkingDir = workDir
	dag.WorkingDir = workDir
	dag.WorkingDirExplicit = true
	return nil
}

func storedDAGHasNonDefaultWorkingDir(dag *ir.DAG) bool {
	if dag.WorkingDir == "" || dag.Location == "" {
		return false
	}
	return filepath.Clean(dag.WorkingDir) != filepath.Clean(filepath.Dir(dag.Location))
}

func queueDispatchRetryRequested() bool {
	return os.Getenv(runenv.EnvKeyQueueDispatchRetry) != ""
}

func ensureQueueDispatchRetryTarget(
	ctx context.Context,
	dagRunRepository *persis.DAGRunRepository,
	ref ir.DAGRunRef,
	rootRun ir.DAGRunRef,
) error {
	_, err := queueDispatchRetryAttempt(ctx, dagRunRepository, ref, rootRun, "")
	return err
}

func queueDispatchRetryAttempt(
	ctx context.Context,
	dagRunRepository *persis.DAGRunRepository,
	ref ir.DAGRunRef,
	rootRun ir.DAGRunRef,
	expectedAttemptID string,
) (dagrun.Attempt, error) {
	attempt, _, err := queueDispatchRetryTarget(ctx, dagRunRepository, ref, rootRun, expectedAttemptID)
	return attempt, err
}

func queueDispatchRetryTarget(
	ctx context.Context,
	dagRunRepository *persis.DAGRunRepository,
	ref ir.DAGRunRef,
	rootRun ir.DAGRunRef,
	expectedAttemptID string,
) (dagrun.Attempt, *ir.DAGRunStatus, error) {
	if dagRunRepository == nil {
		return nil, nil, nil
	}

	attempt, err := findRetryAttempt(ctx, dagRunRepository, ref, rootRun)
	err = normalizeQueueDispatchRetryLookupError(err)
	if err != nil {
		return nil, nil, err
	}
	if expectedAttemptID != "" && attempt.ID() != expectedAttemptID {
		return nil, nil, newQueueDispatchNotQueuedError(nil)
	}

	status, err := attempt.ReadStatus(ctx)
	err = normalizeQueueDispatchRetryLookupError(err)
	if err != nil {
		return nil, nil, err
	}
	if status == nil || status.Status != ir.Queued {
		return nil, nil, newQueueDispatchNotQueuedError(status)
	}

	return attempt, status, nil
}

func shouldUseQueuedDispatchAttempt(status *ir.DAGRunStatus) bool {
	return status != nil && status.TriggerType != ir.TriggerTypeRetry
}

func normalizeQueueDispatchRetryLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) {
		return newQueueDispatchNotQueuedError(nil)
	}
	return err
}

func findRetryAttempt(
	ctx context.Context,
	dagRunRepository *persis.DAGRunRepository,
	ref ir.DAGRunRef,
	rootRun ir.DAGRunRef,
) (dagrun.Attempt, error) {
	if rootRun.Zero() || rootRun.ID == ref.ID {
		return dagRunRepository.FindAttempt(ctx, ref)
	}
	return dagRunRepository.FindSubAttempt(ctx, rootRun, ref.ID)
}

func newQueueDispatchNotQueuedError(status *ir.DAGRunStatus) *queue.DAGRunNotQueuedError {
	if status == nil {
		return &queue.DAGRunNotQueuedError{}
	}
	return &queue.DAGRunNotQueuedError{Status: status.Status, HasStatus: true}
}

// enqueueRetry enqueues the retry and persists Queued status via queue.EnqueueRetry.
// Retries respect global queue capacity because the queue processor picks them up
// when capacity is available.
func enqueueRetry(ctx *Context, dag *ir.DAG, status *ir.DAGRunStatus, triggerActor string) error {
	if _, err := queue.EnqueueRetry(ctx.Context, ctx.Persistence.DAGRunRepository, ctx.Persistence.QueueStore, dag, status, queue.EnqueueRetryOptions{
		TriggerActor: &triggerActor,
	}); err != nil {
		if errors.Is(err, queue.ErrRetryStaleLatest) {
			return fmt.Errorf("dag-run state changed before retry could be queued")
		}
		return err
	}
	logger.Info(ctx, "Enqueued retry; will run when queue capacity is available",
		tag.DAG(dag.Name),
		tag.RunID(status.DAGRunID),
	)
	return nil
}

// prepareQueuedCatchupRetry repairs queued catchup records before they run
// through the retry path. The queue processor executes catchup items via
// `retry`, and executeRetry expects status.Log to already exist. Older or
// previously broken queued catchup statuses may have an empty log path, so
// this fills it in and persists the repaired status before execution.
func prepareQueuedCatchupRetry(ctx *Context, attempt dagrun.Attempt, dag *ir.DAG, status *ir.DAGRunStatus) error {
	if !queue.IsQueuedCatchup(status) || (status.Log != "" && (!dag.ArtifactsEnabled() || status.ArchiveDir != "")) {
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

func waitForRetrySourceRelease(ctx *Context, dag *ir.DAG, status *ir.DAGRunStatus) error {
	if ctx == nil {
		return nil
	}
	return waitForRetrySourceReleaseFor(
		ctx.Context,
		ctx.Persistence.ProcRepository,
		dag,
		status,
		retrySourceReleaseTimeout,
		retrySourceReleasePollInterval,
	)
}

type procHeartbeatRepository interface {
	LatestHeartbeat(context.Context, string, ir.DAGRunRef) (*proc.ProcHeartbeat, error)
}

func waitForRetrySourceReleaseFor(
	ctx context.Context,
	processes procHeartbeatRepository,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	timeout time.Duration,
	pollInterval time.Duration,
) error {
	if processes == nil || dag == nil || !retrySourceMayStillBeFinalizing(status) {
		return nil
	}

	run := status.DAGRun()
	if run.Name == "" {
		run.Name = dag.Name
	}
	if run.ID == "" {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = retrySourceReleaseTimeout
	}
	if pollInterval <= 0 {
		pollInterval = retrySourceReleasePollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		alive, err := retrySourceAlive(waitCtx, processes, dag, status, run)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("previous dag-run %s is still finalizing: %w", run, err)
			}
			return fmt.Errorf("failed to check whether previous dag-run %s is still finalizing: %w", run, err)
		}
		if !alive {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("previous dag-run %s is still finalizing: %w", run, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func retrySourceAlive(
	ctx context.Context,
	processes procHeartbeatRepository,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	run ir.DAGRunRef,
) (bool, error) {
	heartbeat, err := processes.LatestHeartbeat(ctx, dag.ProcGroup(), run)
	if err != nil {
		return false, err
	}
	if heartbeat == nil || !heartbeat.Fresh {
		return false, nil
	}
	if status.AttemptID == "" || heartbeat.AttemptID == status.AttemptID {
		return true, nil
	}
	return false, fmt.Errorf("dag-run %s already has another active attempt", run)
}

func retrySourceMayStillBeFinalizing(status *ir.DAGRunStatus) bool {
	if status == nil {
		return false
	}
	return status.Status != ir.NotStarted && !status.Status.IsActive()
}

// executeRetry runs a retry of a DAG run using the original run's log file.
// Queued catchup runs reuse this path but preserve their catchup trigger type.
func executeRetry(ctx *Context, dag *ir.DAG, status *ir.DAGRunStatus, opts runOptions) error {
	if opts.step != "" {
		ctx.Context = logger.WithValues(ctx.Context, tag.Step(opts.step))
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

	dr, err := ctx.dagRepository(dagRepositoryConfig{
		SearchPaths:           []string{filepath.Dir(dag.Location)},
		SkipDirectoryCreation: opts.workerID != "local",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	as := ctx.runtimeStores()
	triggerType := queue.PreservedQueueTriggerType(status)
	if triggerType == ir.TriggerTypeUnknown {
		triggerType = ir.TriggerTypeRetry
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
			RetryTarget:              status,
			ParentDAGRun:             status.Parent,
			ProgressDisplay:          shouldEnableProgress(ctx),
			ExtraEnvs:                extraEnvs,
			StepRetry:                opts.step,
			IncludeDownstream:        opts.includeDownstream,
			RetryPath:                opts.retryPath,
			WorkerID:                 opts.workerID,
			AttemptID:                agentAttemptID(opts.attemptID, opts.preparedAttempt),
			RunStateStore:            persis.NewRunStateStore(ctx.Persistence.DAGRunRepository, opts.preparedAttempt),
			StateStore:               ctx.Persistence.StateStore,
			MaterializationStore:     as.MaterializationStore,
			NoReuse:                  opts.noReuse,
			SecretStore:              as.SecretStore,
			ProfileStore:             as.ProfileStore,
			ProfileName:              opts.profileName,
			DAGDefinitionID:          opts.definitionID,
			ServiceRegistry:          ctx.Persistence.ServiceRegistry,
			SubWorkflowRunnerFactory: ctx.SubWorkflowRunnerFactory(),
			RootDAGRun:               opts.root,
			PeerConfig:               ctx.Config.Core.Peer,
			TriggerType:              triggerType,
			TriggerActor:             opts.triggerActor,
			DefaultExecMode:          ctx.Config.DefaultExecMode,
			ArtifactDir:              artifactDir,
			DAGRunLogDir:             ctx.Config.Paths.LogDir,
			DAGRunArtifactDir:        ctx.Config.Paths.ArtifactDir,
		},
	)

	// Use the shared agent execution function
	return ExecuteAgent(ctx, agentInstance, dag, status.DAGRunID, logFile)
}
