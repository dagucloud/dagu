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
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/cmn/signalctx"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filebaseconfig "github.com/dagucloud/dagu/v2/internal/persis/file/baseconfig"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
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

	EventSourceInstance string
	Persistence         Persistence
	DAGRunMgr           runtime.Manager
	backend             persis.Backend
	event               *eventstore.Service

	Caches         []fileutil.CacheMetrics
	Proc           proc.ProcHandle
	LicenseManager *license.Manager
	ContextStore   *cliContextStore
	CLIContext     *cliContext
	ContextName    string
	Remote         *remoteClient
}

// WithContext returns a new Context with a different underlying context.Context.
// This is useful for creating a signal-aware context for service operations.
func (c *Context) WithContext(ctx context.Context) *Context {
	clone := *c
	clone.Context = ctx
	return &clone
}

// WithEventSource returns a shallow copy whose context carries the given event source.
// If the event store is not configured, the original context is preserved.
func (c *Context) WithEventSource(service string) *Context {
	if c == nil || c.event == nil {
		return c
	}
	return c.WithContext(eventstore.WithContext(c.Context, c.event, eventstore.Source{
		Service:  service,
		Instance: c.EventSourceInstance,
	}))
}

func (c *Context) withEvent(service *eventstore.Service) *Context {
	clone := *c
	clone.event = service
	if service != nil {
		clone.Context = eventstore.WithContext(clone.Context, service, eventstore.Source{
			Service:  eventSourceServiceForCommand(clone.Command.Name()),
			Instance: clone.EventSourceInstance,
		})
	}
	return &clone
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
		opts = append(opts, logger.WithRunWriter(f))
	}
	c.Context = logger.WithLogger(c.Context, logger.NewLogger(opts...))
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
	selectedContextName := localContextName
	selectedContext := &cliContext{Name: localContextName}
	var (
		contextStore        *cliContextStore
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
				selectedContextName = localContextName
				selectedContext = &cliContext{Name: localContextName}
			}
		}
	}
	if scope == commandScopeLocalOnly && selectedContextName != localContextName {
		commandPath := strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()))
		return nil, fmt.Errorf("command %q only supports the local context", commandPath)
	}

	// Create a logger context based on config and quiet mode
	var opts []logger.Option
	if cfg.Core.Debug || os.Getenv("DEBUG") != "" {
		opts = append(opts, logger.WithDebug())
	}
	if quiet {
		opts = append(opts, logger.WithQuiet())
	}
	// For commands with progress output, suppress console output early to avoid
	// debug logs cluttering the progress display or tree output.
	if !quiet && isProgressOutputCommand(cmd.Name()) && term.IsTerminal(int(os.Stderr.Fd())) && os.Getenv("DISABLE_PROGRESS") == "" {
		opts = append(opts, logger.WithQuiet())
	}
	if cfg.Core.LogFormat != "" {
		opts = append(opts, logger.WithFormat(cfg.Core.LogFormat))
	}
	ctx = logger.WithLogger(ctx, logger.NewLogger(opts...))
	// Log messages collected during configuration loading.
	for _, notice := range cfg.Notices {
		logger.Debug(ctx, notice)
	}
	for _, warning := range cfg.Warnings {
		logger.Warn(ctx, warning)
	}
	if contextStoreWarning != nil {
		logger.Warn(ctx, contextStoreWarning.Error())
	}

	baseCtx := ctx
	eventSourceInstance := eventstore.DefaultSourceInstance()
	if scope == commandScopeContextAware && selectedContextName != localContextName {
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
			EventSourceInstance: eventSourceInstance,
			ContextStore:        contextStore,
			CLIContext:          selectedContext,
			ContextName:         selectedContextName,
			Remote:              remote,
			Scope:               scope,
		}, nil
	}
	backend := file.NewBackend(cfg.Paths)

	workerCommand := cmd.Name() == "worker"
	var eventService *eventstore.Service
	switch cmd.Name() {
	case "server", "scheduler", "start-all", "worker":
	default:
		eventService = newEventService(ctx, cfg)
	}
	if eventService != nil {
		ctx = eventstore.WithContext(ctx, eventService, eventstore.Source{
			Service:  eventSourceServiceForCommand(cmd.Name()),
			Instance: eventSourceInstance,
		})
	}

	// Workers run DAGs through the remote task handler and push runtime state
	// to the coordinator, so they do not need local file-backed run stores.
	if workerCommand {
		logger.Debug(ctx, "Worker mode: skipping file-based run stores",
			slog.Any("coordinators", cfg.Worker.Coordinators),
		)
		return &Context{
			Context:             baseCtx,
			Command:             cmd,
			Config:              cfg,
			Quiet:               quiet,
			Flags:               flags,
			EventSourceInstance: eventSourceInstance,
			ContextStore:        contextStore,
			CLIContext:          selectedContext,
			ContextName:         selectedContextName,
			Scope:               scope,
			backend:             backend,
			// Run stores are nil; worker execution reports runtime state to the coordinator.
			// Status is pushed to coordinator, DAG definitions come from task payload
		}, nil
	}

	// Initialize caches shared by long-running process roles.
	var dagCache *fileutil.Cache[*ir.DAG]
	var dagRunStatusCache *fileutil.Cache[*ir.DAGRunStatus]
	var caches []fileutil.CacheMetrics

	switch cmd.Name() {
	case "server", "scheduler", "start-all", "coordinator":
		// Long-running processes share caches across their service roles.
		limits := cfg.Cache.Limits()
		hc := fileutil.NewCache[*ir.DAGRunStatus]("dag_run_status", limits.DAGRun.Limit, limits.DAGRun.TTL)
		hc.StartEviction(ctx)
		dagRunStatusCache = hc
		dagCache = fileutil.NewCache[*ir.DAG]("dag_definition", limits.DAG.Limit, limits.DAG.TTL)
		dagCache.StartEviction(ctx)
		caches = append(caches, dagCache, hc)
	}

	persistence, err := newFilePersistence(ctx, cfg, backend, filePersistenceOptions{
		DAGCache:          dagCache,
		DAGRunStatusCache: dagRunStatusCache,
	})
	if err != nil {
		return nil, err
	}
	drm := runtime.NewManager(persistence.DAGRunRepository, persistence.ProcRepository, cfg)

	// Initialize license manager for server commands
	var licMgr *license.Manager
	switch cmd.Name() {
	case "server", "start-all":
		pubKey, pubKeyErr := license.PublicKey()
		if pubKeyErr != nil {
			logger.Warn(ctx, "Failed to load license public key", tag.Error(pubKeyErr))
			break
		}
		licenseDir := file.LicenseDir(cfg)
		licStore := file.NewLicenseStore(ctx, backend.Collection(persis.CollectionLicense))
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
		slog.String("dag-state-dir", cfg.Paths.DAGStateDir),
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
		Context:             ctx,
		Command:             cmd,
		Config:              cfg,
		Quiet:               quiet,
		EventSourceInstance: eventSourceInstance,
		Persistence:         persistence,
		DAGRunMgr:           drm,
		backend:             backend,
		event:               eventService,
		Flags:               flags,
		Caches:              caches,
		LicenseManager:      licMgr,
		ContextStore:        contextStore,
		CLIContext:          selectedContext,
		ContextName:         selectedContextName,
		Scope:               scope,
	}, nil
}

