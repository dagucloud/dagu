// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"crypto/md5" //nolint:gosec
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
)

// Supported DAG execution types.
const (
	// TypeGraph runs dependency-aware steps in parallel where possible.
	TypeGraph = "graph"
	// TypeChain runs steps strictly in declaration order.
	TypeChain = "chain"
	// TypeAgent is reserved for agent-oriented execution flows.
	TypeAgent = "agent"
)

// LogOutputMode represents the mode for log output handling.
// It determines how stdout and stderr are written to log files.
type LogOutputMode string

const (
	// LogOutputSeparate keeps stdout and stderr in separate files (.out and .err).
	// This is the default behavior for backward compatibility.
	LogOutputSeparate LogOutputMode = "separate"

	// LogOutputMerged combines stdout and stderr into a single log file (.log).
	// Both streams are interleaved in the order they are written.
	LogOutputMerged LogOutputMode = "merged"
)

// EffectiveLogOutput returns the effective log output mode for a step.
// Priority: step-level > DAG-level > default (LogOutputSeparate).
func EffectiveLogOutput(dag *DAG, step *Step) LogOutputMode {
	if step != nil && step.LogOutput != "" {
		return step.LogOutput
	}
	if dag != nil && dag.LogOutput != "" {
		return dag.LogOutput
	}
	return LogOutputSeparate
}

