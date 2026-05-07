// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/signalctx"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis/fileagentconfig"
	"github.com/dagucloud/dagu/internal/persis/fileagentmodel"
	"github.com/dagucloud/dagu/internal/persis/fileagentoauth"

	"github.com/dagucloud/dagu/internal/persis/controlplanestore"
	"github.com/dagucloud/dagu/internal/persis/dagrunstore"
	"github.com/dagucloud/dagu/internal/persis/fileagentsoul"
	"github.com/dagucloud/dagu/internal/persis/filebaseconfig"
	"github.com/dagucloud/dagu/internal/persis/filedag"
	"github.com/dagucloud/dagu/internal/persis/filedistributed"
	"github.com/dagucloud/dagu/internal/persis/fileeventstore"
	"github.com/dagucloud/dagu/internal/persis/filegithubdispatch"
	"github.com/dagucloud/dagu/internal/persis/filelicense"
	"github.com/dagucloud/dagu/internal/persis/filememory"
	"github.com/dagucloud/dagu/internal/persis/fileproc"
	"github.com/dagucloud/dagu/internal/persis/filequeue"
	"github.com/dagucloud/dagu/internal/persis/fileserviceregistry"
	"github.com/dagucloud/dagu/internal/persis/filewatermark"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/service/frontend"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/internal/service/resource"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Context holds the configuration for a command.
type Context struct {
	context.Context

	Command *cobra.Command
	Flags   []commandLineFlag
	Config  *config.Config
	Quiet   bool
	Scope   commandScope

	EventService              *eventstore.Service
	EventSourceInstance       string
	ControlPlaneStore         controlplanestore.Store
	DAGRunStore               exec.DAGRunStore
	DAGRunMgr                 runtime.Manager
	ProcStore                 exec.ProcStore
	QueueStore                exec.QueueStore
	ServiceRegistry           exec.ServiceRegistry
	DispatchTaskStore         exec.DispatchTaskStore
	WorkerHeartbeatStore      exec.WorkerHeartbeatStore
	DAGRunLeaseStore          exec.DAGRunLeaseStore
	ActiveDistributedRunStore exec.ActiveDistributedRunStore

	Proc           exec.ProcHandle
	LicenseManager *license.Manager
	ContextStore   *clicontext.Store
	CLIContext     *clicontext.Context
	ContextName    string
	Remote         *remoteClient
}

// WithContext returns a new Context with a different underlying context.Context.
// This is useful for creating a signal-aware context for service operations.
func (c *Context) WithContext(ctx context.Context) *Context {
	return &Context{
		Context:                   ctx,
		Command:                   c.Command,
		Flags:                     c.Flags,
		Config:                    c.Config,
		Quiet:                     c.Quiet,
		EventService:              c.EventService,
		EventSourceInstance:       c.EventSourceInstance,
		ControlPlaneStore:         c.ControlPlaneStore,
		DAGRunStore:               c.DAGRunStore,
		DAGRunMgr:                 c.DAGRunMgr,
		ProcStore:                 c.ProcStore,
		QueueStore:                c.QueueStore,
		ServiceRegistry:           c.ServiceRegistry,
		DispatchTaskStore:         c.DispatchTaskStore,
		WorkerHeartbeatStore:      c.WorkerHeartbeatStore,
		DAGRunLeaseStore:          c.DAGRunLeaseStore,
		ActiveDistributedRunStore: c.ActiveDistributedRunStore,
		Proc:                      c.Proc,
		LicenseManager:            c.LicenseManager,
		ContextStore:              c.ContextStore,
		CLIContext:                c.CLIContext,
		ContextName:               c.ContextName,
		Remote:                    c.Remote,
		Scope:                     c.Scope,
	}
}

// WithEventSource returns a shallow copy whose context carries the given event source.
// If the event store is not configured, the original context is preserved.
func (c *Context) WithEventSource(service string) *Context {
	if c == nil || c.EventService == nil {
		return c
	}
	return c.WithContext(eventstore.WithContext(c.Context, c.EventService, eventstore.Source{
		Service:  service,
		Instance: c.EventSourceInstance,
	}))
}

// LogToFile creates a new logger context with a file writer.
func (c *Context) LogToFile(f *os.File) {
	var opts []logger.Option
	if c.Config.Core.Debug {
		opts = append(opts, logger.WithDebug())
	}
	if c.Quiet {
		opts = append(opts, logger.WithQuiet())
	}
	if c.Config.Core.LogFormat != "" {
		opts = append(opts, logger.WithFormat(c.Config.Core.LogFormat))
	}
	if f != nil {
		opts = append(opts, logger.WithWriter(f))
	}
	c.Context = logger.WithLogger(c.Context, logger.NewLogger(opts...))
}

// Close releases resources owned by the command context.
func (c *Context) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if c.ControlPlaneStore != nil {
		return c.ControlPlaneStore.Close()
	}
	return exec.CloseDAGRunStore(ctx, c.DAGRunStore)
}

