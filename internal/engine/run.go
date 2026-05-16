// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	agentstores "github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/agentsnapshot"
	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/fileagentconfig"
	"github.com/dagucloud/dagu/internal/persis/fileagentmodel"
	"github.com/dagucloud/dagu/internal/persis/fileagentoauth"
	"github.com/dagucloud/dagu/internal/persis/fileagentsoul"
	"github.com/dagucloud/dagu/internal/persis/filememory"
	"github.com/dagucloud/dagu/internal/persis/filesecret"
	"github.com/dagucloud/dagu/internal/proto/convert"
	rtagent "github.com/dagucloud/dagu/internal/runtime/agent"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	"github.com/dagucloud/dagu/internal/workspace"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

func (e *Engine) RunFile(ctx context.Context, path string, opts RunOptions) (*Run, error) {
	ctx = e.context(ctx)
	dag, err := e.loadFile(ctx, path, opts)
	if err != nil {
		return nil, err
	}
	return e.runLoaded(ctx, dag, opts)
}

func (e *Engine) RunYAML(ctx context.Context, data []byte, opts RunOptions) (*Run, error) {
	ctx = e.context(ctx)
	dag, err := e.loadYAML(ctx, data, opts)
	if err != nil {
		return nil, err
	}
	return e.runLoaded(ctx, dag, opts)
}

func (e *Engine) Status(ctx context.Context, ref RunRef) (*Status, error) {
	ctx = e.context(ctx)
	if ref.Name == "" || ref.ID == "" {
		return nil, fmt.Errorf("run name and ID are required")
	}
	status, err := e.dagRunMgr.GetSavedStatus(ctx, coreexec.NewDAGRunRef(ref.Name, ref.ID))
	if err != nil {
		return nil, err
	}
	return runStatusToPublic(status)
}

func (e *Engine) Stop(ctx context.Context, ref RunRef) error {
	ctx = e.context(ctx)
	if ref.Name == "" || ref.ID == "" {
		return fmt.Errorf("run name and ID are required")
	}
	attempt, err := e.dagRunStore.FindAttempt(ctx, coreexec.NewDAGRunRef(ref.Name, ref.ID))
	if err != nil {
		return err
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return err
	}
	return e.dagRunMgr.Stop(ctx, dag, ref.ID)
}

func (r *Run) Ref() RunRef {
	return r.ref
}

func (r *Run) ID() string {
	return r.ref.ID
}

func (r *Run) Name() string {
	return r.ref.Name
}

func (r *Run) Wait(ctx context.Context) (*Status, error) {
	if r.mode == ExecutionModeDistributed {
		return r.waitDistributed(ctx)
	}
	return r.waitLocal(ctx)
}

func (r *Run) Status(ctx context.Context) (*Status, error) {
	if r.mode == ExecutionModeDistributed {
		resp, err := r.coordinator.GetDAGRunStatus(ctx, r.ref.Name, r.ref.ID, nil)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.Status == nil {
			return nil, nil
		}
		status, err := convert.ProtoToDAGRunStatus(resp.Status)
		if err != nil {
			return nil, err
		}
		return runStatusToPublic(status)
	}
	if r.agent != nil {
		select {
		case <-r.done:
		default:
			status := r.agent.Status(ctx)
			return statusFromValue(status)
		}
	}
	return r.engine.Status(ctx, r.ref)
}

func (r *Run) Stop(ctx context.Context) error {
	if r.mode == ExecutionModeDistributed {
		if r.cancel != nil {
			r.cancel()
		}
		err := r.coordinator.RequestCancel(ctx, r.ref.Name, r.ref.ID, nil)
		if cleanupErr := r.cleanupCoordinator(context.Background()); err == nil && cleanupErr != nil {
			err = cleanupErr
		}
		return err
	}
	if r.agent != nil {
		r.agent.Signal(ctx, syscall.SIGTERM)
	}
	if r.cancel != nil {
		r.cancel()
	}
	return r.engine.Stop(ctx, r.ref)
}

func (r *Run) waitLocal(ctx context.Context) (*Status, error) {
	select {
	case <-r.done:
		err := r.doneError()
		status, statusErr := r.statusWithFinalTimeout()
		if statusErr != nil && err == nil {
			err = statusErr
		}
		if err != nil {
			return status, err
		}
		if !isSuccess(status) {
			return status, fmt.Errorf("DAG run finished with status %s", status.Status)
		}
		return status, nil
	case <-ctx.Done():
		status, _ := r.statusWithFinalTimeout()
		return status, ctx.Err()
	}
}

