// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/go-viper/mapstructure/v2"
)

const dagRunArtifactsDirEnvKey = "DAG_RUN_ARTIFACTS_DIR"

var dagRunArtifactsDirReferencePattern = regexp.MustCompile(
	`(?:\$\{` + regexp.QuoteMeta(dagRunArtifactsDirEnvKey) + `\}` +
		`|\$` + regexp.QuoteMeta(dagRunArtifactsDirEnvKey) + `(?:\b|[^A-Za-z0-9_])` +
		`|\$env:` + regexp.QuoteMeta(dagRunArtifactsDirEnvKey) + `(?:\b|[^A-Za-z0-9_])` +
		`|%` + regexp.QuoteMeta(dagRunArtifactsDirEnvKey) + `%` +
		`|env\(["']` + regexp.QuoteMeta(dagRunArtifactsDirEnvKey) + `["']\))`,
)

// dag is the intermediate representation of a DAG specification.
// It mirrors the YAML structure and gets validated/transformed into core.DAG.
type dag struct {
	// Name is the name of the DAG.
	Name string `yaml:"name,omitempty"`
	// Group is the group of the DAG for grouping DAGs on the UI.
	Group string `yaml:"group,omitempty"`
	// Description is the description of the DAG.
	Description string `yaml:"description,omitempty"`
	// Type is the execution type for steps (graph, chain, or agent).
	// Default is "chain" which executes steps in the order they are defined.
	// "graph" uses dependency-based parallel execution.
	// "agent" is reserved for future agent-based execution.
	Type string `yaml:"type,omitempty"`
	// Shell is the default shell to use for all steps in this DAG.
	// If not specified, the system default shell is used.
	// Can be overridden at the step level.
	// Can be a string (e.g., "bash -e") or an array (e.g., ["bash", "-e"]).
	Shell types.ShellValue `yaml:"shell,omitempty"`
	// WorkingDir is working directory for DAG execution
	WorkingDir string `yaml:"working_dir,omitempty"`
	// Dotenv is the path to the dotenv file (string or []string).
	Dotenv types.StringOrArray `yaml:"dotenv,omitempty"`
	// Schedule is the cron schedule to run the DAG.
	Schedule types.ScheduleValue `yaml:"schedule,omitempty"`
	// SkipIfSuccessful is the flag to skip the DAG on schedule when it is
	// executed manually before the schedule.
	SkipIfSuccessful bool `yaml:"skip_if_successful,omitempty"`
	// CatchupWindow is the lookback horizon for missed intervals (e.g. "6h", "2d12h").
	// If set, enables catch-up on scheduler restart. If omitted, no catch-up.
	CatchupWindow string `yaml:"catchup_window,omitempty"`
	// OverlapPolicy controls how multiple catch-up runs are handled: "skip" or "all".
	OverlapPolicy string `yaml:"overlap_policy,omitempty"`
	// LogDir is the directory where the logs are stored.
	LogDir string `yaml:"log_dir,omitempty"`
	// Artifacts config controls optional DAG run artifact storage.
	Artifacts *artifactsConfig `yaml:"artifacts,omitempty"`
	// LogOutput specifies how stdout and stderr are handled in log files.
	// Can be "separate" (default) for separate .out and .err files,
	// or "merged" for a single combined .log file.
	LogOutput types.LogOutputValue `yaml:"log_output,omitempty"`
	// Env is the environment variables setting.
	Env types.EnvValue `yaml:"env,omitempty"`
	// HandlerOn is the handler configuration.
	HandlerOn handlerOn `yaml:"handler_on,omitempty"`
	// handlerOnRaw preserves raw handler maps so explicit zero-value call-site
	// overrides remain distinguishable from omission during build.
	handlerOnRaw map[string]map[string]any
	// defaultsRaw preserves the authored defaults map so explicit zero/empty
	// DAG-local overrides can replace inherited base defaults during merge.
	defaultsRaw map[string]any
	// StepTypes defines deprecated legacy step_types entries that expand to builtin-backed steps.
	StepTypes map[string]customStepTypeSpec `yaml:"step_types,omitempty"`
	// Actions defines reusable v2 actions that expand to builtin actions or run steps.
	Actions map[string]customStepTypeSpec `yaml:"actions,omitempty"`
	// Steps is the list of steps to run.
	Steps any `yaml:"steps,omitempty"` // []step or map[string]step
	// SMTP is the SMTP configuration.
	SMTP smtpConfig `yaml:"smtp,omitempty"`
	// MailOn is the mail configuration.
	MailOn *mailOn `yaml:"mail_on,omitempty"`
	// ErrorMail is the mail configuration for error.
	ErrorMail mailConfig `yaml:"error_mail,omitempty"`
	// InfoMail is the mail configuration for information.
	InfoMail mailConfig `yaml:"info_mail,omitempty"`
	// WaitMail is the mail configuration for wait status.
	WaitMail mailConfig `yaml:"wait_mail,omitempty"`
	// TimeoutSec is the timeout in seconds to finish the DAG.
	TimeoutSec int `yaml:"timeout_sec,omitempty"`
	// DelaySec is the delay in seconds to start the first node.
	DelaySec int `yaml:"delay_sec,omitempty"`
	// RestartWaitSec is the wait in seconds to when the DAG is restarted.
	RestartWaitSec int `yaml:"restart_wait_sec,omitempty"`
	// HistRetentionDays is the retention days of the dag-runs history.
	HistRetentionDays *int `yaml:"hist_retention_days,omitempty"`
	// HistRetentionRuns is the number of dag-runs to retain in history.
	HistRetentionRuns *int `yaml:"hist_retention_runs,omitempty"`
	// Preconditions is the condition to run the DAG.
	Preconditions any `yaml:"preconditions,omitempty"`
	// MaxActiveRuns is the maximum number of concurrent dag-runs.
	MaxActiveRuns int `yaml:"max_active_runs,omitempty"`
	// MaxActiveSteps is the maximum number of concurrent steps.
	MaxActiveSteps int `yaml:"max_active_steps,omitempty"`
	// Params is the default parameters for the steps.
	Params any `yaml:"params,omitempty"`
	// MaxCleanUpTimeSec is the maximum time in seconds to clean up the DAG.
	// It is a wait time to kill the processes when it is requested to stop.
	// If the time is exceeded, the process is killed.
	MaxCleanUpTimeSec *int `yaml:"max_clean_up_time_sec,omitempty"`
	// Labels is the labels for the DAG.
	Labels types.LabelsValue `yaml:"labels,omitempty"`
	// DeprecatedTags is the deprecated tags field for backward compatibility.
	DeprecatedTags types.LabelsValue `yaml:"tags,omitempty"`
	// Queue is the name of the queue to assign this DAG to.
	Queue string `yaml:"queue,omitempty"`
	// RetryPolicy is the DAG-level retry policy.
	RetryPolicy *dagRetryPolicy `yaml:"retry_policy,omitempty"`
	// MaxOutputSize is the maximum size of the output for each step.
	MaxOutputSize int `yaml:"max_output_size,omitempty"`
	// OTel is the OpenTelemetry configuration.
	OTel any `yaml:"otel,omitempty"`
	// WorkerSelector specifies required worker labels for execution.
	// Can be a map of label key-value pairs or the string "local" to force local execution.
	WorkerSelector any `yaml:"worker_selector,omitempty"`
	// Container is the container definition for the DAG.
	// Can be a string (existing container name to exec into) or an object (container configuration).
	Container any `yaml:"container,omitempty"`
	// RunConfig contains configuration for controlling user interactions during DAG runs.
	RunConfig *runConfig `yaml:"run_config,omitempty"`
	// Webhook contains DAG-level webhook trigger behavior configuration.
	Webhook *webhookConfig `yaml:"webhook,omitempty"`
	// RegistryAuths maps registry hostnames to authentication configs.
	// Can be either a JSON string or a map of registry to auth config.
	RegistryAuths any `yaml:"registry_auths,omitempty"`
	// SSH is the default SSH configuration for the DAG.
	SSH *ssh `yaml:"ssh,omitempty"`
	// S3 is the default S3 configuration for the DAG.
	// Steps can inherit these settings without specifying them individually.
	S3 *s3Config `yaml:"s3,omitempty"`
	// LLM is the default LLM configuration for all chat steps in this DAG.
	// Steps can override this configuration by specifying their own llm field.
	LLM *llmConfig `yaml:"llm,omitempty"`
	// Redis is the default Redis configuration for all redis steps in this DAG.
	// Steps can override this configuration by specifying their own with fields.
	Redis *redisConfig `yaml:"redis,omitempty"`
	// Harnesses contains reusable custom harness definitions available to harness steps.
	Harnesses map[string]any `yaml:"harnesses,omitempty"`
	// Harness is the default harness configuration for all harness steps in this DAG.
	// Steps can override primary config keys and replace fallback entirely.
	Harness map[string]any `yaml:"harness,omitempty"`
	// Kubernetes is the default Kubernetes configuration for explicit k8s steps in this DAG.
	// Steps can override this configuration by specifying their own with fields.
	Kubernetes map[string]any `yaml:"kubernetes,omitempty"`
	// Secrets contains references to external secrets.
	Secrets []secretRef `yaml:"secrets,omitempty"`
	// Defaults defines default values for step configuration fields.
	// Steps inherit these defaults and can override them individually.
	Defaults any `yaml:"defaults,omitempty"`
}

// dagRetryPolicy defines the retry policy for a DAG run.
type dagRetryPolicy struct {
	Limit          any `yaml:"limit,omitempty"`
	IntervalSec    any `yaml:"interval_sec,omitempty"`
	Backoff        any `yaml:"backoff,omitempty"`
	MaxIntervalSec any `yaml:"max_interval_sec,omitempty"`
}

type artifactsConfig struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Dir     string `yaml:"dir,omitempty"`
}

// handlerOn defines the steps to be executed on different events.
type handlerOn struct {
	Init    *step `yaml:"init,omitempty"`    // Step to execute before steps (after preconditions pass)
	Failure *step `yaml:"failure,omitempty"` // Step to execute on failure
	Success *step `yaml:"success,omitempty"` // Step to execute on success
	Abort   *step `yaml:"abort,omitempty"`   // Step to execute on abort
	Exit    *step `yaml:"exit,omitempty"`    // Step to execute on exit
	Wait    *step `yaml:"wait,omitempty"`    // Step to execute when DAG enters wait status (approval)
}

func (d *dag) rawHandler(name core.HandlerType) map[string]any {
	if d == nil || d.handlerOnRaw == nil {
		return nil
	}

	var key string
	switch name {
	case core.HandlerOnInit:
		key = "init"
	case core.HandlerOnSuccess:
		key = "success"
	case core.HandlerOnFailure:
		key = "failure"
	case core.HandlerOnAbort:
		key = "abort"
	case core.HandlerOnExit:
		key = "exit"
	case core.HandlerOnWait:
		key = "wait"
	default:
		return nil
	}

	return d.handlerOnRaw[key]
}

// smtpConfig defines the SMTP configuration.
type smtpConfig struct {
	Host     string          `yaml:"host,omitempty"`     // SMTP host
	Port     types.PortValue `yaml:"port,omitempty"`     // SMTP port (can be string or number)
	Username string          `yaml:"username,omitempty"` // SMTP username
	Password string          `yaml:"password,omitempty"` // SMTP password
}

// IsZero returns true if all fields are empty/default.
func (s smtpConfig) IsZero() bool {
	return s == smtpConfig{}
}

// mailConfig defines the mail configuration.
type mailConfig struct {
	From       string              `yaml:"from,omitempty"`        // Sender email address
	To         types.StringOrArray `yaml:"to,omitempty"`          // Recipient email address(es) - can be string or []string
	Prefix     string              `yaml:"prefix,omitempty"`      // Prefix for the email subject
	AttachLogs bool                `yaml:"attach_logs,omitempty"` // Flag to attach logs to the email
}