// NewContext creates and initializes an application Context for the given Cobra command.
// It binds command flags, loads configuration scoped to the command, configures logging
// (respecting debug, quiet, and log format settings), logs any configuration warnings,
// and initializes history, DAG run, proc, queue, and service registry stores and managers.
// Returns an initialized Context or an error if flag retrieval, configuration loading,
// or other initialization steps fail.
func NewContext(cmd *cobra.Command, flags []commandLineFlag) (*Context, error) {
	ctx := cmd.Context()
	commandName := commandFamilyName(cmd)
	scope := scopeForCommand(commandName)

	v := viper.New()
	bindFlags(v, cmd, flags...)

	quiet, err := cmd.Flags().GetBool("quiet")
	if err != nil {
		return nil, fmt.Errorf("failed to get quiet flag: %w", err)
	}
	daguHome, err := cmd.Flags().GetString("dagu-home")
	if err != nil {
		return nil, fmt.Errorf("failed to get dagu-home flag: %w", err)
	}

	var configLoaderOpts []config.ConfigLoaderOption
	if daguHome != "" {
		if resolvedHome := fileutil.ResolvePathOrBlank(daguHome); resolvedHome != "" {
			configLoaderOpts = append(configLoaderOpts, config.WithAppHomeDir(resolvedHome))
		}
	}

	// Use a custom config file if provided via the command flag "config"
	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to get config flag: %w", err)
	}
	if cfgPath != "" {
		configLoaderOpts = append(configLoaderOpts, config.WithConfigFile(cfgPath))
	}

	// Set service type based on command to load only necessary config sections
	configLoaderOpts = append(configLoaderOpts, config.WithService(serviceForCommand(commandName)))

	loader := config.NewConfigLoader(v, configLoaderOpts...)
	cfg, err := loader.Load()
	if err != nil {
		return nil, err
	}
	ctx = config.WithConfig(ctx, cfg)

	requestedContextName, err := requestedCLIContextName(cmd)
	if err != nil {
		return nil, err
	}
	selectedContextName := clicontext.LocalContextName
	selectedContext := &clicontext.Context{Name: clicontext.LocalContextName}
	var (
		contextStore        *clicontext.Store
		contextStoreWarning error
	)

	if isContextCommand(cmd) || scope != commandScopeStatic {
		contextStore, err = newCLIContextStore(cfg.Paths.DataDir, cfg.Paths.ContextsDir)
		if err != nil {
			if shouldFailForContextStoreError(cmd, scope, requestedContextName) {
				return nil, fmt.Errorf("failed to initialize context store: %w", err)
			}
			contextStoreWarning = fmt.Errorf("failed to initialize context store, using local context: %w", err)
		} else if !isContextCommand(cmd) {
			selectedContextName, selectedContext, err = resolveCLIContext(cmd, contextStore, requestedContextName)
			if err != nil {
				if shouldFailForContextResolutionError(scope, requestedContextName) {
					return nil, err
				}
				contextStoreWarning = fmt.Errorf("failed to resolve context selection, using local context: %w", err)
				selectedContextName = clicontext.LocalContextName
				selectedContext = &clicontext.Context{Name: clicontext.LocalContextName}
			}
		}
	}
	if scope == commandScopeLocalOnly && selectedContextName != clicontext.LocalContextName {
		return nil, fmt.Errorf("command %q only supports the local context", cmd.Name())
	}

	// Create a logger context based on config and quiet mode
	var opts []logger.Option
	if cfg.Core.Debug || os.Getenv("DEBUG") != "" {
		opts = append(opts, logger.WithDebug())
	}
	if quiet {
		opts = append(opts, logger.WithQuiet())
	}
	// For agent commands running in a terminal, suppress console output early
	// to avoid debug logs cluttering the progress display or tree output
	if !quiet && isAgentCommand(cmd.Name()) && term.IsTerminal(int(os.Stderr.Fd())) && os.Getenv("DISABLE_PROGRESS") == "" {
		opts = append(opts, logger.WithQuiet())
	}
	if cfg.Core.LogFormat != "" {
		opts = append(opts, logger.WithFormat(cfg.Core.LogFormat))
	}
	ctx = logger.WithLogger(ctx, logger.NewLogger(opts...))

	// Log any warnings collected during configuration loading
	for _, notice := range cfg.Notices {
		logger.Info(ctx, notice)
	}
	for _, warning := range cfg.Warnings {
		logger.Warn(ctx, warning)
	}
	if contextStoreWarning != nil {
		logger.Warn(ctx, contextStoreWarning.Error())
	}

	baseCtx := ctx
	eventSourceInstance := eventstore.DefaultSourceInstance()
	var eventSvc *eventstore.Service
	sharedNothingWorker := isSharedNothingWorker(cmd, cfg)
	if !sharedNothingWorker && cfg.EventStore.Enabled {
		store, eventErr := fileeventstore.New(cfg.Paths.EventStoreDir)
		if eventErr != nil {
			logger.Warn(ctx, "Failed to initialize event store; continuing without event persistence", tag.Error(eventErr))
		} else {
			eventSvc = eventstore.New(store)
			ctx = eventstore.WithContext(ctx, eventSvc, eventstore.Source{
				Service:  eventSourceServiceForCommand(cmd.Name()),
				Instance: eventSourceInstance,
			})
		}
	}

	if scope == commandScopeContextAware && selectedContextName != clicontext.LocalContextName {
		remote, err := newRemoteClient(selectedContext)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize remote context %q: %w", selectedContextName, err)
		}
		return &Context{
			Context:             ctx,
			Command:             cmd,
			Config:              cfg,
			Quiet:               quiet,
			Flags:               flags,
			EventService:        eventSvc,
			EventSourceInstance: eventSourceInstance,
			ContextStore:        contextStore,
			CLIContext:          selectedContext,
			ContextName:         selectedContextName,
			Remote:              remote,
			Scope:               scope,
		}, nil
	}

	// For shared-nothing workers, skip creating file-based stores
	// as they only use temporary directories and push status to coordinator
	if sharedNothingWorker {
		logger.Debug(ctx, "Shared-nothing worker mode: skipping file-based stores",
			slog.Any("coordinators", cfg.Worker.Coordinators),
		)
		return &Context{
			Context:             baseCtx,
			Command:             cmd,
			Config:              cfg,
			Quiet:               quiet,
			Flags:               flags,
			EventService:        nil,
			EventSourceInstance: eventSourceInstance,
			ContextStore:        contextStore,
			CLIContext:          selectedContext,
			ContextName:         selectedContextName,
			Scope:               scope,
			// All stores are nil - shared-nothing workers don't need local storage
			// Status is pushed to coordinator, DAG definitions come from task payload
		}, nil
	}

	ps := fileproc.New(cfg.Paths.ProcDir,
		fileproc.WithStaleThreshold(cfg.Proc.StaleThreshold),
		fileproc.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
		fileproc.WithHeartbeatSyncInterval(cfg.Proc.HeartbeatSyncInterval),
	)
	if err := ps.Validate(ctx); err != nil {
		return nil, fmt.Errorf("failed to validate proc directory %s: %w", cfg.Paths.ProcDir, err)
	}
	distributedDir := filepath.Join(cfg.Paths.DataDir, "distributed")
	var dagRunLeaseStore exec.DAGRunLeaseStore = filedistributed.NewDAGRunLeaseStore(distributedDir)
	var activeDistributedRunStore exec.ActiveDistributedRunStore = filedistributed.NewActiveDistributedRunStore(distributedDir)
	var (
		controlStore controlplanestore.Store
		drs          exec.DAGRunStore
		drm          runtime.Manager
	)
	if contextNeedsDAGRunStore(cmd) && !shouldDeferAgentDAGRunStore(cmd, cfg) {
		var err error
		if shouldOpenContextControlPlaneStore(cmd, cfg) {
			controlStore, err = newContextControlPlaneStore(baseCtx, ctx, cmd, cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize control-plane store: %w", err)
			}
			drs = controlStore.DAGRuns()
		}
		if drs == nil {
			drs, err = newContextDAGRunStore(baseCtx, ctx, cmd, cfg)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize DAG-run store: %w", err)
			}
		}
		if drs != nil {
			drm = runtime.NewManager(drs, ps, cfg)
		}
	}
	qs := filequeue.New(cfg.Paths.QueueDir)
	var sm exec.ServiceRegistry = fileserviceregistry.New(cfg.Paths.ServiceRegistryDir)
	var dispatchTaskStore exec.DispatchTaskStore = filedistributed.NewDispatchTaskStore(distributedDir)
	var workerHeartbeatStore exec.WorkerHeartbeatStore = filedistributed.NewWorkerHeartbeatStore(distributedDir)
	if controlStore != nil {
		qs = controlStore.Queue()
		sm = controlStore.Services()
		dispatchTaskStore = controlStore.DispatchTasks()
		workerHeartbeatStore = controlStore.WorkerHeartbeats()
		dagRunLeaseStore = controlStore.DAGRunLeases()
		activeDistributedRunStore = controlStore.ActiveDistributedRuns()
	}
	if cfg.EventStore.Enabled && controlStore != nil {
		eventSvc = eventstore.New(controlStore.Events())
		ctx = eventstore.WithContext(ctx, eventSvc, eventstore.Source{
			Service:  eventSourceServiceForCommand(cmd.Name()),
			Instance: eventSourceInstance,
		})
	}

	// Initialize license manager for server commands
	var licMgr *license.Manager
	switch cmd.Name() {
	case "server", "start-all":
		pubKey, pubKeyErr := license.PublicKey()
		if pubKeyErr != nil {
			logger.Warn(ctx, "Failed to load license public key", tag.Error(pubKeyErr))
			break
		}
		licenseDir := filepath.Join(cfg.Paths.DataDir, "license")
		licStore := filelicense.New(licenseDir)
		licMgr = license.NewManager(license.ManagerConfig{
			LicenseDir: licenseDir,
			ConfigKey:  cfg.License.Key,
			CloudURL:   cfg.License.CloudURL,
		}, pubKey, licStore, slog.Default())
		if err := licMgr.Start(ctx); err != nil {
			logger.Warn(ctx, "License manager initialization failed", tag.Error(err))
		}
	}

	// Log key configuration settings for debugging
	logger.Debug(ctx, "Configuration loaded",
		tag.Config(cfg.Paths.ConfigFileUsed),
		tag.Dir(cfg.Paths.DAGsDir),
	)
	logger.Debug(ctx, "Paths configuration",
		slog.String("log-dir", cfg.Paths.LogDir),
		slog.String("data-dir", cfg.Paths.DataDir),
		slog.String("dag-runs-dir", cfg.Paths.DAGRunsDir),
	)

	// Initialize default base config if it doesn't exist
	if cfg.Paths.BaseConfig != "" {
		bcStore, bcErr := filebaseconfig.New(cfg.Paths.BaseConfig,
			filebaseconfig.WithSkipDefault(cfg.Core.SkipExamples),
		)
		if bcErr != nil {
			logger.Warn(ctx, "Failed to create base config store", tag.Error(bcErr))
		} else {
			if initErr := bcStore.Initialize(); initErr != nil {
				logger.Warn(ctx, "Failed to initialize default base config", tag.Error(initErr))
			}
		}
	}

	return &Context{
		Context:                   ctx,
		Command:                   cmd,
		Config:                    cfg,
		Quiet:                     quiet,
		EventService:              eventSvc,
		EventSourceInstance:       eventSourceInstance,
		ControlPlaneStore:         controlStore,
		DAGRunStore:               drs,
		DAGRunMgr:                 drm,
		Flags:                     flags,
		ProcStore:                 ps,
		QueueStore:                qs,
		ServiceRegistry:           sm,
		DispatchTaskStore:         dispatchTaskStore,
		WorkerHeartbeatStore:      workerHeartbeatStore,
		DAGRunLeaseStore:          dagRunLeaseStore,
		ActiveDistributedRunStore: activeDistributedRunStore,
		LicenseManager:            licMgr,
		ContextStore:              contextStore,
		CLIContext:                selectedContext,
		ContextName:               selectedContextName,
		Scope:                     scope,
	}, nil
}

