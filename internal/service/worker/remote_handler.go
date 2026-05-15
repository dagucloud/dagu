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

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/fileagentconfig"
	"github.com/dagucloud/dagu/internal/persis/fileagentmodel"
	"github.com/dagucloud/dagu/internal/persis/fileagentoauth"
	"github.com/dagucloud/dagu/internal/persis/fileagentsoul"
	"github.com/dagucloud/dagu/internal/persis/filememory"
	"github.com/dagucloud/dagu/internal/persis/filesecret"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime"
	rtagent "github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/runtime/remote"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

var _ TaskHandler = (*remoteTaskHandler)(nil)

// RemoteTaskHandlerConfig contains configuration for the remote task handler
type RemoteTaskHandlerConfig struct {
	// WorkerID is the identifier of this worker
	WorkerID string
	// CoordinatorClient is the coordinator client with load balancing support
	CoordinatorClient coordinator.Client
	// DAGRunStore is the store for DAG run status (may be nil for fully remote mode)
	DAGRunStore exec.DAGRunStore
	// DAGStore is the store for DAG definitions
	DAGStore exec.DAGStore
	// DAGRunMgr is the manager for DAG runs
	DAGRunMgr runtime.Manager
	// ServiceRegistry is the service registry
	ServiceRegistry exec.ServiceRegistry
	// PeerConfig is the peer configuration
	PeerConfig config.Peer
	// Config is the main application configuration
	Config *config.Config
}

// NewRemoteTaskHandler creates a new TaskHandler that runs tasks in-process
// with status pushing and log streaming to the coordinator.
func NewRemoteTaskHandler(cfg RemoteTaskHandlerConfig) TaskHandler {
	if cfg.Config == nil {
		cfg.Config = &config.Config{}
	}
	return &remoteTaskHandler{
		workerID:          cfg.WorkerID,
		coordinatorClient: cfg.CoordinatorClient,
		dagRunStore:       cfg.DAGRunStore,
		dagStore:          cfg.DAGStore,
		dagRunMgr:         cfg.DAGRunMgr,
		serviceRegistry:   cfg.ServiceRegistry,
		peerConfig:        cfg.PeerConfig,
		config:            cfg.Config,
	}
}

