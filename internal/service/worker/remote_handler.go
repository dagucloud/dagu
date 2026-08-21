// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	rtagent "github.com/dagucloud/dagu/v2/internal/runtime/agent"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/secret/providers"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/worker/coordreport"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
	daguaqua "github.com/dagucloud/dagu/v2/internal/tools/aqua"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

var _ TaskHandler = (*remoteTaskHandler)(nil)

// RemoteTaskHandlerConfig contains configuration for the remote task handler
type RemoteTaskHandlerConfig struct {
	// WorkerID is the identifier of this worker
	WorkerID string
	// CoordinatorClient is the coordinator client with load balancing support
	CoordinatorClient coordinator.Client
	// DAGRepository provides access to DAG definitions.
	DAGRepository *persis.DAGRepository
	// DAGRunMgr is the manager for DAG runs
	DAGRunMgr runtime.Manager
	// StateStore is the persistent state store shared across DAG runs.
	StateStore dagrun.StateStore
	// ServiceRegistry is the service registry
	ServiceRegistry serviceregistry.ServiceRegistry
	// PeerConfig is the peer configuration
	PeerConfig config.Peer
	// Config is the main application configuration
	Config *config.Config
	// SecretStore resolves Dagu-managed secrets during execution.
	SecretStore secret.Store
	// ProfileStore resolves profile values during execution.
	ProfileStore profile.Store
}

type runtimeStores struct {
	SecretStore  secret.Store
	ProfileStore profile.Store
}

// NewRemoteTaskHandler creates a new TaskHandler that runs tasks in-process
// with status pushing and log streaming to the coordinator.
func NewRemoteTaskHandler(cfg RemoteTaskHandlerConfig) TaskHandler {
	if cfg.Config == nil {
		cfg.Config = &config.Config{}
	}
	stateStore := cfg.StateStore
	if stateStore == nil {
		if stateClient, ok := cfg.CoordinatorClient.(coordinator.StateClient); ok {
			stateStore = coordinator.NewStateStoreClient(stateClient)
		}
	}
	return &remoteTaskHandler{
		workerID:          cfg.WorkerID,
		coordinatorClient: cfg.CoordinatorClient,
		dagRepository:     cfg.DAGRepository,
		dagRunMgr:         cfg.DAGRunMgr,
		stateStore:        stateStore,
		serviceRegistry:   cfg.ServiceRegistry,
		peerConfig:        cfg.PeerConfig,
		config:            cfg.Config,
		runtimeStores: runtimeStores{
			SecretStore:  cfg.SecretStore,
			ProfileStore: cfg.ProfileStore,
		},
	}
}

type remoteTaskHandler struct {
	workerID          string
	coordinatorClient coordinator.Client
	dagRepository     *persis.DAGRepository
	dagRunMgr         runtime.Manager
	stateStore        dagrun.StateStore
	serviceRegistry   serviceregistry.ServiceRegistry
	peerConfig        config.Peer
	config            *config.Config
	runtimeStores     runtimeStores
}

