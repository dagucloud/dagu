// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	agentpkg "github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/mailer"
	"github.com/dagucloud/dagu/internal/cmn/masking"
	"github.com/dagucloud/dagu/internal/cmn/secrets"
	"github.com/dagucloud/dagu/internal/cmn/signal"
	"github.com/dagucloud/dagu/internal/cmn/sock"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/output"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/builtin/docker"
	"github.com/dagucloud/dagu/internal/runtime/builtin/s3"
	"github.com/dagucloud/dagu/internal/runtime/builtin/ssh"
	"github.com/dagucloud/dagu/internal/runtime/remote"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/coordinator"

	_ "github.com/dagucloud/dagu/internal/runtime/builtin"
)

// Agent is responsible for running the DAG and handling communication
// via the unix socket. The agent performs the following tasks:
// 1. Start the DAG.
// 2. Propagate a signal to the running processes.
// 3. Handle the HTTP request via the unix socket.
// 4. Write the log and status to the data store.
type Agent struct {
	lock sync.RWMutex

	// dry indicates if the agent is running in dry-run mode.
	dry bool

	// retryTarget is the target status to retry the DAG.
	// It is nil if it's not a retry execution.
	retryTarget *exec.DAGRunStatus

	// dagStore is the database to store the DAG definitions.
	dagStore exec.DAGStore

	// dagRunStore is the database to store the run history.
	dagRunStore exec.DAGRunStore

	// registry is the service registry to find the coordinator service.
	registry exec.ServiceRegistry

	// peerConfig is the configuration for the peer connections.
	peerConfig config.Peer

	// dagRunMgr is the runstore dagRunMgr to communicate with the history.
	dagRunMgr runtime.Manager

	// runner is the runner instance to run the DAG.
	runner *runtime.Runner

	// plan is the execution plan for the DAG.
	plan *runtime.Plan

	// reporter is responsible for sending the report to the user.
	reporter *reporter

	// socketServer is the unix socket server to handle HTTP requests.
	// It listens to the requests from the local client (e.g., frontend server).
	socketServer *sock.Server

	// logDir is the directory to store the log files for each node in the DAG.
	logDir string

	// logFile is the file to write the runner log.
	logFile string

	// artifactDir is the per-run artifact directory when artifact storage is enabled.
	artifactDir string

	// artifactFinalizer persists artifacts before the final terminal status is written.
	artifactFinalizer ArtifactFinalizer

	// dag is the DAG to run.
	dag *core.DAG

	// rootDAGRun indicates the root dag-run of the current dag-run.
	// If the current dag-run is the root dag-run, it is the same as the current
	// DAG name and dag-run ID.
	rootDAGRun exec.DAGRunRef

	// parentDAGRun is the execution reference of the parent dag-run.
	parentDAGRun exec.DAGRunRef

	// dagRunID is the ID for the current dag-run.
	dagRunID string

	// dagRunAttemptID is the ID for the current dag-run attempt.
	dagRunAttemptID string

	// finished is true if the dag-run is finished.
	finished atomic.Bool

	// initFailed is true if initialization failed before the runner could start.
	initFailed atomic.Bool

	// lastErr is the last error occurred during the dag-run.
	lastErr error

	// isSubDAGRun is true if the current dag-run is not the root dag-run,
	// meaning that it is a sub dag-run of another dag-run.
	isSubDAGRun atomic.Bool

	// progressDisplay is the progress display for showing real-time execution progress.
	progressDisplay ProgressReporter

	// stepRetry is the name of the step to retry, if specified.
	stepRetry string

	// workerID is the identifier of the worker executing this DAG run.
	workerID string

	// triggerType indicates how this DAG run was initiated.
	triggerType core.TriggerType

	// defaultExecMode is the server-level default execution mode.
	defaultExecMode config.ExecutionMode

	// tracer is the OpenTelemetry tracer for the agent.
	tracer *telemetry.Tracer

	// statusPusher is used to push status updates to a remote coordinator.
	// When nil, status is written to local filesystem via DAGRunAttempt.
	statusPusher StatusPusher

	// logWriterFactory is used to create log writers for step output.
	// When nil, logs are written to local filesystem.
	logWriterFactory exec.LogWriterFactory

	// scheduleTime is the RFC 3339 timestamp of when this run was scheduled.
	// Set by the scheduler for cron-triggered runs; empty for manual runs.
	scheduleTime string

	// queuedRun indicates this execution is from a queued item.
	// The dag-run was already created by the enqueue command.
	queuedRun bool

	// attemptID is the attempt ID from the coordinator.
	// When set, the agent creates an attempt with this ID instead of generating a new one.
	attemptID string

	// preparedAttempt is an exact local attempt prepared before proc acquisition.
	// When set, the agent must reuse this attempt instead of creating another one.
	preparedAttempt exec.DAGRunAttempt

	// agentConfigStore is the agent config store for agent step execution.
	agentConfigStore agentpkg.ConfigStore
	// agentModelStore is the agent model store for agent step execution.
	agentModelStore agentpkg.ModelStore
	// agentMemoryStore is the agent memory store for agent step execution.
	agentMemoryStore agentpkg.MemoryStore
	// agentSoulStore is the agent soul store for agent step execution.
	agentSoulStore agentpkg.SoulStore
	// agentOAuthManager resolves subscription-backed provider credentials.
	agentOAuthManager *agentoauth.Manager
	// agentRemoteContextResolver resolves remote CLI contexts for agent step execution.
	agentRemoteContextResolver agentpkg.RemoteContextResolver

	// workDir is the per-run work directory (for DAG_RUN_WORK_DIR).
	workDir string
	// extraEnvs are additional execution-scoped env vars injected into the DAG run context.
	extraEnvs []string

	// Evaluated configs - these are expanded at runtime and stored separately
	// to avoid mutating the original DAG struct.
	evaluatedSMTP          *core.SMTPConfig
	evaluatedErrorMail     *core.MailConfig
	evaluatedInfoMail      *core.MailConfig
	evaluatedWaitMail      *core.MailConfig
	evaluatedRegistryAuths map[string]*core.AuthConfig
	evaluatedWorkingDir    string
	evaluatedS3            *core.S3Config
}

// StatusPusher is an interface for pushing status updates remotely.
type StatusPusher interface {
	Push(ctx context.Context, status exec.DAGRunStatus) error
}

// ArtifactFinalizer uploads or persists artifacts before the final terminal status is written.
type ArtifactFinalizer interface {
	Finalize(ctx context.Context, attemptID, dir string) error
}

// Options is the configuration for the Agent.
type Options struct {
	// Dry is a dry-run mode. It does not execute the actual command.
	// Dry run does not create runstore data.
	Dry bool
	// RetryTarget is the target status (runstore of execution) to retry.
	// If it's specified the agent will execute the DAG with the same
	// configuration as the specified history.
	RetryTarget *exec.DAGRunStatus
	// ParentDAGRun is the dag-run reference of the parent dag-run.
	// It is required for sub dag-runs to identify the parent dag-run.
	ParentDAGRun exec.DAGRunRef
	// ProgressDisplay indicates if the progress display should be shown.
	// This is typically enabled for CLI execution in a TTY environment.
	ProgressDisplay bool
	// ExtraEnvs are additional execution-scoped env vars injected into the DAG run context.
	ExtraEnvs []string
	// StepRetry is the name of the step to retry, if specified.
	StepRetry string
	// WorkerID is the identifier of the worker executing this DAG run.
	// For distributed execution, this is set to the worker's ID.
	// For local execution, this defaults to "local".
	WorkerID string
	// StatusPusher is used to push status updates to a remote coordinator.
	// When nil, status is written to local filesystem via DAGRunAttempt.
	StatusPusher StatusPusher
	// LogWriterFactory is used to create log writers for step output.
	// When nil, logs are written to local filesystem.
	LogWriterFactory exec.LogWriterFactory
	// QueuedRun indicates this execution is from a queued item.
	// When true, the agent will find the existing dag-run (created by enqueue)
	// instead of creating a new one. This is used for distributed execution
	// where the dag-run directory was already created by the scheduler.
	QueuedRun bool
	// AttemptID is the attempt ID from the coordinator.
	// When set, the agent creates an attempt with this ID instead of generating a new one.
	AttemptID string
	// PreparedAttempt is an exact attempt that was created or reopened before proc acquisition.
	// This is used for local execution so the proc heartbeat can include the final attempt ID.
	PreparedAttempt exec.DAGRunAttempt
	// DAGRunStore is the store for dag-run data. Nil in shared-nothing mode.
	DAGRunStore exec.DAGRunStore
	// ServiceRegistry is the registry for service discovery.
	ServiceRegistry exec.ServiceRegistry
	// RootDAGRun is the root dag-run reference for sub-DAG runs.
	RootDAGRun exec.DAGRunRef
	// PeerConfig is the configuration for peer communication.
	PeerConfig config.Peer
	// TriggerType indicates how this DAG run was initiated.
	TriggerType core.TriggerType
	// DefaultExecMode is the server-level default execution mode.
	DefaultExecMode config.ExecutionMode
	// AgentConfigStore is the agent config store for agent step execution.
	AgentConfigStore agentpkg.ConfigStore
	// AgentModelStore is the agent model store for agent step execution.
	AgentModelStore agentpkg.ModelStore
	// AgentMemoryStore is the agent memory store for agent step execution.
	AgentMemoryStore agentpkg.MemoryStore
	// AgentSoulStore is the agent soul store for agent step execution.
	AgentSoulStore agentpkg.SoulStore
	// AgentOAuthManager resolves subscription-backed provider credentials.
	AgentOAuthManager *agentoauth.Manager
	// AgentRemoteContextResolver resolves remote CLI contexts for agent step execution.
	AgentRemoteContextResolver agentpkg.RemoteContextResolver
	// ScheduleTime is the RFC 3339 timestamp of when this run was scheduled.
	// Set by the scheduler for cron-triggered runs; empty for manual runs.
	ScheduleTime string
	// ArtifactDir is the per-run artifact directory when artifact storage is enabled.
	ArtifactDir string
	// ArtifactFinalizer persists artifacts before the final terminal status is written.
	ArtifactFinalizer ArtifactFinalizer
}