// DAG contains all information about a DAG.
type DAG struct {
	// WorkingDir is the working directory to run the DAG.
	// Default value is the directory of DAG file.
	// Relative paths are resolved at build time; variables are expanded at runtime.
	// Supports environment variable templates (e.g., ${MY_DIR}).
	WorkingDir string `json:"workingDir,omitempty"`
	// WorkingDirExplicit is true when WorkingDir was explicitly set in YAML,
	// base config, or via DefaultWorkingDir option. When false, WorkingDir
	// was auto-defaulted by the loader to the DAG file's parent directory.
	// Not serialized — runtime-only flag.
	WorkingDirExplicit bool `json:"-"`
	// Location is the absolute path to the DAG file.
	// It is used to generate unix socket name and can be blank
	Location string `json:"location,omitempty"`
	// SourceFile is the original DAG file path this run was loaded from.
	// Unlike Location, it is provenance-only and is preserved even when queued
	// execution clears or rewrites runtime locations.
	SourceFile string `json:"sourceFile,omitempty"`
	// Group is the group name of the DAG. This is optional.
	Group string `json:"group,omitempty"`
	// Name is the name of the DAG. The default is the filename without the extension.
	Name string `json:"name,omitempty"`
	// Type is the execution type (graph, chain, or agent). Default is graph.
	Type string `json:"type,omitempty"`
	// Shell is the default shell to use for all steps in this DAG.
	// If not specified, the system default shell is used.
	// Can be overridden at the step level.
	// Supports environment variable templates (e.g., ${MY_SHELL}).
	Shell string `json:"shell,omitempty"`
	// ShellArgs contains additional arguments to pass to the shell.
	// These are populated when Shell is specified as a string with arguments
	// (e.g., "bash -e") or as an array (e.g., ["bash", "-e"]).
	// Supports environment variable templates.
	ShellArgs []string `json:"shellArgs,omitempty"`
	// Dotenv is the path to the dotenv file. This is optional.
	Dotenv []string `json:"dotenv,omitempty"`
	// Labels contains the list of labels for the DAG. This is optional.
	Labels Labels `json:"labels,omitempty"`
	// Description is the description of the DAG. This is optional.
	Description string `json:"description,omitempty"`
	// Schedule configuration for starting, stopping, and restarting the DAG.
	Schedule []Schedule `json:"schedule,omitempty"`
	// StopSchedule contains the cron expressions for stopping the DAG.
	StopSchedule []Schedule `json:"stopSchedule,omitempty"`
	// RestartSchedule contains the cron expressions for restarting the DAG.
	RestartSchedule []Schedule `json:"restartSchedule,omitempty"`
	// SkipIfSuccessful indicates whether to skip the DAG if it was successful previously.
	// E.g., when the DAG has already been executed manually before the scheduled time.
	SkipIfSuccessful bool `json:"skipIfSuccessful,omitempty"`
	// CatchupWindow is the lookback horizon for missed cron intervals.
	// If set, enables catch-up on scheduler restart. If omitted, no catch-up.
	CatchupWindow time.Duration `json:"catchupWindow,omitempty"`
	// OverlapPolicy controls behavior when a new run is triggered while one is active.
	// Defaults to "skip". See OverlapPolicy constants for options.
	OverlapPolicy OverlapPolicy `json:"overlapPolicy,omitempty"`
	// Env contains a list of environment variables to be set before running the DAG.
	// Note: This field is evaluated at build time and may contain secrets.
	// It is excluded from JSON serialization to prevent secret leakage.
	Env []string `json:"-"`
	// PresolvedBuildEnv stores resolved DAG/base-config env entries needed to
	// rebuild the DAG from persisted YAML during retry/restart paths.
	// It is serialized with dag.json because direct retry/restart cannot rely on
	// parent-process transport once the original process has exited.
	PresolvedBuildEnv map[string]string `json:"presolvedBuildEnv,omitempty"`
	// LogDir is the directory where the logs are stored.
	LogDir string `json:"logDir,omitempty"`
	// Artifacts config controls optional DAG run artifact storage.
	Artifacts *ArtifactsConfig `json:"artifacts,omitempty"`
	// LogOutput specifies how stdout and stderr are handled in log files.
	// Can be "separate" (default) for separate .out and .err files,
	// or "merged" for a single combined .log file.
	LogOutput LogOutputMode `json:"logOutput,omitempty"`
	// DefaultParams contains the default parameters to be passed to the DAG.
	DefaultParams string `json:"defaultParams,omitempty"`
	// ParamDefs contains ordered parameter metadata derived from DAG params.
	// It is exposed to the API for typed UI rendering and validation hints.
	ParamDefs []ParamDef `json:"paramDefs,omitempty"`
	// ParamSchema contains the resolved JSON Schema for schema-backed DAG params
	// when that schema is safe for direct UI form rendering.
	ParamSchema json.RawMessage `json:"paramSchema,omitempty"`
	// Params contains the list of parameters to be passed to the DAG.
	// Note: This field is evaluated at build time and may contain secrets.
	// It is excluded from JSON serialization to prevent secret leakage.
	Params []string `json:"-"`
	// ParamsJSON contains the JSON representation of the resolved parameters.
	// When params were supplied as JSON, the original payload is preserved.
	// Steps can consume this via the DAGU_PARAMS_JSON environment variable.
	// Note: This field is evaluated at build time and may contain secrets.
	// It is excluded from JSON serialization to prevent secret leakage.
	ParamsJSON string `json:"-"`
	// Steps contains the list of steps in the DAG.
	Steps []Step `json:"steps,omitempty"`
	// HandlerOn contains the steps to be executed on different events.
	HandlerOn HandlerOn `json:"handlerOn,omitzero"`
	// Preconditions contains the conditions to be met before running the DAG.
	Preconditions []*Condition `json:"preconditions,omitempty"`
	// SMTP contains the SMTP configuration.
	// Excluded from JSON: may contain password.
	SMTP *SMTPConfig `json:"-"`
	// ErrorMail contains the mail configuration for errors.
	ErrorMail *MailConfig `json:"errorMail,omitempty"`
	// InfoMail contains the mail configuration for informational messages.
	InfoMail *MailConfig `json:"infoMail,omitempty"`
	// WaitMail contains the mail configuration for wait status.
	WaitMail *MailConfig `json:"waitMail,omitempty"`
	// MailOn contains the conditions to send mail.
	MailOn *MailOn `json:"mailOn,omitempty"`
	// Timeout specifies the maximum execution time of the DAG task.
	Timeout time.Duration `json:"timeout,omitempty"`
	// Delay is the delay before starting the DAG.
	Delay time.Duration `json:"delay,omitempty"`
	// RestartWait is the time to wait before restarting the DAG.
	RestartWait time.Duration `json:"restartWait,omitempty"`
	// MaxActiveSteps specifies the maximum concurrent steps to run in an execution.
	MaxActiveSteps int `json:"maxActiveSteps,omitempty"`
	// MaxActiveRuns specifies the maximum number of concurrent dag-runs.
	// DEPRECATED: This field is ignored for local (DAG-based) queues.
	// For concurrency control, define a global queue in config and use the 'queue' field.
	MaxActiveRuns int `json:"maxActiveRuns,omitempty"`
	// MaxCleanUpTime is the maximum time to wait for cleanup when the DAG is stopped.
	MaxCleanUpTime time.Duration `json:"maxCleanUpTime,omitempty"`
	// HistRetentionDays is the number of days to keep the history of dag-runs.
	HistRetentionDays int `json:"histRetentionDays,omitempty"`
	// HistRetentionRuns is the number of dag-runs to keep in history.
	HistRetentionRuns int `json:"histRetentionRuns,omitempty"`
	// Queue is the name of the queue to assign this DAG to.
	Queue string `json:"queue,omitempty"`
	// RetryPolicy controls automatic DAG-level retry behavior for failed runs.
	RetryPolicy *DAGRetryPolicy `json:"retryPolicy,omitempty"`
	// WorkerSelector defines labels required for worker selection in distributed execution.
	// If specified, the DAG will only run on workers with matching labels.
	WorkerSelector map[string]string `json:"workerSelector,omitempty"`
	// ForceLocal forces the DAG to run locally even when the server default is distributed.
	// Set by worker_selector: local in the DAG spec.
	ForceLocal bool `json:"forceLocal,omitempty"`
	// MaxOutputSize is the maximum size of step output to capture in bytes.
	// Default is 1MB. Output exceeding this will return an error.
	MaxOutputSize int `json:"maxOutputSize,omitempty"`
	// OTel contains the OpenTelemetry configuration for the DAG.
	OTel *OTelConfig `json:"otel,omitempty"`
	// BuildErrors contains any errors encountered while building the DAG.
	BuildErrors []error `json:"-"`
	// BuildWarnings contains non-fatal warnings detected while building the DAG.
	BuildWarnings []string `json:"-"`
	// LocalDAGs contains DAGs defined in the same file, keyed by DAG name
	LocalDAGs map[string]*DAG `json:"localDAGs,omitempty"`
	// YamlData contains the raw YAML data of the DAG.
	YamlData []byte `json:"yamlData,omitempty"`
	// BaseConfigData contains the raw base config YAML content.
	// This is used to propagate base config through distributed execution
	// and sub-DAG chains, so workers don't need local base config files.
	BaseConfigData []byte `json:"baseConfigData,omitempty"`
	// Container contains the container definition for the DAG.
	Container *Container `json:"container,omitempty"`
	// RunConfig contains configuration for controlling user interactions during DAG runs.
	RunConfig *RunConfig `json:"runConfig,omitempty"`
	// Webhook contains DAG-level webhook trigger behavior configuration.
	Webhook *WebhookConfig `json:"webhook,omitempty"`
	// RegistryAuths maps registry hostnames to authentication configs.
	// Optional: If not specified, falls back to DOCKER_AUTH_CONFIG or docker config.
	// Credentials are evaluated at runtime. Excluded from JSON: may contain passwords.
	RegistryAuths map[string]*AuthConfig `json:"-"`
	// SSH contains the default SSH configuration for the DAG.
	// Excluded from JSON: may contain password.
	SSH *SSHConfig `json:"-"`
	// S3 contains the default S3 configuration for the DAG.
	// Excluded from JSON: may contain credentials.
	S3 *S3Config `json:"-"`
	// LLM contains the default LLM configuration for the DAG.
	// Steps with type: chat inherit this configuration if they don't specify their own llm field.
	LLM *LLMConfig `json:"llm,omitempty"`
	// Redis contains the default Redis configuration for the DAG.
	// Steps with type: redis inherit this configuration.
	// Excluded from JSON: may contain password.
	Redis *RedisConfig `json:"-"`
	// Harness contains the default harness executor configuration for the DAG.
	// Steps with type: harness inherit this configuration.
	// Excluded from JSON: may contain API key references or provider-specific secrets.
	Harness *HarnessConfig `json:"-"`
	// Harnesses contains reusable custom harness definitions available to harness steps.
	// Excluded from JSON: derived from DAG/base config and rebuilt from YAML when needed.
	Harnesses HarnessDefinitions `json:"-"`
	// Kubernetes contains the default Kubernetes executor configuration for the DAG.
	// Steps with type: k8s or type: kubernetes inherit this configuration.
	// Excluded from JSON: may contain secret references.
	Kubernetes KubernetesConfig `json:"-"`
	// Secrets contains references to external secrets to be resolved at runtime.
	Secrets []SecretRef `json:"secrets,omitempty"`
	// dotenvOnce ensures LoadDotEnv is called only once, even with concurrent calls.
	// This provides thread-safe idempotency for dotenv loading.
	dotenvOnce sync.Once
}