// Handle executes a task in-process with remote status/log streaming
func (h *remoteTaskHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error {
	logger.Info(ctx, "Executing remote task",
		slog.String("operation", task.Operation.String()),
		tag.Target(task.Target),
		tag.RunID(task.DagRunId),
		slog.String("root-dag-run-id", task.RootDagRunId),
		slog.String("parent-dag-run-id", task.ParentDagRunId))

	switch task.Operation {
	case coordinatorv1.Operation_OPERATION_START:
		return h.handleStart(ctx, task, false)

	case coordinatorv1.Operation_OPERATION_RETRY:
		return h.handleRetry(ctx, task)

	case coordinatorv1.Operation_OPERATION_UNSPECIFIED:
		return fmt.Errorf("unsupported operation: unspecified")

	default:
		return fmt.Errorf("unsupported operation: %v", task.Operation)
	}
}

func (h *remoteTaskHandler) handleStart(ctx context.Context, task *coordinatorv1.Task, queuedRun bool) error {
	root := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	parent := ir.DAGRunRef{Name: task.ParentDagRunName, ID: task.ParentDagRunId}
	owner, err := taskOwner(task)
	if err != nil {
		return fmt.Errorf("invalid task owner coordinator metadata: %w", err)
	}
	run := remoteRun{
		task:        task,
		root:        root,
		parent:      parent,
		owner:       owner,
		queued:      queuedRun,
		profileName: task.ProfileName,
	}

	loaded, err := h.loadDAG(ctx, task)
	if err != nil {
		h.reportTaskLoadFailure(ctx, run, err)
		return fmt.Errorf("failed to load DAG: %w", err)
	}
	if loaded.cleanup != nil {
		defer loaded.cleanup()
	}
	dag := loaded.dag
	run.workspaceSeed = loaded.workspaceSeed

	run.handlers = h.createRemoteHandlers(run, dag.Name)
	err = h.executeDAGRun(ctx, dag, run)
	var initErr *taskInitError
	if errors.As(err, &initErr) && !initErr.reported {
		h.reportTaskInitFailure(ctx, run, initErr.err)
	}
	return err
}

func (h *remoteTaskHandler) handleRetry(ctx context.Context, task *coordinatorv1.Task) error {
	root := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	parent := ir.DAGRunRef{Name: task.ParentDagRunName, ID: task.ParentDagRunId}
	owner, err := taskOwner(task)
	if err != nil {
		return fmt.Errorf("invalid task owner coordinator metadata: %w", err)
	}

	if task.PreviousStatus == nil {
		return fmt.Errorf("retry requires previous_status in task")
	}

	status, convErr := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if convErr != nil {
		return fmt.Errorf("failed to convert previous status: %w", convErr)
	}
	retryPath, err := dagrun.ParseRetryPath(task.RetryPath)
	if err != nil {
		return fmt.Errorf("invalid retry path: %w", err)
	}
	profileName := retryTaskProfileName(status)
	run := remoteRun{
		task:        task,
		root:        root,
		parent:      parent,
		owner:       owner,
		profileName: profileName,
		retry: &retryConfig{
			target:            status,
			stepName:          task.Step,
			includeDownstream: task.IncludeDownstream,
			triggerType:       queue.PreservedQueueTriggerType(status),
			retryPath:         retryPath,
		},
	}
	logger.Info(ctx, "Using previous status from task for retry",
		tag.RunID(task.DagRunId),
		slog.Int("nodes", len(status.Nodes)))

	loaded, err := h.loadDAG(ctx, task)
	if err != nil {
		h.reportTaskLoadFailure(ctx, run, err)
		return fmt.Errorf("failed to load DAG: %w", err)
	}
	if loaded.cleanup != nil {
		defer loaded.cleanup()
	}
	dag := loaded.dag
	run.workspaceSeed = loaded.workspaceSeed

	run.handlers = h.createRemoteHandlers(run, dag.Name)
	err = h.executeDAGRun(ctx, dag, run)
	var initErr *taskInitError
	if errors.As(err, &initErr) && !initErr.reported {
		h.reportTaskInitFailure(ctx, run, initErr.err)
	}
	return err
}

func retryTaskProfileName(status *ir.DAGRunStatus) string {
	if status == nil {
		return ""
	}
	return status.ProfileName
}

func (h *remoteTaskHandler) reportTaskLoadFailure(ctx context.Context, run remoteRun, loadErr error) {
	task := run.task
	statusPusher := coordreport.NewTaskStatusPusher(h.coordinatorClient, h.workerID, task, run.owner)
	finishedAt := stringutil.FormatTime(time.Now())
	logger.Warn(ctx, "Failed to load DAG on worker",
		tag.Target(task.Target),
		tag.RunID(task.DagRunId),
		tag.Error(loadErr),
	)
	status := ir.DAGRunStatus{
		Root:         run.root,
		Parent:       run.parent,
		Name:         task.Target,
		DAGRunID:     task.DagRunId,
		AttemptID:    task.AttemptId,
		Status:       ir.Failed,
		FinishedAt:   finishedAt,
		Error:        sanitizeTaskLoadError(task.Target, loadErr),
		Params:       task.Params,
		ParallelItem: task.ParallelItem,
		ProfileName:  run.profileName,
		DefinitionID: task.DefinitionId,
		TriggerActor: task.TriggerActor,
	}

	if err := statusPusher.Push(ctx, status); err != nil {
		logger.Warn(ctx, "Failed to report load failure status",
			tag.Target(task.Target),
			tag.RunID(task.DagRunId),
			tag.Error(err),
		)
	}
}

func (h *remoteTaskHandler) reportTaskInitFailure(
	ctx context.Context,
	run remoteRun,
	initErr error,
) {
	if run.handlers.status == nil || initErr == nil {
		return
	}

	h.reportDAGRunInitFailure(ctx, run.task.Target, run.task.Params, run, initErr)
}

func (h *remoteTaskHandler) reportDAGRunInitFailure(
	ctx context.Context,
	target string,
	params string,
	run remoteRun,
	initErr error,
) {
	task := run.task
	statusPusher := run.handlers.status
	if statusPusher == nil || initErr == nil {
		return
	}

	finishedAt := stringutil.FormatTime(time.Now())
	logger.Warn(ctx, "Failed to initialize DAG on worker",
		tag.Target(target),
		tag.RunID(task.DagRunId),
		tag.Error(initErr),
	)
	status := ir.DAGRunStatus{
		Root:         run.root,
		Parent:       run.parent,
		Name:         target,
		DAGRunID:     task.DagRunId,
		AttemptID:    task.AttemptId,
		Status:       ir.Failed,
		FinishedAt:   finishedAt,
		Error:        initErr.Error(),
		Params:       params,
		ProfileName:  run.profileName,
		DefinitionID: task.DefinitionId,
		TriggerActor: task.TriggerActor,
	}

	if err := statusPusher.Push(ctx, status); err != nil {
		logger.Warn(ctx, "Failed to report init failure status",
			tag.Target(target),
			tag.RunID(task.DagRunId),
			tag.Error(err),
		)
	}
}

func sanitizeTaskLoadError(target string, loadErr error) string {
	message := loadErr.Error()
	rest, ok := strings.CutPrefix(message, "failed to load DAG from ")
	if !ok {
		return message
	}

	if _, reason, ok := strings.Cut(rest, ": "); ok {
		return fmt.Sprintf("failed to load DAG %q: %s", target, reason)
	}

	return fmt.Sprintf("failed to load DAG %q", target)
}

// retryConfig holds retry-specific configuration
type retryConfig struct {
	target            *ir.DAGRunStatus
	stepName          string
	includeDownstream bool
	triggerType       ir.TriggerType
	retryPath         dagrun.RetryPath
}

type runHandlers struct {
	status    runtime.StatusPusher
	logs      runtime.SchedulerLogStreamer
	artifacts runtime.ArtifactFinalizer
}

type remoteRun struct {
	task          *coordinatorv1.Task
	root          ir.DAGRunRef
	parent        ir.DAGRunRef
	owner         serviceregistry.HostInfo
	handlers      runHandlers
	queued        bool
	retry         *retryConfig
	profileName   string
	workspaceSeed *runtimeexec.WorkspaceSeed
}

type taskInitError struct {
	err      error
	reported bool
}

func (e *taskInitError) Error() string {
	return e.err.Error()
}

func (e *taskInitError) Unwrap() error {
	return e.err
}

func newTaskInitError(err error) error {
	if err == nil {
		return nil
	}
	return &taskInitError{err: err}
}

func newReportedTaskInitError(err error) error {
	if err == nil {
		return nil
	}
	return &taskInitError{err: err, reported: true}
}

func taskExtraEnvs(task *coordinatorv1.Task) []string {
	if task == nil {
		return nil
	}
	var envs []string
	if task.ExternalStepRetry {
		envs = append(envs, runenv.EnvKeyExternalStepRetry+"=1")
	}
	if task.ParallelItem != "" {
		envs = append(envs, ir.ParallelItemVariable+"="+task.ParallelItem)
	}
	return envs
}

// createRemoteHandlers creates the remote status, log, and artifact transport handlers.
func (h *remoteTaskHandler) createRemoteHandlers(run remoteRun, dagName string) runHandlers {
	task := run.task
	statusPusher := coordreport.NewTaskStatusPusher(h.coordinatorClient, h.workerID, task, run.owner)
	reporter := newRemoteRunReporter(
		h.coordinatorClient,
		h.workerID,
		remoteRunMetadata{
			dagRunID:  task.DagRunId,
			dagName:   dagName,
			attemptID: task.AttemptId,
			claimKey:  task.AttemptKey,
			root:      run.root,
		},
		run.owner,
	)
	return runHandlers{
		status:    statusPusher,
		logs:      reporter,
		artifacts: reporter,
	}
}

type loadedTaskDAG struct {
	dag           *ir.DAG
	workspaceSeed *runtimeexec.WorkspaceSeed
	cleanup       func()
}

// loadDAG loads the DAG from task definition.
func (h *remoteTaskHandler) loadDAG(ctx context.Context, task *coordinatorv1.Task) (*loadedTaskDAG, error) {
	if _, ok, err := taskWorkspaceDescriptor(task); err != nil {
		return nil, err
	} else if ok {
		return h.loadWorkspaceDAG(ctx, task)
	}

	logger.Info(ctx, "Creating temporary DAG file from definition",
		tag.DAG(task.Target),
		tag.Size(len(task.Definition)))

	tempFile, err := fileutil.CreateTempDAGFile("worker-dags", task.Target, []byte(task.Definition))
	if err != nil {
		return nil, fmt.Errorf("failed to create temp DAG file: %w", err)
	}
	cleanupFunc := func() {
		if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
			logger.Errorf(ctx, "Failed to remove temp DAG file: %v", err)
		}
	}

	// Remote tasks load the DAG definition received from the coordinator.
	// Local DAG directories are outside the task payload boundary.
	loadOpts := []spec.LoadOption{
		spec.WithName(task.Target), // Use original DAG name, not temp file path
	}
	if task.SourceFile != "" {
		loadOpts = append(loadOpts, spec.WithSourceFile(task.SourceFile))
	}

	// Use embedded base config from the task if available (distributed mode).
	// Fall back to local base config path if the task doesn't include one.
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	} else {
		loadOpts = append(loadOpts, spec.WithBaseConfig(h.config.Paths.BaseConfig))
	}

	// Pass task params to the DAG (e.g., from parallel execution items)
	if task.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(task.Params))
	} else if params, err := previousStatusParams(task); err != nil {
		cleanupFunc()
		return nil, err
	} else if len(params) > 0 {
		loadOpts = append(loadOpts, spec.WithParams(spec.QuoteRuntimeParams(params, nil)))
	}

	dag, err := spec.Load(ctx, tempFile, loadOpts...)
	if err != nil {
		cleanupFunc()
		return nil, fmt.Errorf("failed to load DAG from %s: %w", tempFile, err)
	}
	dag.SourceFile = task.SourceFile

	return &loadedTaskDAG{dag: dag, cleanup: cleanupFunc}, nil
}