// New creates a new Agent.
func New(
	dagRunID string,
	dag *core.DAG,
	logDir string,
	logFile string,
	drm runtime.Manager,
	ds exec.DAGStore,
	opts Options,
) *Agent {
	a := &Agent{
		rootDAGRun:                 opts.RootDAGRun,
		parentDAGRun:               opts.ParentDAGRun,
		dagRunID:                   dagRunID,
		dag:                        dag,
		dry:                        opts.Dry,
		retryTarget:                opts.RetryTarget,
		logDir:                     logDir,
		logFile:                    logFile,
		artifactDir:                opts.ArtifactDir,
		artifactFinalizer:          opts.ArtifactFinalizer,
		dagRunMgr:                  drm,
		dagStore:                   ds,
		dagRunStore:                opts.DAGRunStore,
		registry:                   opts.ServiceRegistry,
		extraEnvs:                  append([]string{}, opts.ExtraEnvs...),
		stepRetry:                  opts.StepRetry,
		peerConfig:                 opts.PeerConfig,
		workerID:                   opts.WorkerID,
		statusPusher:               opts.StatusPusher,
		logWriterFactory:           opts.LogWriterFactory,
		queuedRun:                  opts.QueuedRun,
		attemptID:                  opts.AttemptID,
		preparedAttempt:            opts.PreparedAttempt,
		triggerType:                opts.TriggerType,
		defaultExecMode:            opts.DefaultExecMode,
		agentConfigStore:           opts.AgentConfigStore,
		agentModelStore:            opts.AgentModelStore,
		agentMemoryStore:           opts.AgentMemoryStore,
		agentSoulStore:             opts.AgentSoulStore,
		agentOAuthManager:          opts.AgentOAuthManager,
		agentRemoteContextResolver: opts.AgentRemoteContextResolver,
		scheduleTime:               opts.ScheduleTime,
	}

	// Initialize progress display if enabled
	if opts.ProgressDisplay {
		a.progressDisplay = createProgressReporter(dag, dagRunID, dag.Params)
	}

	if opts.PreparedAttempt != nil {
		a.dagRunAttemptID = opts.PreparedAttempt.ID()
	} else if opts.AttemptID != "" {
		a.dagRunAttemptID = opts.AttemptID
	}

	return a
}