func (r *Run) waitDistributed(ctx context.Context) (*Status, error) {
	defer func() {
		_ = r.cleanupCoordinator(context.Background())
	}()
	poll := r.engine.distributed.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	maxErrors := r.engine.distributed.MaxStatusErrors
	if maxErrors <= 0 {
		maxErrors = 10
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	consecutiveErrors := 0
	for {
		select {
		case <-ctx.Done():
			status, _ := r.statusWithFinalTimeout()
			return status, ctx.Err()
		case <-ticker.C:
			status, err := r.Status(ctx)
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= maxErrors {
					return nil, fmt.Errorf("lost connection to coordinator after %d attempts: %w", consecutiveErrors, err)
				}
				continue
			}
			consecutiveErrors = 0
			if status == nil {
				continue
			}
			if !isActiveStatus(status.Status) {
				if isSuccess(status) {
					r.markDone(nil)
					return status, nil
				}
				if status.Error != "" {
					err := fmt.Errorf("DAG run failed with status %s: %s", status.Status, status.Error)
					r.markDone(err)
					return status, err
				}
				err := fmt.Errorf("DAG run failed with status %s", status.Status)
				r.markDone(err)
				return status, err
			}
		}
	}
}

func (r *Run) statusWithFinalTimeout() (*Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.Status(ctx)
}

func (e *Engine) loadFile(ctx context.Context, path string, opts RunOptions) (*core.DAG, error) {
	loadOpts := e.loadOptions(opts)
	dag, err := spec.Load(ctx, path, loadOpts...)
	if err != nil {
		return nil, err
	}
	if dag.SourceFile == "" {
		dag.SourceFile = path
	}
	applyRunOverrides(dag, opts)
	return dag, nil
}

func (e *Engine) loadYAML(ctx context.Context, data []byte, opts RunOptions) (*core.DAG, error) {
	loadOpts := e.loadOptions(opts)
	dag, err := spec.LoadYAML(ctx, data, loadOpts...)
	if err != nil {
		return nil, err
	}
	applyRunOverrides(dag, opts)
	return dag, nil
}

func (e *Engine) loadOptions(opts RunOptions) []spec.LoadOption {
	loadOpts := []spec.LoadOption{
		spec.WithBaseConfig(e.cfg.Paths.BaseConfig),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(e.cfg.Paths.DAGsDir)),
		spec.WithDAGsDir(e.cfg.Paths.DAGsDir),
	}
	if opts.Name != "" {
		loadOpts = append(loadOpts, spec.WithName(opts.Name))
	}
	if opts.DefaultWorkingDir != "" {
		loadOpts = append(loadOpts, spec.WithDefaultWorkingDir(opts.DefaultWorkingDir))
	}
	if len(opts.ParamsList) > 0 {
		loadOpts = append(loadOpts, spec.WithParams(opts.ParamsList))
	} else {
		loadOpts = append(loadOpts, spec.WithParams(paramsMapToList(opts.Params)))
	}
	return loadOpts
}

func applyRunOverrides(dag *core.DAG, opts RunOptions) {
	// Name, DefaultWorkingDir, and params are handled during loading; only
	// overrides that must mutate the loaded DAG belong here.
	if len(opts.WorkerSelector) > 0 {
		dag.WorkerSelector = cloneStringMap(opts.WorkerSelector)
	}
	if len(opts.Labels) > 0 {
		seen := make(map[string]struct{}, len(dag.Labels)+len(opts.Labels))
		for _, existing := range dag.Labels {
			seen[existing.String()] = struct{}{}
		}
		for _, candidate := range core.NewLabels(opts.Labels) {
			key := candidate.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			dag.Labels = append(dag.Labels, candidate)
		}
	}
}

func (e *Engine) runLoaded(ctx context.Context, dag *core.DAG, opts RunOptions) (*Run, error) {
	if err := dag.Validate(); err != nil {
		return nil, err
	}
	runID := opts.RunID
	if runID == "" {
		id, err := e.dagRunMgr.GenDAGRunID(ctx)
		if err != nil {
			return nil, err
		}
		runID = id
	} else if err := coreexec.ValidateDAGRunID(runID); err != nil {
		return nil, err
	}
	mode := opts.Mode
	if mode == "" {
		mode = e.defaultMode
	}
	if mode == "" {
		mode = ExecutionModeLocal
	}
	switch mode {
	case ExecutionModeLocal:
		return e.runLocal(ctx, dag, runID, opts)
	case ExecutionModeDistributed:
		return e.runDistributed(ctx, dag, runID, opts)
	default:
		return nil, fmt.Errorf("unsupported execution mode %q", mode)
	}
}

