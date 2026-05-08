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
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

// Context contains the execution metadata for a dag-run.
type Context struct {
	DAGRunID           string
	RootDAGRun         DAGRunRef
	DAG                *core.DAG
	DB                 Database
	BaseEnv            *config.BaseEnv
	EnvScope           *eval.EnvScope // Unified environment scope - THE single source for all env vars
	CoordinatorCli     Dispatcher
	Shell              string               // Default shell for this DAG (from DAG.Shell)
	LogEncodingCharset string               // Character encoding for log files (e.g., "utf-8", "shift_jis", "euc-jp")
	LogWriterFactory   LogWriterFactory     // For remote log streaming (nil = use local files)
	DefaultExecMode    config.ExecutionMode // Server-level default execution mode (local or distributed)
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
		Status             string             `json:"status"`
		PendingStepRetries []PendingStepRetry `json:"pendingStepRetries,omitempty"`
	}{
		Name:               r.Name,
		DAGRunID:           r.DAGRunID,
		Params:             r.Params,
		Outputs:            r.Outputs,
		Status:             r.Status.String(),
		PendingStepRetries: r.PendingStepRetries,
	}, "", "  ")
}

// Dispatcher defines the interface for coordinator operations
type Dispatcher interface {
	// Dispatch sends a task to the coordinator
	Dispatch(ctx context.Context, task *coordinatorv1.Task) error

	// Cleanup cleans up any resources used by the coordinator client
	Cleanup(ctx context.Context) error

	// GetDAGRunStatus retrieves the status of a DAG run from the coordinator.
	// Used by parent DAGs to poll status of remote sub-DAGs.
	// For sub-DAG queries, provide rootRef to look up the status under the root DAG run.
	// Returns (nil, nil) if the DAG run is not found.
	GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *DAGRunRef) (*coordinatorv1.GetDAGRunStatusResponse, error)

	// RequestCancel requests cancellation of a DAG run through the coordinator.
	// Used in shared-nothing mode for sub-DAG cancellation where the parent
	// worker cannot directly access the sub-DAG's attempt.
	RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *DAGRunRef) error
}

// contextOptions holds optional configuration for NewContext.
type contextOptions struct {
	db                 Database
	rootDAGRun         DAGRunRef
	params             []string
	envs               []string
	coordinator        Dispatcher
	secretEnvs         []string
	logEncodingCharset string
	logWriterFactory   LogWriterFactory
	defaultExecMode    config.ExecutionMode
	workDir            string
	artifactDir        string
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

// WithParams sets runtime parameters.
func WithParams(params []string) ContextOption {
	return func(o *contextOptions) {
		o.params = params
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

	envs := make(map[string]string)
	maps.Copy(envs, stringutil.KeyValuesToMap(options.params))

	managedEnvs := buildManagedDAGRunEnvs(ctx, dag, dagRunID, logFile, options)
	maps.Copy(envs, managedEnvs)

	maps.Copy(envs, evaluateDAGEnvRuntime(ctx, dag.Env, envs, managedEnvs))
	maps.Copy(envs, stringutil.KeyValuesToMap(options.envs))

	// Managed DAG-run envs are generated by Dagu and must remain stable even
	// when params, DAG env, or execution-scoped env vars reuse those names.
	maps.Copy(envs, managedEnvs)

	secretEnvs := stringutil.KeyValuesToMap(options.secretEnvs)

	// Build EnvScope with proper source tracking and layering.
	// Seed the lowest-precedence layer from filtered BaseEnv so workflow step
	// subprocesses stay isolated from arbitrary host env inherited by parent-
	// spawned dagu start/retry/restart commands.
	// Precedence (highest to lowest): Secrets > DAG Env > Params > BaseEnv
	scope := eval.NewEnvScope(nil, false)
	if baseEnv := config.GetBaseEnv(ctx); baseEnv != nil {
		scope = scope.WithEntries(stringutil.KeyValuesToMap(baseEnv.AsSlice()), eval.EnvSourceOS)
	}
	scope = scope.WithEntries(envs, eval.EnvSourceDAGEnv)
	if len(secretEnvs) > 0 {
		scope = scope.WithEntries(secretEnvs, eval.EnvSourceSecret)
	}

	return context.WithValue(ctx, dagCtxKey{}, Context{
		RootDAGRun:         options.rootDAGRun,
		DAG:                dag,
		DB:                 options.db,
		EnvScope:           scope,
		DAGRunID:           dagRunID,
		BaseEnv:            config.GetBaseEnv(ctx),
		CoordinatorCli:     options.coordinator,
		Shell:              dag.Shell,
		LogEncodingCharset: options.logEncodingCharset,
		LogWriterFactory:   options.logWriterFactory,
		DefaultExecMode:    options.defaultExecMode,
	})
}

func evaluateDAGEnvRuntime(ctx context.Context, envList []string, base map[string]string, protected map[string]string) map[string]string {
	if len(envList) == 0 {
		return nil
	}

	// DAG env is primarily evaluated during DAG loading. This runtime pass only
	// resolves values that depend on run-scoped variables unavailable at load time.
	result := make(map[string]string, len(envList))
	scope := eval.NewEnvScope(nil, false)
	if baseEnv := config.GetBaseEnv(ctx); baseEnv != nil {
		scope = scope.WithEntries(stringutil.KeyValuesToMap(baseEnv.AsSlice()), eval.EnvSourceOS)
	}
	scope = scope.WithEntries(base, eval.EnvSourceDAGEnv)

	for _, entry := range envList {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, ok := protected[key]; ok {
			continue
		}

		evalCtx := eval.WithEnvScope(ctx, scope)
		evaluated, err := eval.String(evalCtx, value, eval.WithVariables(result), eval.WithoutSubstitute())
		if err != nil {
			evaluated = value
		}
		result[key] = evaluated
		scope = scope.WithEntry(key, evaluated, eval.EnvSourceDAGEnv)
	}

	return result
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

type dagCtxKey struct{}