// Run setups the runner and runs the DAG.
func (a *Agent) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	runningStatusDone := make(chan struct{})
	close(runningStatusDone)
	defer func() {
		cancel()
		<-runningStatusDone
	}()

	// Set DAG context for all logs in this function
	ctx = logger.WithValues(ctx,
		tag.Name(a.dag.Name),
		tag.RunID(a.dagRunID),
		tag.AttemptID(a.dagRunAttemptID),
	)

	// Initialize propagators for W3C trace context before anything else
	telemetry.InitializePropagators()

	// Resolve secrets early so they're available for OTel config evaluation.
	// LoadDotEnv is idempotent - safe to call even if already loaded by caller.
	a.dag.LoadDotEnv(ctx)

	secretEnvs, secretErr := a.resolveSecrets(ctx)

	// Build variables map for config evaluation (DAG env + secrets)
	configVars := envSliceToMap(a.dag.Env, secretEnvs)

	// Extract trace context from environment variables if present
	// This must be done BEFORE initializing the tracer so sub DAGs
	// can continue the parent's trace
	if a.dag.OTel != nil && a.dag.OTel.Enabled {
		ctx = telemetry.ExtractTraceContext(ctx)
	}

	// Initialize OpenTelemetry tracer
	tracer, err := telemetry.NewTracer(ctx, a.dag, configVars)
	if err != nil {
		logger.Warn(ctx, "Failed to initialize OpenTelemetry tracer", tag.Error(err))
		// Continue without tracing
	} else {
		a.tracer = tracer
		defer func() {
			if err := tracer.Shutdown(ctx); err != nil {
				logger.Warn(ctx, "Failed to shutdown OpenTelemetry tracer", tag.Error(err))
			}
		}()
	}

	// Start root span for DAG execution
	var span trace.Span
	if a.tracer != nil && a.tracer.IsEnabled() {
		spanAttrs := []attribute.KeyValue{
			attribute.String("dag.name", a.dag.Name),
			attribute.String("dag.run_id", a.dagRunID),
		}
		if a.parentDAGRun.Name != "" {
			spanAttrs = append(spanAttrs, attribute.String("dag.parent_run_id", a.parentDAGRun.ID))
			spanAttrs = append(spanAttrs, attribute.String("dag.parent_name", a.parentDAGRun.Name))
		}

		// For sub DAGs, ensure we're creating the span as a child of the parent context
		spanName := fmt.Sprintf("DAG: %s", a.dag.Name)
		ctx, span = a.tracer.Start(ctx, spanName, trace.WithAttributes(spanAttrs...))
		defer func() {
			// Set final status
			status := a.Status(ctx)
			span.SetAttributes(attribute.String("dag.status", status.Status.String()))
			span.End()
		}()
	}

	if a.rootDAGRun.ID != a.dagRunID {
		logger.Debug(ctx, "Initiating a sub dag-run",
			slog.String("root-run", a.rootDAGRun.String()),
			slog.String("parent-run", a.parentDAGRun.String()),
		)

		a.isSubDAGRun.Store(true)
		if a.parentDAGRun.Zero() {
			return fmt.Errorf("parent dag-run is not specified for the sub dag-run %s", a.dagRunID)
		}
	}

	var attempt exec.DAGRunAttempt

	// Check if the DAG is already running.
	if err := a.checkIsAlreadyRunning(ctx); err != nil {
		return err
	}

	if !a.dry {
		// Setup the attempt for the dag-run.
		// It's not required for dry-run mode.
		att, err := a.setupDAGRunAttempt(ctx)
		if err != nil {
			return fmt.Errorf("failed to setup execution history: %w", err)
		}
		attempt = att
		a.dagRunAttemptID = attempt.ID()

		// Set the attemptID on the log writer factory if it supports it
		if a.logWriterFactory != nil {
			if setter, ok := a.logWriterFactory.(interface{ SetAttemptID(string) }); ok {
				setter.SetAttemptID(a.dagRunAttemptID)
			}
		}
	}

	// Resolve per-run work directory
	cleanupWorkDir, err := a.prepareWorkDir(ctx, attempt)
	if err != nil {
		return err
	}
	if cleanupWorkDir != nil {
		defer cleanupWorkDir()
	}
	if a.artifactDir != "" {
		if err := os.MkdirAll(a.artifactDir, 0o750); err != nil {
			return fmt.Errorf("failed to create artifact directory: %w", err)
		}
	}

	// Initialize the runner
	a.runner = a.newRunner(attempt)

	// Setup the execution plan for the DAG.
	if err := a.setupPlan(ctx); err != nil {
		return fmt.Errorf("failed to setup execution plan: %w", err)
	}

	// Create a new environment for the dag-run.
	dbClient := newDBClient(a.dagRunStore, a.dagStore)

	// Initialize coordinator client factory for distributed execution
	coordinatorCli := a.createCoordinatorClient(ctx)

	contextOpts := []runtime.ContextOption{
		runtime.WithDatabase(dbClient),
		runtime.WithRootDAGRun(a.rootDAGRun),
		runtime.WithParams(a.dag.Params),
		runtime.WithCoordinator(coordinatorCli),
		runtime.WithSecrets(secretEnvs),
		runtime.WithDefaultExecMode(a.defaultExecMode),
	}
	if len(a.extraEnvs) > 0 {
		contextOpts = append(contextOpts, runtime.WithEnvVars(a.extraEnvs...))
	}

	if a.workDir != "" {
		contextOpts = append(contextOpts, runtime.WithWorkDir(a.workDir))
	}
	if a.artifactDir != "" {
		contextOpts = append(contextOpts, runtime.WithArtifactDir(a.artifactDir))
	}
	if a.logWriterFactory != nil {
		contextOpts = append(contextOpts, runtime.WithLogWriterFactory(a.logWriterFactory))
	}
	ctx = runtime.NewContext(ctx, a.dag, a.dagRunID, a.logFile, contextOpts...)

	// Inject agent stores into context via context.Value.
	// This avoids a backwards dependency from the execution context to the agent package.
	if a.agentConfigStore != nil {
		ctx = agentpkg.WithConfigStore(ctx, a.agentConfigStore)
	}
	if a.agentModelStore != nil {
		ctx = agentpkg.WithModelStore(ctx, a.agentModelStore)
	}
	if a.agentMemoryStore != nil {
		ctx = agentpkg.WithMemoryStore(ctx, a.agentMemoryStore)
	}
	if a.agentSoulStore != nil {
		ctx = agentpkg.WithSoulStore(ctx, a.agentSoulStore)
	}
	if a.agentOAuthManager != nil {
		ctx = agentpkg.WithOAuthManager(ctx, a.agentOAuthManager)
	}
	if a.agentRemoteContextResolver != nil {
		ctx = agentpkg.WithRemoteContextResolver(ctx, a.agentRemoteContextResolver)
	}
	if a.dagStore != nil {
		ctx = agentpkg.WithDAGStore(ctx, a.dagStore)
	}
	if a.dagRunStore != nil {
		ctx = agentpkg.WithDAGRunStore(ctx, a.dagRunStore)
	}

	// Add structured logging context
	logFields := []slog.Attr{
		tag.DAG(a.dag.Name),
		tag.RunID(a.dagRunID),
	}
	if a.isSubDAGRun.Load() {
		logFields = append(logFields,
			slog.String("root", a.rootDAGRun.String()),
			slog.String("parent", a.parentDAGRun.String()),
		)
	}
	ctx = logger.WithValues(ctx, logFields...)

	// Handle dry execution.
	if a.dry {
		return a.dryRun(ctx)
	}

	// initErr is used to capture any initialization errors that occur
	// before the agent starts running the DAG.
	var initErr error

	// Open the run file to write the status.
	// TODO: Check if the run file already exists and if it does, return an error.
	// This is to prevent duplicate execution of the same DAG run.
	if err := attempt.Open(ctx); err != nil {
		return fmt.Errorf("failed to open execution history: %w", err)
	}

	// Evaluate SMTP and mail configs with environment variables and secrets.
	// This must happen AFTER attempt.Open() to avoid persisting expanded secrets.
	if err := a.evaluateMailConfigs(ctx); err != nil {
		return err
	}

	// Evaluate registry auth credentials with environment variables and secrets.
	if err := a.evaluateRegistryAuths(ctx); err != nil {
		return err
	}

	// Evaluate working directory with environment variables.
	if err := a.evaluateWorkingDir(ctx); err != nil {
		return err
	}

	// Evaluate S3 configuration with environment variables and secrets.
	if err := a.evaluateS3Config(ctx); err != nil {
		return err
	}

	// Setup the reporter to send notifications (must be after mail config evaluation)
	a.setupReporter(ctx)

	// Update the initial persisted status.
	st := a.Status(ctx)
	st.Status = core.Running
	a.writeStatus(ctx, attempt, st)

	defer func() {
		if initErr != nil {
			a.initFailed.Store(true)
			logger.Error(ctx, "Failed to initialize DAG execution", tag.Error(initErr))
			st := a.Status(ctx)
			st.Status = core.Failed
			if st.FinishedAt == "" {
				st.FinishedAt = exec.FormatTime(time.Now())
			}
			a.writeStatus(ctx, attempt, st)
		}
		if err := attempt.Close(ctx); err != nil {
			logger.Error(ctx, "Failed to close runstore store", tag.Error(err))
		}
	}()

	// If there was an error resolving secrets, stop execution here
	if secretErr != nil {
		initErr = secretErr // Stop execution if secret resolution failed
		return initErr
	}

	a.writeStatus(ctx, attempt, a.Status(ctx))

	// Start the unix socket server for receiving HTTP requests from
	// the local client (e.g., the frontend server, etc).
	if err := a.setupSocketServer(ctx); err != nil {
		initErr = fmt.Errorf("failed to setup unix socket server: %w", err)
		return initErr
	}

	// Ensure working directory exists
	if err := os.MkdirAll(a.evaluatedWorkingDir, 0o755); err != nil {
		initErr = fmt.Errorf("failed to create working directory: %w", err)
		return initErr
	}

	// Do not change the process working directory here. Agent runs can execute
	// concurrently in the same process, so step executors receive WorkingDir
	// through the runtime context and set per-command working directories.

	// Create a new container if the DAG has a container configuration.
	if a.dag.Container != nil {
		// Expand environment variables in container fields
		expandedContainer, err := docker.EvalContainerFields(ctx, *a.dag.Container)
		if err != nil {
			initErr = fmt.Errorf("failed to evaluate container config: %w", err)
			return initErr
		}
		// Use pre-evaluated registry auth credentials
		ctCfg, err := docker.LoadConfig(a.evaluatedWorkingDir, expandedContainer, a.evaluatedRegistryAuths)
		if err != nil {
			initErr = fmt.Errorf("failed to load container config: %w", err)
			return initErr
		}
		ctCli, err := docker.InitializeClient(ctx, ctCfg)
		if err != nil {
			initErr = fmt.Errorf("failed to initialize container client: %w", err)
			return initErr
		}
		// In exec mode, we use an existing container - don't create a new one
		isExecMode := expandedContainer.IsExecMode()
		if !isExecMode {
			if err := ctCli.CreateContainerKeepAlive(ctx); err != nil {
				initErr = fmt.Errorf("failed to create keepalive container: %w", err)
				return initErr
			}
		}

		// Set the container client in the context for the execution.
		ctx = docker.WithContainerClient(ctx, ctCli)

		defer func() {
			// Only stop the container if we created it (non-exec mode)
			if !isExecMode {
				ctCli.StopContainerKeepAlive(ctx)
			}
			ctCli.Close(ctx)
		}()
	}

	// Create SSH Client if the DAG has SSH configuration.
	if a.dag.SSH != nil {
		var sshTimeout time.Duration
		if a.dag.SSH.Timeout != "" {
			parsed, err := time.ParseDuration(a.dag.SSH.Timeout)
			if err != nil {
				initErr = fmt.Errorf("invalid ssh timeout duration %q: %w", a.dag.SSH.Timeout, err)
				return initErr
			}
			sshTimeout = parsed
		}

		// Build bastion config if present
		var bastionCfg *ssh.BastionConfig
		if a.dag.SSH.Bastion != nil {
			bastionCfg = &ssh.BastionConfig{
				Host:     a.dag.SSH.Bastion.Host,
				Port:     a.dag.SSH.Bastion.Port,
				User:     a.dag.SSH.Bastion.User,
				Key:      a.dag.SSH.Bastion.Key,
				Password: a.dag.SSH.Bastion.Password,
			}
		}

		sshConfig, err := eval.Object(ctx, ssh.Config{
			User:          a.dag.SSH.User,
			Host:          a.dag.SSH.Host,
			Port:          a.dag.SSH.Port,
			Key:           a.dag.SSH.Key,
			Password:      a.dag.SSH.Password,
			StrictHostKey: a.dag.SSH.StrictHostKey,
			KnownHostFile: a.dag.SSH.KnownHostFile,
			Shell:         a.dag.SSH.Shell,
			ShellArgs:     a.dag.SSH.ShellArgs,
			Timeout:       sshTimeout,
			Bastion:       bastionCfg,
		}, runtime.GetEnv(ctx).UserEnvsMap())
		if err != nil {
			initErr = fmt.Errorf("failed to evaluate ssh config: %w", err)
			return initErr
		}
		cli, err := ssh.NewClient(&sshConfig)
		if err != nil {
			initErr = fmt.Errorf("failed to create ssh client: %w", err)
			return initErr
		}
		ctx = ssh.WithSSHClient(ctx, cli)
	}

	listenerErrCh := make(chan error)
	go execWithRecovery(ctx, func() {
		err := a.socketServer.Serve(ctx, listenerErrCh)
		if err != nil && !errors.Is(err, sock.ErrServerRequestedShutdown) {
			logger.Error(ctx, "Failed to start socket frontend", tag.Error(err))
		}
	})

	// Stop the socket server when the dag-run is finished.
	defer func() {
		if err := a.socketServer.Shutdown(ctx); err != nil {
			logger.Error(ctx, "Failed to shutdown socket frontend", tag.Error(err))
		}
	}()

	// It returns error if it failed to start the unix socket server.
	if err := <-listenerErrCh; err != nil {
		initErr = fmt.Errorf("failed to start the unix socket server: %w", err)
		return initErr
	}

	// Start progress display if enabled
	if a.progressDisplay != nil {
		a.progressDisplay.Start()
		// Don't defer Stop() here - we'll do it after all updates are processed
	}

	// Setup channels to receive status updates for each node in the DAG.
	// It should receive node instance when the node status changes, for
	// example, when started, stopped, or cancelled, etc.
	progressCh := make(chan *runtime.Node)
	progressDone := make(chan struct{})
	var progressDrained bool
	defer func() {
		if !progressDrained {
			close(progressCh)
			<-progressDone
		}
		if a.progressDisplay != nil {
			// Give a small delay to ensure final render
			time.Sleep(100 * time.Millisecond)
			a.progressDisplay.Stop()
		}
	}()
	go execWithRecovery(ctx, func() {
		defer close(progressDone)
		for node := range progressCh {
			status := a.Status(ctx)
			if !a.shouldDelayTerminalStatus(status.Status) {
				a.writeStatus(ctx, attempt, status)
			}
			if err := a.reporter.reportStep(ctx, a.dag, status, node); err != nil {
				logger.Error(ctx, "Failed to report step", tag.Error(err))
			}
			// Update progress display if enabled
			if a.progressDisplay != nil {
				// Convert runner node to models node
				nodeData := node.NodeData()
				modelNode := a.nodeToModelNode(nodeData)
				a.progressDisplay.UpdateNode(modelNode)
				a.progressDisplay.UpdateStatus(&status)
			}
		}
	})

	// Write the first status just after the start to store the running status.
	// If the DAG is already finished, skip it.
	runningStatusDone = make(chan struct{})
	go execWithRecovery(ctx, func() {
		defer close(runningStatusDone)

		timer := time.NewTimer(waitForRunning)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		status := a.Status(ctx)
		if a.finished.Load() || a.shouldDelayTerminalStatus(status.Status) {
			return
		}
		a.writeStatus(ctx, attempt, status)
	})

	// Start the dag-run.
	if a.retryTarget != nil {
		logger.Info(ctx, "DAG run retry started",
			slog.String("retry-target-attempt-id", a.retryTarget.AttemptID),
		)
	} else {
		logger.Info(ctx, "DAG run started", slog.Any("params", a.dag.Params))
	}

	go execWithRecovery(ctx, func() {
		a.watchCancelRequested(ctx, attempt)
	})

	// Add registry authentication to context for docker executors
	if len(a.evaluatedRegistryAuths) > 0 {
		ctx = docker.WithRegistryAuth(ctx, a.evaluatedRegistryAuths)
	}

	// Add S3 configuration to context for S3 executors
	if a.evaluatedS3 != nil {
		ctx = s3.WithS3Config(ctx, a.evaluatedS3)
	}

	lastErr := a.runner.Run(ctx, a.plan, progressCh)

	// Drain the progress goroutine before computing the final status.
	// This prevents the progress goroutine from overwriting the final
	// status with a stale intermediate status (e.g., "Running" instead
	// of "Failed") after the final writeStatus call below.
	close(progressCh)
	<-progressDone
	progressDrained = true

	if coordinatorCli != nil {
		// Cleanup the coordinator client resources if it was created.
		if err := coordinatorCli.Cleanup(ctx); err != nil {
			logger.Warn(ctx, "Failed to cleanup coordinator client", tag.Error(err))
		}
	}

	// Update the finished status to the runstore database.
	finishedStatus := a.Status(ctx)

	if a.artifactFinalizer != nil && a.artifactDir != "" {
		artifactFinalizeStartedAt := time.Now()
		logger.Info(ctx, "Finalizing DAG run artifacts before writing terminal status",
			slog.String("attempt-id", finishedStatus.AttemptID),
			slog.String("artifact-dir", a.artifactDir),
		)
		finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), artifactFinalizeTimeout)
		defer cancelFinalize()
		if err := a.artifactFinalizer.Finalize(finalizeCtx, finishedStatus.AttemptID, a.artifactDir); err != nil {
			logger.Error(ctx, "Failed to finalize DAG run artifacts before writing terminal status",
				tag.Error(err),
				slog.String("attempt-id", finishedStatus.AttemptID),
				slog.String("artifact-dir", a.artifactDir),
				slog.Duration("elapsed", time.Since(artifactFinalizeStartedAt)),
			)
			uploadErr := fmt.Errorf("upload artifacts: %w", err)
			if finishedStatus.Status.IsSuccess() {
				finishedStatus.Status = core.Failed
			}
			if finishedStatus.Error != "" {
				finishedStatus.Error = fmt.Sprintf("%s; failed to upload artifacts: %v", finishedStatus.Error, err)
			} else {
				finishedStatus.Error = fmt.Sprintf("failed to upload artifacts: %v", err)
			}
			if lastErr != nil {
				lastErr = errors.Join(lastErr, uploadErr)
			} else {
				lastErr = uploadErr
			}
		} else {
			logger.Info(ctx, "Finished DAG run artifact finalization; terminal status can be written",
				slog.String("attempt-id", finishedStatus.AttemptID),
				slog.String("artifact-dir", a.artifactDir),
				slog.Duration("elapsed", time.Since(artifactFinalizeStartedAt)),
			)
		}
	}

	// Send final progress update if enabled
	if a.progressDisplay != nil {
		// Update all nodes with their final status
		for _, node := range finishedStatus.Nodes {
			a.progressDisplay.UpdateNode(node)
		}
		a.progressDisplay.UpdateStatus(&finishedStatus)
	}

	// Log execution summary
	logger.Info(ctx, "DAG run finished",
		tag.Status(finishedStatus.Status.String()),
		slog.String("started-at", finishedStatus.StartedAt),
		slog.String("finished-at", finishedStatus.FinishedAt),
	)

	// Collect and write step outputs BEFORE finalizing status (per spec)
	if dagOutputs := a.buildOutputs(ctx, finishedStatus.Status); dagOutputs != nil {
		if err := attempt.WriteOutputs(ctx, dagOutputs); err != nil {
			logger.Error(ctx, "Failed to write outputs", tag.Error(err))
		}
	}

	// Finalize status (after outputs are written)
	a.writeStatus(ctx, attempt, finishedStatus)

	// Stream scheduler log to coordinator if using remote logging (shared-nothing mode)
	if a.logWriterFactory != nil {
		if streamer, ok := a.logWriterFactory.(*remote.LogStreamer); ok {
			if err := streamer.StreamSchedulerLog(ctx, a.logFile); err != nil {
				logger.Warn(ctx, "Failed to stream scheduler log", tag.Error(err))
			}
		}
	}

	// Send the execution report if necessary.
	a.lastErr = lastErr
	if err := a.reporter.send(ctx, a.dag, finishedStatus, lastErr); err != nil {
		logger.Error(ctx, "Mail notification failed", tag.Error(err))
	}

	// Mark the agent finished.
	a.finished.Store(true)

	// Return the last error on the dag-run.
	return lastErr
}