func newCLIContextStore(dataDir, contextsDir string) (*clicontext.Store, error) {
	encKey, err := crypto.ResolveKey(dataDir)
	if err != nil {
		return nil, err
	}
	enc, err := crypto.NewEncryptor(encKey)
	if err != nil {
		return nil, err
	}
	return clicontext.NewStore(contextsDir, enc)
}

func commandFamilyName(cmd *cobra.Command) string {
	if isContextCommand(cmd) {
		return "context"
	}
	if isAgentCLICommand(cmd) {
		return "agent"
	}
	return cmd.Name()
}

func isContextCommand(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Name() == "context" {
			return true
		}
	}
	return false
}

func isAgentCLICommand(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Name() == "agent" {
			return true
		}
	}
	return false
}

func requestedCLIContextName(cmd *cobra.Command) (string, error) {
	if cmd.Flags().Lookup("context") == nil {
		return "", nil
	}
	contextName, err := cmd.Flags().GetString("context")
	if err != nil {
		return "", fmt.Errorf("failed to get context flag: %w", err)
	}
	return strings.TrimSpace(contextName), nil
}

func resolveCLIContext(cmd *cobra.Command, store *clicontext.Store, requested string) (string, *clicontext.Context, error) {
	contextName := strings.TrimSpace(requested)
	var err error
	if contextName == "" {
		contextName, err = store.Current(cmd.Context())
		if err != nil {
			return "", nil, fmt.Errorf("failed to resolve current context: %w", err)
		}
	}
	if contextName == "" {
		contextName = clicontext.LocalContextName
	}
	ctx, err := store.Get(cmd.Context(), contextName)
	if err != nil {
		return "", nil, fmt.Errorf("failed to resolve context %q: %w", contextName, err)
	}
	return contextName, ctx, nil
}