// IsZero returns true if all fields are empty/default.
func (m mailConfig) IsZero() bool {
	return reflect.DeepEqual(m, mailConfig{})
}

// mailOn defines the conditions to send mail.
type mailOn struct {
	Failure bool `yaml:"failure,omitempty"` // Send mail on failure
	Success bool `yaml:"success,omitempty"` // Send mail on success
	Wait    bool `yaml:"wait,omitempty"`    // Send mail on wait status
}

// container defines the container configuration for the DAG.
type container struct {
	// Exec specifies an existing container to exec into.
	// Mutually exclusive with Image.
	Exec string `yaml:"exec,omitempty"`
	// Name is the container name to use. If empty, Docker generates a random name.
	Name string `yaml:"name,omitempty"`
	// Image is the container image to use.
	Image string `yaml:"image,omitempty"`
	// PullPolicy is the policy to pull the image (e.g., "Always", "IfNotPresent").
	PullPolicy any `yaml:"pull_policy,omitempty"`
	// Env specifies environment variables for the container.
	Env any `yaml:"env,omitempty"` // Can be a map or struct
	// Volumes specifies the volumes to mount in the container.
	Volumes []string `yaml:"volumes,omitempty"` // Map of volume names to volume definitions
	// User is the user to run the container as.
	User string `yaml:"user,omitempty"` // User to run the container as
	// WorkingDir is the working directory inside the container.
	WorkingDir string `yaml:"working_dir,omitempty"` // Working directory inside the container
	// Platform specifies the platform for the container (e.g., "linux/amd64").
	Platform string `yaml:"platform,omitempty"` // Platform for the container
	// Ports specifies the ports to expose from the container.
	Ports []string `yaml:"ports,omitempty"` // List of ports to expose
	// Network is the network configuration for the container.
	Network string `yaml:"network,omitempty"` // Network configuration for the container
	// KeepContainer is the flag to keep the container after the DAG run.
	KeepContainer bool `yaml:"keep_container,omitempty"` // Keep the container after the DAG run
	// Startup determines how the DAG-level container starts up.
	Startup string `yaml:"startup,omitempty"`
	// Command used when Startup == "command".
	Command []string `yaml:"command,omitempty"`
	// WaitFor readiness condition: running|healthy
	WaitFor string `yaml:"wait_for,omitempty"`
	// LogPattern regex to wait for in container logs.
	LogPattern string `yaml:"log_pattern,omitempty"`
	// RestartPolicy: no|always|unless-stopped
	RestartPolicy string `yaml:"restart_policy,omitempty"`
	// Healthcheck defines a custom healthcheck for the container.
	Healthcheck *healthcheck `yaml:"healthcheck,omitempty"`
	// Shell specifies the shell wrapper for executing step commands.
	Shell []string `yaml:"shell,omitempty"`
}

// healthcheck is the spec representation for custom health checks.
// Durations are specified as strings (e.g., "5s", "1m") for YAML convenience.
type healthcheck struct {
	// Test is the command to run. Must start with NONE, CMD, or CMD-SHELL.
	Test []string `yaml:"test,omitempty"`
	// Interval is the time between checks (e.g., "5s").
	Interval string `yaml:"interval,omitempty"`
	// Timeout is how long to wait for the check to complete (e.g., "3s").
	Timeout string `yaml:"timeout,omitempty"`
	// StartPeriod is the grace period for container initialization (e.g., "10s").
	StartPeriod string `yaml:"start_period,omitempty"`
	// Retries is the number of consecutive failures needed to mark unhealthy.
	Retries int `yaml:"retries,omitempty"`
}

// runConfig defines configuration for controlling user interactions during DAG runs.
type runConfig struct {
	DisableParamEdit bool `yaml:"disable_param_edit,omitempty"`  // Disable parameter editing when starting DAG
	DisableRunIdEdit bool `yaml:"disable_run_id_edit,omitempty"` // Disable custom run ID specification
}

type webhookConfig struct {
	ForwardHeaders []string `yaml:"forward_headers,omitempty"`
}

// ssh defines the SSH configuration for the DAG.
type ssh struct {
	// User is the SSH user.
	User string `yaml:"user,omitempty"`
	// Host is the SSH host.
	Host string `yaml:"host,omitempty"`
	// Port is the SSH port (can be string or number).
	Port types.PortValue `yaml:"port,omitempty"`
	// Key is the path to the SSH private key.
	Key string `yaml:"key,omitempty"`
	// Password is the SSH password.
	Password string `yaml:"password,omitempty"`
	// StrictHostKey enables strict host key checking. Defaults to true if not specified.
	StrictHostKey *bool `yaml:"strict_host_key,omitempty"`
	// KnownHostFile is the path to the known_hosts file. Defaults to ~/.ssh/known_hosts.
	KnownHostFile string `yaml:"known_host_file,omitempty"`
	// Shell is the shell to use for remote command execution.
	// Supports string or array syntax (e.g., "bash -e" or ["bash", "-e"]).
	// If not specified, commands are executed directly without shell wrapping.
	Shell types.ShellValue `yaml:"shell,omitempty"`
	// Timeout is the connection timeout duration (e.g., "30s", "1m"). Defaults to 30s.
	Timeout string `yaml:"timeout,omitempty"`
	// Bastion is the jump host / bastion server configuration.
	Bastion *bastion `yaml:"bastion,omitempty"`
}

// bastion defines the bastion/jump host configuration.
type bastion struct {
	// Host is the bastion host address.
	Host string `yaml:"host,omitempty"`
	// Port is the bastion SSH port (can be string or number).
	Port types.PortValue `yaml:"port,omitempty"`
	// User is the bastion SSH user.
	User string `yaml:"user,omitempty"`
	// Key is the path to the SSH private key for the bastion.
	Key string `yaml:"key,omitempty"`
	// Password is the SSH password for the bastion.
	Password string `yaml:"password,omitempty"`
}

// s3Config defines the default S3 configuration for the DAG.
// This allows steps to inherit S3 settings without specifying them individually.
type s3Config struct {
	// Region is the AWS region (e.g., us-east-1).
	Region string `yaml:"region,omitempty"`
	// Endpoint is a custom S3-compatible endpoint URL.
	// Use this for S3-compatible services like MinIO, LocalStack, etc.
	Endpoint string `yaml:"endpoint,omitempty"`
	// AccessKeyID is the AWS access key ID.
	AccessKeyID string `yaml:"access_key_id,omitempty"`
	// SecretAccessKey is the AWS secret access key.
	SecretAccessKey string `yaml:"secret_access_key,omitempty"`
	// SessionToken is the AWS session token (for temporary credentials).
	SessionToken string `yaml:"session_token,omitempty"`
	// Profile is the AWS credentials profile name.
	Profile string `yaml:"profile,omitempty"`
	// ForcePathStyle enables path-style addressing (required for S3-compatible services).
	ForcePathStyle bool `yaml:"force_path_style,omitempty"`
	// DisableSSL disables SSL for the connection (for local testing only).
	DisableSSL bool `yaml:"disable_ssl,omitempty"`
	// Bucket is the default S3 bucket name.
	// Can be overridden at the step level.
	Bucket string `yaml:"bucket,omitempty"`
}

// redisConfig defines the default Redis configuration for all redis steps in the DAG.
// Steps can override these settings by specifying their own with fields.
type redisConfig struct {
	// URL is the Redis connection URL (redis://user:pass@host:port/db).
	URL string `yaml:"url,omitempty"`
	// Host is the Redis host (alternative to URL).
	Host string `yaml:"host,omitempty"`
	// Port is the Redis port (default: 6379).
	Port int `yaml:"port,omitempty"`
	// Password is the authentication password.
	Password string `yaml:"password,omitempty"`
	// Username is the ACL username (Redis 6+).
	Username string `yaml:"username,omitempty"`
	// DB is the database number (0-15).
	DB int `yaml:"db,omitempty"`
	// TLS enables TLS connection.
	TLS bool `yaml:"tls,omitempty"`
	// TLSSkipVerify skips TLS certificate verification.
	TLSSkipVerify bool `yaml:"tls_skip_verify,omitempty"`
	// Mode is the connection mode (standalone, sentinel, cluster).
	Mode string `yaml:"mode,omitempty"`
	// SentinelMaster is the sentinel master name.
	SentinelMaster string `yaml:"sentinel_master,omitempty"`
	// SentinelAddrs is the list of sentinel addresses.
	SentinelAddrs []string `yaml:"sentinel_addrs,omitempty"`
	// ClusterAddrs is the list of cluster node addresses.
	ClusterAddrs []string `yaml:"cluster_addrs,omitempty"`
	// MaxRetries is the maximum number of retries.
	MaxRetries int `yaml:"max_retries,omitempty"`
}

// secretRef defines a reference to an external secret.
type secretRef struct {
	// Name is the environment variable name (required).
	Name string `yaml:"name"`
	// Provider specifies the secret backend (required).
	Provider string `yaml:"provider"`
	// Key is the provider-specific identifier (required).
	Key string `yaml:"key"`
	// Options contains provider-specific configuration (optional).
	Options map[string]string `yaml:"options,omitempty"`
}

// Transformer transforms a spec field into output field(s).
// C is the context type, T is the input type.
type Transformer[C any, T any] interface {
	// Transform performs the transformation and sets field(s) on out
	Transform(ctx C, in T, out reflect.Value) error
}

// dagTransformer is a generic implementation that provides type safety
// for the builder function while satisfying the DAGTransformer interface.
type dagTransformer[T any] struct {
	fieldName string
	builder   func(ctx BuildContext, d *dag) (T, error)
}

func (t *dagTransformer[T]) Transform(ctx BuildContext, in *dag, out reflect.Value) error {
	v, err := t.builder(ctx, in)
	if err != nil {
		return err
	}
	field := out.FieldByName(t.fieldName)
	if field.IsValid() && field.CanSet() {
		field.Set(reflect.ValueOf(v))
	}
	return nil
}

// newTransformer creates a DAGTransformer for a single field transformation
func newTransformer[T any](fieldName string, builder func(BuildContext, *dag) (T, error)) Transformer[BuildContext, *dag] {
	return &dagTransformer[T]{
		fieldName: fieldName,
		builder:   builder,
	}
}

// transform wraps a DAGTransformer with its name for error reporting
type transform struct {
	name        string
	transformer Transformer[BuildContext, *dag]
}

// metadataTransformers are always run (for listing, scheduling, etc.)
var metadataTransformers = []transform{
	{"name", newTransformer("Name", buildName)},
	{"group", newTransformer("Group", buildGroup)},
	{"description", newTransformer("Description", buildDescription)},
	{"type", newTransformer("Type", buildType)},
	{"labels", newTransformer("Labels", buildLabels)},
	// params must run BEFORE env so that env: values can reference ${param_name}
	{"params", newTransformer("Params", buildParams)},
	{"default_params", newTransformer("DefaultParams", buildDefaultParams)},
	{"param_defs", newTransformer("ParamDefs", buildParamDefs)},
	{"param_schema", newTransformer("ParamSchema", buildParamSchema)},
	{"params_json", newTransformer("ParamsJSON", buildParamsJSON)},
	{"env", newTransformer("Env", buildEnvs)},
	{"schedule", newTransformer("Schedule", buildSchedule)},
	{"stop_schedule", newTransformer("StopSchedule", buildStopSchedule)},
	{"restart_schedule", newTransformer("RestartSchedule", buildRestartSchedule)},
	{"worker_selector", &workerSelectorTransformer{}},
	{"timeout", newTransformer("Timeout", buildTimeout)},
	{"delay", newTransformer("Delay", buildDelay)},
	{"restart_wait", newTransformer("RestartWait", buildRestartWait)},
	{"max_active_runs", newTransformer("MaxActiveRuns", buildMaxActiveRuns)},
	{"max_active_steps", newTransformer("MaxActiveSteps", buildMaxActiveSteps)},
	{"queue", newTransformer("Queue", buildQueue)},
	{"retry_policy", newTransformer("RetryPolicy", buildDAGRetryPolicy)},
	{"max_output_size", newTransformer("MaxOutputSize", buildMaxOutputSize)},
	{"skip_if_successful", newTransformer("SkipIfSuccessful", buildSkipIfSuccessful)},
	{"catchup_window", newTransformer("CatchupWindow", buildCatchupWindow)},
	{"overlap_policy", newTransformer("OverlapPolicy", buildOverlapPolicy)},
}