func (a *Agent) shouldDelayTerminalStatus(status core.Status) bool {
	if a.artifactFinalizer == nil || a.artifactDir == "" {
		return false
	}
	switch status {
	case core.Failed, core.Aborted, core.Succeeded, core.PartiallySucceeded, core.Rejected:
		return true
	default:
		return false
	}
}

// nodeToModelNode converts a runner NodeData to an exec.Node.
func (a *Agent) nodeToModelNode(nodeData runtime.NodeData) *exec.Node {
	subRuns := make([]exec.SubDAGRun, len(nodeData.State.SubRuns))
	for i, child := range nodeData.State.SubRuns {
		subRuns[i] = exec.SubDAGRun(child)
	}

	return &exec.Node{
		Step:            nodeData.Step,
		Stdout:          nodeData.State.Stdout,
		Stderr:          nodeData.State.Stderr,
		StartedAt:       stringutil.FormatTime(nodeData.State.StartedAt),
		FinishedAt:      stringutil.FormatTime(nodeData.State.FinishedAt),
		Status:          nodeData.State.Status,
		RetriedAt:       stringutil.FormatTime(nodeData.State.RetriedAt),
		RetryCount:      nodeData.State.RetryCount,
		DoneCount:       nodeData.State.DoneCount,
		Error:           errorString(nodeData.State.Error),
		SubRuns:         subRuns,
		OutputVariables: nodeData.State.OutputVariables,
	}
}