func shouldFailForContextStoreError(cmd *cobra.Command, scope commandScope, requested string) bool {
	if isContextCommand(cmd) {
		return true
	}
	if scope == commandScopeStatic {
		return false
	}
	return requested != "" && requested != clicontext.LocalContextName
}

func shouldFailForContextResolutionError(scope commandScope, requested string) bool {
	if requested == "" {
		return false
	}
	if requested == clicontext.LocalContextName {
		return false
	}
	return scope != commandScopeStatic
}

func (c *Context) IsRemote() bool {
	return c != nil && c.Remote != nil && c.ContextName != clicontext.LocalContextName
}

func eventSourceServiceForCommand(cmdName string) string {
	switch cmdName {
	case "scheduler":
		return eventstore.SourceServiceScheduler
	case "server":
		return eventstore.SourceServiceServer
	case "coordinator":
		return eventstore.SourceServiceCoordinator
	default:
		return eventstore.SourceServiceCLI
	}
}

// serviceForCommand determines which config.Service to load for a given command name.
// Returns the appropriate service type for the command, or ServiceNone to load all config.
func serviceForCommand(cmdName string) config.Service {
	switch cmdName {
	case "server":
		return config.ServiceServer
	case "scheduler":
		return config.ServiceScheduler
	case "worker":
		return config.ServiceWorker
	case "coordinator":
		return config.ServiceCoordinator
	case "start", "restart", "retry", "dry", "exec", "agent":
		return config.ServiceAgent
	default:
		// For all other commands (status, stop, validate, etc.), load all config
		return config.ServiceNone
	}
}

func controlPlaneStoreRoleForCommand(cmd *cobra.Command) controlplanestore.Role {
	if cmd == nil {
		return controlplanestore.RoleServer
	}
	switch cmd.Name() {
	case "scheduler":
		return controlplanestore.RoleScheduler
	case "start", "restart", "retry", "dry", "exec", "worker":
		return controlplanestore.RoleAgent
	default:
		return controlplanestore.RoleServer
	}
}

func contextNeedsDAGRunStore(cmd *cobra.Command) bool {
	if cmd == nil || isContextCommand(cmd) || isSubcommandOf(cmd, "sync") {
		return false
	}
	switch cmd.Name() {
	case "server", "start-all", "coordinator",
		"start", "restart", "retry", "dry", "exec",
		"status", "history", "stop", "enqueue", "dequeue", "cleanup", "migrate":
		return true
	default:
		return false
	}
}

func isSubcommandOf(cmd *cobra.Command, name string) bool {
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Name() == name {
			return true
		}
	}
	return false
}

func newContextDAGRunStore(baseCtx, cacheCtx context.Context, cmd *cobra.Command, cfg *config.Config) (exec.DAGRunStore, error) {
	drsOpts := []dagrunstore.Option{
		dagrunstore.WithLatestStatusToday(cfg.Server.LatestStatusToday),
		dagrunstore.WithLocation(cfg.Core.Location),
		dagrunstore.WithRole(controlplanestore.Role(controlPlaneStoreRoleForCommand(cmd))),
	}

	switch cmd.Name() {
	case "server", "start-all", "coordinator", "scheduler":
		limits := cfg.Cache.Limits()
		hc := fileutil.NewCache[*exec.DAGRunStatus]("dag_run_status", limits.DAGRun.Limit, limits.DAGRun.TTL)
		hc.StartEviction(cacheCtx)
		drsOpts = append(drsOpts, dagrunstore.WithHistoryFileCache(hc))
	}

	return dagrunstore.New(baseCtx, cfg, drsOpts...)
}