const (
	ParamDefTypeString  = "string"
	ParamDefTypeInteger = "integer"
	ParamDefTypeNumber  = "number"
	ParamDefTypeBoolean = "boolean"
)

// ParamDef describes a single DAG parameter for API/UI consumers.
// Name is empty for positional parameters.
type ParamDef struct {
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type,omitempty"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	MinLength   *int     `json:"minLength,omitempty"`
	MaxLength   *int     `json:"maxLength,omitempty"`
	Pattern     *string  `json:"pattern,omitempty"`
}

// WebhookConfig contains DAG-level webhook trigger behavior.
type WebhookConfig struct {
	// ForwardHeaders is the allowlist of request headers to expose to
	// webhook-triggered DAG runs via the WEBHOOK_HEADERS runtime variable.
	ForwardHeaders []string `json:"forwardHeaders,omitempty"`
}

// ArtifactsConfig controls DAG run artifact storage.
type ArtifactsConfig struct {
	Enabled bool   `json:"enabled"`
	Dir     string `json:"dir,omitempty"`
}

// DAGRetryPolicy contains the retry policy for a DAG run.
type DAGRetryPolicy struct {
	// Limit is the maximum number of retry attempts allowed.
	Limit int `json:"limit,omitempty"`
	// Interval is the base delay before retrying.
	Interval time.Duration `json:"interval,omitempty"`
	// IntervalSecStr preserves the original interval string representation.
	IntervalSecStr string `json:"intervalSecStr,omitempty"`
	// Backoff is the retry delay multiplier. Zero keeps a fixed interval.
	Backoff float64 `json:"backoff,omitempty"`
	// MaxInterval caps the computed retry delay.
	MaxInterval time.Duration `json:"maxInterval,omitempty"`
}

// SecretRef represents a reference to an external secret.
// Secrets are resolved at DAG execution time and never persisted to disk.
type SecretRef struct {
	// Name is the environment variable name to set (required).
	Name string `json:"name"`
	// Ref is the workspace-local registry reference for a team-managed secret.
	Ref string `json:"ref,omitempty"`
	// Provider specifies the secret backend (e.g., "env", "file", "vault", "kubernetes").
	Provider string `json:"provider,omitempty"`
	// Key is the provider-specific identifier for a direct provider reference.
	Key string `json:"key,omitempty"`
	// Options contains provider-specific configuration (optional).
	Options map[string]string `json:"options,omitempty"`
}

