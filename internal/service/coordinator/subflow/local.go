// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	rtagent "github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/secret/providers"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	dagutools "github.com/dagucloud/dagu/v2/internal/tools"
	daguaqua "github.com/dagucloud/dagu/v2/internal/tools/aqua"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Local runs child workflows in the current process through the runtime agent.
type Local struct {
	dagRunMgr                runtime.Manager
	dagRepository            *persis.DAGRepository
	dagRunRepository         *persis.DAGRunRepository
	runStateStore            runstate.Store
	queueStore               queue.QueueStore
	stateStore               dagrun.StateStore
	secretStore              secretpkg.Store
	secretResolver           func(*ir.DAG) providers.ReferenceResolver
	profileStore             profilepkg.Store
	profileResolver          func(*ir.DAG) profilepkg.RuntimeResolver
	serviceRegistry          serviceregistry.ServiceRegistry
	statusPusher             runtime.StatusPusher
	logWriterFactory         runctx.LogWriterFactory
	artifactFinalizer        runtime.ArtifactFinalizer
	subWorkflowRunnerFactory rtagent.SubWorkflowRunnerFactory
	remoteDAGLoader          rtagent.RemoteDAGLoader
	workerID                 string
	dagRunLogDir             string
	dagRunArtifactDir        string
	installer                dagutools.Installer

	mu     sync.Mutex
	active map[string]*rtagent.Agent
}

var _ executor.SubWorkflowRunner = (*Local)(nil)
var _ executor.Enqueuer = (*Local)(nil)

// LocalOption configures Local.
type LocalOption func(*Local)

// WithLocalToolInstaller sets the installer used to make child DAG tools available.
func WithLocalToolInstaller(installer dagutools.Installer) LocalOption {
	return func(r *Local) {
		r.installer = installer
	}
}

// WithLocalDAGRunRepository sets the DAG-run repository used by child workflow agents.
func WithLocalDAGRunRepository(repository *persis.DAGRunRepository) LocalOption {
	return func(r *Local) {
		r.dagRunRepository = repository
	}
}

// WithLocalRunStateStore sets the run-state store used by child workflow agents.
func WithLocalRunStateStore(store runstate.Store) LocalOption {
	return func(r *Local) {
		r.runStateStore = store
	}
}

// WithLocalQueueStore sets the queue store used by child workflow agents.
func WithLocalQueueStore(store queue.QueueStore) LocalOption {
	return func(r *Local) {
		r.queueStore = store
	}
}

