// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/mailer"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/output"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/docker"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/s3"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/ssh"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/resourcelimit"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/secret/providers"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/telemetry"
	"github.com/dagucloud/dagu/v2/internal/workspace"

	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
)

var (
	currentPIDStartedAtOnce  sync.Once
	currentPIDStartedAtValue int64
)

// Agent is responsible for running the DAG and handling communication
// via the unix socket. The agent performs the following tasks:
// 1. Start the DAG.
// 2. Propagate a signal to the running processes.
// 3. Handle the HTTP request via the unix socket.
// 4. Write the log and status to the data store.
type Agent struct {
	lock          sync.RWMutex
	statusWriteMu sync.Mutex

	// dry indicates if the agent is running in dry-run mode.
	dry     bool
	noReuse bool

	// retryTarget is the target status to retry the DAG.
	// It is nil if it's not a retry execution.
	retryTarget *ir.DAGRunStatus

	// dagLoader resolves DAG definitions for runtime lookups.
	dagLoader dagDetailsLoader

	// runStateStore opens execution state for this run.
	runStateStore runstate.Store

	// stateStore is the persistent state store shared across DAG runs.
	stateStore dagrun.StateStore

	// materializationStore coordinates build file materializations.
	materializationStore build.MaterializationStore

	// secretStore resolves workspace-local team-managed secret references.
	secretStore secretpkg.Store

	// profileStore resolves runtime profiles selected for DAG execution.
	profileStore profilepkg.Store

	// registry is the service registry to find the coordinator service.
	registry serviceregistry.ServiceRegistry

	// peerConfig is the configuration for the peer connections.
	peerConfig config.Peer

	// dagRunMgr manages persisted DAG-run history.
	dagRunMgr runtime.Manager

	// runner is the runner instance to run the DAG.
	runner *runtime.Runner

	// plan is the execution plan for the DAG.
	plan *runtime.Plan

	// reporter is responsible for sending the report to the user.
	reporter *reporter

	// socketServer is the unix socket server to handle HTTP requests.
	// It listens to the requests from the local client (e.g., frontend server).
	socketServer SocketServer
	// socketServerFactory creates the local status/control transport.
	socketServerFactory SocketServerFactory

	// logDir is the directory to store the log files for each node in the DAG.
	logDir string

	// logFile is the file to write the runner log.
	logFile string

	// artifactDir is the per-run artifact directory when artifact storage is enabled.
	artifactDir string

	// dagRunLogDir is the base log directory for newly persisted child DAG runs.
	dagRunLogDir string

	// dagRunArtifactDir is the base artifact directory for newly persisted child DAG runs.
	dagRunArtifactDir string

	// artifactFinalizer persists artifacts before the final terminal status is written.
	artifactFinalizer ArtifactFinalizer

	// dag is the DAG to run.
	dag *ir.DAG

	// rootDAGRun indicates the root dag-run of the current dag-run.
	// If the current dag-run is the root dag-run, it is the same as the current
	// DAG name and dag-run ID.
	rootDAGRun ir.DAGRunRef

	// parentDAGRun is the execution reference of the parent dag-run.
	parentDAGRun ir.DAGRunRef

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
	// includeDownstream resets reachable descendants of stepRetry.
	includeDownstream bool

	// retryPath identifies the persisted child invocation selected by a root retry.
	retryPath dagrun.RetryPath

	// workerID is the identifier of the worker executing this DAG run.
	workerID string

	// triggerType indicates how this DAG run was initiated.
	triggerType ir.TriggerType
	// triggerActor identifies the attributable actor that initiated the DAG run.
	triggerActor string
	// parallelItem is the value bound to ITEM for a parallel child run.
	parallelItem string

	// defaultExecMode is the server-level default execution mode.
	defaultExecMode config.ExecutionMode

	// tracer is the OpenTelemetry tracer for the agent.
	tracer *telemetry.Tracer

	// statusPusher is used to push status updates to a remote coordinator.
	// When nil, status is written to local filesystem via the run-state attempt.
	statusPusher StatusPusher

	// subWorkflowRunnerFactory creates a runner for child workflows.
	subWorkflowRunnerFactory SubWorkflowRunnerFactory

	// logWriterFactory is used to create log writers for step output.
	// When nil, logs are written to local filesystem.
	logWriterFactory runctx.LogWriterFactory

	// scheduleTime is the RFC 3339 timestamp of when this run was scheduled.
	// Set by the scheduler for cron-triggered runs; empty for manual runs.
	scheduleTime string

	// queuedRun indicates this execution is from a queued item.
	// The dag-run was already created by the enqueue command.
	queuedRun bool

	// attemptID is the attempt ID from the coordinator.
	// When set, the agent creates an attempt with this ID instead of generating a new one.
	attemptID string

	// workDir is the per-run work directory (for DAG_RUN_WORK_DIR).
	workDir string
	// workspaceSeed carries the immutable workspace into inline child workflows.
	workspaceSeed *runtimeexec.WorkspaceSeed
	// extraEnvs are additional execution-scoped env vars injected into the DAG run context.
	extraEnvs []string
	// profileName is the selected runtime profile name for this run.
	profileName string

	// definitionID identifies the persisted DAG definition that started this run.
	definitionID string
	// profileResolvedAt records when the selected runtime profile was resolved.
	profileResolvedAt string
	// profileEntries records non-secret injected key metadata for status/history.
	profileEntries []ir.RuntimeProfileEntry
	// secretReferenceResolver resolves registry refs without requiring local store access.
	secretReferenceResolver providers.ReferenceResolver
	// secretMasker redacts resolved secret values from status/history snapshots.
	secretMasker *masking.Masker

	// remoteDAGLoader loads a DAG from a remote source when local store misses.
	remoteDAGLoader RemoteDAGLoader

	// Evaluated configs - these are expanded at runtime and stored separately
	// to avoid mutating the original DAG struct.
	evaluatedSMTP          *ir.SMTPConfig
	evaluatedErrorMail     *ir.MailConfig
	evaluatedInfoMail      *ir.MailConfig
	evaluatedWaitMail      *ir.MailConfig
	evaluatedRegistryAuths map[string]*ir.AuthConfig
	evaluatedWorkingDir    string
	evaluatedS3            *ir.S3Config
}

// StatusPusher reports DAG run status outside the current execution process.
type StatusPusher = runtime.StatusPusher

// SocketServer handles local status/control requests for a running DAG.
type SocketServer interface {
	Serve(ctx context.Context, listen chan error) error
	Shutdown(ctx context.Context) error
}

// SocketServerFactory creates a local status/control transport.
type SocketServerFactory func(addr string, handlerFunc sock.HTTPHandlerFunc) (SocketServer, error)

// defaultSocketServerFactory creates the production Unix socket server.
func defaultSocketServerFactory(addr string, handlerFunc sock.HTTPHandlerFunc) (SocketServer, error) {
	return sock.NewServer(addr, handlerFunc)
}

// ArtifactFinalizer uploads or persists artifacts before the final terminal status is written.
type ArtifactFinalizer = runtime.ArtifactFinalizer

// SubWorkflowRunnerFactory creates a runner for child workflows.
type SubWorkflowRunnerFactory func(ctx context.Context) (runtimeexec.SubWorkflowRunner, error)

// RemoteDAGLoader loads a DAG definition from a remote source.
// Returns nil, nil when the remote source does not have the DAG.
type RemoteDAGLoader func(ctx context.Context, name string) (*ir.DAG, error)