// fullTransformers are only run when building the full DAG (not metadata-only)
var fullTransformers = []transform{
	{"log_dir", newTransformer("LogDir", buildLogDir)},
	{"artifacts", newTransformer("Artifacts", buildArtifacts)},
	{"log_output", newTransformer("LogOutput", buildLogOutput)},
	{"mail_on", newTransformer("MailOn", buildMailOn)},
	{"run_config", newTransformer("RunConfig", buildRunConfig)},
	{"webhook", newTransformer("Webhook", buildWebhookConfig)},
	{"hist_retention_days", newTransformer("HistRetentionDays", buildHistRetentionDays)},
	{"hist_retention_runs", newTransformer("HistRetentionRuns", buildHistRetentionRuns)},
	{"max_clean_up_time_sec", newTransformer("MaxCleanUpTime", buildMaxCleanUpTime)},
	{"shell", newTransformer("Shell", buildShell)},
	{"shell_args", newTransformer("ShellArgs", buildShellArgs)},
	{"working_dir", newTransformer("WorkingDir", buildWorkingDir)},
	{"container", newTransformer("Container", buildContainer)},
	{"registry_auths", newTransformer("RegistryAuths", buildRegistryAuths)},
	{"ssh", newTransformer("SSH", buildSSH)},
	{"s3", newTransformer("S3", buildS3)},
	{"llm", newTransformer("LLM", buildLLM)},
	{"redis", newTransformer("Redis", buildRedis)},
	{"harnesses", newTransformer("Harnesses", buildHarnesses)},
	{"harness", newTransformer("Harness", buildHarness)},
	{"kubernetes", newTransformer("Kubernetes", buildKubernetes)},
	{"secrets", newTransformer("Secrets", buildSecrets)},
	{"dotenv", newTransformer("Dotenv", buildDotenv)},
	{"smtp", newTransformer("SMTP", buildSMTPConfig)},
	{"error_mail", newTransformer("ErrorMail", buildErrMailConfig)},
	{"info_mail", newTransformer("InfoMail", buildInfoMailConfig)},
	{"wait_mail", newTransformer("WaitMail", buildWaitMailConfig)},
	{"preconditions", newTransformer("Preconditions", buildPreconditions)},
	{"otel", newTransformer("OTel", buildOTel)},
}

// runTransformers executes all transformers in the pipeline
func runTransformers(ctx BuildContext, spec *dag, result *core.DAG) core.ErrorList {
	var errs core.ErrorList
	out := reflect.ValueOf(result).Elem()

	// Always run metadata transformers
	for _, t := range metadataTransformers {
		if err := t.transformer.Transform(ctx, spec, out); err != nil {
			errs = append(errs, wrapTransformError(t.name, err))
		}
	}

	// Run full transformers only when not in metadata-only mode
	if !ctx.opts.Has(BuildFlagOnlyMetadata) {
		for _, t := range fullTransformers {
			if err := t.transformer.Transform(ctx, spec, out); err != nil {
				errs = append(errs, wrapTransformError(t.name, err))
			}
		}
	}

	return errs
}

// wrapTransformError wraps an error with the transformer name if it's not already a ValidationError
func wrapTransformError(name string, err error) error {
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		return err
	}
	return core.NewValidationError(name, nil, err)
}

// build transforms the dag specification into a core.DAG.
func (d *dag) build(ctx BuildContext) (*core.DAG, error) {
	// Initialize with only Location (set from context, not spec)
	result := &core.DAG{
		Location: ctx.file,
	}
	var errs core.ErrorList
	if err := validateHistoryRetentionConfig(d); err != nil {
		errs = append(errs, err)
	}

	// Initialize shared envScope state for thread-safe env var handling.
	// Start with OS environment as base layer.
	baseScope := eval.NewEnvScope(nil, true)

	// Pre-populate with build env from options (for retry with dotenv).
	// This allows YAML to reference env vars that were loaded from .env files
	// before the rebuild.
	buildEnv := make(map[string]string, len(ctx.opts.BuildEnv))
	maps.Copy(buildEnv, ctx.opts.BuildEnv)
	if len(buildEnv) > 0 {
		baseScope = baseScope.WithEntries(buildEnv, eval.EnvSourceDotEnv)
	}

	ctx.envScope = &envScopeState{
		scope:    baseScope,
		buildEnv: buildEnv,
	}
	ctx.paramsState = &paramsState{}

	// Run the transformer pipeline
	errs = append(errs, runTransformers(ctx, d, result)...)

	buildResult := result
	if ctx.baseDAG != nil {
		merged, err := composeBuildDAGContext(ctx.baseDAG, result, d)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to compose inherited DAG context: %w", err))
		} else {
			buildResult = merged
		}
	}

	// Add deprecation warning for max_active_runs on local queues.
	// Both max_active_runs > 1 (concurrency) and max_active_runs < 0 (queue bypass) are deprecated.
	if result.Queue == "" && (result.MaxActiveRuns > 1 || result.MaxActiveRuns < 0) {
		result.BuildWarnings = append(result.BuildWarnings, fmt.Sprintf(
			"max_active_runs=%d is deprecated for local queues and will be ignored. "+
				"Use a global queue with 'queue:' field for concurrency control.",
			result.MaxActiveRuns,
		))
	}

	// Collect schedule warnings (misleading step values like */33).
	for _, sched := range result.Schedule {
		result.BuildWarnings = append(result.BuildWarnings, sched.Warnings...)
	}
	for _, sched := range result.StopSchedule {
		result.BuildWarnings = append(result.BuildWarnings, sched.Warnings...)
	}
	for _, sched := range result.RestartSchedule {
		result.BuildWarnings = append(result.BuildWarnings, sched.Warnings...)
	}

	// Build handlers and steps directly (they need access to partially built result)
	if !ctx.opts.Has(BuildFlagOnlyMetadata) {
		if handlerOn, err := buildHandlers(ctx, d, buildResult); err != nil {
			errs = append(errs, core.NewValidationError("handlers", nil, err))
		} else {
			result.HandlerOn = handlerOn
		}

		if steps, err := buildSteps(ctx, d, buildResult); err != nil {
			errs = append(errs, core.NewValidationError("steps", nil, err))
		} else {
			result.Steps = steps
		}
	}

	// Validate steps
	if err := core.ValidateSteps(result); err != nil {
		errs = append(errs, err)
	}

	// Validate workerSelector compatibility with approval steps
	if len(result.WorkerSelector) > 0 && result.HasApprovalSteps() {
		errs = append(errs, core.NewValidationError(
			"worker_selector",
			result.WorkerSelector,
			fmt.Errorf("DAG with approval steps cannot be dispatched to workers"),
		))
	}

	// Validate name
	if result.Name != "" {
		if err := core.ValidateDAGName(result.Name); err != nil {
			errs = append(errs, core.NewValidationError("name", result.Name, err))
		}
	}

	if len(ctx.envScope.buildEnv) > 0 {
		result.PresolvedBuildEnv = maps.Clone(ctx.envScope.buildEnv)
	}

	if len(errs) > 0 {
		if ctx.opts.Has(BuildFlagAllowBuildErrors) {
			result.BuildErrors = errs
		} else {
			return nil, fmt.Errorf("failed to build DAG: %w", errs)
		}
	}

	return result, nil
}

func composeBuildDAGContext(base, current *core.DAG, currentSpec *dag) (*core.DAG, error) {
	if base == nil {
		return current, nil
	}

	effective := base.Clone()
	if err := merge(effective, current); err != nil {
		return nil, err
	}
	applyHistoryRetentionOverride(effective, currentSpec.HistRetentionDays != nil, currentSpec.HistRetentionRuns != nil)

	return effective, nil
}

func applyHistoryRetentionOverride(effective *core.DAG, authoredDays, authoredRuns bool) {
	if authoredRuns {
		effective.HistRetentionDays = 0
	}
	if authoredDays {
		effective.HistRetentionRuns = 0
	}
}

// Builder functions - each returns a value instead of modifying result

func buildType(_ BuildContext, d *dag) (string, error) {
	t := strings.TrimSpace(d.Type)
	if t == "" {
		return core.TypeChain, nil
	}
	switch t {
	case core.TypeGraph, core.TypeChain:
		return t, nil
	case core.TypeAgent:
		return "", core.NewValidationError("type", t, fmt.Errorf("type 'agent' is reserved and not yet supported"))
	default:
		return "", core.NewValidationError("type", t, fmt.Errorf("invalid type: %s (must be one of: graph, chain)", t))
	}
}

// Builder functions - all return values instead of modifying result

func buildName(ctx BuildContext, d *dag) (string, error) {
	if ctx.opts.Name != "" && ctx.index == 0 {
		return strings.TrimSpace(ctx.opts.Name), nil
	}
	if name := strings.TrimSpace(d.Name); name != "" {
		return name, nil
	}
	// Fallback to filename without extension only for the main DAG (index 0)
	// Sub-DAGs in multi-DAG files must have explicit names
	if ctx.index == 0 {
		return defaultName(ctx.file), nil
	}
	return "", nil
}

func buildGroup(_ BuildContext, d *dag) (string, error) {
	return strings.TrimSpace(d.Group), nil
}

func buildDescription(_ BuildContext, d *dag) (string, error) {
	return strings.TrimSpace(d.Description), nil
}

func buildTimeout(_ BuildContext, d *dag) (time.Duration, error) {
	return time.Second * time.Duration(d.TimeoutSec), nil
}

func buildDelay(_ BuildContext, d *dag) (time.Duration, error) {
	return time.Second * time.Duration(d.DelaySec), nil
}

func buildRestartWait(_ BuildContext, d *dag) (time.Duration, error) {
	return time.Second * time.Duration(d.RestartWaitSec), nil
}

func buildLabels(_ BuildContext, d *dag) (core.Labels, error) {
	labelsValue := d.Labels
	if labelsValue.IsZero() {
		labelsValue = d.DeprecatedTags
	}
	if labelsValue.IsZero() {
		return nil, nil
	}
	var labels core.Labels
	for _, entry := range labelsValue.Entries() {
		key := strings.ToLower(strings.TrimSpace(entry.Key()))
		if key == "" {
			continue
		}
		labels = append(labels, core.Label{
			Key:   key,
			Value: strings.ToLower(strings.TrimSpace(entry.Value())),
		})
	}
	return labels, nil
}

func buildMaxActiveRuns(_ BuildContext, d *dag) (int, error) {
	if d.MaxActiveRuns != 0 {
		return d.MaxActiveRuns, nil
	}
	return 1, nil // Default
}

func buildMaxActiveSteps(_ BuildContext, d *dag) (int, error) {
	return d.MaxActiveSteps, nil
}

func buildQueue(_ BuildContext, d *dag) (string, error) {
	return strings.TrimSpace(d.Queue), nil
}