func (h *remoteTaskHandler) loadWorkspaceDAG(ctx context.Context, task *coordinatorv1.Task) (*loadedTaskDAG, error) {
	client, ok := h.coordinatorClient.(workspacebundle.Client)
	if !ok {
		return nil, fmt.Errorf("coordinator client does not support workspace bundles")
	}

	workDir := remoteWorkspaceWorkDir(task)
	workspace, err := materializeTaskWorkspace(ctx, task, client, taskWorkspaceDir(workDir))
	if err != nil {
		return nil, err
	}
	cleanupFunc := func() {
		if err := os.RemoveAll(workDir); err != nil {
			logger.Warn(ctx, "Failed to remove task workspace",
				slog.String("path", workDir),
				tag.Error(err))
		}
	}

	loadOpts := []spec.LoadOption{spec.WithDefaultWorkingDir(workspace.dir)}
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	}
	if task.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(task.Params))
	} else if params, err := previousStatusParams(task); err != nil {
		cleanupFunc()
		return nil, err
	} else if len(params) > 0 {
		loadOpts = append(loadOpts, spec.WithParams(spec.QuoteRuntimeParams(params, nil)))
	}

	dag, err := spec.Load(ctx, workspace.dagFile, loadOpts...)
	if err != nil {
		cleanupFunc()
		return nil, fmt.Errorf("failed to load DAG from workspace: %w", err)
	}
	if localDAG, ok := dag.LocalDAGs[task.Target]; ok {
		dag = localDAG.Clone()
	} else {
		dag.Name = task.Target
	}
	dag.SourceFile = task.SourceFile

	logger.Info(ctx, "Materialized task workspace",
		tag.Target(task.Target),
		tag.File(workspace.dagFile),
		slog.String("workspace", workspace.dir),
		slog.String("digest", workspace.desc.Digest))

	return &loadedTaskDAG{
		dag: dag,
		workspaceSeed: &runtimeexec.WorkspaceSeed{
			Descriptor: workspace.desc,
			Archive:    workspace.archive,
		},
		cleanup: cleanupFunc,
	}, nil
}