func newContextControlPlaneStore(baseCtx, cacheCtx context.Context, cmd *cobra.Command, cfg *config.Config) (controlplanestore.Store, error) {
	opts := []controlplanestore.Option{
		controlplanestore.WithLatestStatusToday(cfg.Server.LatestStatusToday),
		controlplanestore.WithLocation(cfg.Core.Location),
		controlplanestore.WithRole(controlPlaneStoreRoleForCommand(cmd)),
	}

	switch cmd.Name() {
	case "server", "start-all", "coordinator", "scheduler":
		limits := cfg.Cache.Limits()
		hc := fileutil.NewCache[*exec.DAGRunStatus]("dag_run_status", limits.DAGRun.Limit, limits.DAGRun.TTL)
		hc.StartEviction(cacheCtx)
		opts = append(opts, controlplanestore.WithHistoryFileCache(hc))
	}

	if shouldAttachWebhookEncryptor(cmd, cfg) {
		encryptor, err := newControlPlaneWebhookEncryptor(cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, controlplanestore.WithWebhookEncryptor(encryptor))
	}

	return controlplanestore.New(baseCtx, cfg, opts...)
}

func shouldOpenContextControlPlaneStore(cmd *cobra.Command, cfg *config.Config) bool {
	if cmd == nil || cfg == nil {
		return false
	}
	switch cfg.ControlPlaneStore.Backend {
	case config.ControlPlaneStoreBackendPostgres:
		return true
	case "", config.ControlPlaneStoreBackendFile:
		return cmd.Name() == "server" || cmd.Name() == "start-all"
	default:
		return true
	}
}

func shouldAttachWebhookEncryptor(cmd *cobra.Command, cfg *config.Config) bool {
	return cfg != nil &&
		cmd != nil &&
		(cmd.Name() == "server" || cmd.Name() == "start-all")
}

func newControlPlaneWebhookEncryptor(cfg *config.Config) (*crypto.Encryptor, error) {
	encKey, err := crypto.ResolveKey(cfg.Paths.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve encryption key for control-plane webhook store: %w", err)
	}
	encryptor, err := crypto.NewEncryptor(encKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor for control-plane webhook store: %w", err)
	}
	return encryptor, nil
}

// isAgentCommand returns true if the command name is an agent command
// that displays progress or tree output.
func isAgentCommand(cmdName string) bool {
	switch cmdName {
	case "start", "restart", "retry", "dry", "exec", "agent":
		return true
	default:
		return false
	}
}

// isSharedNothingWorker checks if the current command is a worker with static coordinators
// configured, indicating shared-nothing mode where no local storage is needed.
func isSharedNothingWorker(cmd *cobra.Command, cfg *config.Config) bool {
	if cmd.Name() != "worker" {
		return false
	}
	return len(cfg.Worker.Coordinators) > 0
}

func shouldDeferAgentDAGRunStore(cmd *cobra.Command, cfg *config.Config) bool {
	if !postgresAgentDirectAccessDisabled(cfg) {
		return false
	}
	switch cmd.Name() {
	case "start", "exec":
		return true
	default:
		return false
	}
}

func postgresAgentDirectAccessDisabled(cfg *config.Config) bool {
	return cfg != nil &&
		cfg.ControlPlaneStore.Backend == config.ControlPlaneStoreBackendPostgres &&
		!cfg.ControlPlaneStore.Postgres.Agent.DirectAccess
}

func errPostgresAgentDirectAccessDisabled(command string) error {
	if command == "" {
		command = "agent command"
	}
	return fmt.Errorf("postgres control-plane store direct agent access is disabled for %s; use coordinator/shared-nothing worker execution or set control_plane_store.postgres.agent.direct_access=true for local development", command)
}

// NewServer creates and returns a new web UI NewServer.
// It initializes in-memory caches for DAGs and runstore, and uses them in the client.
func (c *Context) NewServer(rs *resource.Service, opts ...frontend.ServerOption) (*frontend.Server, error) {
	limits := c.Config.Cache.Limits()
	dc := fileutil.NewCache[*core.DAG]("dag_definition", limits.DAG.Limit, limits.DAG.TTL)
	dc.StartEviction(c)

	dr, err := c.dagStore(dagStoreConfig{Cache: dc})
	if err != nil {
		return nil, err
	}

	// Create coordinator client (may be nil if not configured)
	cc := c.NewCoordinatorClient()

	collector := telemetry.NewCollector(
		config.Version,
		dr,
		c.DAGRunStore,
		c.QueueStore,
		c.ServiceRegistry,
	)
	collector.SetWorkerHeartbeatStore(c.WorkerHeartbeatStore)

	// Register DAG definition cache for metrics
	collector.RegisterCache(dc)

	mr := telemetry.NewRegistry(collector)

	if c.LicenseManager != nil {
		opts = append(opts, frontend.WithLicenseManager(c.LicenseManager))
	}
	if c.DAGRunLeaseStore != nil {
		opts = append(opts, frontend.WithAPIOption(apiv1.WithDAGRunLeaseStore(c.DAGRunLeaseStore)))
	}
	if c.WorkerHeartbeatStore != nil {
		opts = append(opts, frontend.WithAPIOption(apiv1.WithWorkerHeartbeatStore(c.WorkerHeartbeatStore)))
	}
	if c.ControlPlaneStore != nil {
		opts = append(opts, frontend.WithControlPlaneStore(c.ControlPlaneStore))
	}
	opts = append(opts, frontend.WithAPIOption(apiv1.WithSchedulerStateStore(
		filewatermark.New(filepath.Join(c.Config.Paths.DataDir, "scheduler")),
	)))

	return frontend.NewServer(c.Context, c.Config, dr, c.DAGRunStore, c.QueueStore, c.ProcStore, c.DAGRunMgr, cc, c.ServiceRegistry, mr, collector, rs, opts...)
}