func commandFamilyName(cmd *cobra.Command) string {
	if isContextCommand(cmd) {
		return "context"
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

func resolveCLIContext(cmd *cobra.Command, store *cliContextStore, requested string) (string, *cliContext, error) {
	contextName := strings.TrimSpace(requested)
	var err error
	if contextName == "" {
		contextName, err = store.Current(cmd.Context())
		if err != nil {
			return "", nil, fmt.Errorf("failed to resolve current context: %w", err)
		}
	}
	if contextName == "" {
		contextName = localContextName
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
	return requested != "" && requested != localContextName
}

func shouldFailForContextResolutionError(scope commandScope, requested string) bool {
	if requested == "" {
		return false
	}
	if requested == localContextName {
		return false
	}
	return scope != commandScopeStatic
}

func (c *Context) IsRemote() bool {
	return c != nil && c.Remote != nil && c.ContextName != localContextName
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
	case "start", "restart", "retry", "dry", "exec":
		return config.ServiceAgent
	default:
		// For all other commands (status, stop, validate, etc.), load all config
		return config.ServiceNone
	}
}

func isProgressOutputCommand(cmdName string) bool {
	switch cmdName {
	case "start", "restart", "retry", "dry", "exec":
		return true
	default:
		return false
	}
}

// NewCoordinatorClient creates a new coordinator client using the global peer configuration.
// Returns a nil client when the coordinator is disabled via configuration.
func (c *Context) NewCoordinatorClient() (coordinator.Client, error) {
	if !c.Config.Coordinator.Enabled {
		return nil, nil
	}
	clientConfig := coordinator.ConfigFromPeer(c.Config.Core.Peer)
	clientConfig.WorkspaceBundleDir = workspacebundle.StoreDir(c.Config.Paths.DataDir)
	if err := clientConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	return coordinator.New(c.Persistence.ServiceRegistry, clientConfig), nil
}

func (c *Context) SubWorkflowRunnerFactory() func(context.Context) (runtimeexec.SubWorkflowRunner, error) {
	stores := c.runtimeStores()
	return coordinator.NewSubWorkflowRunnerFactory(coordinator.SubWorkflowRunnerConfig{
		DAGRunMgr:         c.DAGRunMgr,
		DAGRepository:     c.Persistence.DAGRepository,
		DAGRunRepository:  c.Persistence.DAGRunRepository,
		QueueStore:        c.Persistence.QueueStore,
		StateStore:        c.Persistence.StateStore,
		SecretStore:       stores.SecretStore,
		ProfileStore:      stores.ProfileStore,
		ServiceRegistry:   c.Persistence.ServiceRegistry,
		PeerConfig:        c.Config.Core.Peer,
		DefaultExecMode:   c.Config.DefaultExecMode,
		WorkerID:          "local",
		DAGRunLogDir:      c.Config.Paths.LogDir,
		DAGRunArtifactDir: c.Config.Paths.ArtifactDir,
	})
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

// dagRepositoryConfig contains options for creating a DAG repository.
type dagRepositoryConfig struct {
	Cache                 *fileutil.Cache[*ir.DAG] // Optional cache for DAG objects
	SearchPaths           []string                 // Additional search paths for DAG files
	SkipDirectoryCreation bool                     // Skip directory creation (for distributed worker execution)
}

// dagRepository returns a new DAGRepository instance.
func (c *Context) dagRepository(cfg dagRepositoryConfig) (*persis.DAGRepository, error) {
	return newDAGRepository(c.Config, cfg)
}

// OpenLogFile creates and opens a log file for a given dag-run.
// It evaluates the log directory, validates settings, creates the log directory,
// builds a filename using the current timestamp and dag-run ID, and then opens the file.
func (c *Context) OpenLogFile(
	dag *ir.DAG,
	dagRunID string,
) (*os.File, error) {
	logPath, err := c.GenLogFileName(dag, dagRunID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate log file name: %w", err)
	}
	return fileutil.OpenOrCreateFile(logPath)
}

// GenLogFileName generates a log file name based on the DAG and dag-run ID.
func (c *Context) GenLogFileName(dag *ir.DAG, dagRunID string) (string, error) {
	return logpath.Generate(c, c.Config.Paths.LogDir, dag.LogDir, dag.Name, dagRunID)
}

// GenArtifactDir generates an artifact directory path for the DAG run when artifacts are enabled.
func (c *Context) GenArtifactDir(dag *ir.DAG, dagRunID string) (string, error) {
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
		return runFunc(ctx, args)
	}

	return cmd
}

// genRunID creates a new auto-generated dag-run ID.
func genRunID() (string, error) {
	return ir.NewDAGRunID()
}

// validateRunID checks if the dag-run ID is valid and not empty.
func validateRunID(dagRunID string) error {
	return ir.ValidateDAGRunID(dagRunID)
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
func (c *Context) RecordEarlyFailure(dag *ir.DAG, dagRunID string, err error) error {
	if dag == nil || dagRunID == "" {
		return fmt.Errorf("DAG and dag-run ID are required to record failure")
	}

	// 1. Check whether an attempt already exists for the run ID.
	ref := ir.NewDAGRunRef(dag.Name, dagRunID)
	attempt, findErr := c.Persistence.DAGRunRepository.FindAttempt(c, ref)
	if findErr != nil && !errors.Is(findErr, dagrun.ErrDAGRunIDNotFound) {
		return fmt.Errorf("failed to check for existing attempt: %w", findErr)
	}

	if attempt == nil {
		// 2. Create the attempt if not exists
		att, createErr := c.Persistence.DAGRunRepository.CreateAttempt(c, dag, time.Now(), dagRunID, persis.DAGRunCreateAttemptOptions{})
		if createErr != nil {
			return fmt.Errorf("failed to create run to record failure: %w", createErr)
		}
		attempt = att
	}

	// 3. Construct the "Failed" status
	statusBuilder := ir.NewStatusBuilder(dag)
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
	status := statusBuilder.Create(dagRunID, ir.Failed, 0, time.Now(),
		ir.WithLogFilePath(logPath),
		ir.WithArchiveDir(artifactDir),
		ir.WithFinishedAt(time.Now()),
		ir.WithError(err.Error()),
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