// UnmarshalJSON deserializes DAGs written by both the canonical labels field
// and the deprecated tags field used by older persisted dag.json files.
func (d *DAG) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	type alias DAG
	aux := struct {
		*alias
		DeprecatedTags Labels `json:"tags,omitempty"`
	}{
		alias: (*alias)(d),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if _, hasLabels := raw["labels"]; !hasLabels && len(aux.DeprecatedTags) > 0 {
		d.Labels = aux.DeprecatedTags
	}
	return nil
}

// HasLabel checks if the DAG has a label matching the given filter.
// Supports both simple labels ("production") and key-value filters ("env=prod").
func (d *DAG) HasLabel(label string) bool {
	filter := ParseLabelFilter(label)
	return filter.MatchesLabels(d.Labels)
}

// HasTag checks if the DAG has a tag matching the given filter.
// Deprecated: use HasLabel.
func (d *DAG) HasTag(tag string) bool {
	return d.HasLabel(tag)
}

// Clone creates a shallow copy of the DAG.
// The sync.Once field is reset to zero value, allowing LoadDotEnv to be called
// independently on the clone. This is safe for read-only field modifications
// like changing Location.
func (d *DAG) Clone() *DAG {
	//nolint:govet // intentional copy; sync.Once is immediately reset below
	clone := *d
	// Reset sync.Once so LoadDotEnv can be called on the clone
	clone.dotenvOnce = sync.Once{}
	if d.PresolvedBuildEnv != nil {
		clone.PresolvedBuildEnv = maps.Clone(d.PresolvedBuildEnv)
	}
	if d.Artifacts != nil {
		artifactsCopy := *d.Artifacts
		clone.Artifacts = &artifactsCopy
	}
	if d.Webhook != nil {
		webhookCopy := *d.Webhook
		webhookCopy.ForwardHeaders = append([]string(nil), d.Webhook.ForwardHeaders...)
		clone.Webhook = &webhookCopy
	}
	if d.Harness != nil {
		clone.Harness = cloneHarnessConfig(d.Harness)
	}
	if d.Harnesses != nil {
		clone.Harnesses = cloneHarnessDefinitions(d.Harnesses)
	}
	if d.Kubernetes != nil {
		clone.Kubernetes = cloneKubernetesConfig(d.Kubernetes)
	}
	return &clone
}

// HasApprovalSteps returns true if the DAG contains any steps that require
// human approval. DAGs with approval steps cannot be dispatched to workers
// because approval steps require local storage access.
func (d *DAG) HasApprovalSteps() bool {
	for _, step := range d.Steps {
		if step.Approval != nil {
			return true
		}
	}
	return false
}

// SockAddr returns the unix socket address for the DAG.
// The address is used to communicate with the agent process.
func (d *DAG) SockAddr(dagRunID string) string {
	if d.Location != "" {
		return SockAddr(d.Location, "")
	}
	return SockAddr(d.Name, dagRunID)
}

// SockAddrForSubDAGRun returns the unix socket address for a specific dag-run ID.
// This is used to control sub dag-runs.
func (d *DAG) SockAddrForSubDAGRun(dagRunID string) string {
	return SockAddr(d.GetName(), dagRunID)
}