// Options is the configuration for the Agent.
type Options struct {
	// Dry is a dry-run mode. It does not execute the actual command.
	// Dry runs do not create persisted history.
	Dry bool
	// NoReuse bypasses manifest hits while retaining staged commits.
	NoReuse bool
	// RetryTarget is the persisted status to retry.
	// If it's specified the agent will execute the DAG with the same
	// configuration as the specified history.
	RetryTarget *ir.DAGRunStatus
	// ParentDAGRun is the dag-run reference of the parent dag-run.
	// It is required for sub dag-runs to identify the parent dag-run.
	ParentDAGRun ir.DAGRunRef
	// ProgressDisplay indicates if the progress display should be shown.
	// This is typically enabled for CLI execution in a TTY environment.
	ProgressDisplay bool
	// ExtraEnvs are additional execution-scoped env vars injected into the DAG run context.
	ExtraEnvs []string
	// WorkDir sets the existing per-run work directory.
	WorkDir string
	// WorkspaceSeed carries the workspace into inline child workflows.
	WorkspaceSeed *runtimeexec.WorkspaceSeed
	// StepRetry is the name of the step to retry, if specified.
	StepRetry string
	// IncludeDownstream resets the selected step and every reachable descendant.
	IncludeDownstream bool
	// RetryPath identifies a persisted child DAG step retry.
	RetryPath dagrun.RetryPath
	// WorkerID is the identifier of the worker executing this DAG run.
	// For distributed execution, this is set to the worker's ID.
	// For local execution, this defaults to "local".
	WorkerID string
	// StatusPusher is used to push status updates to a remote coordinator.
	// When nil, status is written to local filesystem via the run-state attempt.
	StatusPusher StatusPusher
	// SubWorkflowRunnerFactory creates a runner for child workflows.
	SubWorkflowRunnerFactory SubWorkflowRunnerFactory
	// LogWriterFactory is used to create log writers for step output.
	// When nil, logs are written to local filesystem.
	LogWriterFactory runctx.LogWriterFactory
	// QueuedRun indicates this execution is from a queued item.
	// When true, the agent will find the existing dag-run (created by enqueue)
	// instead of creating a new one. This is used for distributed execution
	// where the dag-run directory was already created by the scheduler.
	QueuedRun bool
	// AttemptID uses a caller-assigned attempt identifier when non-empty.
	AttemptID string
	// RunStateStore records execution state for this DAG run.
	RunStateStore runstate.Store
	// StateStore is the persistent state store shared across DAG runs.
	StateStore dagrun.StateStore
	// MaterializationStore coordinates build file materializations.
	MaterializationStore build.MaterializationStore
	// SecretStore resolves local registry refs and runtime profile secrets.
	SecretStore secretpkg.Store
	// SecretReferenceResolver resolves DAG-level registry refs.
	// When nil, SecretStore supplies the local resolver.
	SecretReferenceResolver providers.ReferenceResolver
	// ProfileStore resolves named runtime profiles.
	ProfileStore profilepkg.Store
	// ProfileName selects the runtime profile for this DAG run.
	ProfileName string
	// DAGDefinitionID identifies the persisted definition that started this run.
	DAGDefinitionID string
	// ServiceRegistry is the registry for service discovery.
	ServiceRegistry serviceregistry.ServiceRegistry
	// RootDAGRun is the root dag-run reference for sub-DAG runs.
	RootDAGRun ir.DAGRunRef
	// PeerConfig is the configuration for peer communication.
	PeerConfig config.Peer
	// TriggerType indicates how this DAG run was initiated.
	TriggerType ir.TriggerType
	// TriggerActor identifies the attributable actor that initiated the DAG run.
	TriggerActor string
	// ParallelItem is the value bound to ITEM for a parallel child run.
	ParallelItem string
	// DefaultExecMode is the server-level default execution mode.
	DefaultExecMode config.ExecutionMode
	// ScheduleTime is the RFC 3339 timestamp of when this run was scheduled.
	// Set by the scheduler for cron-triggered runs; empty for manual runs.
	ScheduleTime string
	// ArtifactDir is the per-run artifact directory when artifact storage is enabled.
	ArtifactDir string
	// DAGRunLogDir is the base log directory used for child DAG runs created by executors.
	DAGRunLogDir string
	// DAGRunArtifactDir is the base artifact directory used for child DAG runs created by executors.
	DAGRunArtifactDir string
	// ArtifactFinalizer persists artifacts before the final terminal status is written.
	ArtifactFinalizer ArtifactFinalizer
	// RemoteDAGLoader loads a DAG from a remote source when the local repository misses.
	// When nil, no remote fallback is attempted.
	RemoteDAGLoader RemoteDAGLoader
	// SocketServerFactory creates the local status/control transport.
	// When nil, the default Unix socket transport is used.
	SocketServerFactory SocketServerFactory
}

// New creates a new Agent.
func New(
	dagRunID string,
	dag *ir.DAG,
	logDir string,
	logFile string,
	drm runtime.Manager,
	dagRepository *persis.DAGRepository,
	opts Options,
) *Agent {
	var dagLoader dagDetailsLoader
	if dagRepository != nil {
		dagLoader = dagRepository
	}

	a := &Agent{
		rootDAGRun:               opts.RootDAGRun,
		parentDAGRun:             opts.ParentDAGRun,
		dagRunID:                 dagRunID,
		dag:                      dag,
		dry:                      opts.Dry,
		noReuse:                  opts.NoReuse,
		retryTarget:              opts.RetryTarget,
		logDir:                   logDir,
		logFile:                  logFile,
		artifactDir:              opts.ArtifactDir,
		artifactFinalizer:        opts.ArtifactFinalizer,
		dagRunMgr:                drm,
		dagLoader:                dagLoader,
		runStateStore:            opts.RunStateStore,
		stateStore:               opts.StateStore,
		materializationStore:     opts.MaterializationStore,
		secretStore:              opts.SecretStore,
		secretReferenceResolver:  secretReferenceResolverForDAG(dag, opts),
		profileStore:             opts.ProfileStore,
		registry:                 opts.ServiceRegistry,
		extraEnvs:                append([]string{}, opts.ExtraEnvs...),
		workDir:                  opts.WorkDir,
		workspaceSeed:            opts.WorkspaceSeed,
		profileName:              opts.ProfileName,
		definitionID:             opts.DAGDefinitionID,
		stepRetry:                opts.StepRetry,
		includeDownstream:        opts.IncludeDownstream,
		retryPath:                opts.RetryPath,
		peerConfig:               opts.PeerConfig,
		workerID:                 opts.WorkerID,
		statusPusher:             opts.StatusPusher,
		subWorkflowRunnerFactory: opts.SubWorkflowRunnerFactory,
		logWriterFactory:         opts.LogWriterFactory,
		queuedRun:                opts.QueuedRun,
		attemptID:                opts.AttemptID,
		triggerType:              opts.TriggerType,
		triggerActor:             opts.TriggerActor,
		parallelItem:             opts.ParallelItem,
		defaultExecMode:          opts.DefaultExecMode,
		scheduleTime:             opts.ScheduleTime,
		dagRunLogDir:             opts.DAGRunLogDir,
		dagRunArtifactDir:        opts.DAGRunArtifactDir,
		socketServerFactory:      opts.SocketServerFactory,
		remoteDAGLoader:          opts.RemoteDAGLoader,
	}
	if a.socketServerFactory == nil {
		a.socketServerFactory = defaultSocketServerFactory
	}

	// Initialize progress display if enabled
	if opts.ProgressDisplay {
		a.progressDisplay = createProgressReporter(dag, dagRunID, dag.Params)
	}

	if opts.AttemptID != "" {
		a.dagRunAttemptID = opts.AttemptID
	}

	return a
}

func secretReferenceResolverForDAG(dag *ir.DAG, opts Options) providers.ReferenceResolver {
	if opts.SecretReferenceResolver != nil {
		return opts.SecretReferenceResolver
	}
	if opts.SecretStore == nil {
		return nil
	}
	return secretpkg.NewReferenceResolver(opts.SecretStore, workspaceNameFromDAG(dag))
}

func workspaceNameFromDAG(dag *ir.DAG) string {
	if dag == nil {
		return ""
	}
	name, ok := workspace.WorkspaceNameFromLabels(dag.Labels)
	if !ok {
		return ""
	}
	return name
}