// envSliceToMap converts environment variable slices ("KEY=value") to a map.
func envSliceToMap(envSlices ...[]string) map[string]string {
	result := make(map[string]string)
	for _, envs := range envSlices {
		for _, env := range envs {
			if key, value, found := strings.Cut(env, "="); found {
				result[key] = value
			}
		}
	}
	return result
}

// errorString returns the error message or empty string if err is nil.
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// collectOutputs gathers string-form step outputs into a map for the outputs.json file.
// It iterates through nodes in execution order and collects output values.
// Last value wins for key conflicts.
func (a *Agent) collectOutputs(ctx context.Context) map[string]string {
	outputs := make(map[string]string)

	// Get nodes from the plan in execution order
	nodes := a.plan.Nodes()

	for _, node := range nodes {
		nodeData := node.NodeData()
		step := nodeData.Step

		// Only string-form output participates in outputs.json.
		if step.Output == "" {
			continue
		}

		value, ok := nodeData.StringFormOutputValue()
		if !ok {
			continue
		}

		key := stringutil.ScreamingSnakeToCamel(step.Output)

		// Store the output (last one wins for conflicts)
		outputs[key] = value
	}

	// Warn if total size exceeds 1MB
	if len(outputs) > 0 {
		totalSize := 0
		for k, v := range outputs {
			totalSize += len(k) + len(v)
		}
		if totalSize > 1024*1024 {
			logger.Warn(ctx, "Outputs size exceeds 1MB",
				slog.String("dag", a.dag.Name),
				slog.String("dagRunId", a.dagRunID),
				slog.Int("size", totalSize),
				slog.Int("count", len(outputs)),
			)
		}
	}

	return outputs
}

// buildOutputs creates the full DAGRunOutputs structure with metadata.
// Returns nil if no outputs were collected.
func (a *Agent) buildOutputs(ctx context.Context, finalStatus core.Status) *exec.DAGRunOutputs {
	outputs := a.collectOutputs(ctx)

	if len(outputs) == 0 {
		return nil
	}

	// Mask any secrets in output values to prevent exposing sensitive data
	// Use EnvScope.AllSecrets() for unified source tracking
	rCtx := runtime.GetDAGContext(ctx)
	secrets := rCtx.EnvScope.AllSecrets()
	if len(secrets) > 0 {
		// Convert secret envs map to the format expected by masker
		var secretEnvs []string
		for k, v := range secrets {
			secretEnvs = append(secretEnvs, k+"="+v)
		}
		masker := masking.NewMasker(masking.SourcedEnvVars{
			Secrets: secretEnvs,
		})

		// Mask each output value
		for key, value := range outputs {
			outputs[key] = masker.MaskString(value)
		}
	}

	// Serialize params to JSON
	var paramsJSON string
	if len(a.dag.Params) > 0 {
		if data, err := json.Marshal(a.dag.Params); err == nil {
			paramsJSON = string(data)
		}
	}

	return &exec.DAGRunOutputs{
		Metadata: exec.OutputsMetadata{
			DAGName:     a.dag.Name,
			DAGRunID:    a.dagRunID,
			AttemptID:   a.dagRunAttemptID,
			Status:      finalStatus.String(),
			CompletedAt: stringutil.FormatTime(time.Now()),
			Params:      paramsJSON,
		},
		Outputs: outputs,
	}
}

func (a *Agent) PrintSummary(ctx context.Context) {
	// Always print tree-structured summary after execution
	status := a.Status(ctx)

	// Create a minimal DAG object for the tree renderer
	dag := &core.DAG{Name: status.Name}

	// Enable colors if stdout is a terminal
	config := output.DefaultConfig()
	config.ColorEnabled = term.IsTerminal(int(os.Stdout.Fd()))

	renderer := output.NewRenderer(config)
	summary := renderer.RenderDAGStatus(dag, &status)

	// Write to stdout and sync to ensure output is flushed before program exit
	_, _ = os.Stdout.WriteString(summary)
	_, _ = os.Stdout.WriteString("\n")
	_ = os.Stdout.Sync()
}

// Status collects the current running status of the DAG and returns it.
func (a *Agent) Status(ctx context.Context) exec.DAGRunStatus {
	// Lock to avoid race condition.
	a.lock.RLock()
	defer a.lock.RUnlock()

	source := a.statusSourceTarget()

	// Handle case where runner wasn't initialized (early failure in Run())
	if a.runner == nil {
		statusOpts := []transform.StatusOption{
			transform.WithAttemptID(a.dagRunAttemptID),
			transform.WithHierarchyRefs(a.rootDAGRun, a.parentDAGRun),
			transform.WithArchiveDir(a.artifactDir),
			transform.WithTriggerType(a.triggerType),
			transform.WithAutoRetryCount(a.currentAutoRetryCount()),
		}
		if source != nil {
			statusOpts = append(statusOpts,
				transform.WithQueuedAt(source.QueuedAt),
				transform.WithCreatedAt(source.CreatedAt),
			)
			if source.ScheduleTime != "" {
				statusOpts = append(statusOpts, transform.WithScheduleTime(source.ScheduleTime))
			}
		} else if a.scheduleTime != "" {
			statusOpts = append(statusOpts, transform.WithScheduleTime(a.scheduleTime))
		}
		return transform.NewStatusBuilder(a.dag).
			Create(a.dagRunID, core.Failed, os.Getpid(), time.Time{}, statusOpts...)
	}

	runnerStatus := a.runner.Status(ctx, a.plan)
	if a.initFailed.Load() {
		runnerStatus = core.Failed
	} else if runnerStatus == core.NotStarted && a.plan.IsStarted() {
		// Match the status to the execution plan.
		runnerStatus = core.Running
	}

	opts := []transform.StatusOption{
		transform.WithFinishedAt(a.plan.FinishAt()),
		transform.WithNodes(a.plan.NodeData()),
		transform.WithLogFilePath(a.logFile),
		transform.WithArchiveDir(a.artifactDir),
		transform.WithOnInitNode(a.runner.HandlerNode(core.HandlerOnInit)),
		transform.WithOnExitNode(a.runner.HandlerNode(core.HandlerOnExit)),
		transform.WithOnSuccessNode(a.runner.HandlerNode(core.HandlerOnSuccess)),
		transform.WithOnFailureNode(a.runner.HandlerNode(core.HandlerOnFailure)),
		transform.WithOnAbortNode(a.runner.HandlerNode(core.HandlerOnAbort)),
		transform.WithOnWaitNode(a.runner.HandlerNode(core.HandlerOnWait)),
		transform.WithAttemptID(a.dagRunAttemptID),
		transform.WithHierarchyRefs(a.rootDAGRun, a.parentDAGRun),
		transform.WithPreconditions(a.dag.Preconditions),
		transform.WithWorkerID(a.workerID),
		transform.WithTriggerType(a.triggerType),
		transform.WithAutoRetryCount(a.currentAutoRetryCount()),
	}

	// If the current execution is based on a persisted target, copy timing data
	// from that target. Otherwise, use the schedule time provided directly.
	// Otherwise, use the schedule time provided directly via CLI flag.
	if source != nil {
		opts = append(opts,
			transform.WithQueuedAt(source.QueuedAt),
			transform.WithCreatedAt(source.CreatedAt),
		)
		if source.ScheduleTime != "" {
			opts = append(opts, transform.WithScheduleTime(source.ScheduleTime))
		}
	} else if a.scheduleTime != "" {
		opts = append(opts, transform.WithScheduleTime(a.scheduleTime))
	}

	// Create the status object to record the current status.
	status := transform.NewStatusBuilder(a.dag).
		Create(
			a.dagRunID,
			runnerStatus,
			os.Getpid(),
			a.plan.StartAt(),
			opts...,
		)
	return status
}

