// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runctx

import (
	"context"
	"io"
	"maps"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
)

const runtimeProfileOrigin = "runtime_profile"

// Context contains the execution metadata for a dag-run.
type Context struct {
	DAGRunID             string
	RootDAGRun           ir.DAGRunRef
	RetryPath            dagrun.RetryPath
	IncludeDownstream    bool
	AttemptID            string
	WorkerID             string
	TriggerType          ir.TriggerType
	TriggerActor         string
	RunStartedAt         string
	ScheduleTime         string
	DAG                  *ir.DAG
	DAGLoader            DAGLoader
	BaseEnv              *config.BaseEnv
	EnvScope             *cmnvalue.EnvScope // Unified environment scope for runtime variables
	CoordinatorCli       dispatch.Dispatcher
	RunStateStore        runstate.Store
	StateStore           dagrun.StateStore
	MaterializationStore build.MaterializationStore
	DAGRunLogDir         string
	DAGRunArtifactDir    string
	ProfileName          string
	ProfileResolvedAt    string
	ProfileEntries       []ir.RuntimeProfileEntry
	Shell                string               // Default shell for this DAG (from DAG.Shell)
	LogEncodingCharset   string               // Character encoding for log files (e.g., "utf-8", "shift_jis", "euc-jp")
	LogWriterFactory     LogWriterFactory     // For remote log streaming (nil = use local files)
	DefaultExecMode      config.ExecutionMode // Server-level default execution mode (local or distributed)
	NoReuse              bool
}

// LogWriterFactory creates log writers for step stdout/stderr.
// It abstracts where logs are written, allowing for:
// - Local file-based storage (default)
// - Remote streaming to coordinator
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

// DAGRunRef returns the DAG-run reference for the current DAG context.
func (e Context) DAGRunRef() ir.DAGRunRef {
	return ir.NewDAGRunRef(e.DAG.Name, e.DAGRunID)
}

// AllEnvs returns every environment variable as "key=value" strings.
// Uses EnvScope as the single source of truth for all env vars.
func (e Context) AllEnvs() []string {
	if e.EnvScope == nil {
		return nil
	}
	return e.EnvScope.ToSlice()
}

// InheritedEnvs returns values passed to a child before its profile is resolved.
func (e Context) InheritedEnvs() []string {
	if e.EnvScope == nil {
		return nil
	}
	return e.EnvScope.ToSliceWithoutOrigin(runtimeProfileOrigin)
}

// DAGLoader loads DAG definitions needed during execution.
type DAGLoader interface {
	GetDAG(ctx context.Context, name string) (*ir.DAG, error)
}

// contextOptions holds optional configuration for NewContext.
//
// The embedded Context carries every option that reaches the final dag-run
// context unchanged. The remaining fields are construction inputs: environment
// layers that NewContext folds into EnvScope, and per-run paths that reach the
// context as managed environment variables.
type contextOptions struct {
	Context

	params             []string
	defaultEnvs        []string
	envs               []string
	defaultSecretEnvs  []string
	secretEnvs         []string
	profileDefaults    []string
	profileDefaultSec  []string
	profileSelected    []string
	profileSelectedSec []string
	workDir            string
	artifactDir        string
}

// ContextOption configures optional parameters for NewContext.
type ContextOption func(*contextOptions)

// WithDAGLoader sets the DAG loader.
func WithDAGLoader(loader DAGLoader) ContextOption {
	return func(o *contextOptions) {
		o.DAGLoader = loader
	}
}

// WithRootDAGRun sets the root DAG run reference for sub-DAG execution.
func WithRootDAGRun(ref ir.DAGRunRef) ContextOption {
	return func(o *contextOptions) {
		o.RootDAGRun = ref
	}
}

// WithRetryPath sets the persisted child DAG path for a targeted retry.
func WithRetryPath(path dagrun.RetryPath) ContextOption {
	return func(o *contextOptions) {
		o.RetryPath = path
	}
}

// WithIncludeDownstream records that a targeted step retry should also reset
// reachable descendants in the DAG that contains the selected step.
func WithIncludeDownstream(enabled bool) ContextOption {
	return func(o *contextOptions) {
		o.IncludeDownstream = enabled
	}
}

// WithAttemptID sets the DAG-run attempt identifier for value resolution.
func WithAttemptID(attemptID string) ContextOption {
	return func(o *contextOptions) {
		o.AttemptID = attemptID
	}
}

// WithWorkerID records the execution host that owns the run.
func WithWorkerID(workerID string) ContextOption {
	return func(o *contextOptions) {
		o.WorkerID = workerID
	}
}

// WithTriggerType sets the DAG-run trigger type for value resolution.
func WithTriggerType(triggerType ir.TriggerType) ContextOption {
	return func(o *contextOptions) {
		o.TriggerType = triggerType
	}
}