// Run setups the runner and runs the DAG.
func (a *Agent) Run(ctx context.Context) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	runningStatusDone := make(chan struct{})
	close(runningStatusDone)
	stopRunningStatus := func() {}
	defer func() {
		cancel()
		stopRunningStatus()
		<-runningStatusDone
	}()

	// Set DAG context for all logs in this function.
	centralLogFields := []slog.Attr{
		tag.DAG(a.dag.Name),
		tag.RunID(a.dagRunID),
	}
	if a.workerID != "" {
		centralLogFields = append(centralLogFields, tag.WorkerID(a.workerID))
	}
	ctx = logger.WithValues(ctx, centralLogFields...)
	if !a.parentDAGRun.Zero() && a.dag.HasHumanTaskSteps() {
		return fmt.Errorf("DAG %q contains human task steps and cannot run as a sub-DAG", a.dag.Name)
	}

	// Initialize propagators for W3C trace context before anything else
	telemetry.InitializePropagators()

	// Resolve the per-run environment before secrets and runtime configuration.
	resolvedEnv, dotenvErr := runtimeenv.Resolve(ctx, a.dag)
	a.dag.Env = resolvedEnv.Env
	a.dag.RuntimeResolved = true
	for _, warning := range resolvedEnv.Warnings {
		logger.Warn(ctx, warning)
	}

	secretEnvs, secretErr := a.resolveSecrets(ctx)
	profileValues, profileErr := a.resolveProfile(ctx)
	a.lock.Lock()
	a.secretMasker = newStatusSecretMasker(append(profileValues.allSecrets(), secretEnvs...))
	a.lock.Unlock()

	configVars := runtimeConfigVars(a.dag.Env, profileValues, secretEnvs)

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
		if a.workerID != "" {
			spanAttrs = append(spanAttrs, attribute.String("dag.worker_id", a.workerID))
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
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		traceLogFields := []slog.Attr{
			tag.TraceID(spanContext.TraceID().String()),
			tag.SpanID(spanContext.SpanID().String()),
			tag.TraceFlags(spanContext.TraceFlags().String()),
		}
		centralLogFields = append(centralLogFields, traceLogFields...)
		ctx = logger.WithValues(ctx, traceLogFields...)
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

	var attempt runstate.Attempt

	// Check if the DAG is already running.
	if err := a.checkIsAlreadyRunning(ctx); err != nil {
		return err
	}

	if !a.dry {
		// Setup the attempt for the dag-run.
		// It's not required for dry-run mode.
		att, err := a.setupAttempt(ctx)
		if err != nil {
			return fmt.Errorf("failed to setup execution history: %w", err)
		}
		attempt = att
		a.dagRunAttemptID = attempt.ID()
		if span != nil {
			span.SetAttributes(attribute.String("dag.attempt_id", a.dagRunAttemptID))
		}

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

	// Initialize the runner and execution plan for the DAG.
	runner := a.newRunner(attempt)
	plan, err := a.setupPlan(ctx)
	if err != nil {
		return fmt.Errorf("failed to setup execution plan: %w", err)
	}
	a.lock.Lock()
	a.runner = runner
	a.plan = plan
	a.lock.Unlock()

	// Create a new environment for the dag-run.
	dagLoader := newDAGLoader(a.dagLoader, a.remoteDAGLoader)

	subWorkflowRunner, err := a.createSubWorkflowRunner(ctx)
	if err != nil {
		return err
	}

	contextOpts := []runtime.ContextOption{
		runtime.WithDAGLoader(dagLoader),
		runtime.WithRunStateStore(a.runStateStore),
		runtime.WithRootDAGRun(a.rootDAGRun),
		runtime.WithRetryPath(a.retryPath),
		runtime.WithIncludeDownstream(a.includeDownstream),
		runtime.WithAttemptID(a.dagRunAttemptID),
		runtime.WithWorkerID(a.workerID),
		runtime.WithTriggerType(a.triggerType),
		runtime.WithTriggerActor(a.triggerActor),
		runtime.WithRunStartedAt(contextTimeString(a.plan.StartAt())),
		runtime.WithParams(a.dag.Params),
		runtime.WithDefaultSecrets(profileValues.defaultSecrets),
		runtime.WithSecrets(append(profileValues.selectedSecrets, secretEnvs...)),
		runtime.WithDefaultExecMode(a.defaultExecMode),
		runtime.WithRuntimeProfile(a.profileName, a.profileResolvedAt, a.profileEntries),
	}
	if scheduleTime := a.contextScheduleTime(); scheduleTime != "" {
		contextOpts = append(contextOpts, runtime.WithScheduleTime(scheduleTime))
	}
	if len(profileValues.defaultEnvs) > 0 {
		contextOpts = append(contextOpts, runtime.WithDefaultEnvVars(profileValues.defaultEnvs...))
	}
	envs := append(profileValues.selectedEnvs, a.extraEnvs...)
	if len(envs) > 0 {
		contextOpts = append(contextOpts, runtime.WithEnvVars(envs...))
	}

	if a.workDir != "" {
		contextOpts = append(contextOpts, runtime.WithWorkDir(a.workDir))
	}
	if a.artifactDir != "" {
		contextOpts = append(contextOpts, runtime.WithArtifactDir(a.artifactDir))
	}
	if a.stateStore != nil {
		contextOpts = append(contextOpts, runtime.WithStateStore(a.stateStore))
	}
	if a.materializationStore != nil {
		contextOpts = append(contextOpts, runtime.WithMaterializationStore(a.materializationStore))
	}
	if a.noReuse {
		contextOpts = append(contextOpts, runtime.WithNoReuse(true))
	}
	if a.dagRunLogDir != "" {
		contextOpts = append(contextOpts, runtime.WithDAGRunLogDir(a.dagRunLogDir))
	}
	if a.dagRunArtifactDir != "" {
		contextOpts = append(contextOpts, runtime.WithDAGRunArtifactDir(a.dagRunArtifactDir))
	}
	if a.logWriterFactory != nil {
		contextOpts = append(contextOpts, runtime.WithLogWriterFactory(a.logWriterFactory))
	}
	ctx = runtime.NewContext(ctx, a.dag, a.dagRunID, a.logFile, contextOpts...)
	ctx = runtimeexec.WithSubWorkflowRunner(ctx, subWorkflowRunner)
	if a.workspaceSeed != nil {
		ctx = runtimeexec.WithWorkspaceSeed(ctx, *a.workspaceSeed)
	}
	ctx, closeSubDAGRunSchedulerLog, replacedLogger := a.withSubDAGRunSchedulerLogWriter(ctx)
	defer closeSubDAGRunSchedulerLog()

	if replacedLogger {
		ctx = logger.WithValues(ctx, centralLogFields...)
	}
	if a.isSubDAGRun.Load() {
		ctx = logger.WithValues(ctx,
			slog.String("root", a.rootDAGRun.String()),
			slog.String("parent", a.parentDAGRun.String()),
		)
	}
	attemptBoundaryLogger := logger.FromContext(ctx)
	if a.dagRunAttemptID != "" {
		ctx = logger.WithValues(ctx, tag.AttemptID(a.dagRunAttemptID))
	}

	if cleaner, ok := subWorkflowRunner.(interface{ Cleanup(context.Context) error }); ok {
		defer func() {
			if err := cleaner.Cleanup(context.WithoutCancel(ctx)); err != nil {
				logger.Warn(ctx, "Failed to cleanup sub-workflow runner", tag.Error(err))
			}
		}()
	}

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

	defer func() {
		if initErr != nil {
			a.initFailed.Store(true)
			logger.Error(ctx, "Failed to initialize DAG execution", tag.Error(initErr))
			st := a.Status(ctx)
			st.Status = ir.Failed
			if st.FinishedAt == "" {
				st.FinishedAt = stringutil.FormatTime(time.Now())
			}
			if a.workDir != "" {
				if err := attempt.SnapshotWorkDir(context.WithoutCancel(ctx), a.workDir); err != nil {
					snapshotErr := fmt.Errorf("snapshot DAG-run work directory: %w", err)
					st.Error = appendDAGRunError(st.Error, snapshotErr)
					runErr = errors.Join(runErr, snapshotErr)
				}
			}
			a.writeStatus(ctx, attempt, st)
		}
		if err := attempt.Close(ctx); err != nil {
			logger.Error(ctx, "Failed to close DAG-run attempt", tag.Error(err))
		}
	}()

	if dotenvErr != nil {
		initErr = fmt.Errorf("failed to load dotenv: %w", dotenvErr)
		return initErr
	}

	// Evaluate SMTP and mail configs with environment variables and secrets.
	// This must happen AFTER attempt.Open() to avoid persisting expanded secrets.
	if err := a.evaluateMailConfigs(ctx); err != nil {
		initErr = err
		return initErr
	}

	// Evaluate registry auth credentials with environment variables and secrets.
	if err := a.evaluateRegistryAuths(ctx); err != nil {
		initErr = err
		return initErr
	}

	// Evaluate working directory with environment variables.
	if err := a.evaluateWorkingDir(ctx); err != nil {
		initErr = err
		return initErr
	}

	// Evaluate S3 configuration with environment variables and secrets.
	if err := a.evaluateS3Config(ctx); err != nil {
		initErr = err
		return initErr
	}

	// Setup the reporter to send notifications (must be after mail config evaluation)
	if err := a.setupReporter(ctx); err != nil {
		initErr = err
		return initErr
	}

	// Update the initial persisted status.
	st := a.Status(ctx)
	st.Status = ir.Running
	a.writeStatus(ctx, attempt, st)

	// If there was an error resolving secrets, stop execution here
	if secretErr != nil {
		initErr = secretErr // Stop execution if secret resolution failed
		return initErr
	}
	if profileErr != nil {
		initErr = profileErr
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
		if a.dag.Resources.HasLimits() && !docker.ApplyResourceLimitsToConfig(ctCfg, a.dag.Resources.Limits) {
			logger.Warn(ctx, "Resource limits requested but cannot be applied to an existing container")
		}
		// Select the daemon (docker or podman) for the DAG-level container from the
		// service-level DAGU_CONTAINER_RUNTIME setting, the same selector used by
		// step-level container jobs and harness.run container steps. Empty
		// (docker/unset) preserves upstream client.FromEnv behavior.
		host, err := docker.ResolveDaemonHost(docker.ServiceRuntimeEnv())
		if err != nil {
			initErr = err
			return initErr
		}
		ctCfg.DaemonHost = host
		ctCli, err := docker.InitializeClient(ctx, ctCfg)
		if err != nil {
			initErr = fmt.Errorf("failed to initialize container client: %w", err)
			return initErr
		}
		// Exec mode uses an existing container and does not create a new one.
		isExecMode := expandedContainer.IsExecMode()
		defer func() {
			// Only stop containers created for this DAG run.
			if !isExecMode {
				ctCli.StopContainerKeepAlive(ctx)
			}
			ctCli.Close(ctx)
		}()

		if !isExecMode {
			if err := ctCli.CreateContainerKeepAlive(ctx); err != nil {
				initErr = fmt.Errorf("failed to create keepalive container: %w", err)
				return initErr
			}
		}

		// Set the container client in the context for the execution.
		ctx = docker.WithContainerClient(ctx, ctCli)
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

		sshConfig, err := evalHostConfigObject(ctx, ssh.Config{
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
		}, runtime.GetEnv(ctx).UserEnvsMap(), "ssh")
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
			if errors.Is(err, sock.ErrUnsupported) {
				return
			}
			logger.Error(ctx, "Failed to start socket frontend", tag.Error(err))
		}
	})

	// It returns error if it failed to start the unix socket server.
	if err := <-listenerErrCh; err != nil {
		if errors.Is(err, sock.ErrUnsupported) {
			logger.Warn(ctx,
				"Unix socket transport unavailable; continuing without live status/control socket",
				tag.Error(err),
			)
		} else {
			initErr = fmt.Errorf("failed to start the unix socket server: %w", err)
			return initErr
		}
	} else {
		// Stop the socket server when the dag-run is finished.
		defer func() {
			if err := a.socketServer.Shutdown(ctx); err != nil {
				logger.Error(ctx, "Failed to shutdown socket frontend", tag.Error(err))
			}
		}()
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
			status := a.recordCurrentStatus(ctx, attempt)
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
	runningStatusCtx, stopRunningStatus := context.WithCancel(ctx)
	runningStatusDone = make(chan struct{})
	go execWithRecovery(runningStatusCtx, func() {
		defer close(runningStatusDone)

		timer := time.NewTimer(waitForRunning)
		defer timer.Stop()

		select {
		case <-runningStatusCtx.Done():
			return
		case <-timer.C:
		}

		a.recordCurrentStatus(ctx, attempt)
	})

	// Start the dag-run.
	attemptBoundaryCtx := logger.WithLogger(ctx, attemptBoundaryLogger)
	if a.retryTarget != nil {
		logger.Info(attemptBoundaryCtx, "DAG run retry started",
			tag.AttemptID(a.dagRunAttemptID),
			slog.String("retry-target-attempt-id", a.retryTarget.AttemptID),
		)
	} else {
		logger.Info(attemptBoundaryCtx, "DAG run started",
			tag.AttemptID(a.dagRunAttemptID),
			slog.Any("params", a.dag.Params),
		)
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

	if a.dag.Container == nil && a.dag.Resources.HasLimits() {
		guard := resourcelimit.Start(ctx, resourcelimit.Options{
			DAGName:  a.dag.Name,
			DAGRunID: a.dagRunID,
			Limits:   a.dag.Resources.Limits,
		})
		result := guard.Result()
		if result.Warning != "" {
			logger.Warn(ctx, result.Warning)
		}
		if result.Enforced {
			logger.Info(ctx, "DAG run resource limits enabled",
				slog.String("enforcer", result.Enforcer),
				slog.String("cpu", a.dag.Resources.Limits.CPU),
				slog.String("memory", a.dag.Resources.Limits.Memory),
			)
			ctx = resourcelimit.WithGuard(ctx, guard)
			defer func() {
				if err := guard.Close(ctx); err != nil {
					logger.Warn(ctx, "Failed to clean up resource limits", tag.Error(err))
				}
			}()
		}
	}

	lastErr := a.runner.Run(ctx, a.plan, progressCh)

	// Drain the progress goroutine before computing the final status.
	// This prevents the progress goroutine from overwriting the final
	// status with a stale intermediate status (e.g., "Running" instead
	// of "Failed") after the final writeStatus call below.
	close(progressCh)
	<-progressDone
	progressDrained = true
	stopRunningStatus()
	<-runningStatusDone

	// Update the persisted terminal status.
	finishedStatus := a.Status(ctx)

	if a.artifactFinalizer != nil && a.artifactDir != "" {
		artifactFinalizeStartedAt := time.Now()
		logger.Info(ctx, "Finalizing DAG run artifacts before writing terminal status",
			slog.String("artifact-dir", a.artifactDir),
		)
		finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), artifactFinalizeTimeout)
		defer cancelFinalize()
		if err := a.artifactFinalizer.Finalize(finalizeCtx, finishedStatus.AttemptID, a.artifactDir); err != nil {
			logger.Error(ctx, "Failed to finalize DAG run artifacts before writing terminal status",
				tag.Error(err),
				slog.String("artifact-dir", a.artifactDir),
				slog.Duration("elapsed", time.Since(artifactFinalizeStartedAt)),
			)
			uploadErr := fmt.Errorf("upload artifacts: %w", err)
			if finishedStatus.Status.IsSuccess() {
				finishedStatus.Status = ir.Failed
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
		if err := attempt.RecordOutputs(ctx, dagOutputs); err != nil {
			logger.Error(ctx, "Failed to write outputs", tag.Error(err))
		}
	}
	if err := attempt.SnapshotWorkDir(context.WithoutCancel(ctx), a.workDir); err != nil {
		snapshotErr := fmt.Errorf("snapshot DAG-run work directory: %w", err)
		if finishedStatus.Status.IsSuccess() {
			finishedStatus.Status = ir.Failed
		}
		finishedStatus.Error = appendDAGRunError(finishedStatus.Error, snapshotErr)
		lastErr = errors.Join(lastErr, snapshotErr)
	}

	if err := a.writeStatus(ctx, attempt, finishedStatus); err != nil {
		logger.Error(ctx, "Failed to persist terminal DAG-run status", tag.Error(err))
		lastErr = errors.Join(lastErr, err)
	}

	// Stream scheduler log to coordinator if remote logging is configured.
	if a.logWriterFactory != nil {
		if streamer, ok := a.logWriterFactory.(runtime.SchedulerLogStreamer); ok {
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

func (a *Agent) shouldDelayTerminalStatus(status ir.Status) bool {
	switch status {
	case ir.Waiting:
		return true
	case ir.Failed, ir.Aborted, ir.Succeeded, ir.PartiallySucceeded, ir.Rejected:
		if a.artifactFinalizer != nil && a.artifactDir != "" {
			return true
		}
		_, streamsSchedulerLog := a.logWriterFactory.(runtime.SchedulerLogStreamer)
		return streamsSchedulerLog
	default:
		return false
	}
}

// nodeToModelNode converts a runner NodeData to an ir.Node.
func (a *Agent) nodeToModelNode(nodeData runtime.NodeData) *ir.Node {
	subRuns := make([]ir.SubDAGRun, len(nodeData.State.SubRuns))
	for i, child := range nodeData.State.SubRuns {
		subRuns[i] = ir.SubDAGRun(child)
	}

	return &ir.Node{
		Step:             nodeData.Step,
		Stdout:           nodeData.State.Stdout,
		Stderr:           nodeData.State.Stderr,
		WorkingDir:       nodeData.State.WorkingDir,
		StartedAt:        stringutil.FormatTime(nodeData.State.StartedAt),
		FinishedAt:       stringutil.FormatTime(nodeData.State.FinishedAt),
		Status:           nodeData.State.Status,
		RetriedAt:        stringutil.FormatTime(nodeData.State.RetriedAt),
		RetryCount:       nodeData.State.RetryCount,
		DoneCount:        nodeData.State.DoneCount,
		Error:            errorString(nodeData.State.Error),
		Build:            nodeData.State.Build,
		SubRuns:          subRuns,
		OutputVariables:  nodeData.State.OutputVariables,
		OutputsValue:     nodeData.State.OutputsValue,
		StepOutputsValue: nodeData.State.StepOutputsValue,
		AgentState:       nodeData.State.AgentState,
		AgentSession:     ir.CloneAgentSession(nodeData.State.AgentSession),
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

func runtimeConfigVars(dagEnv []string, profileValues resolvedProfileValues, secretEnvs []string) map[string]string {
	return envSliceToMap(
		profileValues.defaultEnvs,
		profileValues.defaultSecrets,
		dagEnv,
		profileValues.selectedEnvs,
		profileValues.selectedSecrets,
		secretEnvs,
	)
}

// errorString returns the error message or empty string if err is nil.
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// collectOutputs gathers published and string-form step outputs into outputs.json.
// It iterates through nodes in execution order and collects output values.
// Last value wins for key conflicts.
func (a *Agent) collectOutputs(ctx context.Context) map[string]string {
	outputs := make(map[string]string)

	// Steps and lifecycle handlers both publish to the run's outputs.
	nodes := a.runner.NodesInRunOrder(a.plan)

	for _, node := range nodes {
		nodeData := node.NodeData()
		maps.Copy(outputs, nodeData.OutputsValueStringMap())
		step := nodeData.Step

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
				slog.Int("size", totalSize),
				slog.Int("count", len(outputs)),
			)
		}
	}

	return outputs
}

// buildOutputs creates the full DAGRunOutputs structure with metadata.
// Returns nil if no outputs were collected.
func (a *Agent) buildOutputs(ctx context.Context, finalStatus ir.Status) *ir.DAGRunOutputs {
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

	return &ir.DAGRunOutputs{
		Metadata: ir.OutputsMetadata{
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
	dag := &ir.DAG{Name: status.Name}

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
func (a *Agent) Status(ctx context.Context) ir.DAGRunStatus {
	// Lock to avoid race condition.
	a.lock.RLock()
	defer a.lock.RUnlock()

	source := a.statusSourceTarget()
	parallelItem := a.parallelItem
	if parallelItem == "" && source != nil {
		parallelItem = source.ParallelItem
	}

	// Handle case where runner wasn't initialized (early failure in Run())
	if a.runner == nil {
		statusOpts := []ir.StatusOption{
			ir.WithAttemptID(a.dagRunAttemptID),
			ir.WithHierarchyRefs(a.rootDAGRun, a.parentDAGRun),
			ir.WithWorkingDir(a.evaluatedWorkingDir),
			ir.WithArchiveDir(a.artifactDir),
			ir.WithTriggerType(a.triggerType),
			ir.WithTriggerActor(a.triggerActor),
			ir.WithParallelItem(parallelItem),
			ir.WithAutoRetryCount(a.currentAutoRetryCount()),
			ir.WithPIDStartedAt(currentPIDStartedAt()),
			ir.WithRuntimeProfile(a.profileName, a.profileResolvedAt, a.profileEntries),
			ir.WithDAGDefinitionID(a.definitionID),
			ir.WithNoReuse(a.noReuse),
		}
		if source != nil {
			statusOpts = append(statusOpts,
				ir.WithQueuedAt(source.QueuedAt),
				ir.WithCreatedAt(source.CreatedAt),
				ir.WithAgentSessions(source.AgentSessions),
			)
			if source.ScheduleTime != "" {
				statusOpts = append(statusOpts, ir.WithScheduleTime(source.ScheduleTime))
			}
		} else if a.scheduleTime != "" {
			statusOpts = append(statusOpts, ir.WithScheduleTime(a.scheduleTime))
		}
		status := ir.NewStatusBuilder(a.dag).
			Create(a.dagRunID, ir.Failed, os.Getpid(), time.Time{}, statusOpts...)
		a.maskStatusSecrets(&status)
		return status
	}

	runnerStatus := a.runner.Status(ctx, a.plan)
	if a.initFailed.Load() {
		runnerStatus = ir.Failed
	} else if runnerStatus == ir.NotStarted && a.plan.IsStarted() {
		// Match the status to the execution plan.
		runnerStatus = ir.Running
	}

	opts := []ir.StatusOption{
		ir.WithFinishedAt(a.plan.FinishAt()),
		transform.WithNodes(a.plan.NodeData()),
		ir.WithLogFilePath(a.logFile),
		ir.WithWorkingDir(a.evaluatedWorkingDir),
		ir.WithArchiveDir(a.artifactDir),
		transform.WithOnInitNode(a.runner.HandlerNode(ir.HandlerOnInit)),
		transform.WithOnExitNode(a.runner.HandlerNode(ir.HandlerOnExit)),
		transform.WithOnSuccessNode(a.runner.HandlerNode(ir.HandlerOnSuccess)),
		transform.WithOnFailureNode(a.runner.HandlerNode(ir.HandlerOnFailure)),
		transform.WithOnAbortNode(a.runner.HandlerNode(ir.HandlerOnAbort)),
		transform.WithOnWaitNode(a.runner.HandlerNode(ir.HandlerOnWait)),
		ir.WithAttemptID(a.dagRunAttemptID),
		ir.WithHierarchyRefs(a.rootDAGRun, a.parentDAGRun),
		ir.WithPreconditionResults(a.runner.PreconditionResults()),
		ir.WithWorkerID(a.workerID),
		ir.WithTriggerType(a.triggerType),
		ir.WithTriggerActor(a.triggerActor),
		ir.WithParallelItem(parallelItem),
		ir.WithAutoRetryCount(a.currentAutoRetryCount()),
		ir.WithPIDStartedAt(currentPIDStartedAt()),
		ir.WithRuntimeProfile(a.profileName, a.profileResolvedAt, a.profileEntries),
		ir.WithDAGDefinitionID(a.definitionID),
		ir.WithNoReuse(a.noReuse),
	}

	// If the current execution is based on a persisted target, copy timing data
	// from that target. Otherwise, use the schedule time provided directly.
	// Otherwise, use the schedule time provided directly via CLI flag.
	if source != nil {
		opts = append(opts,
			ir.WithQueuedAt(source.QueuedAt),
			ir.WithCreatedAt(source.CreatedAt),
			ir.WithAgentSessions(source.AgentSessions),
		)
		if source.ScheduleTime != "" {
			opts = append(opts, ir.WithScheduleTime(source.ScheduleTime))
		}
	} else if a.scheduleTime != "" {
		opts = append(opts, ir.WithScheduleTime(a.scheduleTime))
	}

	// Create the status object to record the current status.
	status := ir.NewStatusBuilder(a.dag).
		Create(
			a.dagRunID,
			runnerStatus,
			os.Getpid(),
			a.plan.StartAt(),
			opts...,
		)
	a.maskStatusSecrets(&status)
	return status
}

func currentPIDStartedAt() int64 {
	currentPIDStartedAtOnce.Do(func() {
		startedAt, ok := procutil.StartTime(os.Getpid())
		if ok {
			currentPIDStartedAtValue = startedAt
		}
	})
	return currentPIDStartedAtValue
}

func (a *Agent) currentAutoRetryCount() int {
	if a.retryTarget == nil {
		return 0
	}
	return a.retryTarget.AutoRetryCount
}

func (a *Agent) statusSourceTarget() *ir.DAGRunStatus {
	return a.retryTarget
}

func (a *Agent) contextScheduleTime() string {
	var raw string
	if source := a.statusSourceTarget(); source != nil && source.ScheduleTime != "" {
		raw = source.ScheduleTime
	} else {
		raw = a.scheduleTime
	}
	return contextTimeValue(raw)
}

func contextTimeValue(raw string) string {
	if raw == "" {
		return ""
	}
	t, err := stringutil.ParseTime(raw)
	if err != nil {
		return raw
	}
	return contextTimeString(t)
}

func contextTimeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (a *Agent) prepareWorkDir(ctx context.Context, attempt runstate.Attempt) (func(), error) {
	if a.workDir != "" {
		return nil, a.prepareWorkspace(ctx)
	}
	if attempt == nil {
		return nil, nil
	}

	var err error
	a.workDir, err = attempt.MaterializeWorkDir(ctx)
	if err != nil {
		return nil, fmt.Errorf("materialize DAG-run work directory: %w", err)
	}
	if a.workDir != "" {
		return nil, a.prepareWorkspace(ctx)
	}

	a.workDir = filepath.Join(
		os.TempDir(),
		fmt.Sprintf("dagu_%s_%s", fileutil.SafeName(a.dag.Name), a.dagRunID),
	)
	if err := os.MkdirAll(a.workDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}
	cleanup := func() {
		if a.workDir == "" {
			return
		}
		if err := fileutil.RemoveAll(a.workDir); err != nil {
			logger.Warn(ctx, "Failed to remove temp work dir", tag.Error(err))
		}
	}
	if err := a.prepareWorkspace(ctx); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

func (a *Agent) prepareWorkspace(ctx context.Context) error {
	alreadyMaterialized := a.workspaceSeed != nil
	if a.workspaceSeed == nil {
		seed, err := runtimeexec.PrepareDAGWorkspace(ctx, a.dag)
		if err != nil {
			return err
		}
		a.workspaceSeed = seed
	}
	if a.workspaceSeed == nil {
		return nil
	}
	if a.workDir == "" {
		return fmt.Errorf("DAG file dependencies require a run working directory")
	}
	if !alreadyMaterialized {
		if err := workspacebundle.Extract(
			a.workspaceSeed.Archive,
			a.workDir,
			a.workspaceSeed.Descriptor,
			workspacebundle.DefaultLimits(),
		); err != nil {
			return fmt.Errorf("materialize DAG file dependencies: %w", err)
		}
	}

	a.dag = a.dag.Clone()
	a.dag.WorkingDir = a.workDir
	a.dag.WorkingDirExplicit = true
	return nil
}

func appendDAGRunError(current string, err error) string {
	if current == "" {
		return err.Error()
	}
	return current + "; " + err.Error()
}

// writeStatus writes the current status to storage.
// When statusPusher is set, it pushes to the coordinator.
// Otherwise, it writes to local storage via the run-state attempt.
func (a *Agent) writeStatus(ctx context.Context, attempt runstate.Attempt, status ir.DAGRunStatus) error {
	if a.statusPusher != nil {
		return a.pushStatus(ctx, status)
	}
	return a.writeStatusLocally(ctx, attempt, status)
}

func (a *Agent) recordCurrentStatus(ctx context.Context, attempt runstate.Attempt) ir.DAGRunStatus {
	a.statusWriteMu.Lock()
	defer a.statusWriteMu.Unlock()

	status := a.Status(ctx)
	if a.finished.Load() || a.shouldDelayTerminalStatus(status.Status) {
		return status
	}
	a.writeStatus(ctx, attempt, status)
	return status
}

func (a *Agent) pushStatus(ctx context.Context, status ir.DAGRunStatus) error {
	pushCtx := context.WithoutCancel(ctx)
	timeout := remoteStatusPushTimeout
	if status.Status != ir.NotStarted && !status.Status.IsActive() {
		timeout = remoteTerminalStatusPushTimeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		pushCtx, cancel = context.WithTimeout(pushCtx, timeout)
		defer cancel()
	}
	if err := a.statusPusher.Push(pushCtx, status); err != nil {
		logger.Error(ctx, "Failed to push status to coordinator", tag.Error(err))
		var rejectedErr runtime.AttemptRejected
		if errors.As(err, &rejectedErr) && !a.finished.Load() {
			logger.Warn(ctx, "Coordinator rejected the worker attempt; stopping execution",
				slog.String("reason", rejectedErr.AttemptRejectedReason()),
			)
			a.stopChildren(context.Background(), syscall.SIGTERM, true)
		}
		return err
	}
	return nil
}

func (a *Agent) writeStatusLocally(ctx context.Context, attempt runstate.Attempt, status ir.DAGRunStatus) error {
	if attempt == nil {
		return nil
	}
	if err := attempt.RecordStatus(ctx, status); err != nil {
		logger.Error(ctx, "Failed to write status to local storage", tag.Error(err))
		return err
	}
	return nil
}

// watchCancelRequested is a goroutine that watches for cancel requests
func (a *Agent) watchCancelRequested(ctx context.Context, attempt runstate.Attempt) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Only signal if the agent hasn't finished yet.
			// This handles cancellation from the worker via heartbeat CancelledRuns.
			// If the agent already finished normally, sending an extra SIGTERM is unnecessary.
			if !a.finished.Load() {
				a.stopChildren(context.Background(), syscall.SIGTERM, true)
			}
			return
		case <-ticker.C:
			if cancelled, _ := attempt.CancelRequested(ctx); cancelled {
				a.stopChildren(ctx, syscall.SIGTERM, true)
			}
		}
	}
}

// Signal requests that running child processes stop.
func (a *Agent) Signal(ctx context.Context, sig os.Signal) {
	a.stopChildren(ctx, sig, false)
}

// wait before read the running status
const waitForRunning = time.Millisecond * 100
const artifactFinalizeTimeout = 3 * time.Minute

var remoteStatusPushTimeout = 5 * time.Second
var remoteTerminalStatusPushTimeout = 3 * time.Minute

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
			dagStatus.Status = ir.Running
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
				a.stopChildren(ctx, syscall.SIGTERM, true)
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
func (a *Agent) setupReporter(ctx context.Context) error {
	// Lock to prevent race condition.
	a.lock.Lock()
	defer a.lock.Unlock()

	var senderFn SenderFn
	if a.evaluatedSMTP != nil {
		config, err := mailerConfigFromSMTP(a.evaluatedSMTP)
		if err != nil {
			return fmt.Errorf("invalid smtp config: %w", err)
		}
		senderFn = mailer.New(config).Send
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
	return nil
}

func mailerConfigFromSMTP(config *ir.SMTPConfig) (mailer.Config, error) {
	if config == nil {
		return mailer.Config{}, nil
	}
	return mailer.BuildConfig(config.Host, config.Port, config.Username, config.Password, config.OAuth)
}

// newRunner creates a runner instance for the dag-run.
func (a *Agent) newRunner(attempt runstate.Attempt) *runtime.Runner {
	// runnerLogDir is the directory to store the log files for each node in the dag-run.
	const dateTimeFormatUTC = "20060102_150405Z"
	ts := time.Now().UTC().Format(dateTimeFormatUTC)
	runnerLogDir := filepath.Join(a.logDir, "run_"+ts+"_"+a.dagRunAttemptID)

	autoRetryLimit := 0
	if a.dag.RetryPolicy != nil {
		autoRetryLimit = a.dag.RetryPolicy.Limit
	}

	cfg := &runtime.Config{
		LogDir:               runnerLogDir,
		MaxActiveSteps:       a.dag.MaxActiveSteps,
		Timeout:              a.dag.Timeout,
		Delay:                a.dag.Delay,
		Dry:                  a.dry,
		DAGRunID:             a.dagRunID,
		MessagesHandler:      attempt, // Attempt implements ChatMessagesHandler
		OnInit:               a.dag.HandlerOn.Init,
		OnExit:               a.dag.HandlerOn.Exit,
		OnSuccess:            a.dag.HandlerOn.Success,
		OnFailure:            a.dag.HandlerOn.Failure,
		OnAbort:              a.dag.HandlerOn.Abort,
		OnWait:               a.dag.HandlerOn.Wait,
		MaterializationStore: a.materializationStore,
		NoReuse:              a.noReuse,
		DAGRunAutoRetryCount: a.currentAutoRetryCount(),
		DAGRunAutoRetryLimit: autoRetryLimit,
		DAGRunIsRoot:         a.parentDAGRun.Zero(),
	}
	return runtime.New(cfg)
}

func (a *Agent) createSubWorkflowRunner(ctx context.Context) (runtimeexec.SubWorkflowRunner, error) {
	if a.subWorkflowRunnerFactory == nil {
		if a.registry != nil {
			logger.Debug(ctx, "Sub-workflow runner factory is not configured; running in local-only mode")
		}
		return nil, nil
	}

	return a.subWorkflowRunnerFactory(ctx)
}

func (a *Agent) withSubDAGRunSchedulerLogWriter(ctx context.Context) (context.Context, func(), bool) {
	if !a.isSubDAGRun.Load() || a.logFile == "" {
		return ctx, func() {}, false
	}
	streamer, ok := a.logWriterFactory.(runtime.SchedulerLogStreamer)
	if !ok || streamer == nil {
		return ctx, func() {}, false
	}

	file, err := os.OpenFile(a.logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		logger.Warn(ctx, "Failed to open sub DAG scheduler log file", tag.File(a.logFile), tag.Error(err))
		return ctx, func() {}, false
	}

	writer := streamer.NewSchedulerLogWriter(ctx, file)
	ctx = logger.WithLogger(ctx, logger.NewLogger(logger.WithRunWriter(writer)))
	return ctx, func() {
		if err := writer.Close(); err != nil {
			logger.Warn(ctx, "Failed to close sub DAG scheduler log streamer", tag.Error(err))
		}
		if err := file.Close(); err != nil {
			logger.Warn(ctx, "Failed to close sub DAG scheduler log file", tag.File(a.logFile), tag.Error(err))
		}
	}, true
}

type resolvedProfileValues struct {
	defaultEnvs     []string
	defaultSecrets  []string
	selectedEnvs    []string
	selectedSecrets []string
}

func (v resolvedProfileValues) allSecrets() []string {
	out := make([]string, 0, len(v.defaultSecrets)+len(v.selectedSecrets))
	out = append(out, v.defaultSecrets...)
	out = append(out, v.selectedSecrets...)
	return out
}

// resolveProfile resolves inherited defaults and the selected runtime profile.
func (a *Agent) resolveProfile(ctx context.Context) (resolvedProfileValues, error) {
	var values resolvedProfileValues
	if a.profileStore == nil {
		if a.profileName == "" {
			return values, nil
		}
		return values, fmt.Errorf("profile store is not configured")
	}

	resolver := profilepkg.NewResolver(a.profileStore, a.secretStore)
	defaultLayers, err := a.resolveInheritedProfiles(ctx, resolver)
	if err != nil {
		return values, err
	}
	defaults := profilepkg.MergeResolved("defaults", defaultLayers...)
	values.defaultEnvs = defaults.EnvVars(profilepkg.EntryKindVariable)
	values.defaultSecrets = defaults.EnvVars(profilepkg.EntryKindSecret)

	var selected *profilepkg.Resolved
	if a.profileName != "" {
		selected, err = resolver.Resolve(ctx, a.profileName)
		if err != nil {
			return values, fmt.Errorf("failed to resolve profile %q: %w", a.profileName, err)
		}
		values.selectedEnvs = selected.EnvVars(profilepkg.EntryKindVariable)
		values.selectedSecrets = selected.EnvVars(profilepkg.EntryKindSecret)
		a.profileName = selected.Name
		logger.Info(ctx, "Resolved runtime profile",
			slog.String("profile", selected.Name),
			tag.Count(len(selected.Entries)),
		)
	}

	if len(defaultLayers) > 0 || selected != nil {
		layers := append([]*profilepkg.Resolved{}, defaultLayers...)
		layers = append(layers, selected)
		effective := profilepkg.MergeResolved("effective", layers...)
		a.profileResolvedAt = contextTimeString(time.Now())
		a.profileEntries = profileEntries(effective)
	}

	return values, nil
}

func (a *Agent) resolveInheritedProfiles(
	ctx context.Context,
	resolver *profilepkg.Resolver,
) ([]*profilepkg.Resolved, error) {
	defaultLayers := make([]*profilepkg.Resolved, 0, 2)
	globalDefaults, err := resolver.ResolveInherited(ctx, profilepkg.GlobalInheritedRef())
	if err != nil && !errors.Is(err, profilepkg.ErrNotFound) {
		return nil, fmt.Errorf("failed to resolve global profile defaults: %w", err)
	}
	if globalDefaults != nil {
		defaultLayers = append(defaultLayers, globalDefaults)
		logger.Info(ctx, "Resolved global runtime profile defaults",
			tag.Count(len(globalDefaults.Entries)),
		)
	}

	workspaceName, ok := workspace.WorkspaceNameFromLabels(a.dag.Labels)
	if !ok {
		return defaultLayers, nil
	}
	workspaceRef, err := profilepkg.WorkspaceInheritedRef(workspaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workspace profile defaults: %w", err)
	}
	workspaceDefaults, err := resolver.ResolveInherited(ctx, workspaceRef)
	if err != nil && !errors.Is(err, profilepkg.ErrNotFound) {
		return nil, fmt.Errorf("failed to resolve workspace profile defaults %q: %w", workspaceName, err)
	}
	if workspaceDefaults != nil {
		defaultLayers = append(defaultLayers, workspaceDefaults)
		logger.Info(ctx, "Resolved workspace runtime profile defaults",
			slog.String("workspace", workspaceName),
			tag.Count(len(workspaceDefaults.Entries)),
		)
	}
	return defaultLayers, nil
}

func profileEntries(resolved *profilepkg.Resolved) []ir.RuntimeProfileEntry {
	if resolved == nil {
		return nil
	}
	entries := make([]ir.RuntimeProfileEntry, 0, len(resolved.Entries))
	for _, entry := range resolved.Entries {
		entries = append(entries, ir.RuntimeProfileEntry{
			Key:  entry.Key,
			Kind: string(entry.Kind),
		})
	}
	return entries
}

// resolveSecrets resolves all secrets defined in the DAG and returns them as
// environment variable strings in "NAME=value" format.
func (a *Agent) resolveSecrets(ctx context.Context) ([]string, error) {
	if len(a.dag.Secrets) == 0 {
		return nil, nil
	}

	logger.Info(ctx, "Resolving secrets", tag.Count(len(a.dag.Secrets)))

	envScope := a.buildEnvScopeForSecrets()
	secretCtx := cmnvalue.WithEnvScope(ctx, envScope)

	baseDirs := a.buildSecretBaseDirs(envScope)
	secretRegistry := providers.NewRegistryWithReferenceResolver(a.secretReferenceResolver, baseDirs...)
	defer func() {
		if err := secretRegistry.Close(); err != nil {
			logger.Warn(ctx, "Failed to close secret providers", tag.Error(err))
		}
	}()

	resolvedSecrets, err := secretRegistry.ResolveAll(secretCtx, a.dag.Secrets)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve secrets: %w", err)
	}

	logger.Debug(ctx, "Secrets resolved successfully", tag.Count(len(resolvedSecrets)))
	return resolvedSecrets, nil
}

// buildEnvScopeForSecrets creates an EnvScope with DAG env vars for secret resolution.
func (a *Agent) buildEnvScopeForSecrets() *cmnvalue.EnvScope {
	envScope := cmnvalue.NewEnvScope(nil, true)
	dagEnvs := make(map[string]string)
	for _, env := range a.dag.Env {
		if key, value, found := strings.Cut(env, "="); found {
			dagEnvs[key] = value
		}
	}
	if len(dagEnvs) > 0 {
		envScope = envScope.WithEntries(dagEnvs, cmnvalue.EnvSourceDAGEnv)
	}
	return envScope
}

// buildSecretBaseDirs returns base directories for file-based secret resolution.
func (a *Agent) buildSecretBaseDirs(envScope *cmnvalue.EnvScope) []string {
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
		evaluated, err := evalHostConfigObject(ctx, *a.dag.SMTP, vars, "smtp")
		if err != nil {
			return fmt.Errorf("failed to evaluate smtp config: %w", err)
		}
		a.evaluatedSMTP = &evaluated
	}

	// Evaluate error mail config if defined
	if a.dag.ErrorMail != nil {
		evaluated, err := evalHostConfigObject(ctx, *a.dag.ErrorMail, vars, "error_mail")
		if err != nil {
			return fmt.Errorf("failed to evaluate error mail config: %w", err)
		}
		a.evaluatedErrorMail = &evaluated
	}

	// Evaluate info mail config if defined
	if a.dag.InfoMail != nil {
		evaluated, err := evalHostConfigObject(ctx, *a.dag.InfoMail, vars, "info_mail")
		if err != nil {
			return fmt.Errorf("failed to evaluate info mail config: %w", err)
		}
		a.evaluatedInfoMail = &evaluated
	}

	// Evaluate wait mail config if defined
	if a.dag.WaitMail != nil {
		evaluated, err := evalHostConfigObject(ctx, *a.dag.WaitMail, vars, "wait_mail")
		if err != nil {
			return fmt.Errorf("failed to evaluate wait mail config: %w", err)
		}
		a.evaluatedWaitMail = &evaluated
	}

	return nil
}

func evalHostConfigObject[T any](ctx context.Context, obj T, vars map[string]string, path string) (T, error) {
	scope := cmnvalue.GetEnvScope(ctx)
	if scope == nil {
		env := runtime.GetEnv(ctx)
		scope = env.Scope
	}
	if len(vars) > 0 {
		if scope == nil {
			scope = cmnvalue.NewEnvScope(nil, false)
		}
		scope = scope.WithEntries(vars, cmnvalue.EnvSourceStepEnv)
	}
	resolver := cmnvalue.NewResolver(cmnvalue.StaticScope{}, cmnvalue.RuntimeScope{Env: scope})
	got, err := resolver.Object(ctx, obj, cmnvalue.HostConfigObjectField(path))
	if err != nil {
		return obj, err
	}
	value, ok := got.(T)
	if !ok {
		return obj, fmt.Errorf("type assertion failed: expected %T, got %T", obj, got)
	}
	return value, nil
}

// evaluateRegistryAuths evaluates registry authentication credentials with
// environment variables and secrets. Results are stored in agent fields to
// avoid mutating the original DAG struct.
func (a *Agent) evaluateRegistryAuths(ctx context.Context) error {
	if len(a.dag.RegistryAuths) == 0 {
		return nil
	}

	vars := runtime.GetEnv(ctx).UserEnvsMap()
	a.evaluatedRegistryAuths = make(map[string]*ir.AuthConfig)

	for registry, auth := range a.dag.RegistryAuths {
		evaluatedAuth, err := evalHostConfigObject(ctx, *auth, vars, "registry_auth."+registry)
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
	evaluated, err := evalHostConfigObject(ctx, *a.dag.S3, vars, "s3")
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

	dagLoader := newDAGLoader(a.dagLoader, a.remoteDAGLoader)
	contextOpts := []runtime.ContextOption{
		runtime.WithDAGLoader(dagLoader),
		runtime.WithRunStateStore(a.runStateStore),
		runtime.WithRootDAGRun(a.rootDAGRun),
		runtime.WithRetryPath(a.retryPath),
		runtime.WithIncludeDownstream(a.includeDownstream),
		runtime.WithAttemptID(a.dagRunAttemptID),
		runtime.WithWorkerID(a.workerID),
		runtime.WithTriggerType(a.triggerType),
		runtime.WithTriggerActor(a.triggerActor),
		runtime.WithRunStartedAt(contextTimeString(a.plan.StartAt())),
		runtime.WithParams(a.dag.Params),
	}
	if scheduleTime := a.contextScheduleTime(); scheduleTime != "" {
		contextOpts = append(contextOpts, runtime.WithScheduleTime(scheduleTime))
	}
	if a.artifactDir != "" {
		contextOpts = append(contextOpts, runtime.WithArtifactDir(a.artifactDir))
	}
	if a.stateStore != nil {
		contextOpts = append(contextOpts, runtime.WithStateStore(a.stateStore))
	}
	if a.materializationStore != nil {
		contextOpts = append(contextOpts, runtime.WithMaterializationStore(a.materializationStore))
	}
	if a.noReuse {
		contextOpts = append(contextOpts, runtime.WithNoReuse(true))
	}
	if a.dagRunLogDir != "" {
		contextOpts = append(contextOpts, runtime.WithDAGRunLogDir(a.dagRunLogDir))
	}
	if a.dagRunArtifactDir != "" {
		contextOpts = append(contextOpts, runtime.WithDAGRunArtifactDir(a.dagRunArtifactDir))
	}
	dagCtx := runtime.NewContext(ctx, a.dag, a.dagRunID, a.logFile, contextOpts...)
	lastErr := a.runner.Run(dagCtx, a.plan, progressCh)
	a.lastErr = lastErr

	logger.Info(ctx, "Dry-run completed",
		slog.Any("params", a.dag.Params),
	)

	return lastErr
}

// stopChildren requests that all running child processes stop.
// allowOverride specifies whether a node can override the stop request with
// its configured signal on platforms that support signal delivery. If processes
// do not terminate after MaxCleanUp time, it requests forceful termination.
func (a *Agent) stopChildren(ctx context.Context, sig os.Signal, allowOverride bool) {
	intent := cmdutil.TerminationFromSignal(sig)
	logger.Info(ctx, "Stopping running child processes",
		slog.String("stop-mode", string(intent.Mode)),
		tag.Signal(intent.SignalName()),
		slog.Bool("allow-override", allowOverride),
		slog.Duration("max-cleanup-time", a.dag.MaxCleanUpTime),
	)

	// Snapshot runner+plan under the read lock: listenSignals can attach
	// before Run() assigns a.runner and a.plan, so an early signal would
	// otherwise nil-deref below.
	a.lock.RLock()
	runner := a.runner
	plan := a.plan
	a.lock.RUnlock()
	if runner == nil || plan == nil {
		logger.Debug(ctx, "Agent not yet initialized; ignoring stop request",
			tag.Signal(intent.SignalName()))
		return
	}

	if !intent.IsTermination() {
		// For non-termination signals, just forward the request once and return.
		runner.Stop(ctx, plan, intent, nil, allowOverride)
		return
	}

	signalCtx, cancel := context.WithTimeout(ctx, a.dag.MaxCleanUpTime)
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		runner.Stop(ctx, plan, intent, done, allowOverride)
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
			forceIntent := cmdutil.ForceTermination()
			logger.Info(ctx, "Max cleanup time reached, forcing child process termination",
				slog.String("stop-mode", string(forceIntent.Mode)),
				tag.Signal(forceIntent.SignalName()),
			)
			runner.Stop(ctx, plan, forceIntent, nil, false)
			return

		case <-resendTicker.C:
			logger.Info(ctx, "Resending stop request to processes that haven't terminated",
				slog.String("stop-mode", string(intent.Mode)),
				tag.Signal(intent.SignalName()),
			)
			runner.Stop(ctx, plan, intent, nil, false)

		case <-probeTicker.C:
			if !plan.HasActiveNodes() {
				logger.Info(ctx, "No running processes detected, termination complete")
				return
			}
		}
	}
}

// setupPlan setups the DAG plan. If is retry execution, it loads nodes
// from the retry node so that it runs the same DAG as the previous run.
func (a *Agent) setupPlan(ctx context.Context) (*runtime.Plan, error) {
	if a.retryTarget != nil {
		return a.setupRetryPlan(ctx)
	}
	return a.setupFreshPlan()
}

// setupRetryPlan sets up the plan for retry.
func (a *Agent) setupRetryPlan(ctx context.Context) (*runtime.Plan, error) {
	nodes, err := a.retryNodes()
	if err != nil {
		return nil, err
	}
	// If the previous run was killed before writing node data to the status
	// (e.g., SIGKILL before the initial 100ms status write), retryTarget.Nodes
	// will be empty. Fall back to a fresh plan from the DAG definition so that
	// the retry actually runs all steps instead of producing a 0-node run.
	if len(nodes) == 0 {
		logger.Warn(ctx, "Retry target has no nodes; falling back to fresh plan from DAG definition")
		if a.stepRetry != "" {
			return nil, fmt.Errorf("cannot retry step %q: previous attempt has no node state", a.stepRetry)
		}
		return a.setupFreshPlan()
	}
	if a.stepRetry != "" {
		return a.setupStepRetryPlan(nodes)
	}
	return a.setupDefaultRetryPlan(ctx, nodes)
}

func (a *Agent) setupFreshPlan() (*runtime.Plan, error) {
	plan, err := runtime.NewPlan(a.dag.Steps...)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func (a *Agent) retryNodes() ([]*runtime.Node, error) {
	steps := make(map[string]ir.Step, len(a.dag.Steps))
	for _, step := range a.dag.Steps {
		steps[step.Name] = step
	}

	nodes := make([]*runtime.Node, 0, len(a.retryTarget.Nodes))
	for _, node := range a.retryTarget.Nodes {
		if node == nil {
			continue
		}
		step, ok := steps[node.Step.Name]
		if !ok {
			return nil, fmt.Errorf("%w: %s", runtime.ErrMissingNode, node.Step.Name)
		}
		nodes = append(nodes, transform.ToNodeWithStep(node, step))
	}
	return nodes, nil
}

// setupStepRetryPlan sets up the plan for retrying a specific step.
func (a *Agent) setupStepRetryPlan(nodes []*runtime.Node) (*runtime.Plan, error) {
	// Nested child retries remapped to a parent container step should not
	// expand that container's siblings. Downstream expansion applies in the
	// DAG that contains the selected step (empty remaining retry hops).
	includeDownstream := a.includeDownstream && len(a.retryPath.Hops) == 0
	plan, err := runtime.CreateStepRetryPlanWithOptions(a.dag, nodes, a.stepRetry, runtime.StepRetryPlanOptions{
		IncludeDownstream: includeDownstream,
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// setupDefaultRetryPlan sets up the plan for the default retry behavior (all failed/canceled nodes and downstreams).
func (a *Agent) setupDefaultRetryPlan(ctx context.Context, nodes []*runtime.Node) (*runtime.Plan, error) {
	plan, err := runtime.CreateRetryPlan(ctx, a.dag, nodes...)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func (a *Agent) setupAttempt(ctx context.Context) (runstate.Attempt, error) {
	req := runstate.BeginAttemptRequest{
		DAG:        a.dag,
		RunID:      a.dagRunID,
		AttemptID:  a.attemptID,
		Retry:      a.retryTarget != nil || a.queuedRun,
		RootDAGRun: a.rootDAGRun,
	}
	if a.runStateStore == nil {
		return runstate.NewNoopAttempt(req), nil
	}
	return a.runStateStore.BeginAttempt(ctx, req)
}

// setupSocketServer creates a socket server instance.
func (a *Agent) setupSocketServer(ctx context.Context) error {
	socketServer, err := a.socketServerFactory(a.socketAddr(), a.HandleHTTP(ctx))
	if err != nil {
		return err
	}
	a.socketServer = socketServer
	return nil
}

func (a *Agent) socketAddr() string {
	return proc.DAGSocketAddr(ir.NewDAGRunRef(a.dag.Name, a.dagRunID))
}

// checkIsAlreadyRunning returns error if the DAG is already running.
func (a *Agent) checkIsAlreadyRunning(ctx context.Context) error {
	if a.isSubDAGRun.Load() {
		return nil
	}
	if !a.dagRunMgr.IsRunning(ctx, a.dag, a.dagRunID) {
		return nil
	}
	return fmt.Errorf("already running. dag-run ID=%s, socket=%s", a.dagRunID, a.socketAddr())
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