// GetName returns the name of the DAG.
// If the name is not set, it returns the default name (filename without extension).
func (d *DAG) GetName() string {
	if d.Name != "" {
		return d.Name
	}
	filename := filepath.Base(d.Location)
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

// String returns a formatted string representation of the DAG.
func (d *DAG) String() string {
	var sb strings.Builder

	sb.WriteString("{\n")
	fmt.Fprintf(&sb, "\tName: %s\n", d.Name)
	fmt.Fprintf(&sb, "\tDescription: %s\n", strings.TrimSpace(d.Description))
	fmt.Fprintf(&sb, "\tParams: %v\n", strings.Join(d.Params, ", "))
	fmt.Fprintf(&sb, "\tLogDir: %v\n", d.LogDir)

	for i, step := range d.Steps {
		fmt.Fprintf(&sb, "\tStep%d: %s\n", i, step.String())
	}

	sb.WriteString("}\n")
	return sb.String()
}

// Validate performs basic validation of the DAG structure.
// It collects all validation errors instead of returning on first error.
func (d *DAG) Validate() error {
	var errs ErrorList

	if d.Name == "" {
		errs = append(errs, fmt.Errorf("DAG name is required"))
	}

	stepExists := make(map[string]bool, len(d.Steps))
	for _, step := range d.Steps {
		stepExists[step.Name] = true
	}

	for _, step := range d.Steps {
		for _, dep := range step.Depends {
			if !stepExists[dep] {
				errs = append(errs, NewValidationError("depends", dep,
					fmt.Errorf("step %s depends on non-existent step", step.Name)))
			}
		}
	}

	for _, err := range d.validateOutputReferences() {
		errs = append(errs, NewValidationError("output reference", nil, err))
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

// NextRun returns the next scheduled run time based on the DAG's schedules.
func (d *DAG) NextRun(now time.Time) time.Time {
	var next time.Time
	for _, sched := range d.Schedule {
		t := sched.Next(now)
		if t.IsZero() {
			continue
		}
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	return next
}

// deduplicateStrings removes duplicate strings while preserving order.
func deduplicateStrings(input []string) []string {
	seen := make(map[string]bool, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if seen[s] {
			continue
		}
		seen[s] = true
		result = append(result, s)
	}
	return result
}

// LoadDotEnv loads all dotenv files in order, with later files overriding earlier ones.
// This method is thread-safe and idempotent - concurrent calls will only load once.
func (d *DAG) LoadDotEnv(ctx context.Context) {
	d.dotenvOnce.Do(func() {
		d.loadDotEnvFiles(ctx)
	})
}

// loadDotEnvFiles performs the actual dotenv file loading.
func (d *DAG) loadDotEnvFiles(ctx context.Context) {
	if len(d.Dotenv) == 0 {
		return
	}

	scope := d.dotenvEnvScope()
	evalCtx := ctx
	if evalCtx == nil {
		evalCtx = context.Background()
	}
	evalCtx = eval.WithEnvScope(evalCtx, scope)

	workingDir := expandDotEnvPath(d.WorkingDir, scope)
	relativeTos := []string{workingDir}
	if fileDir := filepath.Dir(d.Location); d.Location != "" && fileDir != workingDir {
		relativeTos = append(relativeTos, fileDir)
	}

	resolver := fileutil.NewFileResolver(relativeTos)
	candidates := deduplicateStrings(append([]string{".env"}, d.Dotenv...))

	for _, filePath := range candidates {
		d.loadSingleDotEnvFile(evalCtx, resolver, filePath)
	}
}

// dotenvEnvScope builds the variable scope used to resolve dotenv search paths.
func (d *DAG) dotenvEnvScope() *eval.EnvScope {
	scope := eval.NewEnvScope(nil, true)
	if params := buildenv.ToMap(d.Params); len(params) > 0 {
		scope = scope.WithEntries(params, eval.EnvSourceParam)
	}
	if len(d.PresolvedBuildEnv) > 0 {
		scope = scope.WithEntries(d.PresolvedBuildEnv, eval.EnvSourcePresolved)
	}
	if envs := buildenv.ToMap(d.Env); len(envs) > 0 {
		scope = scope.WithEntries(envs, eval.EnvSourceDAGEnv)
	}
	return scope
}

// expandDotEnvPath expands a dotenv-related path without mutating the DAG definition.
func expandDotEnvPath(path string, scope *eval.EnvScope) string {
	if scope == nil {
		return os.ExpandEnv(path)
	}
	return scope.Expand(path)
}

// loadSingleDotEnvFile loads a single dotenv file and appends its variables to d.Env.
func (d *DAG) loadSingleDotEnvFile(ctx context.Context, resolver *fileutil.FileResolver, filePath string) {
	if strings.TrimSpace(filePath) == "" {
		return
	}

	evaluatedPath, err := eval.String(ctx, filePath, eval.WithOSExpansion())
	if err != nil {
		logger.Warn(ctx, "Failed to evaluate filepath", tag.File(filePath), tag.Error(err))
		return
	}

	resolvedPath, err := resolver.ResolveFilePath(evaluatedPath)
	if err != nil || !fileutil.FileExists(resolvedPath) {
		return
	}

	vars, err := godotenv.Read(resolvedPath)
	if err != nil {
		logger.Warn(ctx, "Failed to load .env file", tag.File(resolvedPath), tag.Error(err))
		return
	}

	for k, v := range vars {
		d.Env = append(d.Env, fmt.Sprintf("%s=%s", k, v))
	}
	logger.Info(ctx, "Loaded dotenv file", tag.File(resolvedPath))
}

// initializeDefaults sets the default values for the DAG.
func (d *DAG) initializeDefaults() {
	const (
		defaultHistRetentionDays = 30
		defaultMaxCleanUpTime    = 5 * time.Second
		defaultMaxActiveRuns     = 1
		defaultMaxOutputSize     = 1024 * 1024 // 1MB
	)

	if d.Type == "" {
		d.Type = TypeChain
	}
	if d.LogOutput == "" {
		d.LogOutput = LogOutputSeparate
	}
	if d.HistRetentionDays == 0 && d.HistRetentionRuns == 0 {
		d.HistRetentionDays = defaultHistRetentionDays
	}
	if d.MaxCleanUpTime == 0 {
		d.MaxCleanUpTime = defaultMaxCleanUpTime
	}
	if d.MaxActiveRuns == 0 {
		d.MaxActiveRuns = defaultMaxActiveRuns
	}
	if d.MaxOutputSize == 0 {
		d.MaxOutputSize = defaultMaxOutputSize
	}
}

// InitializeDefaults exposes initializeDefaults for packages that prepare DAGs before execution.
func InitializeDefaults(d *DAG) {
	d.initializeDefaults()
}

// ArtifactsEnabled reports whether the DAG has artifact storage enabled.
func (d *DAG) ArtifactsEnabled() bool {
	return d != nil && d.Artifacts != nil && d.Artifacts.Enabled
}

// ParamsMap returns the parameters as a map.
func (d *DAG) ParamsMap() map[string]string {
	params := make(map[string]string)
	for _, p := range d.Params {
		key, value, found := strings.Cut(p, "=")
		if found {
			params[key] = value
		}
	}
	return params
}

// ProcGroup returns the name of the process group for this DAG.
// The process group name is used to identify and manage related DAG executions.
//
// Returns:
//   - If Queue is set: returns the Queue value
//   - If Queue is empty: returns the DAG name as the default
//
// The process group name is used by the process store to:
//  1. Manage heartbeat files for active DAG runs
//  2. Enforce concurrency limits (max concurrent runs) across DAGs in the same group
//
// This allows the scheduler to control how many DAGs can run simultaneously
// within the same process group.
func (d *DAG) ProcGroup() string {
	if d.Queue != "" {
		return d.Queue
	}
	return d.Name
}

// SuspendFlagName returns the filename stem used by the file-based suspend
// flag system. This intentionally follows DAG file naming, not dag.Name.
func (d *DAG) SuspendFlagName() string {
	if d == nil {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(d.Location), filepath.Ext(d.Location))
	if base != "" && base != "." {
		return base
	}
	return d.Name
}

// FileName returns the filename of the DAG without the extension.
func (d *DAG) FileName() string {
	if d.Location == "" {
		return ""
	}
	return fileutil.TrimYAMLFileExtension(filepath.Base(d.Location))
}

// AuthConfig represents Docker registry authentication configuration.
// This is a simplified structure for user convenience that will be
// converted to Docker's registry.AuthConfig format when needed.
type AuthConfig struct {
	// Username for registry authentication
	Username string `json:"username,omitempty"`
	// Password for registry authentication
	Password string `json:"password,omitempty"`
	// Auth can be used instead of username/password for pre-encoded credentials
	// This should be base64(username:password)
	Auth string `json:"auth,omitempty"`
}

// RunConfig contains configuration for controlling user interactions during DAG runs.
type RunConfig struct {
	// DisableParamEdit when set to true, prevents users from editing parameters when starting a DAG.
	DisableParamEdit bool `json:"disableParamEdit,omitempty"`
	// DisableRunIdEdit when set to true, prevents users from specifying custom run IDs.
	DisableRunIdEdit bool `json:"disableRunIdEdit,omitempty"`
}

// SSHConfig contains the SSH configuration for the DAG.
type SSHConfig struct {
	// User is the SSH user.
	User string `json:"user,omitempty"`
	// Host is the SSH host.
	Host string `json:"host,omitempty"`
	// Port is the SSH port. Default is "22".
	Port string `json:"port,omitempty"`
	// Key is the path to the SSH private key.
	Key string `json:"key,omitempty"`
	// Password is the SSH password.
	Password string `json:"password,omitempty"`
	// StrictHostKey enables strict host key checking. Defaults to true.
	StrictHostKey bool `json:"strictHostKey,omitempty"`
	// KnownHostFile is the path to the known_hosts file. Defaults to ~/.ssh/known_hosts.
	KnownHostFile string `json:"knownHostFile,omitempty"`
	// Shell is the shell to use for remote command execution.
	// If not specified, commands are executed directly without shell wrapping.
	Shell string `json:"shell,omitempty"`
	// ShellArgs contains additional arguments that should be passed to the shell executable.
	ShellArgs []string `json:"shellArgs,omitempty"`
	// Timeout is the connection timeout duration (e.g., "30s", "1m"). Defaults to 30s.
	Timeout string `json:"timeout,omitempty"`
	// Bastion is the jump host / bastion server configuration for connecting to the target host.
	Bastion *BastionConfig `json:"bastion,omitempty"`
}

// BastionConfig contains the configuration for a bastion/jump host.
type BastionConfig struct {
	// Host is the bastion host address.
	Host string `json:"host,omitempty"`
	// Port is the bastion SSH port. Default is "22".
	Port string `json:"port,omitempty"`
	// User is the bastion SSH user.
	User string `json:"user,omitempty"`
	// Key is the path to the SSH private key for the bastion.
	Key string `json:"key,omitempty"`
	// Password is the SSH password for the bastion.
	Password string `json:"password,omitempty"`
}

// S3Config contains the default S3 configuration for the DAG.
// This allows steps to inherit S3 settings without specifying them individually.
type S3Config struct {
	// Region is the AWS region (e.g., us-east-1).
	Region string `json:"region,omitempty"`
	// Endpoint is a custom S3-compatible endpoint URL.
	// Use this for S3-compatible services like MinIO, LocalStack, etc.
	Endpoint string `json:"endpoint,omitempty"`
	// AccessKeyID is the AWS access key ID.
	AccessKeyID string `json:"accessKeyId,omitempty"`
	// SecretAccessKey is the AWS secret access key.
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	// SessionToken is the AWS session token (for temporary credentials).
	SessionToken string `json:"sessionToken,omitempty"`
	// Profile is the AWS credentials profile name.
	Profile string `json:"profile,omitempty"`
	// ForcePathStyle enables path-style addressing (required for S3-compatible services).
	ForcePathStyle bool `json:"forcePathStyle,omitempty"`
	// DisableSSL disables SSL for the connection (for local testing only).
	DisableSSL bool `json:"disableSSL,omitempty"`
	// Bucket is the default S3 bucket name.
	// Can be overridden at the step level.
	Bucket string `json:"bucket,omitempty"`
}

// RedisConfig contains the default Redis configuration for the DAG.
// Steps with type: redis inherit this configuration.
type RedisConfig struct {
	// URL is the Redis connection URL (redis://user:pass@host:port/db).
	URL string `json:"url,omitempty"`
	// Host is the Redis host (alternative to URL).
	Host string `json:"host,omitempty"`
	// Port is the Redis port (default: 6379).
	Port int `json:"port,omitempty"`
	// Password is the authentication password.
	Password string `json:"password,omitempty"`
	// Username is the ACL username (Redis 6+).
	Username string `json:"username,omitempty"`
	// DB is the database number (0-15).
	DB int `json:"db,omitempty"`
	// TLS enables TLS connection.
	TLS bool `json:"tls,omitempty"`
	// TLSSkipVerify skips TLS certificate verification.
	TLSSkipVerify bool `json:"tlsSkipVerify,omitempty"`
	// Mode is the connection mode (standalone, sentinel, cluster).
	Mode string `json:"mode,omitempty"`
	// SentinelMaster is the sentinel master name.
	SentinelMaster string `json:"sentinelMaster,omitempty"`
	// SentinelAddrs is the list of sentinel addresses.
	SentinelAddrs []string `json:"sentinelAddrs,omitempty"`
	// ClusterAddrs is the list of cluster node addresses.
	ClusterAddrs []string `json:"clusterAddrs,omitempty"`
	// MaxRetries is the maximum number of retries.
	MaxRetries int `json:"maxRetries,omitempty"`
}

// HarnessConfig contains the default harness executor configuration for the DAG.
// Steps with type: harness inherit Config as their primary attempt and Fallback
// as ordered alternative provider configs.
type HarnessConfig struct {
	// Config contains the primary provider selection and CLI flags.
	Config map[string]any `json:"-"`
	// Fallback contains ordered alternative provider configs tried on failure.
	Fallback []map[string]any `json:"-"`
}

// KubernetesConfig contains default Kubernetes executor configuration for the DAG.
// It stores the raw executor config map so step-level overrides can be merged
// using executor-specific semantics during DAG build.
type KubernetesConfig map[string]any

func cloneHarnessConfig(cfg *HarnessConfig) *HarnessConfig {
	if cfg == nil {
		return nil
	}
	return &HarnessConfig{
		Config:   cloneHarnessConfigMap(cfg.Config),
		Fallback: cloneHarnessFallbackConfigs(cfg.Fallback),
	}
}

func cloneHarnessConfigMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	cloned := make(map[string]any, len(cfg))
	for key, value := range cfg {
		cloned[key] = cloneHarnessValue(value)
	}
	return cloned
}

func cloneHarnessFallbackConfigs(cfgs []map[string]any) []map[string]any {
	if cfgs == nil {
		return nil
	}

	cloned := make([]map[string]any, len(cfgs))
	for i := range cfgs {
		cloned[i] = cloneHarnessConfigMap(cfgs[i])
	}
	return cloned
}

func cloneHarnessValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneHarnessConfigMap(v)
	case []any:
		cloned := make([]any, len(v))
		for i := range v {
			cloned[i] = cloneHarnessValue(v[i])
		}
		return cloned
	case []string:
		return append([]string(nil), v...)
	case []map[string]any:
		return cloneHarnessFallbackConfigs(v)
	default:
		return value
	}
}

func cloneKubernetesConfig(cfg KubernetesConfig) KubernetesConfig {
	if cfg == nil {
		return nil
	}

	cloned := make(KubernetesConfig, len(cfg))
	for key, value := range cfg {
		cloned[key] = cloneKubernetesValue(value)
	}
	return cloned
}

func cloneKubernetesValue(value any) any {
	switch v := value.(type) {
	case KubernetesConfig:
		return cloneKubernetesConfig(v)
	case map[string]any:
		return map[string]any(cloneKubernetesConfig(v))
	case []any:
		cloned := make([]any, len(v))
		for i := range v {
			cloned[i] = cloneKubernetesValue(v[i])
		}
		return cloned
	case []string:
		return append([]string(nil), v...)
	case []map[string]any:
		cloned := make([]map[string]any, len(v))
		for i := range v {
			cloned[i] = map[string]any(cloneKubernetesConfig(v[i]))
		}
		return cloned
	default:
		return value
	}
}

// Schedule contains the cron expression and the parsed cron schedule.
type Schedule struct {
	// Kind identifies the schedule type.
	Kind ScheduleKind `json:"kind,omitempty"`
	// Expression is the cron expression.
	Expression string `json:"expression,omitempty"`
	// At is the canonical RFC 3339 timestamp for one-off schedules.
	At string `json:"at,omitempty"`
	// Parsed is the parsed cron schedule.
	Parsed cron.Schedule `json:"-"`
	// AtTime is the parsed one-off schedule time.
	AtTime time.Time `json:"-"`
	// Warnings contains non-fatal schedule warnings.
	Warnings []string `json:"warnings,omitempty"`
}

// MarshalJSON implements the json.Marshaler interface.
func (s Schedule) MarshalJSON() ([]byte, error) {
	normalized, err := s.normalized()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Kind       ScheduleKind `json:"kind,omitempty"`
		Expression string       `json:"expression,omitempty"`
		At         string       `json:"at,omitempty"`
		Warnings   []string     `json:"warnings,omitempty"`
	}{
		Kind:       normalized.Kind,
		Expression: normalized.Expression,
		At:         normalized.At,
		Warnings:   normalized.Warnings,
	})
}