func buildDAGRetryPolicy(_ BuildContext, d *dag) (*core.DAGRetryPolicy, error) {
	if d.RetryPolicy == nil {
		return nil, nil
	}

	// Root DAG retry must be concrete when the DAG is loaded because scheduler
	// retry decisions evaluate the persisted DAG snapshot without re-resolving
	// retry expressions at runtime.
	limit, err := parseDAGRetryLimit(d.RetryPolicy.Limit)
	if err != nil {
		return nil, err
	}

	interval, intervalStr, err := parseDAGRetryInterval(d.RetryPolicy.IntervalSec)
	if err != nil {
		return nil, err
	}

	backoff, err := parseDAGRetryBackoff(d.RetryPolicy.Backoff)
	if err != nil {
		return nil, err
	}

	maxInterval, err := parseDAGRetryMaxInterval(d.RetryPolicy.MaxIntervalSec)
	if err != nil {
		return nil, err
	}

	return &core.DAGRetryPolicy{
		Limit:          limit,
		Interval:       interval,
		IntervalSecStr: intervalStr,
		Backoff:        backoff,
		MaxInterval:    maxInterval,
	}, nil
}

func buildMaxOutputSize(_ BuildContext, d *dag) (int, error) {
	return d.MaxOutputSize, nil
}

func buildSkipIfSuccessful(_ BuildContext, d *dag) (bool, error) {
	return d.SkipIfSuccessful, nil
}

func buildCatchupWindow(_ BuildContext, d *dag) (time.Duration, error) {
	if d.CatchupWindow == "" {
		return 0, nil
	}
	return core.ParseDuration(d.CatchupWindow)
}

func buildOverlapPolicy(_ BuildContext, d *dag) (core.OverlapPolicy, error) {
	return core.ParseOverlapPolicy(d.OverlapPolicy)
}

func buildLogDir(_ BuildContext, d *dag) (string, error) {
	return d.LogDir, nil
}

func buildArtifacts(_ BuildContext, d *dag) (*core.ArtifactsConfig, error) {
	autoEnable := dagReferencesRunArtifactsDir(d)

	if d.Artifacts == nil {
		if autoEnable {
			return &core.ArtifactsConfig{Enabled: true}, nil
		}
		return nil, nil
	}

	cfg := &core.ArtifactsConfig{
		Dir: d.Artifacts.Dir,
	}
	if d.Artifacts.Enabled != nil {
		cfg.Enabled = *d.Artifacts.Enabled
	} else if autoEnable {
		cfg.Enabled = true
	}
	if d.Artifacts.Enabled == nil && cfg.Dir == "" {
		if !cfg.Enabled {
			return nil, nil
		}
	}
	return cfg, nil
}

func dagReferencesRunArtifactsDir(d *dag) bool {
	return valueReferencesRunArtifactsDir(reflect.ValueOf(d))
}

func referencesArtifactsEnvVar(s string) bool {
	return dagRunArtifactsDirReferencePattern.MatchString(s)
}

func valueReferencesRunArtifactsDir(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}

	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.String:
		return referencesArtifactsEnvVar(v.String())
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if valueReferencesRunArtifactsDir(v.Index(i)) {
				return true
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if valueReferencesRunArtifactsDir(iter.Key()) || valueReferencesRunArtifactsDir(iter.Value()) {
				return true
			}
		}
	case reflect.Struct:
		for _, field := range v.Fields() {
			if valueReferencesRunArtifactsDir(field) {
				return true
			}
		}
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.Float32,
		reflect.Float64,
		reflect.Complex64,
		reflect.Complex128,
		reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Pointer,
		reflect.UnsafePointer:
		return false
	default:
		return false
	}

	return false
}

func buildLogOutput(_ BuildContext, d *dag) (core.LogOutputMode, error) {
	if d.LogOutput.IsZero() {
		// Return empty to allow inheritance from base config.
		// Default is applied in core.InitializeDefaults.
		return "", nil
	}
	return d.LogOutput.Mode(), nil
}

func buildMailOn(_ BuildContext, d *dag) (*core.MailOn, error) {
	if d.MailOn == nil {
		return nil, nil
	}
	return &core.MailOn{
		Failure: d.MailOn.Failure,
		Success: d.MailOn.Success,
		Wait:    d.MailOn.Wait,
	}, nil
}

func buildRunConfig(_ BuildContext, d *dag) (*core.RunConfig, error) {
	if d.RunConfig == nil {
		return nil, nil
	}
	return &core.RunConfig{
		DisableParamEdit: d.RunConfig.DisableParamEdit,
		DisableRunIdEdit: d.RunConfig.DisableRunIdEdit,
	}, nil
}

func buildWebhookConfig(_ BuildContext, d *dag) (*core.WebhookConfig, error) {
	if d.Webhook == nil {
		return nil, nil
	}

	headers := make([]string, 0, len(d.Webhook.ForwardHeaders))
	for i, raw := range d.Webhook.ForwardHeaders {
		header := core.NormalizeWebhookForwardHeader(raw)
		if header == "" {
			return nil, core.NewValidationError(
				fmt.Sprintf("webhook.forward_headers[%d]", i),
				raw,
				fmt.Errorf("header name cannot be empty"),
			)
		}
		if !core.IsValidWebhookHeaderToken(header) {
			return nil, core.NewValidationError(
				fmt.Sprintf("webhook.forward_headers[%d]", i),
				raw,
				fmt.Errorf("invalid HTTP header name"),
			)
		}
		if core.IsDeniedWebhookForwardHeader(header) {
			return nil, core.NewValidationError(
				fmt.Sprintf("webhook.forward_headers[%d]", i),
				raw,
				fmt.Errorf("authorization header cannot be forwarded"),
			)
		}
		headers = append(headers, header)
	}

	return &core.WebhookConfig{ForwardHeaders: headers}, nil
}

func buildHistRetentionDays(_ BuildContext, d *dag) (int, error) {
	if d.HistRetentionDays != nil {
		if *d.HistRetentionDays < 0 {
			return 0, fmt.Errorf("hist_retention_days must be >= 0")
		}
		return *d.HistRetentionDays, nil
	}
	return 0, nil
}

func buildHistRetentionRuns(_ BuildContext, d *dag) (int, error) {
	if d.HistRetentionRuns != nil {
		if *d.HistRetentionRuns <= 0 {
			return 0, fmt.Errorf("hist_retention_runs must be > 0")
		}
		return *d.HistRetentionRuns, nil
	}
	return 0, nil
}

func validateHistoryRetentionConfig(d *dag) error {
	if d.HistRetentionDays == nil || d.HistRetentionRuns == nil {
		return nil
	}
	return core.NewValidationError(
		"hist_retention_runs",
		*d.HistRetentionRuns,
		fmt.Errorf("hist_retention_days and hist_retention_runs cannot both be specified"),
	)
}

func buildMaxCleanUpTime(_ BuildContext, d *dag) (time.Duration, error) {
	if d.MaxCleanUpTimeSec != nil {
		return time.Second * time.Duration(*d.MaxCleanUpTimeSec), nil
	}
	return 0, nil
}

func buildEnvs(ctx BuildContext, d *dag) ([]string, error) {
	vars, err := loadVariablesFromEnvValue(ctx, d.Env)
	if err != nil {
		return nil, err
	}

	// Add vars to the shared envScope state so subsequent transformers can use it.
	// This replaces the old pattern of using os.Setenv which caused race conditions.
	if ctx.envScope != nil && len(vars) > 0 {
		ctx.envScope.scope = ctx.envScope.scope.WithEntries(vars, eval.EnvSourceDAGEnv)
		maps.Copy(ctx.envScope.buildEnv, vars)
	}

	var envs []string
	for k, v := range vars {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}
	return envs, nil
}

func buildSchedule(_ BuildContext, d *dag) ([]core.Schedule, error) {
	if d.Schedule.IsZero() {
		return nil, nil
	}
	return slices.Clone(d.Schedule.Starts()), nil
}

func buildStopSchedule(_ BuildContext, d *dag) ([]core.Schedule, error) {
	if d.Schedule.IsZero() {
		return nil, nil
	}
	return slices.Clone(d.Schedule.Stops()), nil
}

func buildRestartSchedule(_ BuildContext, d *dag) ([]core.Schedule, error) {
	if d.Schedule.IsZero() {
		return nil, nil
	}
	return slices.Clone(d.Schedule.Restarts()), nil
}

// paramsResult holds the result of parsing parameters
type paramsResult struct {
	Params        []string
	DefaultParams string
	ParamDefs     []core.ParamDef
	ParamSchema   json.RawMessage
	ParamsJSON    string // JSON representation of resolved params (original payload when provided as JSON)
}

func buildParams(ctx BuildContext, d *dag) ([]string, error) {
	result, err := parseParamsInternal(ctx, d)
	if err != nil {
		return nil, err
	}
	// Add resolved params to envScope so env: can reference ${param_name}
	if ctx.envScope != nil && len(result.Params) > 0 {
		paramVars := make(map[string]string, len(result.Params))
		for _, p := range result.Params {
			if k, v, ok := strings.Cut(p, "="); ok {
				paramVars[k] = v
			}
		}
		ctx.envScope.scope = ctx.envScope.scope.WithEntries(paramVars, eval.EnvSourceParam)
	}
	return result.Params, nil
}

func buildDefaultParams(ctx BuildContext, d *dag) (string, error) {
	result, err := parseParamsInternal(ctx, d)
	if err != nil {
		return "", err
	}
	return result.DefaultParams, nil
}

func buildParamDefs(ctx BuildContext, d *dag) ([]core.ParamDef, error) {
	result, err := parseParamsInternal(ctx, d)
	if err != nil {
		return nil, err
	}
	return result.ParamDefs, nil
}

func buildParamsJSON(ctx BuildContext, d *dag) (string, error) {
	result, err := parseParamsInternal(ctx, d)
	if err != nil {
		return "", err
	}
	return result.ParamsJSON, nil
}

func buildParamSchema(ctx BuildContext, d *dag) (json.RawMessage, error) {
	result, err := parseParamsInternal(ctx, d)
	if err != nil {
		return nil, err
	}
	return cloneParamSchema(result.ParamSchema), nil
}

// detectJSONParams checks if the input string is valid JSON and returns it if so.
// Returns empty string if the input is not JSON.
func detectJSONParams(input string) string {
	input = strings.TrimSpace(input)
	if (strings.HasPrefix(input, "{") && strings.HasSuffix(input, "}")) ||
		(strings.HasPrefix(input, "[") && strings.HasSuffix(input, "]")) {
		var js json.RawMessage
		if json.Unmarshal([]byte(input), &js) == nil {
			return input
		}
	}
	return ""
}

// buildResolvedParamsJSON returns a JSON representation of the resolved params.
// If the raw input was JSON, the original payload is returned to preserve structure.
func buildResolvedParamsJSON(paramPairs []paramPair, rawInput string) (string, error) {
	if rawJSON := detectJSONParams(rawInput); rawJSON != "" {
		return rawJSON, nil
	}
	return marshalParamPairs(paramPairs)
}

func parseDAGRetryInterval(v any) (time.Duration, string, error) {
	if v == nil {
		return 60 * time.Second, "", nil
	}
	interval, intervalStr, err := parseConcreteDAGRetryInt("retry_policy.interval_sec", v, false)
	if err != nil {
		return 0, "", err
	}
	return time.Second * time.Duration(interval), intervalStr, nil
}

func parseDAGRetryBackoff(v any) (float64, error) {
	backoff, err := parseBackoffValue(v, "retry_policy.backoff")
	if err != nil {
		return 0, core.NewValidationError("retry_policy.backoff", v, err)
	}
	return backoff, nil
}

func parseDAGRetryMaxInterval(v any) (time.Duration, error) {
	if v == nil {
		return time.Hour, nil
	}
	seconds, _, err := parseConcreteDAGRetryInt("retry_policy.max_interval_sec", v, false)
	if err != nil {
		return 0, err
	}
	return time.Second * time.Duration(seconds), nil
}

