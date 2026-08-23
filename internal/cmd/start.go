// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/spf13/cobra"
)

// Errors for start command
var (
	// ErrDAGRunIDRequired is returned when a sub dag-run is attempted without providing a dag-run ID
	ErrDAGRunIDRequired = errors.New("dag-run ID must be provided for sub dag-runs")
)

// Start creates and returns a cobra command for starting a dag-run
func Start() *cobra.Command {
	return NewCommand(
		&cobra.Command{
			Use:   "start [flags] <DAG definition> [-- param1 param2 ...]",
			Short: "Execute a DAG from a DAG definition",
			Long: `Begin execution of a DAG-run based on the specified DAG definition.

A DAG definition is a blueprint that defines the DAG structure. This command creates a new DAG-run
instance with a unique DAG-run ID.

Parameters after the "--" separator are passed as execution parameters (either positional or key=value pairs).
Flags can override default settings such as DAG-run ID, DAG name, or suppress output.

Examples:
  dagu start my_dag -- P1=foo P2=bar
  dagu start --name my_custom_name my_dag.yaml -- P1=foo P2=bar

This command parses the DAG definition, resolves parameters, and initiates the DAG-run execution.
`,
			Args: cobra.MinimumNArgs(1),
		}, startFlags, runStart,
	)
}

// Command line flags for the start command
var startFlags = []commandLineFlag{paramsFlag, nameFlag, dagRunIDFlag, fromRunIDFlag, parentDAGRunFlag, rootDAGRunFlag, labelsFlag, tagsFlag, defaultWorkingDirFlag, profileFlag, startWorkerIDFlag, attemptIDFlag, triggerTypeFlag, triggerActorFlag, scheduleTimeFlag, sourceFileFlag, noReuseFlag}

var fromRunIDFlag = commandLineFlag{
	name:  "from-run-id",
	usage: "Historic dag-run ID to use as the template for a new run",
}

// startWorkerIDFlag identifies which worker executes this DAG run (for distributed execution tracking)
var startWorkerIDFlag = commandLineFlag{
	name:  "worker-id",
	usage: "Worker ID executing this DAG run (auto-set in distributed mode, defaults to 'local')",
}

// triggerTypeFlag identifies how this DAG run was initiated
var triggerTypeFlag = commandLineFlag{
	name:         "trigger-type",
	usage:        "How this DAG run was initiated (scheduler, manual, webhook, subdag, retry, catchup)",
	defaultValue: "manual",
}

var triggerActorFlag = commandLineFlag{
	name:   "trigger-actor",
	usage:  "Attributable actor that initiated this DAG run",
	hidden: true,
}

// scheduleTimeFlag records the cron fire time for scheduler-triggered runs
var scheduleTimeFlag = commandLineFlag{
	name:  "schedule-time",
	usage: "RFC 3339 timestamp of when this run was scheduled (set by scheduler, not for manual use)",
}

var sourceFileFlag = commandLineFlag{
	name:   "source-file",
	usage:  "Original DAG source file path for provenance-aware worker execution",
	hidden: true,
}