func (a *Agent) currentAutoRetryCount() int {
	if a.retryTarget == nil {
		return 0
	}
	return a.retryTarget.AutoRetryCount
}

func (a *Agent) statusSourceTarget() *exec.DAGRunStatus {
	return a.retryTarget
}

func (a *Agent) prepareWorkDir(ctx context.Context, attempt exec.DAGRunAttempt) (func(), error) {
	if attempt == nil {
		return nil, nil
	}

	a.workDir = attempt.WorkDir()
	if a.workDir != "" {
		return nil, nil
	}

	a.workDir = filepath.Join(
		os.TempDir(),
		fmt.Sprintf("dagu_%s_%s", fileutil.SafeName(a.dag.Name), a.dagRunID),
	)
	if err := os.MkdirAll(a.workDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}
	if a.dagRunStore != nil {
		return nil, nil
	}

	return func() {
		if a.workDir == "" {
			return
		}
		if err := os.RemoveAll(a.workDir); err != nil {
			logger.Warn(ctx, "Failed to remove temp work dir", tag.Error(err))
		}
	}, nil
}

// writeStatus writes the current status to storage.
// In shared-nothing mode (statusPusher is set), it only pushes to the coordinator.
// In local mode, it writes to local storage via attempt.Write.
func (a *Agent) writeStatus(ctx context.Context, attempt exec.DAGRunAttempt, status exec.DAGRunStatus) {
	if a.statusPusher != nil {
		a.pushStatus(ctx, status)
		return
	}
	a.writeStatusLocally(ctx, attempt, status)
}

func (a *Agent) pushStatus(ctx context.Context, status exec.DAGRunStatus) {
	pushCtx := context.WithoutCancel(ctx)
	if err := a.statusPusher.Push(pushCtx, status); err != nil {
		logger.Error(ctx, "Failed to push status to coordinator", tag.Error(err))
		var rejectedErr *remote.AttemptRejectedError
		if errors.As(err, &rejectedErr) && !a.finished.Load() {
			logger.Warn(ctx, "Coordinator rejected the worker attempt; stopping execution",
				tag.AttemptID(a.dagRunAttemptID),
				slog.String("reason", rejectedErr.Reason),
			)
			a.signal(context.Background(), syscall.SIGTERM, true)
		}
	}
}

func (a *Agent) writeStatusLocally(ctx context.Context, attempt exec.DAGRunAttempt, status exec.DAGRunStatus) {
	if attempt == nil {
		return
	}
	if err := attempt.Write(ctx, status); err != nil {
		logger.Error(ctx, "Failed to write status to local storage", tag.Error(err))
	}
}

// watchCancelRequested is a goroutine that watches for cancel requests
func (a *Agent) watchCancelRequested(ctx context.Context, attempt exec.DAGRunAttempt) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Only signal if the agent hasn't finished yet.
			// This handles cancellation from the worker (via heartbeat CancelledRuns)
			// in shared-nothing mode. If the agent already finished normally,
			// we don't want to send an unnecessary SIGTERM.
			if !a.finished.Load() {
				a.signal(context.Background(), syscall.SIGTERM, true)
			}
			return
		case <-ticker.C:
			if cancelled, _ := attempt.IsAborting(ctx); cancelled {
				a.signal(ctx, syscall.SIGTERM, true)
			}
		}
	}
}

// Signal sends the signal to the processes running
func (a *Agent) Signal(ctx context.Context, sig os.Signal) {
	a.signal(ctx, sig, false)
}

// wait before read the running status
const waitForRunning = time.Millisecond * 100
const artifactFinalizeTimeout = 30 * time.Second

// Simple regular expressions for request routing
var (
	statusRe = regexp.MustCompile(`^/status[/]?$`)
	stopRe   = regexp.MustCompile(`^/stop[/]?$`)
)

// HandleHTTP handles HTTP requests via unix socket.
func (a *Agent) HandleHTTP(ctx context.Context) sock.HTTPHandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch {
		case r.Method == http.MethodGet && statusRe.MatchString(r.URL.Path):
			// Return the current status of the dag-run.
			dagStatus := a.Status(ctx)
			dagStatus.Status = core.Running
			statusJSON, err := json.Marshal(dagStatus)
			if err != nil {
				encodeError(w, err)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(statusJSON)
		case r.Method == http.MethodPost && stopRe.MatchString(r.URL.Path):
			// Handle Stop request for the dag-run.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			go func() {
				logger.Info(ctx, "Stop request received")
				a.signal(ctx, syscall.SIGTERM, true)
			}()
		default:
			// Unknown request
			encodeError(
				w, &httpError{Code: http.StatusNotFound, Message: "Not found"},
			)
		}
	}
}

// setupReporter setups the reporter to send the report to the user.
func (a *Agent) setupReporter(ctx context.Context) {
	// Lock to prevent race condition.
	a.lock.Lock()
	defer a.lock.Unlock()

	var senderFn SenderFn
	if a.evaluatedSMTP != nil {
		senderFn = mailer.New(mailer.Config{
			Host:     a.evaluatedSMTP.Host,
			Port:     a.evaluatedSMTP.Port,
			Username: a.evaluatedSMTP.Username,
			Password: a.evaluatedSMTP.Password,
		}).Send
	} else {
		senderFn = func(ctx context.Context, _ string, _ []string, subject, _ string, _ []string) error {
			logger.Debug(ctx, "Mail notification is disabled",
				slog.String("subject", subject),
			)
			return nil
		}
	}

	a.reporter = newReporter(senderFn, reporterConfig{
		ErrorMail: a.evaluatedErrorMail,
		InfoMail:  a.evaluatedInfoMail,
		WaitMail:  a.evaluatedWaitMail,
	})
}

// newRunner creates a runner instance for the dag-run.
func (a *Agent) newRunner(attempt exec.DAGRunAttempt) *runtime.Runner {
	// runnerLogDir is the directory to store the log files for each node in the dag-run.
	const dateTimeFormatUTC = "20060102_150405Z"
	ts := time.Now().UTC().Format(dateTimeFormatUTC)
	runnerLogDir := filepath.Join(a.logDir, "run_"+ts+"_"+a.dagRunAttemptID)

	cfg := &runtime.Config{
		LogDir:          runnerLogDir,
		MaxActiveSteps:  a.dag.MaxActiveSteps,
		Timeout:         a.dag.Timeout,
		Delay:           a.dag.Delay,
		Dry:             a.dry,
		DAGRunID:        a.dagRunID,
		MessagesHandler: attempt, // Attempt implements ChatMessagesHandler
		OnInit:          a.dag.HandlerOn.Init,
		OnExit:          a.dag.HandlerOn.Exit,
		OnSuccess:       a.dag.HandlerOn.Success,
		OnFailure:       a.dag.HandlerOn.Failure,
		OnAbort:         a.dag.HandlerOn.Abort,
		OnWait:          a.dag.HandlerOn.Wait,
	}

	return runtime.New(cfg)
}