// NewCoordinatorClient creates a new coordinator client using the global peer configuration.
// Returns nil when the coordinator is disabled via configuration.
func (c *Context) NewCoordinatorClient() coordinator.Client {
	if !c.Config.Coordinator.Enabled {
		return nil
	}

	coordinatorCliCfg := coordinator.DefaultConfig()
	coordinatorCliCfg.CAFile = c.Config.Core.Peer.ClientCaFile
	coordinatorCliCfg.CertFile = c.Config.Core.Peer.CertFile
	coordinatorCliCfg.KeyFile = c.Config.Core.Peer.KeyFile
	coordinatorCliCfg.SkipTLSVerify = c.Config.Core.Peer.SkipTLSVerify
	coordinatorCliCfg.Insecure = c.Config.Core.Peer.Insecure

	// Use config values for retry if provided
	if c.Config.Core.Peer.MaxRetries > 0 {
		coordinatorCliCfg.MaxRetries = c.Config.Core.Peer.MaxRetries
	}
	if c.Config.Core.Peer.RetryInterval > 0 {
		coordinatorCliCfg.RetryInterval = c.Config.Core.Peer.RetryInterval
	}

	if err := coordinatorCliCfg.Validate(); err != nil {
		logger.Error(c.Context, "Invalid coordinator client configuration", tag.Error(err))
		return nil
	}
	return coordinator.New(c.ServiceRegistry, coordinatorCliCfg)
}

// NewScheduler creates a new NewScheduler instance using the default client.
// It builds a DAG job manager to handle scheduled executions.
func (c *Context) NewScheduler() (*scheduler.Scheduler, error) {
	limits := c.Config.Cache.Limits()
	cache := fileutil.NewCache[*core.DAG]("dag_definition", limits.DAG.Limit, limits.DAG.TTL)
	cache.StartEviction(c)

	dr, err := c.dagStore(dagStoreConfig{Cache: cache})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize DAG client: %w", err)
	}

	coordinatorCli := c.NewCoordinatorClient()
	m := scheduler.NewEntryReader(c.Config.Paths.DAGsDir, dr)
	watermarkDir := filepath.Join(c.Config.Paths.DataDir, "scheduler")
	wmStore := filewatermark.New(watermarkDir)

	statusCache := fileutil.NewCache[*exec.DAGRunStatus]("scheduler_dag_run_status", limits.DAGRun.Limit, limits.DAGRun.TTL)
	statusCache.StartEviction(c)
	var (
		schedulerRunStore          exec.DAGRunStore
		schedulerQueueStore        = c.QueueStore
		schedulerServiceRegistry   = c.ServiceRegistry
		schedulerDAGRunLeaseStore  = c.DAGRunLeaseStore
		schedulerDispatchTaskStore = c.DispatchTaskStore
		schedulerControlPlaneStore controlplanestore.Store
	)
	if c.Config.ControlPlaneStore.Backend == config.ControlPlaneStoreBackendPostgres {
		if c.ControlPlaneStore != nil && controlPlaneStoreRoleForCommand(c.Command) == controlplanestore.RoleScheduler {
			schedulerControlPlaneStore = c.ControlPlaneStore
		} else {
			schedulerControlPlaneStore, err = controlplanestore.New(
				c,
				c.Config,
				controlplanestore.WithRole(controlplanestore.RoleScheduler),
				controlplanestore.WithLatestStatusToday(false),
				controlplanestore.WithLocation(c.Config.Core.Location),
				controlplanestore.WithHistoryFileCache(statusCache),
			)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize scheduler control-plane store: %w", err)
			}
		}
		if schedulerControlPlaneStore != nil {
			schedulerRunStore = schedulerControlPlaneStore.DAGRuns()
			schedulerQueueStore = schedulerControlPlaneStore.Queue()
			schedulerServiceRegistry = schedulerControlPlaneStore.Services()
			schedulerDAGRunLeaseStore = schedulerControlPlaneStore.DAGRunLeases()
			schedulerDispatchTaskStore = schedulerControlPlaneStore.DispatchTasks()
		}
	}
	if schedulerRunStore == nil {
		schedulerRunStore, err = dagrunstore.New(
			c,
			c.Config,
			dagrunstore.WithRole(dagrunstore.RoleScheduler),
			dagrunstore.WithLatestStatusToday(false),
			dagrunstore.WithLocation(c.Config.Core.Location),
			dagrunstore.WithHistoryFileCache(statusCache),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize scheduler DAG-run store: %w", err)
		}
	}
	ownsSchedulerRunStore := true
	defer func() {
		if ownsSchedulerRunStore {
			if closeErr := exec.CloseDAGRunStore(context.Background(), schedulerRunStore); closeErr != nil {
				logger.Warn(c, "Failed to close scheduler DAG-run store", tag.Error(closeErr))
			}
		}
	}()
	schedulerRunMgr := runtime.NewManager(schedulerRunStore, c.ProcStore, c.Config)

	sched, err := scheduler.New(c.Config, m, schedulerRunMgr, schedulerRunStore, schedulerQueueStore, c.ProcStore, schedulerServiceRegistry, coordinatorCli, wmStore)
	if err != nil {
		return nil, err
	}
	ownsSchedulerRunStore = false
	if c.EventService != nil {
		collector, eventErr := fileeventstore.NewCollector(c.Config.Paths.EventStoreDir, c.Config.EventStore.RetentionDays)
		if eventErr != nil {
			logger.Warn(c, "Failed to initialize event collector; continuing without collection", tag.Error(eventErr))
		} else {
			sched.SetEventCollector(collector)
		}
	}
	sched.SetDAGRunLeaseStore(schedulerDAGRunLeaseStore)
	sched.SetDispatchTaskStore(schedulerDispatchTaskStore)
	if c.LicenseManager != nil {
		githubTracker := filegithubdispatch.New(filepath.Join(c.Config.Paths.DataDir, "github-dispatch"))
		sched.SetGitHubDispatchWorker(scheduler.NewGitHubDispatchWorker(
			c.Config,
			dr,
			schedulerRunStore,
			schedulerQueueStore,
			&schedulerRunMgr,
			c.LicenseManager,
			license.NewCloudClient(c.Config.License.CloudURL),
			githubTracker,
			logger.FromContext(c.Context),
		))
	}
	return sched, nil
}