type remoteTaskHandler struct {
	workerID          string
	coordinatorClient coordinator.Client
	dagRunStore       exec.DAGRunStore
	dagStore          exec.DAGStore
	dagRunMgr         runtime.Manager
	serviceRegistry   exec.ServiceRegistry
	peerConfig        config.Peer
	config            *config.Config
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
	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	parent := exec.DAGRunRef{Name: task.ParentDagRunName, ID: task.ParentDagRunId}
	owner, err := taskOwner(task)
	if err != nil {
		return fmt.Errorf("invalid task owner coordinator metadata: %w", err)
	}

	dag, cleanup, err := h.loadDAG(ctx, task)
	if err != nil {
		h.reportTaskLoadFailure(ctx, task, root, parent, owner, err)
		return fmt.Errorf("failed to load DAG: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	statusPusher, logStreamer, artifactUploader := h.createRemoteHandlers(task.DagRunId, dag.Name, root, owner)
	err = h.executeDAGRun(ctx, dag, task.DagRunId, task.AttemptId, task.ScheduleTime, root, parent, statusPusher, logStreamer, artifactUploader, queuedRun, nil, task.AgentSnapshot, taskExtraEnvs(task))
	var initErr *taskInitError
	if errors.As(err, &initErr) {
		h.reportTaskInitFailure(ctx, task, root, parent, statusPusher, initErr.err)
	}
	return err
}

func (h *remoteTaskHandler) handleRetry(ctx context.Context, task *coordinatorv1.Task) error {
	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	parent := exec.DAGRunRef{Name: task.ParentDagRunName, ID: task.ParentDagRunId}
	owner, err := taskOwner(task)
	if err != nil {
		return fmt.Errorf("invalid task owner coordinator metadata: %w", err)
	}

	if task.PreviousStatus == nil {
		return fmt.Errorf("retry requires previous_status in task for shared-nothing mode")
	}

	status, convErr := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if convErr != nil {
		return fmt.Errorf("failed to convert previous status: %w", convErr)
	}
	logger.Info(ctx, "Using previous status from task for retry",
		tag.RunID(task.DagRunId),
		slog.Int("nodes", len(status.Nodes)))

	dag, cleanup, err := h.loadDAG(ctx, task)
	if err != nil {
		h.reportTaskLoadFailure(ctx, task, root, parent, owner, err)
		return fmt.Errorf("failed to load DAG: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	statusPusher, logStreamer, artifactUploader := h.createRemoteHandlers(task.DagRunId, dag.Name, root, owner)
	triggerType := exec.PreservedQueueTriggerType(status)

	err = h.executeDAGRun(ctx, dag, task.DagRunId, task.AttemptId, task.ScheduleTime, root, parent, statusPusher, logStreamer, artifactUploader, false, &retryConfig{
		target:      status,
		stepName:    task.Step,
		triggerType: triggerType,
	}, task.AgentSnapshot, taskExtraEnvs(task))
	var initErr *taskInitError
	if errors.As(err, &initErr) {
		h.reportTaskInitFailure(ctx, task, root, parent, statusPusher, initErr.err)
	}
	return err
}

func (h *remoteTaskHandler) reportTaskLoadFailure(ctx context.Context, task *coordinatorv1.Task, root, parent exec.DAGRunRef, owner exec.HostInfo, loadErr error) {
	statusPusher := remote.NewStatusPusher(h.coordinatorClient, h.workerID, owner)
	finishedAt := stringutil.FormatTime(time.Now())
	logger.Warn(ctx, "Failed to load DAG on worker",
		tag.Target(task.Target),
		tag.RunID(task.DagRunId),
		tag.Error(loadErr),
	)
	status := exec.DAGRunStatus{
		Root:       root,
		Parent:     parent,
		Name:       task.Target,
		DAGRunID:   task.DagRunId,
		AttemptID:  task.AttemptId,
		Status:     core.Failed,
		FinishedAt: finishedAt,
		Error:      sanitizeTaskLoadError(task.Target, loadErr),
		Params:     task.Params,
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
	task *coordinatorv1.Task,
	root exec.DAGRunRef,
	parent exec.DAGRunRef,
	statusPusher *remote.StatusPusher,
	initErr error,
) {
	if statusPusher == nil || initErr == nil {
		return
	}

	finishedAt := stringutil.FormatTime(time.Now())
	logger.Warn(ctx, "Failed to initialize DAG on worker",
		tag.Target(task.Target),
		tag.RunID(task.DagRunId),
		tag.Error(initErr),
	)
	status := exec.DAGRunStatus{
		Root:       root,
		Parent:     parent,
		Name:       task.Target,
		DAGRunID:   task.DagRunId,
		AttemptID:  task.AttemptId,
		Status:     core.Failed,
		FinishedAt: finishedAt,
		Error:      initErr.Error(),
		Params:     task.Params,
	}

	if err := statusPusher.Push(ctx, status); err != nil {
		logger.Warn(ctx, "Failed to report init failure status",
			tag.Target(task.Target),
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
	target      *exec.DAGRunStatus
	stepName    string
	triggerType core.TriggerType
}

type agentStoreBundle struct {
	configStore  agent.ConfigStore
	modelStore   agent.ModelStore
	soulStore    agent.SoulStore
	memoryStore  agent.MemoryStore
	oauthManager *agentoauth.Manager
	secretStore  secretpkg.Store
}

type taskInitError struct {
	err error
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

func taskExtraEnvs(task *coordinatorv1.Task) []string {
	if task == nil || !task.ExternalStepRetry {
		return nil
	}
	return []string{exec.EnvKeyExternalStepRetry + "=1"}
}

// createRemoteHandlers creates the remote status, log, and artifact transport handlers.
func (h *remoteTaskHandler) createRemoteHandlers(dagRunID, dagName string, root exec.DAGRunRef, owner ...exec.HostInfo) (*remote.StatusPusher, *remote.LogStreamer, *remote.ArtifactUploader) {
	var target exec.HostInfo
	if len(owner) > 0 {
		target = owner[0]
	}
	statusPusher := remote.NewStatusPusher(h.coordinatorClient, h.workerID, target)
	logStreamer := remote.NewLogStreamer(
		h.coordinatorClient,
		h.workerID,
		dagRunID,
		dagName,
		"", // attemptID will be set by agent after attempt creation
		root,
		target,
	)
	artifactUploader := remote.NewArtifactUploader(
		h.coordinatorClient,
		h.workerID,
		dagRunID,
		dagName,
		"",
		root,
		target,
	)
	return statusPusher, logStreamer, artifactUploader
}

// agentStores creates the agent config, model, soul, memory, and OAuth stores from the config paths.
func (h *remoteTaskHandler) agentStores(ctx context.Context) agentStoreBundle {
	var bundle agentStoreBundle
	if store, err := filesecret.NewFromDataDir(h.config.Paths.DataDir); err != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(err))
	} else {
		bundle.secretStore = store
	}

	acs, err := fileagentconfig.New(h.config.Paths.DataDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent config store", tag.Error(err))
		return bundle
	}
	if acs == nil {
		return bundle
	}
	bundle.configStore = acs

	ams, err := fileagentmodel.New(filepath.Join(h.config.Paths.DataDir, "agent", "models"))
	if err != nil {
		logger.Warn(ctx, "Failed to create agent model store", tag.Error(err))
		return bundle
	}
	bundle.modelStore = ams

	soulsDir := filepath.Join(h.config.Paths.DAGsDir, "souls")
	soulStore, err := fileagentsoul.New(ctx, soulsDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent soul store", tag.Error(err))
		return bundle
	}
	bundle.soulStore = soulStore

	ms, err := filememory.New(h.config.Paths.DAGsDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent memory store", tag.Error(err))
		return bundle
	}
	bundle.memoryStore = ms

	oauthManager, err := fileagentoauth.NewManager(h.config.Paths.DataDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent OAuth store", tag.Error(err))
		return bundle
	}
	bundle.oauthManager = oauthManager

	return bundle
}

func (h *remoteTaskHandler) agentStoresFromSnapshot(snapshotPayload []byte) (agentStoreBundle, error) {
	snapshot, err := agent.UnmarshalSnapshot(snapshotPayload)
	if err != nil {
		return agentStoreBundle{}, err
	}
	if snapshot == nil {
		return agentStoreBundle{}, fmt.Errorf("agent snapshot is empty")
	}

	stores := agent.NewSnapshotStores(snapshot)
	if stores.ConfigStore == nil {
		return agentStoreBundle{}, fmt.Errorf("agent snapshot is missing config")
	}
	if stores.ModelStore == nil {
		return agentStoreBundle{}, fmt.Errorf("agent snapshot is missing models")
	}

	return agentStoreBundle{
		configStore: stores.ConfigStore,
		modelStore:  stores.ModelStore,
		soulStore:   stores.SoulStore,
		memoryStore: stores.MemoryStore,
	}, nil
}

// loadDAG loads the DAG from task definition.
// Returns the loaded DAG and a cleanup function that should be called after task execution.
func (h *remoteTaskHandler) loadDAG(ctx context.Context, task *coordinatorv1.Task) (*core.DAG, func(), error) {
	logger.Info(ctx, "Creating temporary DAG file from definition",
		tag.DAG(task.Target),
		tag.Size(len(task.Definition)))

	tempFile, err := fileutil.CreateTempDAGFile("worker-dags", task.Target, []byte(task.Definition))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp DAG file: %w", err)
	}
	cleanupFunc := func() {
		if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
			logger.Errorf(ctx, "Failed to remove temp DAG file: %v", err)
		}
	}

	// Prepare load options
	// Note: DAGsDir is intentionally NOT included because:
	// 1. Remote handlers always receive DAG definitions from the coordinator
	// 2. Shared-nothing workers should not access local DAG directories
	loadOpts := []spec.LoadOption{
		spec.WithName(task.Target), // Use original DAG name, not temp file path
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
	}

	dag, err := spec.Load(ctx, tempFile, loadOpts...)
	if err != nil {
		cleanupFunc()
		return nil, nil, fmt.Errorf("failed to load DAG from %s: %w", tempFile, err)
	}
	dag.SourceFile = task.SourceFile

	return dag, cleanupFunc, nil
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
func (h *remoteTaskHandler) createAgentEnv(ctx context.Context, dag *core.DAG, dagRunID string) (*agentEnv, error) {
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
	dag *core.DAG,
	dagRunID string,
	attemptID string,
	scheduleTime string,
	root exec.DAGRunRef,
	parent exec.DAGRunRef,
	statusPusher *remote.StatusPusher,
	logStreamer *remote.LogStreamer,
	artifactUploader *remote.ArtifactUploader,
	queuedRun bool,
	retry *retryConfig,
	agentSnapshot []byte,
	extraEnvs []string,
) error {
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

	// Create a writer that writes to both local file AND streams to coordinator in real-time.
	// This enables viewing scheduler logs while the DAG is still running.
	var logWriter io.Writer = logFile
	if logStreamer != nil {
		streamingWriter := logStreamer.NewSchedulerLogWriter(ctx, logFile)
		defer func() {
			if closeErr := streamingWriter.Close(); closeErr != nil {
				logger.Warn(ctx, "Failed to close scheduler log streamer", tag.Error(closeErr))
			}
		}()
		logWriter = streamingWriter
	}

	// Configure logger to use the streaming writer
	ctx = logger.WithLogger(ctx, logger.NewLogger(logger.WithWriter(logWriter)))

	// Create agent stores for agent step execution
	var agentStores agentStoreBundle
	if len(agentSnapshot) > 0 {
		agentStores, err = h.agentStoresFromSnapshot(agentSnapshot)
		if err != nil {
			return newTaskInitError(fmt.Errorf("hydrate agent snapshot: %w", err))
		}
	} else {
		agentStores = h.agentStores(ctx)
	}

	// Build agent options
	opts := rtagent.Options{
		ParentDAGRun:      parent,
		WorkerID:          h.workerID,
		StatusPusher:      statusPusher,
		LogWriterFactory:  logStreamer,
		ExtraEnvs:         extraEnvs,
		QueuedRun:         queuedRun,
		AttemptID:         attemptID,
		DAGRunStore:       h.dagRunStore,
		SecretStore:       agentStores.secretStore,
		ServiceRegistry:   h.serviceRegistry,
		RootDAGRun:        root,
		PeerConfig:        h.peerConfig,
		DefaultExecMode:   h.config.DefaultExecMode,
		AgentConfigStore:  agentStores.configStore,
		AgentModelStore:   agentStores.modelStore,
		AgentSoulStore:    agentStores.soulStore,
		AgentMemoryStore:  agentStores.memoryStore,
		AgentOAuthManager: agentStores.oauthManager,
		ScheduleTime:      scheduleTime,
		ArtifactDir:       env.artifactDir,
		ArtifactFinalizer: artifactUploader,
	}

	if retry != nil {
		opts.RetryTarget = retry.target
		opts.StepRetry = retry.stepName
		opts.TriggerType = retry.triggerType
	}

	// Create the agent
	agentInstance := rtagent.New(
		dagRunID,
		dag,
		env.logDir,
		env.logFile,
		h.dagRunMgr,
		h.dagStore,
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
