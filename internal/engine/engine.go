// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/dagrunstore"
	"github.com/dagucloud/dagu/internal/persis/filedag"
	"github.com/dagucloud/dagu/internal/persis/fileproc"
	"github.com/dagucloud/dagu/internal/persis/fileserviceregistry"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/spf13/viper"
)

type Engine struct {
	cfg             *config.Config
	dagRunStore     coreexec.DAGRunStore
	procStore       coreexec.ProcStore
	serviceRegistry coreexec.ServiceRegistry
	dagStore        coreexec.DAGStore
	dagRunMgr       runtime.Manager
	defaultMode     ExecutionMode
	distributed     DistributedOptions
	logger          logger.Logger
}

func New(ctx context.Context, opts Options) (*Engine, error) {
	cfg, err := loadConfig(opts)
	if err != nil {
		return nil, err
	}
	applyOptions(cfg, opts)

	log := logger.NewLogger(logger.WithQuiet())
	if opts.Logger != nil {
		log = newSlogAdapter(opts.Logger)
	}
	ctx = logger.WithLogger(config.WithConfig(ctx, cfg), log)

	if err := os.MkdirAll(cfg.Paths.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Paths.LogDir, 0o750); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Paths.ArtifactDir, 0o750); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}

	procStore := fileproc.New(
		cfg.Paths.ProcDir,
		fileproc.WithStaleThreshold(cfg.Proc.StaleThreshold),
		fileproc.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
		fileproc.WithHeartbeatSyncInterval(cfg.Proc.HeartbeatSyncInterval),
	)
	if err := procStore.Validate(ctx); err != nil {
		return nil, err
	}

	dagRunStore, err := dagrunstore.New(
		ctx,
		cfg,
		dagrunstore.WithRole(dagrunstore.RoleAgent),
		dagrunstore.WithLatestStatusToday(false),
		dagrunstore.WithLocation(cfg.Core.Location),
	)
	if err != nil {
		return nil, fmt.Errorf("initialize DAG-run store: %w", err)
	}
	cleanupDAGRunStore := true
	defer func() {
		if cleanupDAGRunStore {
			_ = coreexec.CloseDAGRunStore(context.Background(), dagRunStore)
		}
	}()
	serviceRegistry := fileserviceregistry.New(cfg.Paths.ServiceRegistryDir)
	dagRunMgr := runtime.NewManager(dagRunStore, procStore, cfg)

	dagStore, err := newDAGStore(cfg, nil, false)
	if err != nil {
		return nil, err
	}
	cleanupDAGRunStore = false

	mode := opts.DefaultMode
	if mode == "" {
		mode = ExecutionModeLocal
	}
	distributed := DistributedOptions{}
	if opts.Distributed != nil {
		distributed = cloneDistributedOptions(*opts.Distributed)
	}

	return &Engine{
		cfg:             cfg,
		dagRunStore:     dagRunStore,
		procStore:       procStore,
		serviceRegistry: serviceRegistry,
		dagStore:        dagStore,
		dagRunMgr:       dagRunMgr,
		defaultMode:     mode,
		distributed:     distributed,
		logger:          log,
	}, nil
}

func (e *Engine) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.serviceRegistry.Unregister(ctx)
	return coreexec.CloseDAGRunStore(ctx, e.dagRunStore)
}

func (e *Engine) context(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = config.WithConfig(ctx, e.cfg)
	if e.logger != nil {
		ctx = logger.WithLogger(ctx, e.logger)
	}
	return ctx
}