func parseDAGRetryLimit(v any) (int, error) {
	if v == nil {
		return 0, core.NewValidationError("retry_policy.limit", nil, fmt.Errorf("limit is required when retry_policy is specified"))
	}
	limit, _, err := parseConcreteDAGRetryInt("retry_policy.limit", v, true)
	if err != nil {
		return 0, err
	}
	return limit, nil
}

func parseConcreteDAGRetryInt(fieldName string, val any, allowZero bool) (int, string, error) {
	invalidPositiveValue := func(value any) (int, string, error) {
		operator := "> 0"
		if allowZero {
			operator = ">= 0"
		}
		return 0, "", core.NewValidationError(fieldName, value, fmt.Errorf("%s must be %s", retryFieldLabel(fieldName), operator))
	}

	switch v := val.(type) {
	case int:
		if v < 0 || (!allowZero && v == 0) {
			return invalidPositiveValue(v)
		}
		return v, "", nil
	case int64:
		if v < 0 || (!allowZero && v == 0) {
			return invalidPositiveValue(v)
		}
		if v > math.MaxInt {
			return 0, "", core.NewValidationError(fieldName, v, fmt.Errorf("value %d exceeds maximum int", v))
		}
		return int(v), "", nil
	case uint64:
		if !allowZero && v == 0 {
			return invalidPositiveValue(v)
		}
		if v > math.MaxInt {
			return 0, "", core.NewValidationError(fieldName, v, fmt.Errorf("value %d exceeds maximum int", v))
		}
		return int(v), "", nil
	case string:
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return 0, "", core.NewValidationError(fieldName, v, fmt.Errorf("%s must be an integer or numeric string", retryFieldLabel(fieldName)))
		}
		if parsed < 0 || (!allowZero && parsed == 0) {
			return invalidPositiveValue(v)
		}
		return parsed, v, nil
	default:
		return 0, "", core.NewValidationError(fieldName, val, fmt.Errorf("invalid type: %T", val))
	}
}

func retryFieldLabel(fieldName string) string {
	if idx := strings.LastIndex(fieldName, "."); idx >= 0 {
		return fieldName[idx+1:]
	}
	return fieldName
}

// marshalParamPairs converts the final param pairs into a JSON object string.
// Returns an empty string when there are no params to serialize.
func marshalParamPairs(paramPairs []paramPair) (string, error) {
	if len(paramPairs) == 0 {
		return "", nil
	}

	payload := make(map[string]string, len(paramPairs))
	for _, pair := range paramPairs {
		if pair.Name == "" {
			continue
		}
		payload[pair.Name] = pair.Value
	}

	if len(payload) == 0 {
		return "", nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal params to JSON: %w", err)
	}
	return string(data), nil
}

func parseParamsInternal(ctx BuildContext, d *dag) (*paramsResult, error) {
	if ctx.paramsState != nil && ctx.paramsState.cached {
		return ctx.paramsState.result, ctx.paramsState.err
	}

	result, err := buildDAGParamsResult(ctx, d)
	if ctx.paramsState != nil {
		ctx.paramsState.cached = true
		ctx.paramsState.result = result
		ctx.paramsState.err = err
	}
	return result, err
}

// workerSelectorTransformer is a custom transformer that sets both WorkerSelector and ForceLocal fields.
type workerSelectorTransformer struct{}

func (t *workerSelectorTransformer) Transform(ctx BuildContext, in *dag, out reflect.Value) error {
	ws, forceLocal, err := buildWorkerSelector(ctx, in)
	if err != nil {
		return err
	}

	if ws != nil {
		wsField := out.FieldByName("WorkerSelector")
		if wsField.IsValid() && wsField.CanSet() {
			wsField.Set(reflect.ValueOf(ws))
		}
	}

	if forceLocal {
		flField := out.FieldByName("ForceLocal")
		if flField.IsValid() && flField.CanSet() {
			flField.SetBool(true)
		}
	}

	return nil
}

func buildWorkerSelector(_ BuildContext, d *dag) (map[string]string, bool, error) {
	if d.WorkerSelector == nil {
		return nil, false, nil
	}

	switch v := d.WorkerSelector.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.EqualFold(trimmed, "local") {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("unsupported worker_selector string value %q; the only allowed string value is \"local\"", trimmed)

	case map[string]string:
		if len(v) == 0 {
			return nil, false, nil
		}
		ret := make(map[string]string)
		for key, val := range v {
			ret[strings.TrimSpace(key)] = strings.TrimSpace(val)
		}
		return ret, false, nil

	case map[string]any:
		if len(v) == 0 {
			return nil, false, nil
		}
		ret := make(map[string]string)
		for key, val := range v {
			ret[strings.TrimSpace(key)] = strings.TrimSpace(fmt.Sprint(val))
		}
		return ret, false, nil

	case map[any]any:
		if len(v) == 0 {
			return nil, false, nil
		}
		ret := make(map[string]string)
		for key, val := range v {
			strKey, ok := key.(string)
			if !ok {
				return nil, false, fmt.Errorf("worker_selector keys must be strings, got %T", key)
			}
			ret[strings.TrimSpace(strKey)] = strings.TrimSpace(fmt.Sprint(val))
		}
		return ret, false, nil

	default:
		return nil, false, fmt.Errorf("worker_selector must be a map or \"local\", got %T", d.WorkerSelector)
	}
}

// shellResult holds both shell and args for internal use
type shellResult struct {
	Shell string
	Args  []string
}

func parseShellInternal(_ BuildContext, d *dag) (*shellResult, error) {
	if d.Shell.IsZero() {
		return &shellResult{Shell: cmdutil.GetShellCommand(""), Args: nil}, nil
	}

	// For array form, Command() returns first element, Arguments() returns rest
	if d.Shell.IsArray() {
		shell := d.Shell.Command()
		// Empty array should fall back to default shell
		if shell == "" {
			return &shellResult{Shell: cmdutil.GetShellCommand(""), Args: nil}, nil
		}
		// Shell expansion is deferred to runtime - see runtime/env.go Shell()
		args := d.Shell.Arguments()
		return &shellResult{Shell: shell, Args: args}, nil
	}

	// For string form, need to split command and args
	command := d.Shell.Command()
	if command == "" {
		return &shellResult{Shell: cmdutil.GetShellCommand(""), Args: nil}, nil
	}

	// Shell expansion is deferred to runtime - see runtime/env.go Shell()
	shell, args, err := cmdutil.SplitCommand(command)
	if err != nil {
		return nil, core.NewValidationError("shell", d.Shell.Value(), fmt.Errorf("failed to parse shell command: %w", err))
	}
	return &shellResult{Shell: strings.TrimSpace(shell), Args: args}, nil
}

func buildShell(ctx BuildContext, d *dag) (string, error) {
	result, err := parseShellInternal(ctx, d)
	if err != nil {
		return "", err
	}
	return result.Shell, nil
}

func buildShellArgs(ctx BuildContext, d *dag) ([]string, error) {
	result, err := parseShellInternal(ctx, d)
	if err != nil {
		return nil, err
	}
	return result.Args, nil
}

func buildWorkingDir(ctx BuildContext, d *dag) (string, error) {
	if d.WorkingDir != "" {
		return resolveWorkingDirPath(d.WorkingDir, ctx.file)
	}
	if ctx.opts.DefaultWorkingDir != "" {
		return ctx.opts.DefaultWorkingDir, nil
	}
	// Return empty to allow inheritance from base config.
	// Default is applied post-merge in loadDAG.
	return "", nil
}

// resolveWorkingDirPath resolves the working directory path at build time.
// Absolute paths, home dir (~), and variable ($) paths are stored as-is for runtime expansion.
// Relative paths are resolved against the DAG file location.
func resolveWorkingDirPath(wd, dagFile string) (string, error) {
	if filepath.IsAbs(wd) || strings.HasPrefix(wd, "~") || strings.HasPrefix(wd, "$") {
		return wd, nil
	}
	if dagFile != "" {
		return filepath.Join(filepath.Dir(dagFile), wd), nil
	}
	return wd, nil
}

// getDefaultWorkingDir returns the current working directory or user home as fallback.
func getDefaultWorkingDir() (string, error) {
	if dir, _ := os.Getwd(); dir != "" {
		return dir, nil
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}
	return dir, nil
}

func buildContainer(ctx BuildContext, d *dag) (*core.Container, error) {
	return buildContainerField(ctx, d.Container)
}

// buildContainerField handles both string and object forms of container field.
// String form: "container-name" -> exec into existing container
// Object form: {image: "...", ...} or {exec: "...", ...} -> create new or exec into existing
func buildContainerField(ctx BuildContext, raw any) (*core.Container, error) {
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case string:
		// String mode: exec into existing container with defaults
		name := strings.TrimSpace(v)
		if name == "" {
			return nil, core.NewValidationError("container", nil,
				fmt.Errorf("container name cannot be empty"))
		}
		return &core.Container{
			Exec: name,
		}, nil

	case map[string]any:
		// Object mode: decode and validate
		var c container
		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			Result:           &c,
			ErrorUnused:      true,
			WeaklyTypedInput: true,
			TagName:          "yaml",
		})
		if err != nil {
			return nil, core.NewValidationError("container", nil,
				fmt.Errorf("failed to create decoder: %w", err))
		}
		if err := decoder.Decode(v); err != nil {
			return nil, core.NewValidationError("container", nil,
				fmt.Errorf("failed to decode container: %w", withSnakeCaseKeyHint(err)))
		}
		return buildContainerFromSpec(ctx, &c)

	case *container:
		// Already decoded container struct (for backward compatibility)
		if v == nil {
			return nil, nil
		}
		return buildContainerFromSpec(ctx, v)

	default:
		return nil, core.NewValidationError("container", nil,
			fmt.Errorf("container must be a string or object, got %T", raw))
	}
}

