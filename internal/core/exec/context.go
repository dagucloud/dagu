// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/dagstate"
)

// Context contains the execution metadata for a dag-run.
type Context struct {
	DAGRunID           string
	RootDAGRun         DAGRunRef
	AttemptID          string
	TriggerType        core.TriggerType
	TriggerActor       string
	RunStartedAt       string
	ScheduleTime       string
	DAG                *core.DAG
	DB                 Database
	BaseEnv            *config.BaseEnv
	EnvScope           *cmnvalue.EnvScope // Unified environment scope for runtime variables
	CoordinatorCli     Dispatcher
	DAGRunStore        DAGRunStore
	QueueStore         QueueStore
	StateStore         dagstate.Store
	DAGRunLogDir       string
	DAGRunArtifactDir  string
	ProfileName        string
	ProfileResolvedAt  string
	ProfileEntries     []RuntimeProfileEntry
	Shell              string               // Default shell for this DAG (from DAG.Shell)
	LogEncodingCharset string               // Character encoding for log files (e.g., "utf-8", "shift_jis", "euc-jp")
	LogWriterFactory   LogWriterFactory     // For remote log streaming (nil = use local files)
	DefaultExecMode    config.ExecutionMode // Server-level default execution mode (local or distributed)
}

// RuntimeProfileEntry is non-secret metadata about a profile key injected into a run.
type RuntimeProfileEntry struct {
	// Key is the injected environment variable name.
	Key string `json:"key"`
	// Kind is the profile entry type, such as variable or secret.
	Kind string `json:"kind"`
}

// LogWriterFactory creates log writers for step stdout/stderr.
// It abstracts where logs are written, allowing for:
// - Local file-based storage (default)
// - Remote streaming to coordinator (shared-nothing mode)
type LogWriterFactory interface {
	// NewStepWriter creates a writer for a step's log output.
	// stepName identifies the step, streamType should be StreamTypeStdout or StreamTypeStderr.
	NewStepWriter(ctx context.Context, stepName string, streamType int) io.WriteCloser
}

// Stream type constants for LogWriterFactory.NewStepWriter
const (
	// StreamTypeStdout indicates stdout stream
	StreamTypeStdout = 1
	// StreamTypeStderr indicates stderr stream
	StreamTypeStderr = 2
)

// UserEnvsMap returns only user-defined environment variables as a map,
// excluding OS environment (BaseEnv). Use this for isolated execution environments.
func (e Context) UserEnvsMap() map[string]string {
	if e.EnvScope == nil {
		return make(map[string]string)
	}
	return e.EnvScope.AllUserEnvs()
}

// DAGRunRef returns the DAGRunRef for the current DAG context.
func (e Context) DAGRunRef() DAGRunRef {
	return NewDAGRunRef(e.DAG.Name, e.DAGRunID)
}

// AllEnvs returns every environment variable as "key=value" strings.
// Uses EnvScope as the single source of truth for all env vars.
func (e Context) AllEnvs() []string {
	if e.EnvScope == nil {
		return nil
	}
	return e.EnvScope.ToSlice()
}

// Database is the interface for accessing the database to retrieve DAGs and dag-run statuses.
// This interface abstracts the underlying storage mechanism, allowing for different implementations (e.g., SQL, NoSQL, in-memory).
type Database interface {
	// GetDAG retrieves a DAG by its name.
	GetDAG(ctx context.Context, name string) (*core.DAG, error)
	// GetSubDAGRunStatus retrieves the status of a sub dag-run by its ID and the root dag-run reference.
	GetSubDAGRunStatus(ctx context.Context, dagRunID string, rootDAGRun DAGRunRef) (*RunStatus, error)
	// IsSubDAGRunCompleted checks if a sub dag-run has completed.
	IsSubDAGRunCompleted(ctx context.Context, dagRunID string, rootDAGRun DAGRunRef) (bool, error)
	// RequestChildCancel requests cancellation of a sub dag-run.
	RequestChildCancel(ctx context.Context, dagRunID string, rootDAGRun DAGRunRef) error
}

// SubDAGRunStatus is an interface that represents the status of a sub dag-run.
type PendingStepRetry struct {
	StepName string        `json:"stepName"`
	Interval time.Duration `json:"interval"`
}

// MarshalJSON emits Interval as a Go duration string while keeping the
// surrounding shape stable for callers.
func (p PendingStepRetry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		StepName string `json:"stepName"`
		Interval string `json:"interval"`
	}{
		StepName: p.StepName,
		Interval: p.Interval.String(),
	})
}