func runStart(ctx *Context, args []string) error {
	if ctx.IsRemote() {
		return remoteRunStart(ctx, args)
	}
	fromRunID, err := ctx.StringParam("from-run-id")
	if err != nil {
		return fmt.Errorf("failed to get from-run-id: %w", err)
	}

	workerID := getWorkerID(ctx)
	attemptID, err := requireWorkerAttemptID(ctx, workerID)
	if err != nil {
		return err
	}

	triggerType, err := parseTriggerTypeParam(ctx)
	if err != nil {
		return err
	}
	triggerActor, err := ctx.StringParam("trigger-actor")
	if err != nil {
		return fmt.Errorf("failed to get trigger actor: %w", err)
	}

	scheduleTime, err := parseScheduleTimeParam(ctx)
	if err != nil {
		return err
	}
	profileName, err := runtimeProfileNameParam(ctx)
	if err != nil {
		return err
	}

	dagRunID, rootRef, parentRef, isSubDAGRun, err := getDAGRunInfo(ctx)
	if err != nil {
		return err
	}

	if fromRunID != "" && isSubDAGRun {
		return fmt.Errorf("--from-run-id cannot be combined with --parent or --root")
	}

	var (
		dag             *ir.DAG
		params          string
		historicNoReuse bool
	)

	if fromRunID != "" {
		if len(args) == 0 {
			return fmt.Errorf("DAG name or file must be provided when using --from-run-id")
		}
		if len(args) > 1 || ctx.Command.Flags().Changed("params") || ctx.Command.ArgsLenAtDash() != -1 {
			return fmt.Errorf("parameters cannot be provided when using --from-run-id")
		}

		dagName, err := extractDAGName(ctx, args[0])
		if err != nil {
			return fmt.Errorf("failed to resolve DAG name: %w", err)
		}

		attempt, err := ctx.Persistence.DAGRunRepository.FindAttempt(ctx, ir.NewDAGRunRef(dagName, fromRunID))
		if err != nil {
			return fmt.Errorf("failed to find historic dag-run %s for DAG %s: %w", fromRunID, dagName, err)
		}

		status, err := attempt.ReadStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to read status for dag-run %s: %w", fromRunID, err)
		}
		if profileName == "" {
			profileName = status.ProfileName
		}
		historicNoReuse = status.NoReuse

		snapshot, err := attempt.ReadDAG(ctx)
		if err != nil {
			return fmt.Errorf("failed to read DAG snapshot for dag-run %s: %w", fromRunID, err)
		}

		params = status.Params
		dag, err = restoreDAGFromStatus(ctx.Context, snapshot, status)
		if err != nil {
			return fmt.Errorf("failed to restore DAG from status: %w", err)
		}

		nameOverride, err := ctx.StringParam("name")
		if err != nil {
			return fmt.Errorf("failed to read name override: %w", err)
		}
		if nameOverride != "" {
			if err := ir.ValidateDAGName(nameOverride); err != nil {
				return fmt.Errorf("invalid DAG name override: %w", err)
			}
			dag.Name = nameOverride
		}
	} else {
		if err := validateStartArgumentSeparator(ctx, args); err != nil {
			return err
		}

		// Load parameters and DAG
		dag, params, err = loadDAGWithParams(ctx, args, isSubDAGRun)
		if err != nil {
			return err
		}

		if err := validateStartPositionalParamCount(ctx, args, dag); err != nil {
			return err
		}
	}

	if err := parseAndAppendLabels(ctx, dag); err != nil {
		return err
	}

	root, err := determineRootDAGRun(isSubDAGRun, rootRef, dag, dagRunID)
	if err != nil {
		return err
	}
	noReuse, err := ctx.Command.Flags().GetBool("no-reuse")
	if err != nil {
		return fmt.Errorf("failed to read no-reuse: %w", err)
	}
	if fromRunID != "" && !ctx.Command.Flags().Changed("no-reuse") {
		noReuse = historicNoReuse
	}

	opts := runOptions{
		root:         root,
		workerID:     workerID,
		attemptID:    attemptID,
		triggerType:  triggerType,
		triggerActor: triggerActor,
		parallelItem: parallelItemFromEnv(dag.Env),
		scheduleTime: scheduleTime,
		profileName:  profileName,
		definitionID: dagDefinitionIDFromEnv(),
		noReuse:      noReuse,
	}

	ctx.Context = logger.WithValues(ctx.Context, tag.DAG(dag.Name), tag.RunID(dagRunID))

	if isSubDAGRun {
		parent, err := ir.ParseDAGRunRef(parentRef)
		if err != nil {
			return fmt.Errorf("failed to parse parent dag-run reference: %w", err)
		}
		opts.parent = parent
		return handleSubDAGRun(ctx, dag, dagRunID, params, opts)
	}

	if fromRunID != "" {
		logger.Info(ctx, "Rescheduling dag-run",
			slog.String("from-dag-run-id", fromRunID),
			slog.String("params", params),
		)
	} else {
		logger.Info(ctx, "Executing root dag-run", slog.String("params", params))
	}

	return tryExecuteDAG(ctx, dag, dagRunID, opts)
}