// StringParam retrieves a string parameter from the command line flags.
// It checks if the parameter is wrapped in quotes and removes them if necessary.
func (c *Context) StringParam(name string) (string, error) {
	val, err := c.Command.Flags().GetString(name)
	if err != nil {
		return "", fmt.Errorf("failed to get flag %s: %w", name, err)
	}

	// If it's wrapped in quotes, remove them
	val = stringutil.RemoveQuotes(val)
	return val, nil
}

// getWorkerID retrieves the worker ID from context, defaulting to "local" if not set or on error.
func getWorkerID(ctx *Context) string {
	workerID, err := ctx.StringParam("worker-id")
	if err != nil {
		logger.Warn(ctx, "Failed to read worker-id flag, defaulting to 'local'", tag.Error(err))
		return "local"
	}
	if workerID == "" {
		return "local"
	}
	return workerID
}

// dagStoreConfig contains options for creating a DAG store.
type dagStoreConfig struct {
	Cache                 *fileutil.Cache[*core.DAG] // Optional cache for DAG objects
	SearchPaths           []string                   // Additional search paths for DAG files
	SkipDirectoryCreation bool                       // Skip directory creation (for distributed worker execution)
}

// dagStore returns a new DAGRepository instance.
func (c *Context) dagStore(cfg dagStoreConfig) (exec.DAGStore, error) {
	// Merge configured alternate DAGs directory into search paths if provided
	searchPaths := append([]string{}, cfg.SearchPaths...)
	if c.Config != nil && c.Config.Paths.AltDAGsDir != "" {
		searchPaths = append(searchPaths, c.Config.Paths.AltDAGsDir)
	}

	store := filedag.New(
		c.Config.Paths.DAGsDir,
		filedag.WithFlagsBaseDir(c.Config.Paths.SuspendFlagsDir),
		filedag.WithSearchPaths(searchPaths),
		filedag.WithBaseConfig(c.Config.Paths.BaseConfig),
		filedag.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(c.Config.Paths.DAGsDir)),
		filedag.WithFileCache(cfg.Cache),
		filedag.WithSkipExamples(c.Config.Core.SkipExamples),
		filedag.WithSkipDirectoryCreation(cfg.SkipDirectoryCreation),
	)

	// Initialize the store (creates directory and example DAGs if needed, unless SkipDirectoryCreation is true)
	if s, ok := store.(*filedag.Storage); ok {
		if err := s.Initialize(); err != nil {
			return nil, fmt.Errorf("failed to initialize DAG store: %w", err)
		}
	}

	return store, nil
}

// agentStoresResult holds the agent stores created by agentStores().
type agentStoresResult struct {
	ConfigStore     agent.ConfigStore
	ModelStore      agent.ModelStore
	MemoryStore     agent.MemoryStore
	SoulStore       agent.SoulStore
	OAuthManager    *agentoauth.Manager
	ContextResolver agent.RemoteContextResolver
}

// agentStores creates the agent config, model, memory, and soul stores from the config paths.
// Errors are logged as warnings; nil stores are returned if creation fails.
func (c *Context) agentStores() agentStoresResult {
	var result agentStoresResult

	acs, err := fileagentconfig.New(c.Config.Paths.DataDir)
	if err != nil {
		logger.Warn(c, "Failed to create agent config store", tag.Error(err))
		return result
	}
	if acs == nil {
		return result
	}
	result.ConfigStore = acs

	ams, err := fileagentmodel.New(filepath.Join(c.Config.Paths.DataDir, "agent", "models"))
	if err != nil {
		logger.Warn(c, "Failed to create agent model store", tag.Error(err))
		return result
	}
	result.ModelStore = ams

	ms, err := filememory.New(c.Config.Paths.DAGsDir)
	if err != nil {
		logger.Warn(c, "Failed to create agent memory store", tag.Error(err))
		return result
	}
	result.MemoryStore = ms

	soulsDir := filepath.Join(c.Config.Paths.DAGsDir, "souls")
	soulStore, err := fileagentsoul.New(c, soulsDir)
	if err != nil {
		logger.Warn(c, "Failed to create agent soul store", tag.Error(err))
		return result
	}
	result.SoulStore = soulStore

	oauthManager, err := fileagentoauth.NewManager(c.Config.Paths.DataDir)
	if err != nil {
		logger.Warn(c, "Failed to create agent OAuth store", tag.Error(err))
	} else {
		result.OAuthManager = oauthManager
	}

	// Build context resolver for agent step remote tools.
	result.ContextResolver = c.buildRemoteContextResolver()

	return result
}

// buildRemoteContextResolver creates a RemoteContextResolver from the CLI context store.
func (c *Context) buildRemoteContextResolver() agent.RemoteContextResolver {
	if c.ContextStore == nil {
		return nil
	}
	return &agent.RemoteContextResolverAdapter{Store: c.ContextStore}
}

// OpenLogFile creates and opens a log file for a given dag-run.
// It evaluates the log directory, validates settings, creates the log directory,
// builds a filename using the current timestamp and dag-run ID, and then opens the file.
func (c *Context) OpenLogFile(
	dag *core.DAG,
	dagRunID string,
) (*os.File, error) {
	logPath, err := c.GenLogFileName(dag, dagRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate log file name: %w", err)
	}
	return fileutil.OpenOrCreateFile(logPath)
}

// GenLogFileName generates a log file name based on the DAG and dag-run ID.
func (c *Context) GenLogFileName(dag *core.DAG, dagRunID string) (string, error) {
	return logpath.Generate(c, c.Config.Paths.LogDir, dag.LogDir, dag.Name, dagRunID)
}