// buildContainerFromSpec is a shared function that builds a core.Container from a container spec.
// It is used by both DAG-level and step-level container configuration.
func buildContainerFromSpec(_ BuildContext, c *container) (*core.Container, error) {
	// Validate mutual exclusivity
	if c.Exec != "" && c.Image != "" {
		return nil, core.NewValidationError("container", nil,
			fmt.Errorf("'exec' and 'image' are mutually exclusive"))
	}

	// Require one of exec or image
	if c.Exec == "" && c.Image == "" {
		return nil, core.NewValidationError("container", nil,
			fmt.Errorf("either 'exec' or 'image' must be specified"))
	}

	// Handle exec mode
	if c.Exec != "" {
		// Validate no incompatible fields in exec mode
		var invalidFields []string
		if c.Name != "" {
			invalidFields = append(invalidFields, "name")
		}
		if c.PullPolicy != nil {
			invalidFields = append(invalidFields, "pull_policy")
		}
		if len(c.Volumes) > 0 {
			invalidFields = append(invalidFields, "volumes")
		}
		if len(c.Ports) > 0 {
			invalidFields = append(invalidFields, "ports")
		}
		if c.Network != "" {
			invalidFields = append(invalidFields, "network")
		}
		if c.Platform != "" {
			invalidFields = append(invalidFields, "platform")
		}
		if c.Startup != "" {
			invalidFields = append(invalidFields, "startup")
		}
		if len(c.Command) > 0 {
			invalidFields = append(invalidFields, "command")
		}
		if c.WaitFor != "" {
			invalidFields = append(invalidFields, "wait_for")
		}
		if c.LogPattern != "" {
			invalidFields = append(invalidFields, "log_pattern")
		}
		if c.RestartPolicy != "" {
			invalidFields = append(invalidFields, "restart_policy")
		}
		if c.KeepContainer {
			invalidFields = append(invalidFields, "keep_container")
		}
		if c.Healthcheck != nil {
			invalidFields = append(invalidFields, "healthcheck")
		}

		if len(invalidFields) > 0 {
			return nil, core.NewValidationError("container", nil,
				fmt.Errorf("fields %v cannot be used with 'exec'", invalidFields))
		}

		// Collect raw env pairs without evaluation — evaluation is deferred
		// to runtime so that DAG-level env, params, and step outputs are in scope.
		envs, err := collectRawPairs(c.Env)
		if err != nil {
			return nil, core.NewValidationError("container.env", c.Env, err)
		}

		// Build exec-mode container
		return &core.Container{
			Exec:       strings.TrimSpace(c.Exec),
			User:       c.User,
			WorkingDir: c.WorkingDir,
			Env:        envs,
			Shell:      c.Shell,
		}, nil
	}

	// Handle image mode (existing behavior)
	pullPolicy, err := core.ParsePullPolicy(c.PullPolicy)
	if err != nil {
		return nil, core.NewValidationError("container.pull_policy", c.PullPolicy, err)
	}

	// Collect raw env pairs without evaluation — evaluation is deferred
	// to runtime so that DAG-level env, params, and step outputs are in scope.
	envs, err := collectRawPairs(c.Env)
	if err != nil {
		return nil, core.NewValidationError("container.env", c.Env, err)
	}

	// Parse healthcheck if provided
	var hc *core.Healthcheck
	if c.Healthcheck != nil {
		var err error
		hc, err = parseHealthcheck(c.Healthcheck)
		if err != nil {
			return nil, core.NewValidationError("container.healthcheck", c.Healthcheck, err)
		}
	}

	return &core.Container{
		Name:          strings.TrimSpace(c.Name),
		Image:         c.Image,
		PullPolicy:    pullPolicy,
		Env:           envs,
		Volumes:       c.Volumes,
		User:          c.User,
		WorkingDir:    c.WorkingDir,
		Platform:      c.Platform,
		Ports:         c.Ports,
		Network:       c.Network,
		KeepContainer: c.KeepContainer,
		Startup:       core.ContainerStartup(strings.ToLower(strings.TrimSpace(c.Startup))),
		Command:       c.Command,
		WaitFor:       core.ContainerWaitFor(strings.ToLower(strings.TrimSpace(c.WaitFor))),
		LogPattern:    c.LogPattern,
		RestartPolicy: strings.TrimSpace(c.RestartPolicy),
		Healthcheck:   hc,
		Shell:         c.Shell,
	}, nil
}

// parseHealthcheck converts a spec healthcheck to a core.Healthcheck with validation.
func parseHealthcheck(h *healthcheck) (*core.Healthcheck, error) {
	if h == nil {
		return nil, nil
	}

	// Validate test field
	if len(h.Test) == 0 {
		return nil, fmt.Errorf("test is required")
	}

	// First element must be a valid command type
	validPrefixes := map[string]bool{
		"NONE":      true,
		"CMD":       true,
		"CMD-SHELL": true,
	}
	if !validPrefixes[h.Test[0]] {
		return nil, fmt.Errorf("test must start with NONE, CMD, or CMD-SHELL, got %q", h.Test[0])
	}

	// NONE should be the only element
	if h.Test[0] == "NONE" && len(h.Test) > 1 {
		return nil, fmt.Errorf("NONE healthcheck should not have additional arguments")
	}

	// CMD and CMD-SHELL need at least one more element (the command)
	if (h.Test[0] == "CMD" || h.Test[0] == "CMD-SHELL") && len(h.Test) < 2 {
		return nil, fmt.Errorf("%s healthcheck requires a command", h.Test[0])
	}

	// Validate retries
	if h.Retries < 0 {
		return nil, fmt.Errorf("retries must be non-negative, got %d", h.Retries)
	}

	hc := &core.Healthcheck{
		Test:    h.Test,
		Retries: h.Retries,
	}

	// Parse duration strings
	if h.Interval != "" {
		d, err := time.ParseDuration(h.Interval)
		if err != nil {
			return nil, fmt.Errorf("invalid interval %q: %w", h.Interval, err)
		}
		hc.Interval = d
	}

	if h.Timeout != "" {
		d, err := time.ParseDuration(h.Timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q: %w", h.Timeout, err)
		}
		hc.Timeout = d
	}

	if h.StartPeriod != "" {
		d, err := time.ParseDuration(h.StartPeriod)
		if err != nil {
			return nil, fmt.Errorf("invalid start_period %q: %w", h.StartPeriod, err)
		}
		hc.StartPeriod = d
	}

	return hc, nil
}

func buildRegistryAuths(_ BuildContext, d *dag) (map[string]*core.AuthConfig, error) {
	if d.RegistryAuths == nil {
		return nil, nil
	}

	// No expansion at build time - credentials are evaluated at runtime.
	// See runtime/agent/agent.go where RegistryAuths are evaluated before use.

	// parseAuthConfig parses auth config from a map with string keys.
	parseAuthConfig := func(m map[string]any) *core.AuthConfig {
		cfg := &core.AuthConfig{}
		if v, ok := m["username"].(string); ok {
			cfg.Username = v
		}
		if v, ok := m["password"].(string); ok {
			cfg.Password = v
		}
		if v, ok := m["auth"].(string); ok {
			cfg.Auth = v
		}
		return cfg
	}

	// parseAuthData parses auth data which can be a string or a map.
	parseAuthData := func(authData any) *core.AuthConfig {
		switch auth := authData.(type) {
		case string:
			return &core.AuthConfig{Auth: auth}
		case map[string]any:
			return parseAuthConfig(auth)
		case map[any]any:
			// Convert map[any]any to map[string]any
			m := make(map[string]any)
			for k, v := range auth {
				if ks, ok := k.(string); ok {
					m[ks] = v
				}
			}
			return parseAuthConfig(m)
		default:
			return &core.AuthConfig{}
		}
	}

	registryAuths := make(map[string]*core.AuthConfig)

	switch v := d.RegistryAuths.(type) {
	case string:
		registryAuths["_json"] = &core.AuthConfig{Auth: v}

	case map[string]any:
		for registry, authData := range v {
			registryAuths[registry] = parseAuthData(authData)
		}

	case map[any]any:
		for registryKey, authData := range v {
			if registry, ok := registryKey.(string); ok {
				registryAuths[registry] = parseAuthData(authData)
			}
		}

	default:
		return nil, core.NewValidationError("registry_auths", d.RegistryAuths, fmt.Errorf("invalid type: %T", d.RegistryAuths))
	}

	return registryAuths, nil
}

func buildSSH(_ BuildContext, d *dag) (*core.SSHConfig, error) {
	if d.SSH == nil {
		return nil, nil
	}

	shell, shellArgs, err := parseSSHShell(d.SSH.Shell)
	if err != nil {
		return nil, err
	}

	return &core.SSHConfig{
		User:          d.SSH.User,
		Host:          d.SSH.Host,
		Port:          defaultPort(d.SSH.Port.String(), "22"),
		Key:           d.SSH.Key,
		Password:      d.SSH.Password,
		StrictHostKey: d.SSH.StrictHostKey == nil || *d.SSH.StrictHostKey,
		KnownHostFile: d.SSH.KnownHostFile,
		Shell:         shell,
		ShellArgs:     shellArgs,
		Timeout:       d.SSH.Timeout,
		Bastion:       buildBastionConfig(d.SSH.Bastion),
	}, nil
}

// parseSSHShell parses shell configuration from ShellValue.
func parseSSHShell(shellVal types.ShellValue) (string, []string, error) {
	if shellVal.IsZero() {
		return "", nil, nil
	}

	command := strings.TrimSpace(shellVal.Command())
	if command == "" {
		return "", nil, nil
	}

	if shellVal.IsArray() {
		return command, shellVal.Arguments(), nil
	}

	parsed, args, err := cmdutil.SplitCommand(command)
	if err != nil {
		return "", nil, core.NewValidationError("ssh.shell", shellVal.Value(), fmt.Errorf("failed to parse shell command: %w", err))
	}
	return strings.TrimSpace(parsed), args, nil
}

// buildBastionConfig builds bastion configuration from spec.
func buildBastionConfig(bastion *bastion) *core.BastionConfig {
	if bastion == nil {
		return nil
	}
	return &core.BastionConfig{
		Host:     bastion.Host,
		Port:     defaultPort(bastion.Port.String(), "22"),
		User:     bastion.User,
		Key:      bastion.Key,
		Password: bastion.Password,
	}
}

// defaultPort returns the port if non-empty, otherwise returns the default value.
func defaultPort(port, defaultVal string) string {
	if port == "" {
		return defaultVal
	}
	return port
}

func buildS3(_ BuildContext, d *dag) (*core.S3Config, error) {
	if d.S3 == nil {
		return nil, nil
	}

	return &core.S3Config{
		Region:          d.S3.Region,
		Endpoint:        d.S3.Endpoint,
		AccessKeyID:     d.S3.AccessKeyID,
		SecretAccessKey: d.S3.SecretAccessKey,
		SessionToken:    d.S3.SessionToken,
		Profile:         d.S3.Profile,
		ForcePathStyle:  d.S3.ForcePathStyle,
		DisableSSL:      d.S3.DisableSSL,
		Bucket:          d.S3.Bucket,
	}, nil
}

func buildLLM(_ BuildContext, d *dag) (*core.LLMConfig, error) {
	if d.LLM == nil {
		return nil, nil
	}

	cfg := d.LLM

	// Validate provider if specified (optional at DAG level)
	if cfg.Provider != "" {
		validProviders := map[string]bool{
			"openai": true, "anthropic": true, "gemini": true,
			"openrouter": true, "local": true,
			// Aliases for local provider
			"ollama": true, "vllm": true, "llama": true,
		}
		if !validProviders[cfg.Provider] {
			return nil, core.NewValidationError("llm.provider", cfg.Provider,
				fmt.Errorf("invalid provider: must be one of openai, anthropic, gemini, openrouter, local (or aliases: ollama, vllm, llama)"))
		}
	}

	// Get model string or entries (optional at DAG level)
	var modelString string
	var models []core.ModelEntry

	if !cfg.Model.IsZero() {
		if cfg.Model.IsArray() {
			var err error
			models, err = convertModelEntries(cfg.Model.Entries())
			if err != nil {
				return nil, err
			}
		} else {
			modelString = cfg.Model.String()
		}
	}

	// Validate temperature range if specified
	if cfg.Temperature != nil {
		if *cfg.Temperature < 0.0 || *cfg.Temperature > 2.0 {
			return nil, core.NewValidationError("llm.temperature", *cfg.Temperature,
				fmt.Errorf("temperature must be between 0.0 and 2.0"))
		}
	}

	// Validate top_p range if specified
	if cfg.TopP != nil {
		if *cfg.TopP < 0.0 || *cfg.TopP > 1.0 {
			return nil, core.NewValidationError("llm.top_p", *cfg.TopP,
				fmt.Errorf("top_p must be between 0.0 and 1.0"))
		}
	}

	// Validate max_tokens if specified
	if cfg.MaxTokens != nil {
		if *cfg.MaxTokens < 1 {
			return nil, core.NewValidationError("llm.max_tokens", *cfg.MaxTokens,
				fmt.Errorf("max_tokens must be at least 1"))
		}
	}

	thinking, err := buildThinkingConfig(cfg.Thinking)
	if err != nil {
		return nil, err
	}

	return &core.LLMConfig{
		Provider:    cfg.Provider,
		Model:       modelString,
		Models:      models,
		System:      cfg.System,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		TopP:        cfg.TopP,
		BaseURL:     cfg.BaseURL,
		APIKeyName:  cfg.APIKeyName,
		Stream:      cfg.Stream,
		Thinking:    thinking,
	}, nil
}