// WithLocalStateStore sets the state store used by child workflow agents.
func WithLocalStateStore(store dagrun.StateStore) LocalOption {
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

// WithLocalSecretResolver sets the registry resolver used by child agents.
func WithLocalSecretResolver(factory func(*ir.DAG) providers.ReferenceResolver) LocalOption {
	return func(r *Local) {
		r.secretResolver = factory
	}
}

// WithLocalProfileStore sets the runtime profile store used by child workflow agents.
func WithLocalProfileStore(store profilepkg.Store) LocalOption {
	return func(r *Local) {
		r.profileStore = store
	}
}

// WithLocalProfileResolver sets the runtime profile resolver used by child agents.
func WithLocalProfileResolver(resolver func(*ir.DAG) profilepkg.RuntimeResolver) LocalOption {
	return func(r *Local) {
		r.profileResolver = resolver
	}
}

// WithLocalServiceRegistry sets the service registry used by child workflow agents.
func WithLocalServiceRegistry(registry serviceregistry.ServiceRegistry) LocalOption {
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
func WithLocalLogWriterFactory(factory runctx.LogWriterFactory) LocalOption {
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

// WithLocalRemoteDAGLoader sets the remote fallback used by nested child workflows.
func WithLocalRemoteDAGLoader(loader rtagent.RemoteDAGLoader) LocalOption {
	return func(r *Local) {
		r.remoteDAGLoader = loader
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
func NewLocal(dagRunMgr runtime.Manager, dagRepository *persis.DAGRepository, opts ...LocalOption) *Local {
	r := &Local{
		dagRunMgr:     dagRunMgr,
		dagRepository: dagRepository,
		installer:     daguaqua.New(),
		active:        make(map[string]*rtagent.Agent),
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.runStateStore == nil && r.dagRunRepository != nil {
		r.runStateStore = persis.NewRunStateStore(r.dagRunRepository, nil)
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
func (r *Local) Run(ctx context.Context, req executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	if err := validateInProcessRequest(req); err != nil {
		return nil, err
	}
	if err := r.validateBuildDAG(req.DAG); err != nil {
		return nil, err
	}

	retryTarget, err := r.existingChildRetryTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.Reuse {
		if retryTarget == nil {
			return nil, fmt.Errorf("persisted child workflow status not found for DAG run %s", req.RunID)
		}
		result := statusToRunStatus(retryTarget, req.RunID)
		result.PendingStepRetries = nil
		return result, nil
	}
	if req.ExternalStepRetry && retryTarget != nil && retryTarget.Status == ir.Succeeded {
		return statusToRunStatus(retryTarget, req.RunID), nil
	}

	dag, workspaceDir, cleanup, err := loadInProcessDAG(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	opts := rtagent.Options{
		TriggerType: ir.TriggerTypeSubDAG,
		WorkDir:     workspaceDir,
	}
	if req.Workspace != nil {
		opts.WorkspaceSeed = &executor.WorkspaceSeed{
			Descriptor: req.Workspace.Descriptor,
			Archive:    req.Workspace.Archive,
		}
	}
	if retryTarget != nil {
		opts.RetryTarget = retryTarget
		opts.TriggerType = inProcessRetryTriggerType(retryTarget)
	}

	child, err := r.newAgent(ctx, req, dag, opts)
	if err != nil {
		return nil, err
	}
	return r.runAgent(ctx, req.RunID, child)
}

// Enqueue admits a child workflow to the local control-plane queue.
func (r *Local) Enqueue(ctx context.Context, req executor.EnqueueRequest) (executor.EnqueueResult, error) {
	if r.dagRunRepository == nil {
		return executor.EnqueueResult{}, fmt.Errorf("dag.enqueue requires a DAG-run repository")
	}
	if r.queueStore == nil {
		return executor.EnqueueResult{}, fmt.Errorf("dag.enqueue requires a queue store")
	}

	ref := ir.NewDAGRunRef(req.DAG.Name, req.RunID)
	attempt, err := r.dagRunRepository.FindAttempt(ctx, ref)
	switch {
	case errors.Is(err, dagrun.ErrNoStatusData):
		return executor.EnqueueResult{Status: ir.Queued, AlreadyExists: true}, nil
	case err == nil:
		status := ir.Queued
		if existing, readErr := attempt.ReadStatus(ctx); readErr == nil && existing != nil {
			status = existing.Status
		}
		return executor.EnqueueResult{Status: status, AlreadyExists: true}, nil
	case !errors.Is(err, dagrun.ErrDAGRunIDNotFound):
		return executor.EnqueueResult{}, fmt.Errorf("failed to check existing DAG run: %w", err)
	}

	rCtx := runctx.GetContext(ctx)
	logDir := r.dagRunLogDir
	if logDir == "" {
		logDir = rCtx.DAGRunLogDir
	}
	artifactDir := r.dagRunArtifactDir
	if artifactDir == "" {
		artifactDir = rCtx.DAGRunArtifactDir
	}
	_, err = intake.EnqueueRun(ctx, intake.QueueRequest{
		DAGRunRepository: r.dagRunRepository,
		QueueStore:       r.queueStore,
		DAG:              req.DAG,
		DAGRunID:         req.RunID,
		QueueName:        req.QueueName,
		LogBaseDir:       logDir,
		ArtifactBaseDir:  artifactDir,
		TriggerType:      ir.TriggerTypeSubDAG,
		TriggerActor:     req.TriggerActor,
		ParallelItem:     req.ParallelItem,
		ProfileName:      req.ProfileName,
	})
	if err != nil {
		return executor.EnqueueResult{}, fmt.Errorf("failed to enqueue DAG run: %w", err)
	}
	return executor.EnqueueResult{Status: ir.Queued}, nil
}

// Retry retries a child workflow step in the current process.
func (r *Local) Retry(ctx context.Context, req executor.SubWorkflowRetryRequest) (*ir.RunStatus, error) {
	if err := validateInProcessRequest(req.SubWorkflowRequest); err != nil {
		return nil, err
	}
	if err := r.validateBuildDAG(req.DAG); err != nil {
		return nil, err
	}
	if req.StepName == "" {
		return nil, errStepNameNotSet
	}

	retryTarget, err := r.existingChildStatus(ctx, req.SubWorkflowRequest)
	if err != nil {
		return nil, err
	}

	dag, workspaceDir, cleanup, err := loadInProcessDAG(ctx, req.SubWorkflowRequest)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	opts := rtagent.Options{
		RetryTarget:       retryTarget,
		StepRetry:         req.StepName,
		IncludeDownstream: req.IncludeDownstream,
		TriggerType:       inProcessRetryTriggerType(retryTarget),
		WorkDir:           workspaceDir,
	}
	if req.Workspace != nil {
		opts.WorkspaceSeed = &executor.WorkspaceSeed{
			Descriptor: req.Workspace.Descriptor,
			Archive:    req.Workspace.Archive,
		}
	}
	child, err := r.newAgent(ctx, req.SubWorkflowRequest, dag, opts)
	if err != nil {
		return nil, err
	}
	return r.runAgent(ctx, req.RunID, child)
}

func (r *Local) validateBuildDAG(dag *ir.DAG) error {
	if dag.Type == ir.TypeBuild && dispatch.IsRemoteWorkerID(r.workerID) {
		return dispatch.ErrBuildRequiresLocal
	}
	return nil
}

func (r *Local) existingChildRetryTarget(
	ctx context.Context,
	req executor.SubWorkflowRequest,
) (*ir.DAGRunStatus, error) {
	retryTarget, err := r.existingChildStatus(ctx, req)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, errNoRunStateStore) {
			return nil, nil
		}
		return nil, err
	}
	return retryTarget, nil
}

func (r *Local) existingChildStatus(
	ctx context.Context,
	req executor.SubWorkflowRequest,
) (*ir.DAGRunStatus, error) {
	if req.RootDAGRun.ID != "" && req.RunID != "" {
		status, err := r.dagRunMgr.FindSubDAGRunStatus(ctx, req.RootDAGRun, req.RunID)
		if err == nil {
			if status == nil {
				return nil, fmt.Errorf("failed to read child workflow status: status data is nil")
			}
			return status, nil
		}
		if !errors.Is(err, dagrun.ErrNoStatusData) && !errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil, fmt.Errorf("failed to find child workflow attempt: %w", err)
		}
	}

	runStateStore := r.runStateStoreFromContext(ctx)
	if runStateStore == nil {
		return nil, errNoRunStateStore
	}
	attempt, err := runStateStore.OpenChildAttempt(ctx, req.RootDAGRun, req.RunID)
	if err != nil {
		return nil, fmt.Errorf("failed to find child workflow attempt: %w", err)
	}
	retryTarget, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read child workflow status: %w", err)
	}
	if retryTarget == nil {
		return nil, fmt.Errorf("failed to read child workflow status: status data is nil")
	}
	return retryTarget, nil
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
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return nil
		}
		return fmt.Errorf("failed to find child workflow attempt: %w", err)
	}
	return attempt.RequestCancel(ctx)
}

func (r *Local) newAgent(
	ctx context.Context,
	req executor.SubWorkflowRequest,
	dag *ir.DAG,
	opts rtagent.Options,
) (*rtagent.Agent, error) {
	rCtx := runctx.GetContext(ctx)
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
	toolEnvs, err := r.prepareDAGTools(ctx, rCtx, dag)
	if err != nil {
		return nil, err
	}

	opts.ParentDAGRun = req.ParentDAGRun
	opts.RootDAGRun = req.RootDAGRun
	opts.TriggerActor = req.TriggerActor
	opts.ParallelItem = req.ParallelItem
	opts.RetryPath = req.RetryPath
	opts.ExtraEnvs = append(inProcessExtraEnvs(rCtx, req), toolEnvs...)
	opts.WorkerID = r.workerID
	opts.StatusPusher = r.statusPusher
	opts.SubWorkflowRunnerFactory = r.subWorkflowRunnerFactory
	opts.RemoteDAGLoader = r.remoteDAGLoader
	opts.LogWriterFactory = r.logWriterFactory
	opts.RunStateStore = r.runStateStoreFromContext(ctx)
	opts.StateStore = r.stateStoreFromContext(ctx)
	opts.MaterializationStore = rCtx.MaterializationStore
	opts.NoReuse = rCtx.NoReuse
	opts.SecretStore = r.secretStore
	if r.secretResolver != nil {
		opts.SecretReferenceResolver = r.secretResolver(dag)
	}
	opts.ProfileStore = r.profileStore
	if r.profileResolver != nil {
		opts.ProfileResolver = r.profileResolver(dag)
	}
	opts.ProfileName = req.ProfileName
	opts.ServiceRegistry = r.serviceRegistry
	opts.DefaultExecMode = rCtx.DefaultExecMode
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
		r.dagRepository,
		opts,
	), nil
}

func (r *Local) prepareDAGTools(ctx context.Context, rCtx runctx.Context, dag *ir.DAG) ([]string, error) {
	cfg := config.GetConfig(ctx)
	workDir := ""
	if dag != nil {
		workDir = dag.WorkingDir
	}
	return dagutools.PrepareDAG(ctx, dag, r.installer, dagutools.InstallOptions{
		ToolsDir: cfg.Paths.ToolsDir,
		DataDir:  cfg.Paths.DataDir,
		WorkDir:  workDir,
	}, toolsBasePath(rCtx))
}

func (r *Local) runAgent(ctx context.Context, runID string, child *rtagent.Agent) (*ir.RunStatus, error) {
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

func (r *Local) runStateStoreFromContext(ctx context.Context) runstate.Store {
	if r.runStateStore != nil {
		return r.runStateStore
	}
	return runctx.GetContext(ctx).RunStateStore
}

func (r *Local) stateStoreFromContext(ctx context.Context) dagrun.StateStore {
	if r.stateStore != nil {
		return r.stateStore
	}
	return runctx.GetContext(ctx).StateStore
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
) (*ir.DAG, string, func(), error) {
	cleanup := func() {}
	workDir := req.WorkDir
	workspaceDir := ""
	target := req.DAG.Location
	if req.Workspace != nil {
		var workspaceTarget string
		var workspaceCleanup func()
		var err error
		workspaceDir, workspaceTarget, workspaceCleanup, err = materializeLocalWorkspace(req)
		if err != nil {
			return nil, "", nil, err
		}
		cleanup = workspaceCleanup
		workDir = workspaceDir
		target = workspaceTarget
	}

	loadOpts := inProcessLoadOptions(ctx, req, workDir)
	var (
		dag *ir.DAG
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
		return nil, "", nil, fmt.Errorf("failed to load child workflow DAG: %w", err)
	}
	// Build paths remain anchored to the authored definition when a local
	// child is reloaded from a temporary copy.
	if req.DAG.Type == ir.TypeBuild &&
		!req.DAG.WorkingDirExplicit &&
		!dag.WorkingDirExplicit &&
		req.DAG.WorkingDir != "" {
		dag.WorkingDir = req.DAG.WorkingDir
	}
	dag.SourceFile = req.DAG.SourceFile
	return dag, workspaceDir, cleanup, nil
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
	if req.DAG.SourceFile != "" {
		loadOpts = append(loadOpts, spec.WithSourceFile(req.DAG.SourceFile))
	}
	if cfg != nil {
		loadOpts = append(loadOpts, spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(cfg.Paths.DAGsDir)))
	}
	if req.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(req.Params))
	}
	if req.Workspace != nil && workDir != "" {
		loadOpts = append(loadOpts, spec.WithDefaultWorkingDir(workDir))
	}

	if baseConfig := subWorkflowBaseConfig(req); len(baseConfig) > 0 {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent(baseConfig))
	} else if req.Workspace == nil && cfg != nil && cfg.Paths.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(cfg.Paths.BaseConfig))
	}
	return loadOpts
}

func inProcessExtraEnvs(rCtx runctx.Context, req executor.SubWorkflowRequest) []string {
	envs := inheritedEnvForLocalRunner(rCtx.InheritedEnvs())
	if req.ParallelItem != "" {
		envs = append(envs, ir.ParallelItemVariable+"="+req.ParallelItem)
	}
	if req.ExternalStepRetry {
		envs = append(envs, runenv.EnvKeyExternalStepRetry+"=1")
	}
	return envs
}

func inProcessArtifactDir(ctx context.Context, dag *ir.DAG, baseDir, runID string, retryTarget *ir.DAGRunStatus) (string, error) {
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

func inProcessRetryTriggerType(status *ir.DAGRunStatus) ir.TriggerType {
	triggerType := queue.PreservedQueueTriggerType(status)
	if triggerType != ir.TriggerTypeUnknown {
		return triggerType
	}
	return ir.TriggerTypeRetry
}