// WithTriggerActor sets the attributable trigger actor for value resolution.
func WithTriggerActor(actor string) ContextOption {
	return func(o *contextOptions) {
		o.TriggerActor = actor
	}
}

// WithRunStartedAt sets the recorded DAG-run start timestamp for value resolution.
func WithRunStartedAt(startedAt string) ContextOption {
	return func(o *contextOptions) {
		o.RunStartedAt = startedAt
	}
}

// WithScheduleTime sets the logical schedule time for value resolution.
func WithScheduleTime(scheduleTime string) ContextOption {
	return func(o *contextOptions) {
		o.ScheduleTime = scheduleTime
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
func WithCoordinator(cli dispatch.Dispatcher) ContextOption {
	return func(o *contextOptions) {
		o.CoordinatorCli = cli
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

// WithRuntimeProfileValues sets profile layers independently from inherited env.
func WithRuntimeProfileValues(defaultEnvs, defaultSecrets, selectedEnvs, selectedSecrets []string) ContextOption {
	return func(o *contextOptions) {
		o.profileDefaults = append([]string(nil), defaultEnvs...)
		o.profileDefaultSec = append([]string(nil), defaultSecrets...)
		o.profileSelected = append([]string(nil), selectedEnvs...)
		o.profileSelectedSec = append([]string(nil), selectedSecrets...)
	}
}

// WithLogEncoding sets the log file character encoding.
func WithLogEncoding(charset string) ContextOption {
	return func(o *contextOptions) {
		o.LogEncodingCharset = charset
	}
}

// WithLogWriterFactory sets the log writer factory for remote log streaming.
// When set, logs are streamed to the coordinator instead of written to local files.
func WithLogWriterFactory(factory LogWriterFactory) ContextOption {
	return func(o *contextOptions) {
		o.LogWriterFactory = factory
	}
}

// WithDefaultExecMode sets the server-level default execution mode.
func WithDefaultExecMode(mode config.ExecutionMode) ContextOption {
	return func(o *contextOptions) {
		o.DefaultExecMode = mode
	}
}

// WithRunStateStore sets the execution-state store.
func WithRunStateStore(store runstate.Store) ContextOption {
	return func(o *contextOptions) {
		o.RunStateStore = store
	}
}

// WithStateStore sets the persistent DAG state store for state actions.
func WithStateStore(store dagrun.StateStore) ContextOption {
	return func(o *contextOptions) {
		o.StateStore = store
	}
}

// WithMaterializationStore sets the build materialization store.
func WithMaterializationStore(store build.MaterializationStore) ContextOption {
	return func(o *contextOptions) {
		o.MaterializationStore = store
	}
}

// WithNoReuse records that manifest hits are disabled for the run.
func WithNoReuse(disabled bool) ContextOption {
	return func(o *contextOptions) {
		o.NoReuse = disabled
	}
}

// WithDAGRunLogDir sets the base log directory for newly persisted DAG runs.
func WithDAGRunLogDir(dir string) ContextOption {
	return func(o *contextOptions) {
		o.DAGRunLogDir = dir
	}
}

// WithDAGRunArtifactDir sets the base artifact directory for newly persisted DAG runs.
func WithDAGRunArtifactDir(dir string) ContextOption {
	return func(o *contextOptions) {
		o.DAGRunArtifactDir = dir
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
func WithRuntimeProfile(name, resolvedAt string, entries []ir.RuntimeProfileEntry) ContextOption {
	return func(o *contextOptions) {
		o.ProfileName = name
		o.ProfileResolvedAt = resolvedAt
		o.ProfileEntries = append([]ir.RuntimeProfileEntry(nil), entries...)
	}
}

// NewContext creates a new context with DAG execution metadata.
// Required: ctx, dag, dagRunID, logFile
// Optional: use ContextOption functions (WithDAGLoader, WithParams, etc.)
func NewContext(
	ctx context.Context,
	dag *ir.DAG,
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
	profileDefaults := stringutil.KeyValuesToMap(options.profileDefaults)
	profileDefaultSecrets := stringutil.KeyValuesToMap(options.profileDefaultSec)
	profileSelected := stringutil.KeyValuesToMap(options.profileSelected)
	profileSelectedSecrets := stringutil.KeyValuesToMap(options.profileSelectedSec)

	baseForDAGEnv := make(map[string]string)
	maps.Copy(baseForDAGEnv, profileDefaults)
	maps.Copy(baseForDAGEnv, profileDefaultSecrets)
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
	scope = scope.WithEntriesOrigin(profileDefaults, cmnvalue.EnvSourceDAGEnv, runtimeProfileOrigin)
	scope = scope.WithEntriesOrigin(profileDefaultSecrets, cmnvalue.EnvSourceSecret, runtimeProfileOrigin)
	scope = scope.WithEntries(defaultEnvs, cmnvalue.EnvSourceDAGEnv)
	scope = scope.WithEntries(defaultSecretEnvs, cmnvalue.EnvSourceSecret)
	scope = scope.WithEntries(params, cmnvalue.EnvSourceParam)
	scope = scope.WithEntries(managedEnvs, cmnvalue.EnvSourceDAGEnv)
	scope = scope.WithEntries(evaluatedDAGEnvs, cmnvalue.EnvSourceDAGEnv)
	scope = scope.WithEntriesOrigin(profileSelected, cmnvalue.EnvSourceDAGEnv, runtimeProfileOrigin)
	scope = scope.WithEntries(selectedEnvs, cmnvalue.EnvSourceDAGEnv)
	// Managed DAG-run envs are generated by Dagu and must remain stable even
	// when params, DAG env, or execution-scoped env vars reuse those names.
	scope = scope.WithEntries(managedEnvs, cmnvalue.EnvSourceDAGEnv)
	scope = scope.WithEntriesOrigin(profileSelectedSecrets, cmnvalue.EnvSourceSecret, runtimeProfileOrigin)
	if len(secretEnvs) > 0 {
		scope = scope.WithEntries(secretEnvs, cmnvalue.EnvSourceSecret)
	}

	// Fields the caller cannot set through an option, because they are derived
	// from the required arguments or from the environment layering above.
	options.DAG = dag
	options.DAGRunID = dagRunID
	options.Shell = dag.Shell
	options.BaseEnv = config.GetBaseEnv(ctx)
	options.EnvScope = scope

	return context.WithValue(ctx, dagCtxKey{}, options.Context)
}

func evaluateDAGEnvRuntime(
	ctx context.Context,
	dag *ir.DAG,
	runtimeParams map[string]string,
	base map[string]string,
	protected map[string]string,
	runBuiltinContext cmnvalue.BuiltinContext,
) map[string]string {
	var envList []string
	var params cmnvalue.Values
	var paramsJSON string
	var paramDeclarations cmnvalue.Values
	if dag != nil {
		envList = dag.Env
		params = dag.ParamValues()
		paramsJSON = dag.ParamsJSON
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
			cmnvalue.RuntimeScope{Params: params, ParamsJSON: paramsJSON, Env: scope, BuiltinContext: runBuiltinContext},
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
	dag *ir.DAG,
	dagRunID string,
	managedEnvs map[string]string,
	options *contextOptions,
) cmnvalue.BuiltinContext {
	values := make(map[string]string)
	if dag != nil && dag.Name != "" {
		values["context.dag.name"] = dag.Name
	}
	addDAGRunBuiltinValue(values, "context.run.id", dagRunID)
	addDAGRunBuiltinValue(values, "context.attempt.started_at", options.RunStartedAt)
	addDAGRunBuiltinValue(values, "context.run.scheduled_at", options.ScheduleTime)
	if rootDAGRunContextAvailable(options.RootDAGRun, dag, dagRunID) {
		addDAGRunBuiltinValue(values, "context.run.root_name", options.RootDAGRun.Name)
		addDAGRunBuiltinValue(values, "context.run.root_id", options.RootDAGRun.ID)
	}
	addDAGRunBuiltinValue(values, "context.attempt.id", options.AttemptID)
	addDAGRunBuiltinValue(values, "context.trigger.type", options.TriggerType.String())
	addDAGRunBuiltinValue(values, "context.trigger.actor", options.TriggerActor)
	addDAGRunBuiltinValue(values, "context.paths.log_file", managedEnvs[runenv.EnvKeyDAGRunLogFile])
	addDAGRunBuiltinValue(values, "context.paths.work_dir", managedEnvs[runenv.EnvKeyDAGRunWorkDir])
	addDAGRunBuiltinValue(values, "context.paths.artifacts_dir", managedEnvs[runenv.EnvKeyDAGRunArtifactsDir])
	addDAGRunBuiltinValue(values, "context.paths.wiki_dir", managedEnvs[runenv.EnvKeyDAGWikiDir])
	addDAGRunBuiltinValue(values, "context.paths.docs_dir", managedEnvs[runenv.EnvKeyDAGDocsDir])
	addDAGRunBuiltinValue(values, "context.profile.name", options.ProfileName)
	addDAGRunBuiltinValue(values, "context.profile.resolved_at", options.ProfileResolvedAt)
	return cmnvalue.NewBuiltinContext(values)
}

func rootDAGRunContextAvailable(root ir.DAGRunRef, dag *ir.DAG, dagRunID string) bool {
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

// MustContext returns the DAGContext from the context and panics when one is
// not present. Use it where a fully initialized dag-run context is a caller
// invariant; use LookupContext where absence is valid.
func MustContext(ctx context.Context) Context {
	rCtx, ok := LookupContext(ctx)
	if !ok {
		panic("exec: no dag-run Context is installed in the context")
	}
	return rCtx
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