// agentEnv holds temporary directories and cleanup function for agent execution.
type agentEnv struct {
	logDir      string
	logFile     string
	artifactDir string
	cleanup     func()
}

// createAgentEnv creates temporary directories for agent execution.
// The cleanup function must be called after execution completes.
// Includes workerID in path to prevent collisions with concurrent workers on the same host.
func (h *remoteTaskHandler) createAgentEnv(ctx context.Context, dag *ir.DAG, dagRunID string) (*agentEnv, error) {
	logDir := filepath.Join(os.TempDir(), "dagu", "worker-logs", h.workerID, dagRunID)
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	artifactDir := ""
	if dag != nil && dag.ArtifactsEnabled() {
		var err error
		artifactDir, err = logpath.GenerateDir(
			ctx,
			filepath.Join(os.TempDir(), "dagu", "worker-artifacts", h.workerID),
			"",
			dag.Name,
			dagRunID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create artifact directory: %w", err)
		}
	}

	return &agentEnv{
		logDir:      logDir,
		logFile:     filepath.Join(logDir, "scheduler.log"),
		artifactDir: artifactDir,
		cleanup: func() {
			if err := os.RemoveAll(logDir); err != nil {
				logger.Warn(ctx, "Failed to cleanup temp log directory",
					slog.String("path", logDir),
					tag.Error(err))
			}
			if artifactDir != "" {
				if err := os.RemoveAll(artifactDir); err != nil {
					logger.Warn(ctx, "Failed to cleanup temp artifact directory",
						slog.String("path", artifactDir),
						tag.Error(err))
				}
			}
		},
	}, nil
}

