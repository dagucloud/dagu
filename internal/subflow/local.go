// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	agentctx "github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/dagstate"
	profilepkg "github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/runtime"
	rtagent "github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/workspace"
)

// Local runs child workflows in the current process through the runtime agent.
type Local struct {
	dagRunMgr                runtime.Manager
	dagStore                 exec.DAGStore
	dagRunStore              exec.DAGRunStore
	runStateStore            runstate.Store
	queueStore               exec.QueueStore
	stateStore               dagstate.Store
	secretStore              secretpkg.Store
	profileStore             profilepkg.Store
	serviceRegistry          exec.ServiceRegistry
	statusPusher             runtime.StatusPusher
	logWriterFactory         exec.LogWriterFactory
	artifactFinalizer        runtime.ArtifactFinalizer
	subWorkflowRunnerFactory rtagent.SubWorkflowRunnerFactory
	workerID                 string
	dagRunLogDir             string
	dagRunArtifactDir        string

	mu     sync.Mutex
	active map[string]*rtagent.Agent
}

var _ executor.SubWorkflowRunner = (*Local)(nil)

// LocalOption configures Local.
type LocalOption func(*Local)

// WithLocalDAGRunStore sets the dag-run store used by child workflow agents.
func WithLocalDAGRunStore(store exec.DAGRunStore) LocalOption {
	return func(r *Local) {
		r.dagRunStore = store
	}
}

// WithLocalRunStateStore sets the run-state store used by child workflow agents.
func WithLocalRunStateStore(store runstate.Store) LocalOption {
	return func(r *Local) {
		r.runStateStore = store
	}
}

// WithLocalQueueStore sets the queue store used by child workflow agents.
func WithLocalQueueStore(store exec.QueueStore) LocalOption {
	return func(r *Local) {
		r.queueStore = store
	}
}

// WithLocalStateStore sets the state store used by child workflow agents.
func WithLocalStateStore(store dagstate.Store) LocalOption {
	return func(r *Local) {
		r.stateStore = store
	}
}

// WithLocalSecretStore sets the secret store used by child workflow agents.
func WithLocalSecretStore(store secretpkg.Store) LocalOption {
	return func(r *Local) {
		r.secretStore = store
	}
}

// WithLocalProfileStore sets the runtime profile store used by child workflow agents.
func WithLocalProfileStore(store profilepkg.Store) LocalOption {
	return func(r *Local) {
		r.profileStore = store
	}
}

// WithLocalServiceRegistry sets the service registry used by child workflow agents.
func WithLocalServiceRegistry(registry exec.ServiceRegistry) LocalOption {
	return func(r *Local) {
		r.serviceRegistry = registry
	}
}

// WithLocalStatusPusher sets the status pusher used by child workflow agents.
func WithLocalStatusPusher(pusher runtime.StatusPusher) LocalOption {
	return func(r *Local) {
		r.statusPusher = pusher
	}
}

// WithLocalLogWriterFactory sets the log writer factory used by child workflow agents.
func WithLocalLogWriterFactory(factory exec.LogWriterFactory) LocalOption {
	return func(r *Local) {
		r.logWriterFactory = factory
	}
}

// WithLocalArtifactFinalizer sets the artifact finalizer used by child workflow agents.
func WithLocalArtifactFinalizer(finalizer runtime.ArtifactFinalizer) LocalOption {
	return func(r *Local) {
		r.artifactFinalizer = finalizer
	}
}

// WithLocalSubWorkflowRunnerFactory sets the nested child workflow runner factory.
func WithLocalSubWorkflowRunnerFactory(factory rtagent.SubWorkflowRunnerFactory) LocalOption {
	return func(r *Local) {
		r.subWorkflowRunnerFactory = factory
	}
}

// WithLocalWorkerID sets the worker ID reported by child workflow agents.
func WithLocalWorkerID(workerID string) LocalOption {
	return func(r *Local) {
		r.workerID = workerID
	}
}