// tryExecuteDAG acquires a process handle and executes the DAG.
func tryExecuteDAG(ctx *Context, dag *ir.DAG, dagRunID string, opts runOptions) error {
	if dag.Type == ir.TypeBuild && opts.workerID != "local" {
		return dispatch.ErrBuildRequiresLocal
	}
	// Check for dispatch to coordinator for distributed execution.
	// Skip if already running on a worker (workerID != "local").
	if opts.workerID == "local" {
		coordinatorCli, err := ctx.NewCoordinatorClient()
		if err != nil {
			return err
		}
		if dispatch.ShouldDispatchToCoordinator(dag, coordinatorCli != nil, ctx.Config.DefaultExecMode) {
			if dag.Type == ir.TypeBuild {
				return dispatch.ErrBuildRequiresLocal
			}
			return dispatchToCoordinatorAndWait(ctx, dag, dagRunID, opts, coordinatorCli)
		}
	}

	if opts.workerID != "local" && ctx.Persistence.DAGRunRepository == nil {
		return executeDAGRun(ctx, dag, dagRunID, opts)
	}

	return withPreparedLocalExecution(
		ctx,
		dag,
		dagRunID,
		opts,
		func(execCtx context.Context) (dagrun.Attempt, error) {
			if opts.workerID != "local" {
				attempt, _, err := resolveWorkerPreparedAttempt(execCtx, ctx.Persistence.DAGRunRepository, dag.Name, dagRunID, opts.root, opts.attemptID)
				if err != nil {
					return nil, err
				}
				return attempt, nil
			}
			return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, dag, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{})
		},
		func(preparedAttempt dagrun.Attempt) error {
			run := opts
			run.preparedAttempt = preparedAttempt
			return executeDAGRun(ctx, dag, dagRunID, run)
		},
	)
}

// getDAGRunInfo extracts and validates dag-run ID and references from command flags.
// nolint:revive
func getDAGRunInfo(ctx *Context) (dagRunID, rootDAGRun, parentDAGRun string, isSubDAGRun bool, err error) {
	dagRunID, err = ctx.StringParam("run-id")
	if err != nil {
		return "", "", "", false, fmt.Errorf("failed to get dag-run ID: %w", err)
	}

	rootDAGRun, _ = ctx.Command.Flags().GetString("root")
	parentDAGRun, _ = ctx.Command.Flags().GetString("parent")
	isSubDAGRun = parentDAGRun != ""

	if isSubDAGRun && dagRunID == "" {
		return "", "", "", false, ErrDAGRunIDRequired
	}

	if dagRunID != "" {
		if err := validateRunID(dagRunID); err != nil {
			return "", "", "", false, err
		}
	} else {
		dagRunID, err = genRunID()
		if err != nil {
			return "", "", "", false, fmt.Errorf("failed to generate dag-run ID: %w", err)
		}
	}

	return dagRunID, rootDAGRun, parentDAGRun, isSubDAGRun, nil
}

// loadDAGWithParams loads the DAG and its parameters from command arguments.
func loadDAGWithParams(ctx *Context, args []string, isSubDAGRun bool) (*ir.DAG, string, error) {
	dagPath := args[0]

	loadOpts := []spec.LoadOption{
		spec.WithBaseConfig(ctx.Config.Paths.BaseConfig),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
		spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
	}
	if definitionID := dagDefinitionIDFromEnv(); definitionID != "" {
		loadOpts = append(loadOpts, spec.WithDefaultName(
			fileutil.TrimYAMLFileExtension(filepath.Base(definitionID)),
		))
	}

	if isSubDAGRun {
		loadOpts = append(loadOpts, spec.WithSkipBaseHandlers())
	}

	nameOverride, err := ctx.StringParam("name")
	if err != nil {
		return nil, "", fmt.Errorf("failed to get name override: %w", err)
	}
	if nameOverride != "" {
		loadOpts = append(loadOpts, spec.WithName(nameOverride))
	}

	defaultWorkingDir, err := ctx.StringParam("default-working-dir")
	if err != nil {
		return nil, "", fmt.Errorf("failed to get default-working-dir: %w", err)
	}
	if defaultWorkingDir != "" {
		loadOpts = append(loadOpts, spec.WithDefaultWorkingDir(defaultWorkingDir))
	}
	presolvedBuildEnv, err := buildenv.Load()
	if err != nil {
		return nil, "", fmt.Errorf("failed to load presolved build env: %w", err)
	}
	if len(presolvedBuildEnv.Env) > 0 || presolvedBuildEnv.RuntimeResolved {
		loadOpts = append(loadOpts, spec.WithBuildEnvSnapshot(presolvedBuildEnv))
	}

	var params string

	if ctx.Command.ArgsLenAtDash() != -1 && len(args) > 0 {
		dashArgs := args[ctx.Command.ArgsLenAtDash():]
		loadOpts = append(loadOpts, spec.WithParams(quoteStartDashArgs(dashArgs)))
		params = strings.Join(dashArgs, " ")
	} else {
		params, err = ctx.Command.Flags().GetString("params")
		if err != nil {
			return nil, "", fmt.Errorf("failed to get parameters: %w", err)
		}
		loadOpts = append(loadOpts, spec.WithParams(stringutil.RemoveQuotes(params)))
	}

	dag, err := spec.Load(ctx, dagPath, loadOpts...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load DAG from %s: %w", dagPath, err)
	}
	if ctx.Command.Flags().Changed(sourceFileFlag.name) {
		sourceFile, err := ctx.StringParam(sourceFileFlag.name)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get source file override: %w", err)
		}
		dag.SourceFile = sourceFile
	}

	return dag, params, nil
}