func (e *Engine) runLocal(ctx context.Context, dag *core.DAG, runID string, opts RunOptions) (*Run, error) {
	logFile, err := e.openLogFile(ctx, dag, runID)
	if err != nil {
		return nil, err
	}
	artifactDir, err := e.artifactDir(ctx, dag, runID)
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	dagStore, err := newDAGStore(e.cfg, []string{filepath.Dir(dag.Location)}, false)
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	root := coreexec.NewDAGRunRef(dag.Name, runID)
	var prepared *localPreparation
	if !opts.DryRun {
		prepared, err = e.prepareLocal(ctx, dag, runID, root)
		if err != nil {
			_ = logFile.Close()
			return nil, err
		}
	}

	stores := e.agentStores(ctx)
	agentInstance := rtagent.New(
		runID,
		dag,
		filepath.Dir(logFile.Name()),
		logFile.Name(),
		e.dagRunMgr,
		dagStore,
		rtagent.Options{
			Dry:                        opts.DryRun,
			WorkerID:                   "local",
			PreparedAttempt:            preparedAttempt(prepared),
			DAGRunStore:                e.dagRunStore,
			SecretStore:                stores.SecretStore,
			ServiceRegistry:            e.serviceRegistry,
			RootDAGRun:                 root,
			PeerConfig:                 e.cfg.Core.Peer,
			TriggerType:                core.TriggerTypeManual,
			DefaultExecMode:            configExecutionMode(e.defaultMode),
			AgentConfigStore:           stores.ConfigStore,
			AgentModelStore:            stores.ModelStore,
			AgentMemoryStore:           stores.MemoryStore,
			AgentSoulStore:             stores.SoulStore,
			AgentOAuthManager:          stores.OAuthManager,
			AgentRemoteContextResolver: stores.ContextResolver,
			ArtifactDir:                artifactDir,
			ExtraEnvs: dagutools.ToolDirEnvVars(dagutools.InstallOptions{
				ToolsDir: e.cfg.Paths.ToolsDir,
				DataDir:  e.cfg.Paths.DataDir,
			}),
		},
	)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	run := &Run{
		engine: e,
		ref:    RunRef{Name: dag.Name, ID: runID},
		mode:   ExecutionModeLocal,
		cancel: cancel,
		done:   done,
		agent:  agentInstance,
		dag:    dag,
	}

	go func() {
		var err error
		defer func() {
			_ = logFile.Close()
			if prepared != nil && prepared.proc != nil {
				_ = prepared.proc.Stop(context.Background())
			}
			run.markDone(err)
		}()
		runCtx = logger.WithLogger(config.WithConfig(runCtx, e.cfg), e.logger)
		err = agentInstance.Run(runCtx)
		if err != nil {
			logger.Error(runCtx, "Embedded DAG run failed",
				tag.DAG(dag.Name),
				tag.RunID(runID),
				tag.Error(err),
			)
		}
	}()

	return run, nil
}

func (e *Engine) runDistributed(ctx context.Context, dag *core.DAG, runID string, opts RunOptions) (*Run, error) {
	dist := e.distributed
	if len(opts.WorkerSelector) > 0 {
		dist.WorkerSelector = cloneStringMap(opts.WorkerSelector)
	}
	client, err := e.coordinatorClient(dist)
	if err != nil {
		return nil, err
	}
	taskOpts := []runtimeexec.TaskOption{
		runtimeexec.WithBaseConfig(runtimeexec.ResolveBaseConfig(dag.BaseConfigData, e.cfg.Paths.BaseConfig)),
	}
	if len(dist.WorkerSelector) > 0 {
		taskOpts = append(taskOpts, runtimeexec.WithWorkerSelector(dist.WorkerSelector))
	}
	if len(dag.Labels) > 0 {
		taskOpts = append(taskOpts, runtimeexec.WithLabels(strings.Join(dag.Labels.Strings(), ",")))
	}
	if dag.SourceFile != "" {
		taskOpts = append(taskOpts, runtimeexec.WithSourceFile(dag.SourceFile))
	}
	if snapshot, snapErr := agentsnapshot.BuildFromPaths(ctx, dag, e.cfg.Paths, e.dagStore); snapErr != nil {
		_ = client.Cleanup(ctx)
		return nil, fmt.Errorf("build agent snapshot: %w", snapErr)
	} else if len(snapshot) > 0 {
		taskOpts = append(taskOpts, runtimeexec.WithAgentSnapshot(snapshot))
	}
	task := runtimeexec.CreateTask(
		dag.Name,
		string(dag.YamlData),
		coordinatorv1.Operation_OPERATION_START,
		runID,
		taskOpts...,
	)
	if len(dag.Params) > 0 {
		task.Params = strings.Join(dag.Params, " ")
	}
	if err := client.Dispatch(ctx, task); err != nil {
		_ = client.Cleanup(ctx)
		return nil, fmt.Errorf("dispatch DAG run: %w", err)
	}
	run := &Run{
		engine:      e,
		ref:         RunRef{Name: dag.Name, ID: runID},
		mode:        ExecutionModeDistributed,
		done:        make(chan struct{}),
		dag:         dag,
		coordinator: client,
	}
	goruntime.SetFinalizer(run, func(r *Run) {
		_ = r.cleanupCoordinator(context.Background())
	})
	return run, nil
}