// createCoordinatorClient creates a coordinator client factory for distributed execution
func (a *Agent) createCoordinatorClient(ctx context.Context) runtime.Dispatcher {
	if a.registry == nil {
		logger.Debug(ctx, "Service monitor is not configured; skipping coordinator client creation")
		return nil
	}

	// Create and configure factory
	coordinatorCliCfg := coordinator.DefaultConfig()
	coordinatorCliCfg.MaxRetries = 50

	// Configure the coordinator client based on the global configuration
	coordinatorCliCfg.CAFile = a.peerConfig.ClientCaFile
	coordinatorCliCfg.CertFile = a.peerConfig.CertFile
	coordinatorCliCfg.KeyFile = a.peerConfig.KeyFile
	coordinatorCliCfg.SkipTLSVerify = a.peerConfig.SkipTLSVerify
	coordinatorCliCfg.Insecure = a.peerConfig.Insecure

	return coordinator.New(a.registry, coordinatorCliCfg)
}

// resolveSecrets resolves all secrets defined in the DAG and returns them as
// environment variable strings in "NAME=value" format.
func (a *Agent) resolveSecrets(ctx context.Context) ([]string, error) {
	if len(a.dag.Secrets) == 0 {
		return nil, nil
	}

	logger.Info(ctx, "Resolving secrets", tag.Count(len(a.dag.Secrets)))

	envScope := a.buildEnvScopeForSecrets()
	secretCtx := eval.WithEnvScope(ctx, envScope)

	baseDirs := a.buildSecretBaseDirs(envScope)
	secretRegistry := secrets.NewRegistry(baseDirs...)

	resolvedSecrets, err := secretRegistry.ResolveAll(secretCtx, a.dag.Secrets)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve secrets: %w", err)
	}

	logger.Debug(ctx, "Secrets resolved successfully", tag.Count(len(resolvedSecrets)))
	return resolvedSecrets, nil
}

// buildEnvScopeForSecrets creates an EnvScope with DAG env vars for secret resolution.
func (a *Agent) buildEnvScopeForSecrets() *eval.EnvScope {
	envScope := eval.NewEnvScope(nil, true)
	dagEnvs := make(map[string]string)
	for _, env := range a.dag.Env {
		if key, value, found := strings.Cut(env, "="); found {
			dagEnvs[key] = value
		}
	}
	if len(dagEnvs) > 0 {
		envScope = envScope.WithEntries(dagEnvs, eval.EnvSourceDAGEnv)
	}
	return envScope
}

// buildSecretBaseDirs returns base directories for file-based secret resolution.
func (a *Agent) buildSecretBaseDirs(envScope *eval.EnvScope) []string {
	baseDirs := []string{envScope.Expand(a.dag.WorkingDir)}
	if a.dag.Location != "" {
		baseDirs = append(baseDirs, filepath.Dir(a.dag.Location))
	}
	return baseDirs
}

// evaluateMailConfigs evaluates SMTP and mail notification configs with
// environment variables and secrets. Results are stored in agent fields to
// avoid mutating the original DAG struct.
func (a *Agent) evaluateMailConfigs(ctx context.Context) error {
	vars := runtime.GetEnv(ctx).UserEnvsMap()

	// Evaluate SMTP config if defined
	if a.dag.SMTP != nil {
		evaluated, err := eval.Object(ctx, *a.dag.SMTP, vars)
		if err != nil {
			return fmt.Errorf("failed to evaluate smtp config: %w", err)
		}
		a.evaluatedSMTP = &evaluated
	}

	// Evaluate error mail config if defined
	if a.dag.ErrorMail != nil {
		evaluated, err := eval.Object(ctx, *a.dag.ErrorMail, vars)
		if err != nil {
			return fmt.Errorf("failed to evaluate error mail config: %w", err)
		}
		a.evaluatedErrorMail = &evaluated
	}

	// Evaluate info mail config if defined
	if a.dag.InfoMail != nil {
		evaluated, err := eval.Object(ctx, *a.dag.InfoMail, vars)
		if err != nil {
			return fmt.Errorf("failed to evaluate info mail config: %w", err)
		}
		a.evaluatedInfoMail = &evaluated
	}

	// Evaluate wait mail config if defined
	if a.dag.WaitMail != nil {
		evaluated, err := eval.Object(ctx, *a.dag.WaitMail, vars)
		if err != nil {
			return fmt.Errorf("failed to evaluate wait mail config: %w", err)
		}
		a.evaluatedWaitMail = &evaluated
	}

	return nil
}

// evaluateRegistryAuths evaluates registry authentication credentials with
// environment variables and secrets. Results are stored in agent fields to
// avoid mutating the original DAG struct.
func (a *Agent) evaluateRegistryAuths(ctx context.Context) error {
	if len(a.dag.RegistryAuths) == 0 {
		return nil
	}

	vars := runtime.GetEnv(ctx).UserEnvsMap()
	a.evaluatedRegistryAuths = make(map[string]*core.AuthConfig)

	for registry, auth := range a.dag.RegistryAuths {
		evaluatedAuth, err := eval.Object(ctx, *auth, vars)
		if err != nil {
			return fmt.Errorf("failed to evaluate registry auth for %s: %w", registry, err)
		}
		a.evaluatedRegistryAuths[registry] = &evaluatedAuth
	}

	return nil
}

// evaluateWorkingDir evaluates the working directory with environment variables.
// The result is stored in evaluatedWorkingDir to avoid mutating the original DAG.
func (a *Agent) evaluateWorkingDir(ctx context.Context) error {
	// If working_dir was not explicitly set and we have a per-run work dir,
	// use the work dir as the process working directory.
	if !a.dag.WorkingDirExplicit && a.workDir != "" {
		a.evaluatedWorkingDir = a.workDir
		return nil
	}

	if a.dag.WorkingDir == "" {
		return nil
	}

	// Use runtime context's EnvScope for consistent variable expansion
	rCtx := runtime.GetDAGContext(ctx)
	if rCtx.EnvScope != nil {
		a.evaluatedWorkingDir = rCtx.EnvScope.Expand(a.dag.WorkingDir)
	} else {
		// Fallback to OS expansion if no scope available
		a.evaluatedWorkingDir = os.ExpandEnv(a.dag.WorkingDir)
	}

	// Resolve ~ prefix after variable expansion
	if strings.HasPrefix(a.evaluatedWorkingDir, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to resolve home directory: %w", err)
		}
		a.evaluatedWorkingDir = filepath.Join(homeDir, a.evaluatedWorkingDir[1:])
	}

	return nil
}

// evaluateS3Config evaluates S3 configuration with environment variables and secrets.
// Results are stored in agent fields to avoid mutating the original DAG struct.
func (a *Agent) evaluateS3Config(ctx context.Context) error {
	if a.dag.S3 == nil {
		return nil
	}

	vars := runtime.GetEnv(ctx).UserEnvsMap()
	evaluated, err := eval.Object(ctx, *a.dag.S3, vars)
	if err != nil {
		return fmt.Errorf("failed to evaluate s3 config: %w", err)
	}
	a.evaluatedS3 = &evaluated
	return nil
}

// dryRun performs a dry-run of the DAG. It only simulates the execution of
// the DAG without running the actual command.
func (a *Agent) dryRun(ctx context.Context) error {
	// progressCh channel receives the node when the node status changes.
	// It provides a way to update the status in real-time efficiently.
	progressCh := make(chan *runtime.Node)
	defer close(progressCh)

	go func() {
		for node := range progressCh {
			status := a.Status(ctx)
			_ = a.reporter.reportStep(ctx, a.dag, status, node)
		}
	}()

	db := newDBClient(a.dagRunStore, a.dagStore)
	contextOpts := []runtime.ContextOption{
		runtime.WithDatabase(db),
		runtime.WithRootDAGRun(a.rootDAGRun),
		runtime.WithParams(a.dag.Params),
	}
	if a.artifactDir != "" {
		contextOpts = append(contextOpts, runtime.WithArtifactDir(a.artifactDir))
	}
	dagCtx := runtime.NewContext(ctx, a.dag, a.dagRunID, a.logFile, contextOpts...)
	lastErr := a.runner.Run(dagCtx, a.plan, progressCh)
	a.lastErr = lastErr

	logger.Info(ctx, "Dry-run completed",
		slog.Any("params", a.dag.Params),
	)

	return lastErr
}