// WithLocalDAGRunDirs sets the log and artifact directories used by child workflow agents.
func WithLocalDAGRunDirs(logDir, artifactDir string) LocalOption {
	return func(r *Local) {
		r.dagRunLogDir = logDir
		r.dagRunArtifactDir = artifactDir
	}
}

// NewLocal creates an in-process child workflow runner.
func NewLocal(dagRunMgr runtime.Manager, dagStore exec.DAGStore, opts ...LocalOption) *Local {
	r := &Local{
		dagRunMgr: dagRunMgr,
		dagStore:  dagStore,
		active:    make(map[string]*rtagent.Agent),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ShouldRun reports whether req can use the in-process local path.
func (r *Local) ShouldRun(_ context.Context, req executor.SubWorkflowRequest) bool {
	if r == nil || req.DAG == nil {
		return false
	}
	if req.RunID == "" || req.RootDAGRun.Zero() {
		return false
	}
	if req.DAG.ForceLocal {
		return true
	}
	return len(req.WorkerSelector) == 0
}

// Run executes a child workflow in the current process.
func (r *Local) Run(ctx context.Context, req executor.SubWorkflowRequest) (*exec.RunStatus, error) {
	if err := validateInProcessRequest(req); err != nil {
		return nil, err
	}

	dag, cleanup, err := loadInProcessDAG(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	child, err := r.newAgent(ctx, req, dag, rtagent.Options{
		TriggerType: core.TriggerTypeSubDAG,
	})
	if err != nil {
		return nil, err
	}
	return r.runAgent(ctx, req.RunID, child)
}

// Retry retries a child workflow step in the current process.
func (r *Local) Retry(ctx context.Context, req executor.SubWorkflowRetryRequest) (*exec.RunStatus, error) {
	if err := validateInProcessRequest(req.SubWorkflowRequest); err != nil {
		return nil, err
	}
	if req.StepName == "" {
		return nil, errStepNameNotSet
	}

	runStateStore := r.runStateStoreFromContext(ctx)
	if runStateStore == nil {
		return nil, errNoRunDatabase
	}
	attempt, err := runStateStore.OpenChildAttempt(ctx, req.RootDAGRun, req.RunID)
	if err != nil {
		return nil, fmt.Errorf("failed to find child workflow attempt: %w", err)
	}
	retryTarget, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read child workflow status: %w", err)
	}

	dag, cleanup, err := loadInProcessDAG(ctx, req.SubWorkflowRequest)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	child, err := r.newAgent(ctx, req.SubWorkflowRequest, dag, rtagent.Options{
		RetryTarget: retryTarget,
		StepRetry:   req.StepName,
		TriggerType: inProcessRetryTriggerType(retryTarget),
	})
	if err != nil {
		return nil, err
	}
	return r.runAgent(ctx, req.RunID, child)
}

// Cancel requests cancellation for a running in-process child workflow.
func (r *Local) Cancel(ctx context.Context, req executor.SubWorkflowCancelRequest) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	child := r.active[req.RunID]
	r.mu.Unlock()
	if child != nil {
		child.Signal(ctx, localCancelSignal(req.Intent))
		return nil
	}

	runStateStore := r.runStateStoreFromContext(ctx)
	if runStateStore == nil || req.RunID == "" || req.RootDAGRun.Zero() {
		return nil
	}
	attempt, err := runStateStore.OpenChildAttempt(ctx, req.RootDAGRun, req.RunID)
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) {
			return nil
		}
		return fmt.Errorf("failed to find child workflow attempt: %w", err)
	}
	return attempt.RequestCancel(ctx)
}