func (h *remoteTaskHandler) executeDAGRun(
	ctx context.Context,
	dag *ir.DAG,
	run remoteRun,
) error {
	if dag != nil && dag.Type == ir.TypeBuild {
		return newTaskInitError(dispatch.ErrBuildRequiresLocal)
	}
	task := run.task
	dagRunID := task.DagRunId
	attemptID := task.AttemptId
	statusPusher := run.handlers.status
	logStreamer := run.handlers.logs
	artifactUploader := run.handlers.artifacts

	// Create temporary directory for local operations
	env, err := h.createAgentEnv(ctx, dag, dagRunID)
	if err != nil {
		return newTaskInitError(err)
	}
	defer env.cleanup()

	// Open scheduler log file for writing
	logFile, err := os.OpenFile(env.logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return newTaskInitError(fmt.Errorf("failed to create scheduler log file: %w", err))
	}
	defer func() {
		if closeErr := logFile.Close(); closeErr != nil {
			logger.Warn(ctx, "Failed to close scheduler log file", tag.Error(closeErr))
		}
	}()

	var schedulerFinalizer schedulerLogStatusFinalizer
	if reporter, ok := logStreamer.(*remoteRunReporter); ok {
		if reporter.EnableSchedulerFinalizer(env.logFile) != nil {
			schedulerFinalizer = reporter
			if statusPusher != nil {
				statusPusher = &finalSchedulerLogStatusPusher{
					pusher:    statusPusher,
					finalizer: reporter,
				}
			}
		}
	}

	// Create a scheduler log writer. Remote reporters close it before terminal
	// status so buffered scheduler chunks and the final marker reach coordinator.
	var logWriter io.Writer = logFile
	if logStreamer != nil {
		streamingWriter := logStreamer.NewSchedulerLogWriter(ctx, logFile)
		defer func() {
			closeCtx, cancel := schedulerLogCloseContext(ctx, remoteSchedulerLogFinalizeTimeout)
			defer cancel()
			if closeErr := closeSchedulerLogWriter(closeCtx, streamingWriter); closeErr != nil {
				logger.Warn(ctx, "Failed to close scheduler log streamer", tag.Error(closeErr))
			}
		}()
		logWriter = streamingWriter
	}

	// Configure logger to use the streaming writer
	ctx = logger.WithLogger(ctx, logger.NewLogger(logger.WithWriter(logWriter)))

	runtimeStores := h.runtimeStores

	toolEnvs, err := h.prepareDAGTools(ctx, dag)
	if err != nil {
		if schedulerFinalizer != nil && statusPusher != nil {
			target := dagRunID
			params := ""
			if dag != nil {
				target = dag.Name
				params = strings.Join(dag.Params, " ")
			}
			failedRun := run
			failedRun.handlers.status = statusPusher
			h.reportDAGRunInitFailure(ctx, target, params, failedRun, err)
			return newReportedTaskInitError(err)
		}
		return newTaskInitError(err)
	}
	extraEnvs := append(taskExtraEnvs(task), toolEnvs...)

	// Create a remote DAG loader that fetches DAG definitions from the coordinator
	// as a fallback when the local DAG repository misses.
	remoteDAGLoader := rtagent.RemoteDAGLoader(func(ctx context.Context, name string) (*ir.DAG, error) {
		dagYAML, err := h.coordinatorClient.GetDAG(ctx, name)
		if err != nil {
			return nil, err
		}
		if dagYAML == "" {
			return nil, nil
		}
		dag, loadErr := spec.LoadYAML(ctx, []byte(dagYAML), spec.WithName(name))
		if loadErr != nil {
			return nil, fmt.Errorf("failed to parse DAG from remote: %w", loadErr)
		}
		return dag, nil
	})

	subWorkflowRunnerFactory := coordinator.NewSubWorkflowRunnerFactory(coordinator.SubWorkflowRunnerConfig{
		Dispatcher:        h.coordinatorClient,
		DAGRunMgr:         h.dagRunMgr,
		DAGRepository:     h.dagRepository,
		StateStore:        h.stateStore,
		SecretStore:       runtimeStores.SecretStore,
		ProfileStore:      runtimeStores.ProfileStore,
		ServiceRegistry:   h.serviceRegistry,
		PeerConfig:        h.peerConfig,
		DefaultExecMode:   h.config.DefaultExecMode,
		StatusPusher:      statusPusher,
		LogWriterFactory:  logStreamer,
		ArtifactFinalizer: artifactUploader,
		RemoteDAGLoader:   remoteDAGLoader,
		WorkerID:          h.workerID,
		DAGRunLogDir:      h.config.Paths.LogDir,
		DAGRunArtifactDir: h.config.Paths.ArtifactDir,
	})

	// Build agent options
	opts := rtagent.Options{
		ParentDAGRun:             run.parent,
		WorkerID:                 h.workerID,
		StatusPusher:             statusPusher,
		LogWriterFactory:         logStreamer,
		ExtraEnvs:                extraEnvs,
		QueuedRun:                run.queued,
		AttemptID:                attemptID,
		StateStore:               h.stateStore,
		SecretStore:              runtimeStores.SecretStore,
		SecretReferenceResolver:  h.secretReferenceResolver(dag, run.owner, coordinator.SecretReferenceRun{WorkerID: h.workerID, AttemptKey: task.AttemptKey, AttemptID: attemptID}),
		ProfileStore:             runtimeStores.ProfileStore,
		ProfileName:              run.profileName,
		DAGDefinitionID:          task.DefinitionId,
		TriggerActor:             task.TriggerActor,
		ParallelItem:             task.ParallelItem,
		ServiceRegistry:          h.serviceRegistry,
		SubWorkflowRunnerFactory: subWorkflowRunnerFactory,
		RemoteDAGLoader:          remoteDAGLoader,
		RootDAGRun:               run.root,
		PeerConfig:               h.peerConfig,
		DefaultExecMode:          h.config.DefaultExecMode,
		ScheduleTime:             task.ScheduleTime,
		ArtifactDir:              env.artifactDir,
		ArtifactFinalizer:        artifactUploader,
		WorkspaceSeed:            run.workspaceSeed,
	}
	if run.workspaceSeed != nil {
		opts.WorkDir = taskWorkspaceDir(remoteWorkspaceWorkDir(task))
	}

	if run.retry != nil {
		opts.RetryTarget = run.retry.target
		opts.StepRetry = run.retry.stepName
		opts.IncludeDownstream = run.retry.includeDownstream
		opts.TriggerType = run.retry.triggerType
		opts.RetryPath = run.retry.retryPath
	}

	// Create the agent
	agentInstance := rtagent.New(
		dagRunID,
		dag,
		env.logDir,
		env.logFile,
		h.dagRunMgr,
		h.dagRepository,
		opts,
	)

	// Run the agent
	if err := agentInstance.Run(ctx); err != nil {
		logger.Error(ctx, "DAG execution failed",
			tag.RunID(dagRunID),
			tag.Error(err))
		return err
	}

	logger.Info(ctx, "DAG execution completed",
		tag.RunID(dagRunID))

	return nil
}