// parseAndAppendLabels parses the --labels flag and appends validated labels to the DAG.
func parseAndAppendLabels(ctx *Context, dag *ir.DAG) error {
	labelsStr, err := labelsParam(ctx)
	if err != nil {
		return err
	}
	if labelsStr != "" {
		extraLabels := ir.NewLabels(strings.Split(labelsStr, ","))
		if err := ir.ValidateLabels(extraLabels); err != nil {
			return fmt.Errorf("invalid labels: %w", err)
		}
		dag.Labels = append(dag.Labels, extraLabels...)
	}
	return nil
}

// determineRootDAGRun creates or parses the root execution reference.
func determineRootDAGRun(isSubDAGRun bool, rootDAGRun string, dag *ir.DAG, dagRunID string) (ir.DAGRunRef, error) {
	if isSubDAGRun {
		ref, err := ir.ParseDAGRunRef(rootDAGRun)
		if err != nil {
			return ir.DAGRunRef{}, fmt.Errorf("failed to parse root exec ref: %w", err)
		}
		return ref, nil
	}
	return ir.NewDAGRunRef(dag.Name, dagRunID), nil
}

// handleSubDAGRun processes a sub dag-run, checking for previous runs.
func handleSubDAGRun(ctx *Context, dag *ir.DAG, dagRunID string, params string, opts runOptions) error {
	logger.Info(ctx, "Executing sub dag-run",
		slog.String("params", params),
		slog.Any("root", opts.root),
		slog.Any("parent", opts.parent),
		slog.String("workerID", opts.workerID),
	)

	if dagRunID == "" {
		return fmt.Errorf("dag-run ID must be provided for sub DAGrun")
	}

	// For distributed execution, the coordinator already created the sub-attempt record.
	if opts.workerID != "local" {
		if ctx.Persistence.DAGRunRepository == nil {
			return executeDAGRun(ctx, dag, dagRunID, opts)
		}
		return withPreparedLocalExecution(
			ctx,
			dag,
			dagRunID,
			opts,
			func(execCtx context.Context) (dagrun.Attempt, error) {
				attempt, _, err := resolveWorkerPreparedAttempt(execCtx, ctx.Persistence.DAGRunRepository, dag.Name, dagRunID, opts.root, opts.attemptID)
				if err != nil {
					return nil, err
				}
				return attempt, nil
			},
			func(preparedAttempt dagrun.Attempt) error {
				run := opts
				run.preparedAttempt = preparedAttempt
				return executeDAGRun(ctx, dag, dagRunID, run)
			},
		)
	}

	logger.Debug(ctx, "Checking for previous sub dag-run with the dag-run ID")

	subAttempt, err := ctx.Persistence.DAGRunRepository.FindSubAttempt(ctx, opts.root, dagRunID)
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return withPreparedLocalExecution(
			ctx,
			dag,
			dagRunID,
			opts,
			func(execCtx context.Context) (dagrun.Attempt, error) {
				return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, dag, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{
					RootDAGRun: opts.root,
				})
			},
			func(preparedAttempt dagrun.Attempt) error {
				run := opts
				run.preparedAttempt = preparedAttempt
				return executeDAGRun(ctx, dag, dagRunID, run)
			},
		)
	}
	if err != nil {
		return fmt.Errorf("failed to find the record for dag-run ID %s: %w", dagRunID, err)
	}

	status, err := subAttempt.ReadStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to read previous run status for dag-run ID %s: %w", dagRunID, err)
	}

	retry := opts
	retry.parent = status.Parent
	retry.triggerType = queue.PreservedQueueTriggerType(status)
	retry.triggerActor = status.TriggerActor
	retry.parallelItem = status.ParallelItem
	retry.scheduleTime = status.ScheduleTime

	return withPreparedLocalExecution(
		ctx,
		dag,
		dagRunID,
		retry,
		func(execCtx context.Context) (dagrun.Attempt, error) {
			if status.Status == ir.Queued {
				subAttempt.SetDAG(dag)
				return subAttempt, nil
			}
			return ctx.Persistence.DAGRunRepository.CreateAttempt(execCtx, dag, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{
				Retry:      true,
				RootDAGRun: opts.root,
			})
		},
		func(preparedAttempt dagrun.Attempt) error {
			prepared := retry
			prepared.preparedAttempt = preparedAttempt
			return executeRetry(ctx, dag, status, prepared)
		},
	)
}