func (r *Run) markDone(err error) {
	r.setDoneError(err)
	if r.done == nil {
		return
	}
	r.doneOnce.Do(func() {
		close(r.done)
	})
}

func (r *Run) cleanupCoordinator(ctx context.Context) error {
	if r == nil || r.coordinator == nil {
		return nil
	}
	err := r.coordinator.Cleanup(ctx)
	goruntime.SetFinalizer(r, nil)
	return err
}

func (r *Run) setDoneError(err error) {
	r.doneErrMu.Lock()
	defer r.doneErrMu.Unlock()
	r.doneErr = err
}

func (r *Run) doneError() error {
	r.doneErrMu.RLock()
	defer r.doneErrMu.RUnlock()
	return r.doneErr
}

func (e *Engine) prepareLocal(ctx context.Context, dag *core.DAG, runID string, root coreexec.DAGRunRef) (*localPreparation, error) {
	if err := e.procStore.Lock(ctx, dag.ProcGroup()); err != nil {
		return nil, fmt.Errorf("lock process group: %w", err)
	}
	defer e.procStore.Unlock(ctx, dag.ProcGroup())

	attempt, err := e.dagRunStore.CreateAttempt(ctx, dag, time.Now(), runID, coreexec.NewDAGRunAttemptOptions{})
	if err != nil {
		if errors.Is(err, coreexec.ErrDAGRunAlreadyExists) {
			return nil, fmt.Errorf("dag-run ID %s already exists for DAG %s: %w", runID, dag.Name, err)
		}
		return nil, fmt.Errorf("create DAG run attempt: %w", err)
	}
	attempt.SetDAG(dag)
	proc, err := e.procStore.Acquire(ctx, dag.ProcGroup(), coreexec.ProcMeta{
		StartedAt:    time.Now().Unix(),
		Name:         dag.Name,
		DAGRunID:     runID,
		AttemptID:    attempt.ID(),
		RootName:     root.Name,
		RootDAGRunID: root.ID,
	})
	if err != nil {
		_ = e.recordPreparedFailure(ctx, attempt, dag, runID, root, err)
		return nil, fmt.Errorf("acquire process handle: %w", err)
	}
	return &localPreparation{attempt: attempt, proc: proc}, nil
}

func (e *Engine) recordPreparedFailure(
	ctx context.Context,
	attempt coreexec.DAGRunAttempt,
	dag *core.DAG,
	runID string,
	root coreexec.DAGRunRef,
	runErr error,
) error {
	logFile, logErr := logpath.Generate(ctx, e.cfg.Paths.LogDir, dag.LogDir, dag.Name, runID)
	if logErr != nil {
		logger.Warn(ctx, "Failed to generate log path for prepared local execution failure", tag.Error(logErr))
	}
	artifactDir, artifactErr := e.artifactDir(ctx, dag, runID)
	if artifactErr != nil {
		logger.Warn(ctx, "Failed to generate artifact path for prepared local execution failure", tag.Error(artifactErr))
	}
	status := transform.NewStatusBuilder(dag).Create(
		runID,
		core.Failed,
		0,
		time.Now(),
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(root, coreexec.DAGRunRef{}),
		transform.WithLogFilePath(logFile),
		transform.WithArchiveDir(artifactDir),
		transform.WithFinishedAt(time.Now()),
		transform.WithError(runErr.Error()),
		transform.WithWorkerID("local"),
		transform.WithTriggerType(core.TriggerTypeManual),
	)
	if err := attempt.Open(ctx); err != nil {
		return err
	}
	defer func() {
		_ = attempt.Close(ctx)
	}()
	return attempt.Write(ctx, status)
}