// signal propagates the received signal to the all running child processes.
// allowOverride parameters is used to specify if a node can override
// the signal to send to the process, in case the node is configured
// to send a custom signal (e.g., SIGSTOP instead of SIGTERM).
// The reason we need this is to allow the system to kill the child
// process by sending a SIGKILL to force the process to be shutdown.
// if processes do not terminate after MaxCleanUp time, it sends KILL signal.
func (a *Agent) signal(ctx context.Context, sig os.Signal, allowOverride bool) {
	logger.Info(ctx, "Sending signal to running child processes",
		tag.Signal(sig.String()),
		slog.Bool("allow-override", allowOverride),
		slog.Duration("max-cleanup-time", a.dag.MaxCleanUpTime),
	)

	if !signal.IsTerminationSignalOS(sig) {
		// For non-termination signals, just send the signal once and return.
		a.runner.Signal(ctx, a.plan, sig, nil, allowOverride)
		return
	}

	signalCtx, cancel := context.WithTimeout(ctx, a.dag.MaxCleanUpTime)
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		a.runner.Signal(ctx, a.plan, sig, done, allowOverride)
	}()

	resendTicker := time.NewTicker(5 * time.Second)
	defer resendTicker.Stop()
	probeTicker := time.NewTicker(500 * time.Millisecond)
	defer probeTicker.Stop()

	for {
		select {
		case <-done:
			logger.Info(ctx, "All child processes have been terminated")
			return

		case <-signalCtx.Done():
			logger.Info(ctx, "Max cleanup time reached, sending SIGKILL to force termination")
			a.runner.Signal(ctx, a.plan, syscall.SIGKILL, nil, false)
			return

		case <-resendTicker.C:
			logger.Info(ctx, "Resending signal to processes that haven't terminated",
				tag.Signal(sig.String()),
			)
			a.runner.Signal(ctx, a.plan, sig, nil, false)

		case <-probeTicker.C:
			if a.plan != nil && !a.plan.HasActiveNodes() {
				logger.Info(ctx, "No running processes detected, termination complete")
				return
			}
		}
	}
}

// setupPlan setups the DAG plan. If is retry execution, it loads nodes
// from the retry node so that it runs the same DAG as the previous run.
func (a *Agent) setupPlan(ctx context.Context) error {
	if a.retryTarget != nil {
		return a.setupRetryPlan(ctx)
	}
	return a.setupFreshPlan()
}

// setupRetryPlan sets up the plan for retry.
func (a *Agent) setupRetryPlan(ctx context.Context) error {
	nodes := a.retryNodes()
	// If the previous run was killed before writing node data to the status
	// (e.g., SIGKILL before the initial 100ms status write), retryTarget.Nodes
	// will be empty. Fall back to a fresh plan from the DAG definition so that
	// the retry actually runs all steps instead of producing a 0-node run.
	if len(nodes) == 0 {
		logger.Warn(ctx, "Retry target has no nodes; falling back to fresh plan from DAG definition")
		if a.stepRetry != "" {
			return fmt.Errorf("cannot retry step %q: previous attempt has no node state", a.stepRetry)
		}
		return a.setupFreshPlan()
	}
	if a.stepRetry != "" {
		return a.setupStepRetryPlan(nodes)
	}
	return a.setupDefaultRetryPlan(ctx, nodes)
}

func (a *Agent) setupFreshPlan() error {
	plan, err := runtime.NewPlan(a.dag.Steps...)
	if err != nil {
		return err
	}
	a.plan = plan
	return nil
}

func (a *Agent) retryNodes() []*runtime.Node {
	nodes := make([]*runtime.Node, 0, len(a.retryTarget.Nodes))
	for _, node := range a.retryTarget.Nodes {
		nodes = append(nodes, transform.ToNode(node))
	}
	return nodes
}

// setupStepRetryPlan sets up the plan for retrying a specific step.
func (a *Agent) setupStepRetryPlan(nodes []*runtime.Node) error {
	plan, err := runtime.CreateStepRetryPlan(a.dag, nodes, a.stepRetry)
	if err != nil {
		return err
	}
	a.plan = plan
	return nil
}

// setupDefaultRetryPlan sets up the plan for the default retry behavior (all failed/canceled nodes and downstreams).
func (a *Agent) setupDefaultRetryPlan(ctx context.Context, nodes []*runtime.Node) error {
	plan, err := runtime.CreateRetryPlan(ctx, a.dag, nodes...)
	if err != nil {
		return err
	}
	a.plan = plan
	return nil
}

func (a *Agent) setupDAGRunAttempt(ctx context.Context) (exec.DAGRunAttempt, error) {
	if a.dagRunStore == nil {
		logger.Debug(ctx, "Using no-op DAGRunAttempt in shared-nothing mode")
		return exec.NewNoopDAGRunAttempt(a.noopAttemptID(), a.dag), nil
	}

	if a.dag.HistRetentionRuns == 0 {
		retentionDays := a.dag.HistRetentionDays
		if _, err := a.dagRunStore.RemoveOldDAGRuns(ctx, a.dag.Name, retentionDays); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	var attempt exec.DAGRunAttempt
	if a.preparedAttempt != nil {
		if a.attemptID != "" && a.preparedAttempt.ID() != a.attemptID {
			return nil, fmt.Errorf(
				"prepared attempt ID %q does not match requested attempt ID %q",
				a.preparedAttempt.ID(),
				a.attemptID,
			)
		}
		a.preparedAttempt.SetDAG(a.dag)
		attempt = a.preparedAttempt
	} else {
		var err error
		attempt, err = a.dagRunStore.CreateAttempt(ctx, a.dag, time.Now(), a.dagRunID, a.attemptOptions())
		if err != nil {
			return nil, err
		}
	}

	if a.dag.HistRetentionRuns > 0 {
		if _, err := a.dagRunStore.RemoveOldDAGRuns(ctx, a.dag.Name, 0, exec.WithRetentionRuns(a.dag.HistRetentionRuns)); err != nil {
			logger.Error(ctx, "DAG runs data cleanup failed", tag.Error(err))
		}
	}

	return attempt, nil
}

func (a *Agent) noopAttemptID() string {
	if a.attemptID != "" {
		return a.attemptID
	}
	return a.dagRunID
}

func (a *Agent) attemptOptions() exec.NewDAGRunAttemptOptions {
	opts := exec.NewDAGRunAttemptOptions{
		Retry:     a.retryTarget != nil || a.queuedRun,
		AttemptID: a.attemptID,
	}
	if a.isSubDAGRun.Load() {
		opts.RootDAGRun = &a.rootDAGRun
	}
	return opts
}

// setupSocketServer creates a socket server instance.
func (a *Agent) setupSocketServer(ctx context.Context) error {
	socketServer, err := sock.NewServer(a.socketAddr(), a.HandleHTTP(ctx))
	if err != nil {
		return err
	}
	a.socketServer = socketServer
	return nil
}

func (a *Agent) socketAddr() string {
	if a.isSubDAGRun.Load() {
		return a.dag.SockAddrForSubDAGRun(a.dagRunID)
	}
	return a.dag.SockAddr(a.dagRunID)
}

// checkIsAlreadyRunning returns error if the DAG is already running.
func (a *Agent) checkIsAlreadyRunning(ctx context.Context) error {
	if a.isSubDAGRun.Load() {
		return nil
	}
	if !a.dagRunMgr.IsRunning(ctx, a.dag, a.dagRunID) {
		return nil
	}
	return fmt.Errorf("already running. dag-run ID=%s, socket=%s", a.dagRunID, a.dag.SockAddr(a.dagRunID))
}

// execWithRecovery executes a function with panic recovery and logs any panics.
func execWithRecovery(ctx context.Context, fn func()) {
	defer func() {
		if panicObj := recover(); panicObj != nil {
			logRecoveredPanic(ctx, panicObj)
		}
	}()
	fn()
}

func logRecoveredPanic(ctx context.Context, panicObj any) {
	logger.Error(ctx, "Recovered from panic",
		slog.String("err", panicToError(panicObj).Error()),
		slog.String("errType", fmt.Sprintf("%T", panicObj)),
		slog.String("stackTrace", string(debug.Stack())),
	)
}

// panicToError converts a panic value to an error.
func panicToError(panicObj any) error {
	if err, ok := panicObj.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", panicObj)
}

type httpError struct {
	Code    int
	Message string
}

// Error implements error interface.
func (e *httpError) Error() string { return e.Message }

// encodeError returns error to the HTTP client.
func encodeError(w http.ResponseWriter, err error) {
	var httpErr *httpError
	if !errors.As(err, &httpErr) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Error(w, httpErr.Error(), httpErr.Code)
}