// UnmarshalJSON accepts both the current string encoding and the legacy
// numeric nanosecond encoding for backward compatibility with persisted data.
func (p *PendingStepRetry) UnmarshalJSON(data []byte) error {
	var current struct {
		StepName string `json:"stepName"`
		Interval string `json:"interval"`
	}
	if err := json.Unmarshal(data, &current); err == nil && current.Interval != "" {
		interval, parseErr := time.ParseDuration(current.Interval)
		if parseErr != nil {
			return fmt.Errorf("parse pending step retry interval: %w", parseErr)
		}
		p.StepName = current.StepName
		p.Interval = interval
		return nil
	}

	var legacy struct {
		StepName string        `json:"stepName"`
		Interval time.Duration `json:"interval"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	p.StepName = legacy.StepName
	p.Interval = legacy.Interval
	return nil
}

type RunStatus struct {
	// Name represents the name of the executed DAG.
	Name string
	// DAGRunID is the ID of the dag-run.
	DAGRunID string
	// Params is the parameters of the DAG.
	Params string
	// Outputs is the outputs of the dag-run.
	Outputs map[string]string
	// OutputValues contains typed outputs published through stdout.outputs or outputs.write.
	OutputValues map[string]any
	// Status is the execution status of the dag-run.
	Status core.Status
	// PendingStepRetries contains any step retries that are waiting to be scheduled
	// by the parent executor.
	PendingStepRetries []PendingStepRetry
}

// MarshalJSON implements the json.Marshaler interface for RunStatus.
func (r *RunStatus) MarshalJSON() ([]byte, error) {
	return json.MarshalIndent(struct {
		Name               string             `json:"name,omitempty"`
		DAGRunID           string             `json:"dagRunId,omitempty"`
		Params             string             `json:"params,omitempty"`
		Outputs            map[string]string  `json:"outputs,omitzero"`
		OutputValues       map[string]any     `json:"outputValues,omitzero"`
		Status             string             `json:"status"`
		PendingStepRetries []PendingStepRetry `json:"pendingStepRetries,omitempty"`
	}{
		Name:               r.Name,
		DAGRunID:           r.DAGRunID,
		Params:             r.Params,
		Outputs:            r.Outputs,
		OutputValues:       r.OutputValues,
		Status:             r.Status.String(),
		PendingStepRetries: r.PendingStepRetries,
	}, "", "  ")
}

// contextOptions holds optional configuration for NewContext.
type contextOptions struct {
	db                 Database
	rootDAGRun         DAGRunRef
	params             []string
	defaultEnvs        []string
	envs               []string
	coordinator        Dispatcher
	defaultSecretEnvs  []string
	secretEnvs         []string
	logEncodingCharset string
	logWriterFactory   LogWriterFactory
	defaultExecMode    config.ExecutionMode
	dagRunStore        DAGRunStore
	queueStore         QueueStore
	stateStore         dagstate.Store
	dagRunLogDir       string
	dagRunArtifactDir  string
	profileName        string
	profileResolvedAt  string
	profileEntries     []RuntimeProfileEntry
	workDir            string
	artifactDir        string
	attemptID          string
	triggerType        core.TriggerType
	triggerActor       string
	runStartedAt       string
	scheduleTime       string
}

// ContextOption configures optional parameters for NewContext.
type ContextOption func(*contextOptions)

// WithDatabase sets the database interface.
func WithDatabase(db Database) ContextOption {
	return func(o *contextOptions) {
		o.db = db
	}
}

// WithRootDAGRun sets the root DAG run reference for sub-DAG execution.
func WithRootDAGRun(ref DAGRunRef) ContextOption {
	return func(o *contextOptions) {
		o.rootDAGRun = ref
	}
}

// WithAttemptID sets the DAG-run attempt identifier for value resolution.
func WithAttemptID(attemptID string) ContextOption {
	return func(o *contextOptions) {
		o.attemptID = attemptID
	}
}

// WithTriggerType sets the DAG-run trigger type for value resolution.
func WithTriggerType(triggerType core.TriggerType) ContextOption {
	return func(o *contextOptions) {
		o.triggerType = triggerType
	}
}

// WithTriggerActor sets the attributable trigger actor for value resolution.
func WithTriggerActor(actor string) ContextOption {
	return func(o *contextOptions) {
		o.triggerActor = actor
	}
}

// WithRunStartedAt sets the recorded DAG-run start timestamp for value resolution.
func WithRunStartedAt(startedAt string) ContextOption {
	return func(o *contextOptions) {
		o.runStartedAt = startedAt
	}
}

// WithScheduleTime sets the logical schedule time for value resolution.
func WithScheduleTime(scheduleTime string) ContextOption {
	return func(o *contextOptions) {
		o.scheduleTime = scheduleTime
	}
}

// WithParams sets runtime parameters.
func WithParams(params []string) ContextOption {
	return func(o *contextOptions) {
		o.params = params
	}
}

// WithDefaultEnvVars sets low-precedence inherited environment variables.
func WithDefaultEnvVars(envs ...string) ContextOption {
	return func(o *contextOptions) {
		o.defaultEnvs = append(o.defaultEnvs, envs...)
	}
}

// WithEnvVars sets additional execution-scoped environment variables.
func WithEnvVars(envs ...string) ContextOption {
	return func(o *contextOptions) {
		o.envs = append(o.envs, envs...)
	}
}

// WithCoordinator sets the coordinator dispatcher for distributed execution.
func WithCoordinator(cli Dispatcher) ContextOption {
	return func(o *contextOptions) {
		o.coordinator = cli
	}
}

// WithDefaultSecrets sets low-precedence inherited secret environment variables.
func WithDefaultSecrets(secrets []string) ContextOption {
	return func(o *contextOptions) {
		o.defaultSecretEnvs = append([]string(nil), secrets...)
	}
}

// WithSecrets sets secret environment variables.
func WithSecrets(secrets []string) ContextOption {
	return func(o *contextOptions) {
		o.secretEnvs = secrets
	}
}

// WithLogEncoding sets the log file character encoding.
func WithLogEncoding(charset string) ContextOption {
	return func(o *contextOptions) {
		o.logEncodingCharset = charset
	}
}

// WithLogWriterFactory sets the log writer factory for remote log streaming.
// When set, logs are streamed to the coordinator instead of written to local files.
func WithLogWriterFactory(factory LogWriterFactory) ContextOption {
	return func(o *contextOptions) {
		o.logWriterFactory = factory
	}
}

// WithDefaultExecMode sets the server-level default execution mode.
func WithDefaultExecMode(mode config.ExecutionMode) ContextOption {
	return func(o *contextOptions) {
		o.defaultExecMode = mode
	}
}

// WithDAGRunStore sets the dag-run store for executors that persist DAG runs.
func WithDAGRunStore(store DAGRunStore) ContextOption {
	return func(o *contextOptions) {
		o.dagRunStore = store
	}
}

// WithQueueStore sets the queue store for executors that enqueue DAG runs.
func WithQueueStore(store QueueStore) ContextOption {
	return func(o *contextOptions) {
		o.queueStore = store
	}
}

// WithStateStore sets the persistent DAG state store for state actions.
func WithStateStore(store dagstate.Store) ContextOption {
	return func(o *contextOptions) {
		o.stateStore = store
	}
}

// WithDAGRunLogDir sets the base log directory for newly persisted DAG runs.
func WithDAGRunLogDir(dir string) ContextOption {
	return func(o *contextOptions) {
		o.dagRunLogDir = dir
	}
}

// WithDAGRunArtifactDir sets the base artifact directory for newly persisted DAG runs.
func WithDAGRunArtifactDir(dir string) ContextOption {
	return func(o *contextOptions) {
		o.dagRunArtifactDir = dir
	}
}

// WithWorkDir sets the per-DAG-run working directory path.
func WithWorkDir(dir string) ContextOption {
	return func(o *contextOptions) {
		o.workDir = dir
	}
}

// WithArtifactDir sets the per-DAG-run artifacts directory path.
func WithArtifactDir(dir string) ContextOption {
	return func(o *contextOptions) {
		o.artifactDir = dir
	}
}

// WithRuntimeProfile sets the selected profile metadata for this run context.
func WithRuntimeProfile(name, resolvedAt string, entries []RuntimeProfileEntry) ContextOption {
	return func(o *contextOptions) {
		o.profileName = name
		o.profileResolvedAt = resolvedAt
		o.profileEntries = append([]RuntimeProfileEntry(nil), entries...)
	}
}

// NewContext creates a new context with DAG execution metadata.
// Required: ctx, dag, dagRunID, logFile
// Optional: use ContextOption functions (WithDatabase, WithParams, etc.)
func NewContext(
	ctx context.Context,
	dag *core.DAG,
	dagRunID string,
	logFile string,
	opts ...ContextOption,
) context.Context {
	// Apply options
	options := &contextOptions{}
	for _, opt := range opts {
		opt(options)
	}

	defaultEnvs := stringutil.KeyValuesToMap(options.defaultEnvs)
	defaultSecretEnvs := stringutil.KeyValuesToMap(options.defaultSecretEnvs)
	params := stringutil.KeyValuesToMap(options.params)
	managedEnvs := buildManagedDAGRunEnvs(ctx, dag, dagRunID, logFile, options)
	selectedEnvs := stringutil.KeyValuesToMap(options.envs)

	baseForDAGEnv := make(map[string]string)
	maps.Copy(baseForDAGEnv, defaultEnvs)
	maps.Copy(baseForDAGEnv, defaultSecretEnvs)
	maps.Copy(baseForDAGEnv, params)
	maps.Copy(baseForDAGEnv, managedEnvs)

	runBuiltinContext := buildDAGRunBuiltinContext(dag, dagRunID, managedEnvs, options)
	evaluatedDAGEnvs := evaluateDAGEnvRuntime(ctx, dag, params, baseForDAGEnv, managedEnvs, runBuiltinContext)

	secretEnvs := stringutil.KeyValuesToMap(options.secretEnvs)

	// Build EnvScope with proper source tracking and layering.
	// Seed the lowest-precedence layer from filtered BaseEnv so workflow step
	// subprocesses stay isolated from arbitrary host env inherited by parent-
	// spawned dagu start/retry/restart commands.
	// Precedence (highest to lowest): secrets > managed run env >
	// execution env > DAG env > params > defaults > BaseEnv.
	scope := cmnvalue.NewEnvScope(nil, false)
	if baseEnv := config.GetBaseEnv(ctx); baseEnv != nil {
		scope = scope.WithEntries(stringutil.KeyValuesToMap(baseEnv.AsSlice()), cmnvalue.EnvSourceOS)
	}
	scope = scope.WithEntries(defaultEnvs, cmnvalue.EnvSourceDAGEnv)
	scope = scope.WithEntries(defaultSecretEnvs, cmnvalue.EnvSourceSecret)
	scope = scope.WithEntries(params, cmnvalue.EnvSourceParam)
	scope = scope.WithEntries(managedEnvs, cmnvalue.EnvSourceDAGEnv)
	scope = scope.WithEntries(evaluatedDAGEnvs, cmnvalue.EnvSourceDAGEnv)
	scope = scope.WithEntries(selectedEnvs, cmnvalue.EnvSourceDAGEnv)
	// Managed DAG-run envs are generated by Dagu and must remain stable even
	// when params, DAG env, or execution-scoped env vars reuse those names.
	scope = scope.WithEntries(managedEnvs, cmnvalue.EnvSourceDAGEnv)
	if len(secretEnvs) > 0 {
		scope = scope.WithEntries(secretEnvs, cmnvalue.EnvSourceSecret)
	}

	return context.WithValue(ctx, dagCtxKey{}, Context{
		RootDAGRun:         options.rootDAGRun,
		AttemptID:          options.attemptID,
		TriggerType:        options.triggerType,
		TriggerActor:       options.triggerActor,
		RunStartedAt:       options.runStartedAt,
		ScheduleTime:       options.scheduleTime,
		DAG:                dag,
		DB:                 options.db,
		EnvScope:           scope,
		DAGRunID:           dagRunID,
		BaseEnv:            config.GetBaseEnv(ctx),
		CoordinatorCli:     options.coordinator,
		DAGRunStore:        options.dagRunStore,
		QueueStore:         options.queueStore,
		StateStore:         options.stateStore,
		DAGRunLogDir:       options.dagRunLogDir,
		DAGRunArtifactDir:  options.dagRunArtifactDir,
		ProfileName:        options.profileName,
		ProfileResolvedAt:  options.profileResolvedAt,
		ProfileEntries:     append([]RuntimeProfileEntry(nil), options.profileEntries...),
		Shell:              dag.Shell,
		LogEncodingCharset: options.logEncodingCharset,
		LogWriterFactory:   options.logWriterFactory,
		DefaultExecMode:    options.defaultExecMode,
	})
}

func evaluateDAGEnvRuntime(
	ctx context.Context,
	dag *core.DAG,
	runtimeParams map[string]string,
	base map[string]string,
	protected map[string]string,
	runBuiltinContext cmnvalue.BuiltinContext,
) map[string]string {
	var envList []string
	var params cmnvalue.Values
	var paramDeclarations cmnvalue.Values
	if dag != nil {
		envList = dag.Env
		params = dag.ParamValues()
		paramDeclarations = dag.ParamDeclarations()
	}
	if len(runtimeParams) > 0 {
		params = cmnvalue.Values{}
		for key, value := range runtimeParams {
			params[key] = value
		}
	}
	if len(envList) == 0 {
		return nil
	}

	// DAG env is primarily evaluated during DAG loading. This runtime pass only
	// resolves values that depend on run-scoped variables unavailable at load time.
	result := make(map[string]string, len(envList))
	scope := cmnvalue.NewEnvScope(nil, false)
	if baseEnv := config.GetBaseEnv(ctx); baseEnv != nil {
		scope = scope.WithEntries(stringutil.KeyValuesToMap(baseEnv.AsSlice()), cmnvalue.EnvSourceOS)
	}
	scope = scope.WithEntries(base, cmnvalue.EnvSourceDAGEnv)

	for _, entry := range envList {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, ok := protected[key]; ok {
			continue
		}

		resolver := cmnvalue.NewResolver(
			cmnvalue.StaticScope{Params: paramDeclarations},
			cmnvalue.RuntimeScope{Params: params, Env: scope, BuiltinContext: runBuiltinContext},
		)
		evaluated, err := resolver.String(ctx, value, cmnvalue.RuntimeDAGEnvField("env."+key))
		if err != nil {
			evaluated = value
		}
		result[key] = evaluated
		scope = scope.WithEntry(key, evaluated, cmnvalue.EnvSourceDAGEnv)
	}

	return result
}

func buildDAGRunBuiltinContext(
	dag *core.DAG,
	dagRunID string,
	managedEnvs map[string]string,
	options *contextOptions,
) cmnvalue.BuiltinContext {
	values := make(map[string]string)
	if dag != nil && dag.Name != "" {
		values["dag.name"] = dag.Name
	}
	addDAGRunBuiltinValue(values, "run.id", dagRunID)
	addDAGRunBuiltinValue(values, "run.started_at", options.runStartedAt)
	addDAGRunBuiltinValue(values, "run.scheduled_at", options.scheduleTime)
	if rootDAGRunContextAvailable(options.rootDAGRun, dag, dagRunID) {
		addDAGRunBuiltinValue(values, "run.root_name", options.rootDAGRun.Name)
		addDAGRunBuiltinValue(values, "run.root_id", options.rootDAGRun.ID)
	}
	addDAGRunBuiltinValue(values, "attempt.id", options.attemptID)
	addDAGRunBuiltinValue(values, "trigger.type", options.triggerType.String())
	addDAGRunBuiltinValue(values, "trigger.actor", options.triggerActor)
	addDAGRunBuiltinValue(values, "paths.log_file", managedEnvs[EnvKeyDAGRunLogFile])
	addDAGRunBuiltinValue(values, "paths.work_dir", managedEnvs[EnvKeyDAGRunWorkDir])
	addDAGRunBuiltinValue(values, "paths.artifacts_dir", managedEnvs[EnvKeyDAGRunArtifactsDir])
	addDAGRunBuiltinValue(values, "paths.docs_dir", managedEnvs[EnvKeyDAGDocsDir])
	addDAGRunBuiltinValue(values, "profile.name", options.profileName)
	addDAGRunBuiltinValue(values, "profile.resolved_at", options.profileResolvedAt)
	return cmnvalue.NewBuiltinContext(values)
}

func rootDAGRunContextAvailable(root DAGRunRef, dag *core.DAG, dagRunID string) bool {
	if root.Zero() {
		return false
	}
	if dag != nil && root.Name == dag.Name && root.ID == dagRunID {
		return false
	}
	return true
}

func addDAGRunBuiltinValue(values map[string]string, path, value string) {
	if value == "" {
		return
	}
	values[path] = value
}

// WithContext returns a new context with the given DAGContext.
// This is useful for tests that need to set up a DAGContext directly.
func WithContext(ctx context.Context, rCtx Context) context.Context {
	return context.WithValue(ctx, dagCtxKey{}, rCtx)
}

// GetContext retrieves the DAGContext from the context.
func GetContext(ctx context.Context) Context {
	value := ctx.Value(dagCtxKey{})
	if value == nil {
		logger.Error(ctx, "DAGContext not found in context")
		return Context{}
	}
	execEnv, ok := value.(Context)
	if !ok {
		logger.Error(ctx, "Invalid DAGContext type in context")
		return Context{}
	}
	return execEnv
}

// LookupContext returns the DAGContext when one is present in ctx.
func LookupContext(ctx context.Context) (Context, bool) {
	value := ctx.Value(dagCtxKey{})
	if value == nil {
		return Context{}, false
	}
	execEnv, ok := value.(Context)
	if !ok {
		return Context{}, false
	}
	return execEnv, true
}

type dagCtxKey struct{}