func (r *Local) newAgent(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	dag *core.DAG,
	opts rtagent.Options,
) (*rtagent.Agent, error) {
	rCtx := exec.GetContext(ctx)
	logDir := r.dagRunLogDir
	if logDir == "" {
		logDir = rCtx.DAGRunLogDir
	}
	if logDir == "" {
		logDir = config.GetConfig(ctx).Paths.LogDir
	}
	artifactBaseDir := r.dagRunArtifactDir
	if artifactBaseDir == "" {
		artifactBaseDir = rCtx.DAGRunArtifactDir
	}
	if artifactBaseDir == "" {
		artifactBaseDir = config.GetConfig(ctx).Paths.ArtifactDir
	}

	artifactDir, err := inProcessArtifactDir(ctx, dag, artifactBaseDir, req.RunID, opts.RetryTarget)
	if err != nil {
		return nil, err
	}

	opts.ParentDAGRun = req.ParentDAGRun
	opts.RootDAGRun = req.RootDAGRun
	opts.ExtraEnvs = inProcessExtraEnvs(rCtx, req)
	opts.WorkerID = r.workerID
	opts.StatusPusher = r.statusPusher
	opts.SubWorkflowRunnerFactory = r.subWorkflowRunnerFactory
	opts.LogWriterFactory = r.logWriterFactory
	opts.RunStateStore = r.runStateStoreFromContext(ctx)
	opts.DAGRunStore = r.dagRunStoreFromContext(ctx)
	opts.QueueStore = r.queueStoreFromContext(ctx)
	opts.StateStore = r.stateStoreFromContext(ctx)
	opts.SecretStore = r.secretStore
	opts.ProfileStore = r.profileStore
	opts.ProfileName = req.ProfileName
	opts.ServiceRegistry = r.serviceRegistry
	opts.DefaultExecMode = rCtx.DefaultExecMode
	opts.AgentConfigStore = agentctx.GetConfigStore(ctx)
	opts.AgentModelStore = agentctx.GetModelStore(ctx)
	opts.AgentMemoryStore = agentctx.GetMemoryStore(ctx)
	opts.AgentSoulStore = agentctx.GetSoulStore(ctx)
	opts.AgentOAuthManager = agentctx.GetOAuthManager(ctx)
	opts.AgentRemoteContextResolver = agentctx.GetRemoteContextResolver(ctx)
	opts.ArtifactDir = artifactDir
	opts.DAGRunLogDir = logDir
	opts.DAGRunArtifactDir = artifactBaseDir
	opts.ArtifactFinalizer = r.artifactFinalizer

	logFile := ""
	if logDir != "" {
		logFile = filepath.Join(logDir, req.RunID+".log")
	}

	return rtagent.New(
		req.RunID,
		dag,
		logDir,
		logFile,
		r.dagRunMgr,
		r.dagStoreFromContext(ctx),
		opts,
	), nil
}

func (r *Local) runAgent(ctx context.Context, runID string, child *rtagent.Agent) (*exec.RunStatus, error) {
	r.mu.Lock()
	r.active[runID] = child
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.active, runID)
		r.mu.Unlock()
	}()

	runErr := child.Run(ctx)
	status := child.Status(ctx)
	result := statusToRunStatus(&status, runID)
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (r *Local) dagStoreFromContext(ctx context.Context) exec.DAGStore {
	if store := agentctx.GetDAGStore(ctx); store != nil {
		return store
	}
	return r.dagStore
}

func (r *Local) dagRunStoreFromContext(ctx context.Context) exec.DAGRunStore {
	if r.dagRunStore != nil {
		return r.dagRunStore
	}
	rCtx := exec.GetContext(ctx)
	if rCtx.DAGRunStore != nil {
		return rCtx.DAGRunStore
	}
	return agentctx.GetDAGRunStore(ctx)
}

func (r *Local) runStateStoreFromContext(ctx context.Context) runstate.Store {
	if r.runStateStore != nil {
		return r.runStateStore
	}
	if dagRunStore := r.dagRunStoreFromContext(ctx); dagRunStore != nil {
		return runstate.NewHistoryStore(dagRunStore)
	}
	return nil
}

func (r *Local) queueStoreFromContext(ctx context.Context) exec.QueueStore {
	if r.queueStore != nil {
		return r.queueStore
	}
	return exec.GetContext(ctx).QueueStore
}