func buildRedis(_ BuildContext, d *dag) (*core.RedisConfig, error) {
	if d.Redis == nil {
		return nil, nil
	}

	return &core.RedisConfig{
		URL:            d.Redis.URL,
		Host:           d.Redis.Host,
		Port:           d.Redis.Port,
		Password:       d.Redis.Password,
		Username:       d.Redis.Username,
		DB:             d.Redis.DB,
		TLS:            d.Redis.TLS,
		TLSSkipVerify:  d.Redis.TLSSkipVerify,
		Mode:           d.Redis.Mode,
		SentinelMaster: d.Redis.SentinelMaster,
		SentinelAddrs:  d.Redis.SentinelAddrs,
		ClusterAddrs:   d.Redis.ClusterAddrs,
		MaxRetries:     d.Redis.MaxRetries,
	}, nil
}

func buildHarnesses(_ BuildContext, d *dag) (core.HarnessDefinitions, error) {
	defs, err := parseHarnessDefinitions(d.Harnesses)
	if err != nil {
		return nil, err
	}
	return defs, nil
}

func parseHarnessDefinitions(raw map[string]any) (core.HarnessDefinitions, error) {
	if raw == nil {
		return nil, nil
	}

	defs := make(core.HarnessDefinitions, len(raw))
	for name, value := range raw {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return nil, core.NewValidationError("harnesses", name, fmt.Errorf("harness name is required"))
		}
		if core.IsBuiltinHarnessProvider(trimmedName) {
			return nil, core.NewValidationError(
				fmt.Sprintf("harnesses.%s", trimmedName),
				value,
				fmt.Errorf("custom harness name %q conflicts with built-in provider", trimmedName),
			)
		}
		if value == nil {
			defs[trimmedName] = nil
			continue
		}

		specMap, ok := value.(map[string]any)
		if !ok {
			return nil, core.NewValidationError(
				fmt.Sprintf("harnesses.%s", trimmedName),
				value,
				fmt.Errorf("harness definition must be an object or null"),
			)
		}

		def, err := parseHarnessDefinition(trimmedName, specMap)
		if err != nil {
			return nil, err
		}
		defs[trimmedName] = def
	}

	return defs, nil
}

func parseHarnessDefinition(name string, raw map[string]any) (*core.HarnessDefinition, error) {
	def := &core.HarnessDefinition{
		PromptMode:     core.HarnessPromptModeArg,
		PromptPosition: core.HarnessPromptPositionBeforeFlags,
		FlagStyle:      core.HarnessFlagStyleGNULong,
	}

	for key, value := range raw {
		switch key {
		case "binary":
			binary, ok := value.(string)
			if !ok {
				return nil, core.NewValidationError(
					fmt.Sprintf("harnesses.%s.binary", name),
					value,
					fmt.Errorf("binary must be a string"),
				)
			}
			def.Binary = strings.TrimSpace(binary)
		case "prefix_args":
			prefixArgs, err := harnessStringSlice(value)
			if err != nil {
				return nil, core.NewValidationError(fmt.Sprintf("harnesses.%s.prefix_args", name), value, err)
			}
			def.PrefixArgs = prefixArgs
		case "prompt_mode":
			mode, ok := value.(string)
			if !ok {
				return nil, core.NewValidationError(
					fmt.Sprintf("harnesses.%s.prompt_mode", name),
					value,
					fmt.Errorf("prompt_mode must be a string"),
				)
			}
			def.PromptMode = core.HarnessPromptMode(strings.TrimSpace(mode))
		case "prompt_flag":
			promptFlag, ok := value.(string)
			if !ok {
				return nil, core.NewValidationError(
					fmt.Sprintf("harnesses.%s.prompt_flag", name),
					value,
					fmt.Errorf("prompt_flag must be a string"),
				)
			}
			def.PromptFlag = strings.TrimSpace(promptFlag)
		case "prompt_position":
			position, ok := value.(string)
			if !ok {
				return nil, core.NewValidationError(
					fmt.Sprintf("harnesses.%s.prompt_position", name),
					value,
					fmt.Errorf("prompt_position must be a string"),
				)
			}
			def.PromptPosition = core.HarnessPromptPosition(strings.TrimSpace(position))
		case "flag_style":
			flagStyle, ok := value.(string)
			if !ok {
				return nil, core.NewValidationError(
					fmt.Sprintf("harnesses.%s.flag_style", name),
					value,
					fmt.Errorf("flag_style must be a string"),
				)
			}
			def.FlagStyle = core.HarnessFlagStyle(strings.TrimSpace(flagStyle))
		case "option_flags":
			optionFlags, err := harnessStringMap(value)
			if err != nil {
				return nil, core.NewValidationError(fmt.Sprintf("harnesses.%s.option_flags", name), value, err)
			}
			def.OptionFlags = optionFlags
		default:
			return nil, core.NewValidationError(
				fmt.Sprintf("harnesses.%s.%s", name, key),
				value,
				fmt.Errorf("unknown harness definition key %q", key),
			)
		}
	}

	if def.Binary == "" {
		return nil, core.NewValidationError(
			fmt.Sprintf("harnesses.%s.binary", name),
			raw["binary"],
			fmt.Errorf("binary is required"),
		)
	}

	switch def.PromptMode {
	case core.HarnessPromptModeArg, core.HarnessPromptModeFlag, core.HarnessPromptModeStdin:
	default:
		return nil, core.NewValidationError(
			fmt.Sprintf("harnesses.%s.prompt_mode", name),
			def.PromptMode,
			fmt.Errorf("prompt_mode must be one of: arg, flag, stdin"),
		)
	}

	switch def.PromptPosition {
	case core.HarnessPromptPositionBeforeFlags, core.HarnessPromptPositionAfterFlags:
	default:
		return nil, core.NewValidationError(
			fmt.Sprintf("harnesses.%s.prompt_position", name),
			def.PromptPosition,
			fmt.Errorf("prompt_position must be one of: before_flags, after_flags"),
		)
	}

	switch def.FlagStyle {
	case core.HarnessFlagStyleGNULong, core.HarnessFlagStyleSingleDash:
	default:
		return nil, core.NewValidationError(
			fmt.Sprintf("harnesses.%s.flag_style", name),
			def.FlagStyle,
			fmt.Errorf("flag_style must be one of: gnu_long, single_dash"),
		)
	}

	if def.PromptMode == core.HarnessPromptModeFlag && def.PromptFlag == "" {
		return nil, core.NewValidationError(
			fmt.Sprintf("harnesses.%s.prompt_flag", name),
			raw["prompt_flag"],
			fmt.Errorf("prompt_flag is required when prompt_mode is flag"),
		)
	}

	if def.PromptMode != core.HarnessPromptModeFlag && def.PromptFlag != "" {
		return nil, core.NewValidationError(
			fmt.Sprintf("harnesses.%s.prompt_flag", name),
			def.PromptFlag,
			fmt.Errorf("prompt_flag is only valid when prompt_mode is flag"),
		)
	}

	return def, nil
}

func harnessStringSlice(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		values := make([]string, len(v))
		for i := range v {
			s, ok := v[i].(string)
			if !ok {
				return nil, fmt.Errorf("value at index %d must be a string", i)
			}
			values[i] = strings.TrimSpace(s)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("must be an array of strings")
	}
}

func harnessStringMap(raw any) (map[string]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case map[string]string:
		return maps.Clone(v), nil
	case map[string]any:
		values := make(map[string]string, len(v))
		for key, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("value for key %q must be a string", key)
			}
			values[key] = strings.TrimSpace(s)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("must be an object with string values")
	}
}

func buildHarness(ctx BuildContext, d *dag) (*core.HarnessConfig, error) {
	if d.Harness == nil {
		return nil, nil
	}

	config := cloneHarnessSpecMap(d.Harness)
	fallbacks, err := extractHarnessFallback(config)
	if err != nil {
		return nil, core.NewValidationError("harness", d.Harness, err)
	}

	if err := core.ValidateExecutorConfig("harness", config); err != nil {
		return nil, core.NewValidationError("harness", d.Harness, err)
	}
	defs, err := parseHarnessDefinitions(d.Harnesses)
	if err != nil {
		return nil, err
	}
	if ctx.baseDAG != nil && ctx.baseDAG.Harnesses != nil {
		effective := ctx.baseDAG.Clone()
		if defs != nil {
			if err := merge(effective, &core.DAG{Harnesses: defs}); err != nil {
				return nil, err
			}
		}
		defs = effective.Harnesses
	}
	if err := validateHarnessProviderConfig(defs, config); err != nil {
		return nil, core.NewValidationError("harness", d.Harness, err)
	}
	for i := range fallbacks {
		if err := core.ValidateExecutorConfig("harness", fallbacks[i]); err != nil {
			return nil, core.NewValidationError(fmt.Sprintf("harness.fallback[%d]", i), fallbacks[i], err)
		}
		if err := validateHarnessProviderConfig(defs, fallbacks[i]); err != nil {
			return nil, core.NewValidationError(fmt.Sprintf("harness.fallback[%d]", i), fallbacks[i], err)
		}
	}

	return &core.HarnessConfig{
		Config:   config,
		Fallback: fallbacks,
	}, nil
}

func buildSecrets(_ BuildContext, d *dag) ([]core.SecretRef, error) {
	if len(d.Secrets) == 0 {
		return nil, nil
	}
	return parseSecretRefs(d.Secrets)
}

func extractHarnessFallback(config map[string]any) ([]map[string]any, error) {
	raw, ok := config["fallback"]
	if !ok {
		return nil, nil
	}
	delete(config, "fallback")

	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return cloneHarnessSpecFallback(v), nil
	case []any:
		fallbacks := make([]map[string]any, len(v))
		for i := range v {
			item, ok := v[i].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("harness: fallback[%d] must be an object", i)
			}
			fallbacks[i] = cloneHarnessSpecMap(item)
		}
		return fallbacks, nil
	default:
		return nil, fmt.Errorf("harness: fallback must be an array of objects")
	}
}

func validateHarnessProviderConfig(defs core.HarnessDefinitions, cfg map[string]any) error {
	if cfg == nil {
		return fmt.Errorf("harness: config is required")
	}
	if _, exists := cfg["binary"]; exists {
		return fmt.Errorf("harness: config.binary is not supported; define a named harness under top-level harnesses and reference it via config.provider")
	}
	if _, exists := cfg["prompt_args"]; exists {
		return fmt.Errorf("harness: config.prompt_args is not supported; define a named harness under top-level harnesses and reference it via config.provider")
	}

	providerName, _ := cfg["provider"].(string)
	if strings.TrimSpace(providerName) == "" {
		return fmt.Errorf("harness: config.provider is required")
	}
	if strings.Contains(providerName, "${") {
		return nil
	}
	if core.IsBuiltinHarnessProvider(providerName) {
		return nil
	}
	if defs != nil {
		if def, ok := defs[providerName]; ok && def != nil {
			return nil
		}
	}
	return fmt.Errorf("harness: unknown provider %q", providerName)
}

func cloneHarnessSpecMap(cfg map[string]any) map[string]any {
	if cfg == nil {
		return nil
	}

	cloned := make(map[string]any, len(cfg))
	for key, value := range cfg {
		cloned[key] = cloneHarnessSpecValue(value)
	}
	return cloned
}

func cloneHarnessSpecFallback(cfgs []map[string]any) []map[string]any {
	if cfgs == nil {
		return nil
	}

	cloned := make([]map[string]any, len(cfgs))
	for i := range cfgs {
		cloned[i] = cloneHarnessSpecMap(cfgs[i])
	}
	return cloned
}

func cloneHarnessSpecValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneHarnessSpecMap(v)
	case []any:
		cloned := make([]any, len(v))
		for i := range v {
			cloned[i] = cloneHarnessSpecValue(v[i])
		}
		return cloned
	case []string:
		return append([]string(nil), v...)
	case []map[string]any:
		return cloneHarnessSpecFallback(v)
	default:
		return value
	}
}

func buildDotenv(_ BuildContext, d *dag) ([]string, error) {
	if d.Dotenv.IsZero() {
		return []string{".env"}, nil
	}
	return d.Dotenv.Values(), nil
}

func buildHandlers(ctx BuildContext, d *dag, result *core.DAG) (core.HandlerOn, error) {
	buildCtx := StepBuildContext{BuildContext: ctx, dag: result}
	var handlerOn core.HandlerOn

	localDefs, err := decodeDefaults(d.Defaults)
	if err != nil {
		return handlerOn, err
	}
	defs := mergeDefaults(ctx.baseDefaults, localDefs, d.defaultsRaw)

	// buildHandler is a helper that builds a single handler step.
	buildHandler := func(s *step, name core.HandlerType) (*core.Step, error) {
		if s == nil {
			return nil, nil
		}
		return buildStepFromSpec(buildCtx, 0, s, d.rawHandler(name), map[string]struct{}{}, defs, name.String())
	}

	if handlerOn.Init, err = buildHandler(d.HandlerOn.Init, core.HandlerOnInit); err != nil {
		return handlerOn, err
	}
	if handlerOn.Exit, err = buildHandler(d.HandlerOn.Exit, core.HandlerOnExit); err != nil {
		return handlerOn, err
	}
	if handlerOn.Success, err = buildHandler(d.HandlerOn.Success, core.HandlerOnSuccess); err != nil {
		return handlerOn, err
	}
	if handlerOn.Failure, err = buildHandler(d.HandlerOn.Failure, core.HandlerOnFailure); err != nil {
		return handlerOn, err
	}

	if handlerOn.Abort, err = buildHandler(d.HandlerOn.Abort, core.HandlerOnAbort); err != nil {
		return handlerOn, err
	}

	if handlerOn.Wait, err = buildHandler(d.HandlerOn.Wait, core.HandlerOnWait); err != nil {
		return handlerOn, err
	}

	return handlerOn, nil
}

func buildSMTPConfig(_ BuildContext, d *dag) (*core.SMTPConfig, error) {
	if d.SMTP.IsZero() {
		return nil, nil
	}

	return &core.SMTPConfig{
		Host:     d.SMTP.Host,
		Port:     d.SMTP.Port.String(),
		Username: d.SMTP.Username,
		Password: d.SMTP.Password,
	}, nil
}

func buildErrMailConfig(_ BuildContext, d *dag) (*core.MailConfig, error) {
	return buildMailConfigInternal(d.ErrorMail)
}

func buildInfoMailConfig(_ BuildContext, d *dag) (*core.MailConfig, error) {
	return buildMailConfigInternal(d.InfoMail)
}

func buildWaitMailConfig(_ BuildContext, d *dag) (*core.MailConfig, error) {
	return buildMailConfigInternal(d.WaitMail)
}

func buildPreconditions(ctx BuildContext, d *dag) ([]*core.Condition, error) {
	return parsePrecondition(ctx, d.Preconditions)
}

func buildOTel(_ BuildContext, d *dag) (*core.OTelConfig, error) {
	if d.OTel == nil {
		return nil, nil
	}

	switch v := d.OTel.(type) {
	case map[string]any:
		config := &core.OTelConfig{}

		if enabled, ok := v["enabled"].(bool); ok {
			config.Enabled = enabled
		}
		if endpoint, ok := v["endpoint"].(string); ok {
			config.Endpoint = endpoint
		}
		if headers, ok := v["headers"].(map[string]any); ok {
			config.Headers = make(map[string]string)
			for key, val := range headers {
				if strVal, ok := val.(string); ok {
					config.Headers[key] = strVal
				}
			}
		}
		if insecure, ok := v["insecure"].(bool); ok {
			config.Insecure = insecure
		}
		if timeout, ok := v["timeout"].(string); ok {
			duration, err := time.ParseDuration(timeout)
			if err != nil {
				return nil, core.NewValidationError("otel.timeout", timeout, err)
			}
			config.Timeout = duration
		}
		if resource, ok := v["resource"].(map[string]any); ok {
			config.Resource = resource
		}

		return config, nil

	default:
		return nil, core.NewValidationError("otel", v, fmt.Errorf("otel must be a map"))
	}
}

func buildSteps(ctx BuildContext, d *dag, result *core.DAG) ([]core.Step, error) {
	buildCtx := StepBuildContext{BuildContext: ctx, dag: result}
	names := make(map[string]struct{})

	localDefs, err := decodeDefaults(d.Defaults)
	if err != nil {
		return nil, err
	}
	defs := mergeDefaults(ctx.baseDefaults, localDefs, d.defaultsRaw)

	switch v := d.Steps.(type) {
	case nil:
		return nil, nil

	case []any:
		normalized := normalizeStepData(ctx, v)

		var builtSteps []*core.Step
		var prevSteps []*core.Step
		for i, raw := range normalized {
			switch v := raw.(type) {
			case map[string]any:
				st, err := buildStepFromRaw(buildCtx, i, v, names, defs)
				if err != nil {
					return nil, err
				}

				if err := validateNoDependsForChainType(result, st); err != nil {
					return nil, err
				}

				if err := validateNoRouterForChainType(result, st); err != nil {
					return nil, err
				}

				injectChainDependencies(result, prevSteps, st)
				builtSteps = append(builtSteps, st)
				prevSteps = []*core.Step{st}

			case []any:
				var tempSteps []*core.Step
				var normalizedNested = normalizeStepData(ctx, v)
				for _, nested := range normalizedNested {
					switch vv := nested.(type) {
					case map[string]any:
						st, err := buildStepFromRaw(buildCtx, i, vv, names, defs)
						if err != nil {
							return nil, err
						}

						if err := validateNoDependsForChainType(result, st); err != nil {
							return nil, err
						}

						if err := validateNoRouterForChainType(result, st); err != nil {
							return nil, err
						}

						injectChainDependencies(result, prevSteps, st)
						builtSteps = append(builtSteps, st)
						tempSteps = append(tempSteps, st)

					default:
						return nil, core.NewValidationError("steps", raw, ErrInvalidStepData)
					}
				}
				prevSteps = tempSteps

			default:
				return nil, core.NewValidationError("steps", raw, ErrInvalidStepData)
			}
		}

		var steps []core.Step
		for _, st := range builtSteps {
			steps = append(steps, *st)
		}
		// Transform router steps: inject preconditions into targets
		if err := transformRouterSteps(steps); err != nil {
			return nil, err
		}
		return steps, nil

	case map[string]any:
		stepsMap := make(map[string]step)
		md, _ := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			ErrorUnused: true,
			Result:      &stepsMap,
			DecodeHook:  TypedUnionDecodeHook(),
		})
		if err := md.Decode(v); err != nil {
			return nil, core.NewValidationError("steps", v, err)
		}

		var steps []core.Step
		for name, st := range stepsMap {
			rawStep, _ := v[name].(map[string]any)
			builtStep, err := buildStepFromSpec(buildCtx, 0, &st, rawStep, names, defs, name)
			if err != nil {
				return nil, err
			}

			if err := validateNoDependsForChainType(result, builtStep); err != nil {
				return nil, err
			}

			if err := validateNoRouterForChainType(result, builtStep); err != nil {
				return nil, err
			}

			steps = append(steps, *builtStep)
		}
		// Sort steps by name for deterministic output when built from a map.
		// Go map iteration is non-deterministic, which causes the SSE watcher's
		// JSON hash to change on every poll, triggering unnecessary broadcasts.
		slices.SortFunc(steps, func(a, b core.Step) int {
			return strings.Compare(a.Name, b.Name)
		})
		// Transform router steps: inject preconditions into targets
		if err := transformRouterSteps(steps); err != nil {
			return nil, err
		}
		return steps, nil

	default:
		return nil, core.NewValidationError("steps", v, ErrStepsMustBeArrayOrMap)
	}
}

// buildMailConfigInternal builds a core.MailConfig from the mail configuration.
func buildMailConfigInternal(def mailConfig) (*core.MailConfig, error) {
	if def.IsZero() {
		return nil, nil
	}

	// StringOrArray already parsed during YAML unmarshal
	rawAddresses := def.To.Values()

	// Trim whitespace and filter out empty entries
	var toAddresses []string
	for _, addr := range rawAddresses {
		trimmed := strings.TrimSpace(addr)
		if trimmed != "" {
			toAddresses = append(toAddresses, trimmed)
		}
	}

	return &core.MailConfig{
		From:       strings.TrimSpace(def.From),
		To:         toAddresses,
		Prefix:     strings.TrimSpace(def.Prefix),
		AttachLogs: def.AttachLogs,
	}, nil
}

// validateNoDependsForChainType ensures that steps in chain type DAGs do not have explicit depends.
// Chain type DAGs should have fully automatic sequential execution with no manual dependency control.
func validateNoDependsForChainType(dag *core.DAG, step *core.Step) error {
	if dag.Type != core.TypeChain {
		return nil
	}
	if len(step.Depends) > 0 || step.ExplicitlyNoDeps {
		return core.NewValidationError("depends", step.Depends,
			fmt.Errorf("step '%s': %w", step.Name, core.ErrDependsNotAllowedInChainType))
	}
	return nil
}

// validateNoRouterForChainType returns an error if a router step is used in chain mode
func validateNoRouterForChainType(dag *core.DAG, step *core.Step) error {
	if dag.Type != core.TypeChain {
		return nil
	}
	if step.Router != nil {
		return core.NewValidationError("type", step.Name,
			fmt.Errorf("step '%s': router steps require type 'graph'; change DAG type from 'chain' to 'graph' to use router steps", step.Name))
	}
	return nil
}

// transformRouterSteps processes router-type steps and injects preconditions
// into their target steps. It modifies the steps slice in place.
func transformRouterSteps(steps []core.Step) error {
	// Build step index for lookup (using pointers to modify in place)
	stepIndex := make(map[string]*core.Step)
	for i := range steps {
		stepIndex[steps[i].Name] = &steps[i]
	}

	for i := range steps {
		if steps[i].Router == nil {
			continue
		}

		router := steps[i].Router
		routerName := steps[i].Name

		// Track targets to detect duplicates across routes
		seenTargets := make(map[string]string) // target -> first pattern that used it

		// For each route, inject precondition into target steps
		for _, route := range router.Routes {
			for _, targetName := range route.Targets {
				// Check for duplicate target
				if firstPattern, exists := seenTargets[targetName]; exists {
					return core.NewValidationError("routes", targetName,
						fmt.Errorf("router %q: step %q is targeted by multiple routes (%q and %q); each step can only be a target of one route",
							routerName, targetName, firstPattern, route.Pattern))
				}
				seenTargets[targetName] = route.Pattern

				target, ok := stepIndex[targetName]
				if !ok {
					return core.NewValidationError("routes", targetName,
						fmt.Errorf("router %q references non-existent step %q", routerName, targetName))
				}

				// Inject precondition: check if value matches pattern
				condition := &core.Condition{
					Condition: router.Value,
					Expected:  route.Pattern,
				}
				target.Preconditions = append(target.Preconditions, condition)

				// Add router as dependency if not already present
				if !slices.Contains(target.Depends, routerName) {
					target.Depends = append(target.Depends, routerName)
				}

				// Enable continueOn.skipped for proper flow
				target.ContinueOn.Skipped = true
			}
		}

		// Router itself allows downstream to continue
		steps[i].ContinueOn.Skipped = true
	}

	return nil
}