func (h *remoteTaskHandler) secretReferenceResolver(dag *ir.DAG, owner serviceregistry.HostInfo, run coordinator.SecretReferenceRun) providers.ReferenceResolver {
	client, ok := h.coordinatorClient.(coordinator.SecretReferenceClient)
	if !ok {
		return nil
	}
	workspaceName := ""
	if dag != nil {
		if name, found := workspace.WorkspaceNameFromLabels(dag.Labels); found {
			workspaceName = name
		}
	}
	return coordinator.NewSecretReferenceResolver(client, workspaceName, owner, run)
}

func (h *remoteTaskHandler) prepareDAGTools(ctx context.Context, dag *ir.DAG) ([]string, error) {
	workDir := ""
	if dag != nil {
		workDir = dag.WorkingDir
	}
	dataDir := ""
	toolsDir := ""
	if h.config != nil {
		dataDir = h.config.Paths.DataDir
		toolsDir = h.config.Paths.ToolsDir
	}
	return dagutools.PrepareDAG(ctx, dag, daguaqua.New(), dagutools.InstallOptions{
		ToolsDir: toolsDir,
		DataDir:  dataDir,
		WorkDir:  workDir,
	}, h.dagToolsBasePath())
}

func (h *remoteTaskHandler) dagToolsBasePath() string {
	if h.config != nil {
		for _, env := range h.config.Core.BaseEnv.AsSlice() {
			key, value, ok := strings.Cut(env, "=")
			if ok && strings.EqualFold(key, "PATH") {
				return value
			}
		}
	}
	return os.Getenv("PATH")
}

func previousStatusParams(task *coordinatorv1.Task) ([]string, error) {
	if task.Operation != coordinatorv1.Operation_OPERATION_RETRY || task.PreviousStatus == nil {
		return nil, nil
	}

	status, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to decode previous task status: %w", err)
	}

	return append([]string(nil), status.ParamsList...), nil
}