// executeDAGRun initializes execution state for a DAG run and invokes the shared agent executor.
func executeDAGRun(ctx *Context, d *ir.DAG, dagRunID string, opts runOptions) error {
	logFile, err := ctx.OpenLogFile(d, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize log file for DAG %s: %w", d.Name, err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	logger.Debug(ctx, "Dag-run initiated", tag.File(logFile.Name()))

	artifactDir, err := ctx.GenArtifactDir(d, dagRunID)
	if err != nil {
		return fmt.Errorf("failed to initialize artifact directory for DAG %s: %w", d.Name, err)
	}

	dr, err := ctx.dagRepository(dagRepositoryConfig{
		SearchPaths:           []string{filepath.Dir(d.Location)},
		SkipDirectoryCreation: opts.workerID != "local",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize DAG store: %w", err)
	}

	// When running on a worker, the dag-run was already created by the coordinator.
	queuedRun := opts.workerID != "local"

	as := ctx.runtimeStores()
	extraEnvs, err := prepareDAGTools(ctx, d)
	if err != nil {
		return err
	}

	agentInstance := agent.New(
		dagRunID,
		d,
		filepath.Dir(logFile.Name()),
		logFile.Name(),
		ctx.DAGRunMgr,
		dr,
		agent.Options{
			ParentDAGRun:             opts.parent,
			ProgressDisplay:          shouldEnableProgress(ctx),
			ExtraEnvs:                extraEnvs,
			WorkerID:                 opts.workerID,
			AttemptID:                agentAttemptID(opts.attemptID, opts.preparedAttempt),
			QueuedRun:                queuedRun,
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
			TriggerType:              opts.triggerType,
			TriggerActor:             opts.triggerActor,
			ParallelItem:             opts.parallelItem,
			DefaultExecMode:          ctx.Config.DefaultExecMode,
			ScheduleTime:             opts.scheduleTime,
			ArtifactDir:              artifactDir,
			DAGRunLogDir:             ctx.Config.Paths.LogDir,
			DAGRunArtifactDir:        ctx.Config.Paths.ArtifactDir,
		},
	)

	return ExecuteAgent(ctx, agentInstance, d, dagRunID, logFile)
}

// dispatchToCoordinatorAndWait dispatches a DAG to coordinator and waits for completion.
func dispatchToCoordinatorAndWait(ctx *Context, d *ir.DAG, dagRunID string, opts runOptions, coordinatorCli coordinator.Client) error {
	signalCtx, stop := signal.NotifyContext(ctx.Context, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	signalAwareCtx := ctx.WithContext(signalCtx)

	showProgress := shouldEnableProgress(ctx)
	var progress *RemoteProgressDisplay
	if showProgress {
		progress = NewRemoteProgressDisplay(d, dagRunID)
		progress.Start()
	}

	defer func() {
		if progress != nil {
			progress.Stop()
			if !ctx.Quiet {
				progress.PrintSummary()
			}
		}
	}()

	logger.Info(ctx, "Dispatching DAG for distributed execution",
		slog.Any("worker-selector", d.WorkerSelector),
	)

	var taskOpts []executor.TaskOption
	if len(d.WorkerSelector) > 0 {
		taskOpts = append(taskOpts, executor.WithWorkerSelector(d.WorkerSelector))
	}
	if len(d.Labels) > 0 {
		taskOpts = append(taskOpts, executor.WithLabels(strings.Join(d.Labels.Strings(), ",")))
	}
	if d.SourceFile != "" {
		taskOpts = append(taskOpts, executor.WithSourceFile(d.SourceFile))
	}
	if opts.scheduleTime != "" {
		taskOpts = append(taskOpts, executor.WithScheduleTime(opts.scheduleTime))
	}
	if opts.profileName != "" {
		taskOpts = append(taskOpts, executor.WithProfileName(opts.profileName))
	}
	if opts.triggerActor != "" {
		taskOpts = append(taskOpts, executor.WithTriggerActor(opts.triggerActor))
	}

	task := executor.CreateTask(
		d.Name,
		string(d.YamlData),
		dispatch.DispatchOperationStart,
		dagRunID,
		taskOpts...,
	)

	if err := coordinatorCli.Dispatch(signalAwareCtx, dispatch.DispatchRequest{Task: task}); err != nil {
		return fmt.Errorf("failed to dispatch task: %w", err)
	}

	logger.Info(ctx, "DAG dispatched to coordinator; awaiting completion")
	err := waitForDAGCompletionWithProgress(signalAwareCtx, d, dagRunID, coordinatorCli, progress)

	// If context was cancelled (e.g., Ctrl+C), request cancellation on coordinator
	if signalCtx.Err() != nil {
		return handleDistributedCancellation(ctx, d, dagRunID, coordinatorCli, progress, err)
	}

	return err
}

// handleDistributedCancellation handles the cancellation of a distributed DAG run when a signal is received.
// It requests cancellation from the coordinator and polls for status updates until the DAG is no longer active.
func handleDistributedCancellation(ctx context.Context, dag *ir.DAG, dagRunID string, coordinatorCli coordinator.Client, progress *RemoteProgressDisplay, originalErr error) error {
	logger.Info(ctx, "Requesting cancellation of distributed DAG run", tag.RunID(dagRunID))
	cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if cancelErr := coordinatorCli.RequestCancel(cancelCtx, dag.Name, dagRunID, nil); cancelErr != nil {
		logger.Warn(ctx, "Failed to request cancellation", tag.Error(cancelErr))
	}

	if progress == nil {
		return originalErr
	}

	// Poll for up to 5 seconds until status reflects cancellation
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-cancelCtx.Done():
			progress.SetCancelled()
			return originalErr
		case <-ticker.C:
			resp, fetchErr := coordinatorCli.GetDAGRunStatus(cancelCtx, dag.Name, dagRunID, nil)
			if fetchErr != nil || resp == nil || resp.Status == nil {
				continue
			}
			progress.Update(resp.Status)
			if !resp.Status.Status.IsActive() {
				return originalErr
			}
		}
	}
}

// waitForDAGCompletionWithProgress polls the coordinator until the DAG run completes.
// Progress display is managed by the caller.
func waitForDAGCompletionWithProgress(ctx *Context, d *ir.DAG, dagRunID string, coordinatorCli coordinator.Client, progress *RemoteProgressDisplay) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	logTicker := time.NewTicker(15 * time.Second)
	defer logTicker.Stop()

	var consecutiveErrors int
	const maxConsecutiveErrors = 10 // Fail after 10 consecutive errors (10 seconds)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-logTicker.C:
			if progress == nil {
				// Only log if not showing progress (progress display shows its own updates)
				logger.Info(ctx, "Waiting for DAG completion...", tag.RunID(dagRunID))
			}

		case <-ticker.C:
			resp, err := coordinatorCli.GetDAGRunStatus(ctx, d.Name, dagRunID, nil)
			if err != nil {
				consecutiveErrors++
				logger.Debug(ctx, "Failed to get status from coordinator",
					tag.Error(err), slog.Int("consecutive_errors", consecutiveErrors))

				if consecutiveErrors >= maxConsecutiveErrors {
					return fmt.Errorf("lost connection to coordinator after %d attempts: %w", consecutiveErrors, err)
				}
				continue
			}
			consecutiveErrors = 0 // Reset on success

			if resp == nil || resp.Status == nil {
				continue
			}

			// Update progress display with current status
			if progress != nil {
				progress.Update(resp.Status)
			}

			dagStatus := resp.Status
			if !dagStatus.Status.IsActive() {
				if dagStatus.Status.IsSuccess() {
					logger.Info(ctx, "DAG completed successfully", tag.RunID(dagRunID))
					return nil
				}
				if dagStatus.Error != "" {
					return fmt.Errorf("DAG run failed with status %s: %s", dagStatus.Status, dagStatus.Error)
				}
				return fmt.Errorf("DAG run failed with status: %s", dagStatus.Status)
			}
		}
	}
}