func loadConfig(opts Options) (*config.Config, error) {
	var loaderOpts []config.ConfigLoaderOption
	if opts.HomeDir != "" {
		loaderOpts = append(loaderOpts, config.WithAppHomeDir(opts.HomeDir))
	}
	if opts.ConfigFile != "" {
		loaderOpts = append(loaderOpts, config.WithConfigFile(opts.ConfigFile))
	}
	loader := config.NewConfigLoader(viper.New(), loaderOpts...)
	cfg, err := loader.Load()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyOptions(cfg *config.Config, opts Options) {
	cfg.Core.SkipExamples = true
	if opts.DAGsDir != "" {
		cfg.Paths.DAGsDir = resolvePath(opts.DAGsDir)
	}
	if opts.DataDir != "" {
		cfg.Paths.DataDir = resolvePath(opts.DataDir)
		cfg.Paths.DAGRunsDir = filepath.Join(cfg.Paths.DataDir, "dag-runs")
		cfg.Paths.ProcDir = filepath.Join(cfg.Paths.DataDir, "proc")
		cfg.Paths.QueueDir = filepath.Join(cfg.Paths.DataDir, "queue")
		cfg.Paths.ServiceRegistryDir = filepath.Join(cfg.Paths.DataDir, "service-registry")
		cfg.Paths.ContextsDir = filepath.Join(cfg.Paths.DataDir, "contexts")
	}
	if opts.LogDir != "" {
		cfg.Paths.LogDir = resolvePath(opts.LogDir)
	}
	if opts.ArtifactDir != "" {
		cfg.Paths.ArtifactDir = resolvePath(opts.ArtifactDir)
	}
	if opts.BaseConfig != "" {
		cfg.Paths.BaseConfig = resolvePath(opts.BaseConfig)
	}
	if opts.DefaultMode != "" {
		cfg.DefaultExecMode = config.ExecutionMode(opts.DefaultMode)
	}
	if opts.Distributed != nil {
		cfg.Worker.Coordinators = append([]string{}, opts.Distributed.Coordinators...)
	}
}

func cloneDistributedOptions(opts DistributedOptions) DistributedOptions {
	return DistributedOptions{
		Coordinators:    append([]string{}, opts.Coordinators...),
		TLS:             opts.TLS,
		WorkerSelector:  cloneStringMap(opts.WorkerSelector),
		PollInterval:    opts.PollInterval,
		MaxStatusErrors: opts.MaxStatusErrors,
	}
}

func resolvePath(path string) string {
	if resolved := fileutil.ResolvePathOrBlank(path); resolved != "" {
		return resolved
	}
	return path
}

func newDAGStore(cfg *config.Config, searchPaths []string, skipDirectoryCreation bool) (coreexec.DAGStore, error) {
	store := filedag.New(
		cfg.Paths.DAGsDir,
		filedag.WithFlagsBaseDir(cfg.Paths.SuspendFlagsDir),
		filedag.WithSearchPaths(searchPaths),
		filedag.WithBaseConfig(cfg.Paths.BaseConfig),
		filedag.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(cfg.Paths.DAGsDir)),
		filedag.WithSkipExamples(cfg.Core.SkipExamples),
		filedag.WithSkipDirectoryCreation(skipDirectoryCreation),
	)
	if s, ok := store.(*filedag.Storage); ok {
		if err := s.Initialize(); err != nil {
			return nil, fmt.Errorf("initialize DAG store: %w", err)
		}
	}
	return store, nil
}

func (e *Engine) coordinatorClient(opts DistributedOptions) (coordinator.Client, error) {
	if len(opts.Coordinators) == 0 {
		return nil, fmt.Errorf("distributed execution requires at least one coordinator address")
	}
	cfg := coordinator.DefaultConfig()
	cfg.CAFile = opts.TLS.ClientCAFile
	cfg.CertFile = opts.TLS.CertFile
	cfg.KeyFile = opts.TLS.KeyFile
	cfg.SkipTLSVerify = opts.TLS.SkipTLSVerify
	cfg.Insecure = opts.TLS.Insecure
	noTLSMaterial := cfg.CAFile == "" && cfg.CertFile == "" && cfg.KeyFile == ""
	if !cfg.Insecure && !cfg.SkipTLSVerify && noTLSMaterial {
		return nil, fmt.Errorf("coordinator TLS is not configured; provide TLS files or set TLS.Insecure=true for plaintext coordinator connections")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	registry, err := coordinator.NewStaticRegistry(opts.Coordinators)
	if err != nil {
		return nil, err
	}
	return coordinator.New(registry, cfg), nil
}

func runStatusToPublic(status *coreexec.DAGRunStatus) (*Status, error) {
	if status == nil {
		return nil, nil
	}
	startedAt, err := parseStatusTime(status.StartedAt)
	if err != nil {
		return nil, fmt.Errorf("parse startedAt: %w", err)
	}
	finishedAt, err := parseStatusTime(status.FinishedAt)
	if err != nil {
		return nil, fmt.Errorf("parse finishedAt: %w", err)
	}
	return &Status{
		Name:        status.Name,
		RunID:       status.DAGRunID,
		AttemptID:   status.AttemptID,
		Status:      status.Status.String(),
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Error:       status.Error,
		LogFile:     status.Log,
		ArchiveDir:  status.ArchiveDir,
		WorkerID:    status.WorkerID,
		TriggerType: status.TriggerType.String(),
	}, nil
}

func parseStatusTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func statusFromValue(status coreexec.DAGRunStatus) (*Status, error) {
	return runStatusToPublic(&status)
}

func isSuccess(status *Status) bool {
	return status != nil && (status.Status == core.Succeeded.String() || status.Status == core.PartiallySucceeded.String())
}