func (e *Engine) openLogFile(ctx context.Context, dag *core.DAG, runID string) (*os.File, error) {
	path, err := logpath.Generate(ctx, e.cfg.Paths.LogDir, dag.LogDir, dag.Name, runID)
	if err != nil {
		return nil, err
	}
	return fileutil.OpenOrCreateFile(path)
}

func (e *Engine) artifactDir(ctx context.Context, dag *core.DAG, runID string) (string, error) {
	if dag == nil || !dag.ArtifactsEnabled() {
		return "", nil
	}
	dagArtifactDir := ""
	if dag.Artifacts != nil {
		dagArtifactDir = dag.Artifacts.Dir
	}
	return logpath.GenerateDir(ctx, e.cfg.Paths.ArtifactDir, dagArtifactDir, dag.Name, runID)
}

type agentStoresResult struct {
	ConfigStore     agentstores.ConfigStore
	ModelStore      agentstores.ModelStore
	MemoryStore     agentstores.MemoryStore
	SoulStore       agentstores.SoulStore
	OAuthManager    *agentoauth.Manager
	ContextResolver agentstores.RemoteContextResolver
	SecretStore     secretpkg.Store
}

func (e *Engine) agentStores(ctx context.Context) agentStoresResult {
	var result agentStoresResult
	if store, err := filesecret.NewFromDataDir(e.cfg.Paths.DataDir); err == nil {
		result.SecretStore = store
	} else {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(err))
	}
	if store, err := fileagentconfig.New(e.cfg.Paths.DataDir); err == nil {
		result.ConfigStore = store
	} else {
		logger.Warn(ctx, "Failed to create agent config store", tag.Error(err))
	}
	if store, err := fileagentmodel.New(filepath.Join(e.cfg.Paths.DataDir, "agent", "models")); err == nil {
		result.ModelStore = store
	} else {
		logger.Warn(ctx, "Failed to create agent model store", tag.Error(err))
	}
	if store, err := filememory.New(e.cfg.Paths.DAGsDir); err == nil {
		result.MemoryStore = store
	} else {
		logger.Warn(ctx, "Failed to create agent memory store", tag.Error(err))
	}
	if store, err := fileagentsoul.New(ctx, filepath.Join(e.cfg.Paths.DAGsDir, "souls")); err == nil {
		result.SoulStore = store
	} else {
		logger.Warn(ctx, "Failed to create agent soul store", tag.Error(err))
	}
	if manager, err := fileagentoauth.NewManager(e.cfg.Paths.DataDir); err == nil {
		result.OAuthManager = manager
	} else {
		logger.Warn(ctx, "Failed to create agent OAuth manager", tag.Error(err))
	}
	if resolver, err := e.buildRemoteContextResolver(); err == nil {
		result.ContextResolver = resolver
	} else {
		logger.Warn(ctx, "Failed to create agent remote context resolver", tag.Error(err))
	}
	return result
}

func (e *Engine) buildRemoteContextResolver() (agentstores.RemoteContextResolver, error) {
	encKey, err := crypto.ResolveKey(e.cfg.Paths.DataDir)
	if err != nil {
		return nil, err
	}
	enc, err := crypto.NewEncryptor(encKey)
	if err != nil {
		return nil, err
	}
	store, err := clicontext.NewStore(e.cfg.Paths.ContextsDir, enc)
	if err != nil {
		return nil, err
	}
	return &agentstores.RemoteContextResolverAdapter{Store: store}, nil
}

func preparedAttempt(prepared *localPreparation) coreexec.DAGRunAttempt {
	if prepared == nil {
		return nil
	}
	return prepared.attempt
}

func paramsMapToList(params map[string]string) []string {
	if len(params) == 0 {
		return nil
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(params))
	for _, key := range keys {
		out = append(out, key+"="+params[key])
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out
}

func configExecutionMode(mode ExecutionMode) config.ExecutionMode {
	if mode == "" {
		return config.ExecutionModeLocal
	}
	return config.ExecutionMode(mode)
}

func isActiveStatus(status string) bool {
	switch status {
	case core.Running.String(), core.Queued.String(), core.Waiting.String(), core.NotStarted.String():
		return true
	default:
		return false
	}
}