func (r *Local) stateStoreFromContext(ctx context.Context) dagstate.Store {
	if r.stateStore != nil {
		return r.stateStore
	}
	return exec.GetContext(ctx).StateStore
}

func validateInProcessRequest(req executor.SubWorkflowRequest) error {
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

func loadInProcessDAG(
	ctx context.Context,
	req executor.SubWorkflowRequest,
) (*core.DAG, func(), error) {
	cleanup := func() {}
	workDir := req.WorkDir
	target := req.DAG.Location
	if req.Workspace != nil {
		workspaceDir, workspaceTarget, workspaceCleanup, err := materializeLocalWorkspace(req)
		if err != nil {
			return nil, nil, err
		}
		cleanup = workspaceCleanup
		workDir = workspaceDir
		target = workspaceTarget
	}

	loadOpts := inProcessLoadOptions(ctx, req, workDir)
	var (
		dag *core.DAG
		err error
	)
	switch {
	case target != "":
		dag, err = spec.Load(ctx, target, loadOpts...)
	case len(req.DAG.YamlData) > 0:
		dag, err = spec.LoadYAML(ctx, req.DAG.YamlData, loadOpts...)
	default:
		err = errMissingDAGPath
	}
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to load child workflow DAG: %w", err)
	}
	dag.SourceFile = req.DAG.SourceFile
	return dag, cleanup, nil
}

func inProcessLoadOptions(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	workDir string,
) []spec.LoadOption {
	cfg := config.GetConfig(ctx)
	loadOpts := []spec.LoadOption{
		spec.WithName(req.DAG.Name),
		spec.WithSkipBaseHandlers(),
	}
	if cfg != nil {
		loadOpts = append(loadOpts, spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(cfg.Paths.DAGsDir)))
	}
	if req.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(req.Params))
	}
	if workDir != "" {
		loadOpts = append(loadOpts, spec.WithDefaultWorkingDir(workDir))
	}

	if req.Workspace == nil {
		baseConfig := req.DAG.BaseConfigData
		if len(baseConfig) == 0 && req.ParentDAG != nil {
			baseConfig = req.ParentDAG.BaseConfigData
		}
		if len(baseConfig) > 0 {
			loadOpts = append(loadOpts, spec.WithBaseConfigContent(baseConfig))
		} else if cfg != nil && cfg.Paths.BaseConfig != "" {
			loadOpts = append(loadOpts, spec.WithBaseConfig(cfg.Paths.BaseConfig))
		}
	}
	return loadOpts
}

func inProcessExtraEnvs(rCtx exec.Context, req executor.SubWorkflowRequest) []string {
	envs := inheritedEnvForLocalRunner(rCtx.AllEnvs())
	if req.ExternalStepRetry {
		envs = append(envs, exec.EnvKeyExternalStepRetry+"=1")
	}
	return envs
}

func inProcessArtifactDir(ctx context.Context, dag *core.DAG, baseDir, runID string, retryTarget *exec.DAGRunStatus) (string, error) {
	if retryTarget != nil && retryTarget.ArchiveDir != "" {
		return retryTarget.ArchiveDir, nil
	}
	if dag == nil || !dag.ArtifactsEnabled() {
		return "", nil
	}

	dagArtifactDir := ""
	if dag.Artifacts != nil {
		dagArtifactDir = dag.Artifacts.Dir
	}

	dir, err := logpath.GenerateDir(ctx, baseDir, dagArtifactDir, dag.Name, runID)
	if err != nil {
		return "", fmt.Errorf("failed to generate child workflow artifact directory: %w", err)
	}
	return dir, nil
}

func inProcessRetryTriggerType(status *exec.DAGRunStatus) core.TriggerType {
	triggerType := exec.PreservedQueueTriggerType(status)
	if triggerType != core.TriggerTypeUnknown {
		return triggerType
	}
	return core.TriggerTypeRetry
}