// UnmarshalJSON implements the json.Unmarshaler interface.
// It also parses the cron expression to populate the Parsed field.
func (s *Schedule) UnmarshalJSON(data []byte) error {
	var alias struct {
		Kind       ScheduleKind `json:"kind"`
		Expression string       `json:"expression"`
		At         string       `json:"at"`
	}
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	if alias.Kind == "" && alias.Expression == "" && alias.At == "" {
		*s = Schedule{}
		return nil
	}

	raw := make(map[string]any, 3)
	if alias.Kind != "" {
		raw["kind"] = string(alias.Kind)
	}
	if alias.Expression != "" {
		raw["expression"] = alias.Expression
	}
	if alias.At != "" {
		raw["at"] = alias.At
	}

	schedule, err := parseScheduleMap(raw, ScheduleParseOptions{AllowAt: true})
	if err != nil {
		return err
	}
	*s = schedule
	return nil
}

// HandlerOn contains the steps to be executed on different events in the DAG.
type HandlerOn struct {
	Init    *Step `json:"init,omitempty"`
	Failure *Step `json:"failure,omitempty"`
	Success *Step `json:"success,omitempty"`
	Abort   *Step `json:"abort,omitempty"`
	Exit    *Step `json:"exit,omitempty"`
	Wait    *Step `json:"wait,omitempty"`
}