// GenArtifactDir generates an artifact directory path for the DAG run when artifacts are enabled.
func (c *Context) GenArtifactDir(dag *core.DAG, dagRunID string) (string, error) {
	if dag == nil || !dag.ArtifactsEnabled() {
		return "", nil
	}

	dagArtifactDir := ""
	if dag.Artifacts != nil {
		dagArtifactDir = dag.Artifacts.Dir
	}

	return logpath.GenerateDir(c, c.Config.Paths.ArtifactDir, dagArtifactDir, dag.Name, dagRunID)
}

// NewCommand creates a new command instance with the given cobra command and run function.
func NewCommand(cmd *cobra.Command, flags []commandLineFlag, runFunc func(cmd *Context, args []string) error) *cobra.Command {
	initFlags(cmd, flags...)

	cmd.SilenceUsage = true

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Setup cpu profiling if enabled.
		cpuProfileEnabled, err := cmd.Flags().GetBool("cpu-profile")
		if err != nil {
			return fmt.Errorf("failed to read cpu-profile flag: %w", err)
		}
		if cpuProfileEnabled {
			f, err := os.Create("cpu.prof")
			if err != nil {
				return fmt.Errorf("failed to create CPU profile file: %w", err)
			}
			_ = pprof.StartCPUProfile(f)
			defer func() {
				pprof.StopCPUProfile()
				if err := f.Close(); err != nil {
					fmt.Printf("Failed to close CPU profile file: %v\n", err)
				}
			}()
		}

		ctx, err := NewContext(cmd, flags)

		if err != nil {
			return fmt.Errorf("initialization error: %w", err)
		}
		runErr := runFunc(ctx, args)
		if closeErr := closeCommandContext(ctx); closeErr != nil {
			return errors.Join(runErr, closeErr)
		}
		return runErr
	}

	return cmd
}

func closeCommandContext(ctx *Context) error {
	closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return ctx.Close(closeCtx)
}

// genRunID creates a new UUID string to be used as a dag-run IDentifier.
func genRunID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// validateRunID checks if the dag-run ID is valid and not empty.
func validateRunID(dagRunID string) error {
	return exec.ValidateDAGRunID(dagRunID)
}

// signalListener is an interface for types that can receive OS signals.
type signalListener interface {
	Signal(context.Context, os.Signal)
}

// listenSignals subscribes to SIGINT and SIGTERM signals and forwards them to the provided listener.
// It also listens for context cancellation and signals the listener with an os.Interrupt.
func listenSignals(ctx context.Context, listener signalListener) {
	go func() {
		if signalctx.OSSignalsDisabled(ctx) {
			<-ctx.Done()
			listener.Signal(ctx, os.Interrupt)
			return
		}

		signalChan := make(chan os.Signal, 1)
		signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(signalChan)

		select {
		// If context is cancelled, signal with os.Interrupt.
		case <-ctx.Done():
			listener.Signal(ctx, os.Interrupt)
		// Forward the received signal.
		case sig := <-signalChan:
			listener.Signal(ctx, sig)
		}
	}()
}

// LogConfig defines configuration for log file creation.
type LogConfig = logpath.Config

// RecordEarlyFailure records a failure in the execution history before the DAG has fully started.
// This is used for infrastructure errors like singleton conflicts or process acquisition failures.
func (c *Context) RecordEarlyFailure(dag *core.DAG, dagRunID string, err error) error {
	if dag == nil || dagRunID == "" {
		return fmt.Errorf("DAG and dag-run ID are required to record failure")
	}

	// 1. Check if a DAGRunAttempt already exists for the given run-id.
	ref := exec.NewDAGRunRef(dag.Name, dagRunID)
	attempt, findErr := c.DAGRunStore.FindAttempt(c, ref)
	if findErr != nil && !errors.Is(findErr, exec.ErrDAGRunIDNotFound) {
		return fmt.Errorf("failed to check for existing attempt: %w", findErr)
	}

	if attempt == nil {
		// 2. Create the attempt if not exists
		att, createErr := c.DAGRunStore.CreateAttempt(c, dag, time.Now(), dagRunID, exec.NewDAGRunAttemptOptions{})
		if createErr != nil {
			return fmt.Errorf("failed to create run to record failure: %w", createErr)
		}
		attempt = att
	}

	// 3. Construct the "Failed" status
	statusBuilder := transform.NewStatusBuilder(dag)
	logPath, logPathErr := c.GenLogFileName(dag, dagRunID)
	if logPathErr != nil {
		logger.Warn(c, "Failed to generate log file path for early failure status",
			tag.Error(logPathErr),
			tag.DAG(dag.Name),
			tag.RunID(dagRunID),
		)
	}
	artifactDir, artifactDirErr := c.GenArtifactDir(dag, dagRunID)
	if artifactDirErr != nil {
		logger.Warn(c, "Failed to generate artifact directory for early failure status",
			tag.Error(artifactDirErr),
			tag.DAG(dag.Name),
			tag.RunID(dagRunID),
		)
	}
	status := statusBuilder.Create(dagRunID, core.Failed, 0, time.Now(),
		transform.WithLogFilePath(logPath),
		transform.WithArchiveDir(artifactDir),
		transform.WithFinishedAt(time.Now()),
		transform.WithError(err.Error()),
	)

	// 4. Write the status
	if err := attempt.Open(c); err != nil {
		return fmt.Errorf("failed to open attempt for recording failure: %w", err)
	}
	defer func() {
		_ = attempt.Close(c)
	}()

	if err := attempt.Write(c, status); err != nil {
		return fmt.Errorf("failed to write failed status: %w", err)
	}

	return nil
}