// MailOn contains the conditions to send mail.
type MailOn struct {
	Failure bool `json:"failure,omitempty"`
	Success bool `json:"success,omitempty"`
	Wait    bool `json:"wait,omitempty"`
}

// SMTPConfig contains the SMTP configuration.
type SMTPConfig struct {
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// MailConfig contains the mail configuration.
type MailConfig struct {
	From       string   `json:"from,omitempty"`
	To         []string `json:"to,omitempty"`
	Prefix     string   `json:"prefix,omitempty"`
	AttachLogs bool     `json:"attachLogs,omitempty"`
}

// OTelConfig contains the OpenTelemetry configuration.
type OTelConfig struct {
	Enabled  bool              `json:"enabled,omitempty"`
	Endpoint string            `json:"endpoint,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Insecure bool              `json:"insecure,omitempty"`
	Timeout  time.Duration     `json:"timeout,omitempty"`
	Resource map[string]any    `json:"resource,omitempty"`
}

// HandlerType is the type of the handler.
type HandlerType string

const (
	HandlerOnInit    HandlerType = "onInit"
	HandlerOnSuccess HandlerType = "onSuccess"
	HandlerOnFailure HandlerType = "onFailure"
	HandlerOnAbort   HandlerType = "onAbort"
	HandlerOnExit    HandlerType = "onExit"
	HandlerOnWait    HandlerType = "onWait"
)

func (h HandlerType) String() string {
	return string(h)
}

// SockAddr returns the unix socket address for the DAG.
// The address is used to communicate with the agent process.
func SockAddr(name, dagRunID string) string {
	const (
		hashLength          = 6
		maxSocketNameLength = 50
		prefix              = "@dagu_"
		suffix              = ".sock"
	)

	hash := fmt.Sprintf("%x", md5.Sum([]byte(name+dagRunID)))[:hashLength] //nolint:gosec
	safeName := fileutil.SafeName(name)

	// Calculate available space for the name
	fixedLen := len(prefix) + 1 + len(hash) + len(suffix) // +1 for underscore connector
	maxNameLen := maxSocketNameLength - fixedLen
	if len(safeName) > maxNameLen {
		safeName = safeName[:maxNameLen]
	}

	return getSocketPath(fmt.Sprintf("%s%s_%s%s", prefix, safeName, hash, suffix))
}

// getSocketPath returns the appropriate socket path for the current platform.
// On Unix systems, it uses /tmp directory. On Windows, it uses the system temp directory.
func getSocketPath(socketName string) string {
	baseDir := "/tmp"
	if runtime.GOOS == "windows" {
		baseDir = os.TempDir()
	}
	return filepath.Join(baseDir, socketName)
}
