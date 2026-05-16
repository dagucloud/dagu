// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/cmn/signal"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/dagucloud/dagu/internal/llm"
)

// step defines a step in the DAG.
type step struct {
	// Name is the name of the step.
	Name string `yaml:"name,omitempty"`
	// ID is the optional unique identifier for the step.
	ID string `yaml:"id,omitempty"`
	// Description is the description of the step.
	Description string `yaml:"description,omitempty"`
	// WorkingDir is the working directory of the step.
	WorkingDir string `yaml:"working_dir,omitempty"`
	// Run is the v2 canonical local command/script field.
	Run any `yaml:"run,omitempty"`
	// Action is the v2 canonical named action field.
	Action string `yaml:"action,omitempty"`
	// Command is the command to run (on shell).
	Command any `yaml:"command,omitempty"`
	// Exec is a structured argv form for direct execution without shell parsing.
	Exec *execSpec `yaml:"exec,omitempty"`
	// Shell is the shell to run the command. Default is `$SHELL` or `sh`.
	// Can be a string (e.g., "bash -e") or an array (e.g., ["bash", "-e"]).
	Shell types.ShellValue `yaml:"shell,omitempty"`
	// ShellArgs is the list of additional arguments passed to the shell.
	ShellArgs []string `yaml:"shell_args,omitempty"`
	// ShellPackages is the list of packages to install.
	// This is used only when the shell is `nix-shell`.
	ShellPackages []string `yaml:"shell_packages,omitempty"`
	// Script is the script to run.
	Script string `yaml:"script,omitempty"`
	// Stdout is the file to write the stdout.
	Stdout any `yaml:"stdout,omitempty"`
	// Stderr is the file to write the stderr.
	Stderr any `yaml:"stderr,omitempty"`
	// LogOutput specifies how stdout and stderr are handled in log files for this step.
	// Overrides the DAG-level logOutput setting.
	// Can be "separate" (default) for separate .out and .err files,
	// or "merged" for a single combined .log file.
	LogOutput types.LogOutputValue `yaml:"log_output,omitempty"`
	// Output is the variable name to store the output.
	// Can be a string for captured stdout or an object for structured step output.
	Output any `yaml:"output,omitempty"`
	// OutputSchema validates stdout JSON against an inline JSON Schema object.
	OutputSchema any `yaml:"output_schema,omitempty"`
	// Depends is the list of steps to depend on.
	Depends types.StringOrArray `yaml:"depends,omitempty"`
	// ContinueOn is the condition to continue on.
	// Can be a string ("skipped", "failed") or an object with detailed config.
	ContinueOn types.ContinueOnValue `yaml:"continue_on,omitempty"`
	// RetryPolicy is the retry policy.
	RetryPolicy *retryPolicy `yaml:"retry_policy,omitempty"`
	// RepeatPolicy is the repeat policy.
	RepeatPolicy *repeatPolicy `yaml:"repeat_policy,omitempty"`
	// MailOnError is the flag to send mail on error.
	MailOnError bool `yaml:"mail_on_error,omitempty"`
	// Preconditions is the condition to run the step.
	Preconditions any `yaml:"preconditions,omitempty"`
	// SignalOnStop is the signal when the step is requested to stop.
	// When it is empty, the same signal as the parent process is sent.
	// It can be KILL when the process does not stop over the timeout.
	SignalOnStop *string `yaml:"signal_on_stop,omitempty"`
	// Call is the name of a DAG to run as a sub dag-run.
	Call string `yaml:"call,omitempty"`
	// Params specifies the parameters for the sub dag-run.
	Params any `yaml:"params,omitempty"`
	// Parallel specifies parallel execution configuration.
	// Can be:
	// - Direct array reference: parallel: ${ITEMS}
	// - Static array: parallel: [item1, item2]
	// - Object configuration: parallel: {items: ${ITEMS}, max_concurrent: 5}
	Parallel any `yaml:"parallel,omitempty"`
	// WorkerSelector specifies required worker labels for execution.
	WorkerSelector map[string]string `yaml:"worker_selector,omitempty"`
	// Env specifies the environment variables for the step.
	Env types.EnvValue `yaml:"env,omitempty"`
	// TimeoutSec specifies the maximum runtime for the step in seconds.
	TimeoutSec int `yaml:"timeout_sec,omitempty"`
	// Container specifies the container configuration for this step.
	// If set, the step runs in its own container instead of the DAG-level container.
	// Can be a string (existing container name to exec into) or an object (container configuration).
	Container any `yaml:"container,omitempty"`

	// Type specifies the executor type (ssh, http, jq, mail, docker, archive).
	Type string `yaml:"type,omitempty"`

	// With contains executor-specific configuration.
	With map[string]any `yaml:"with,omitempty"`

	// Config contains executor-specific configuration.
	// Deprecated: use With.
	Config map[string]any `yaml:"config,omitempty"`

	// LLM contains the configuration for LLM-based executors (chat, agent, etc.).
	// Requires explicit type: chat (or future type: agent).
	LLM *llmConfig `yaml:"llm,omitempty"`

	// Messages contains the session messages for chat steps.
	// Only valid when type is "chat".
	Messages []llmMessage `yaml:"messages,omitempty"`

	// Agent contains the configuration for agent-type steps.
	// Only valid when type is "agent".
	Agent *agentConfig `yaml:"agent,omitempty"`

	// Approval configures a human approval gate after step execution.
	Approval *approvalConfig `yaml:"approval,omitempty"`

	// Router configuration (type: router)
	// Value is the expression to evaluate for routing
	Value string `yaml:"value,omitempty"`
	// Routes maps patterns to target step names
	Routes map[string][]string `yaml:"routes,omitempty"`

	// parsedOutput caches parsed output configuration during a single step build.
	parsedOutput       *outputConfig
	parsedOutputErr    error
	parsedOutputCached bool
}

type execSpec struct {
	Command string `yaml:"command,omitempty"`
	Args    []any  `yaml:"args,omitempty"`
}

func (s *step) executorConfig() map[string]any {
	if s != nil && s.With != nil {
		return s.With
	}
	if s != nil {
		return s.Config
	}
	return nil
}

func (s *step) executorConfigFieldName() string {
	if s != nil && s.With != nil {
		return "with"
	}
	if s != nil && s.Config != nil {
		return "config"
	}
	return "with"
}

func (s *step) parsedOutputConfig() (*outputConfig, error) {
	if s == nil {
		return nil, nil
	}
	if s.parsedOutputCached {
		return s.parsedOutput, s.parsedOutputErr
	}

	s.parsedOutput, s.parsedOutputErr = parseOutputConfig(s.Output)
	s.parsedOutputCached = true
	return s.parsedOutput, s.parsedOutputErr
}

func validateStepConfigAliasStruct(s *step) error {
	if s == nil || s.With == nil || s.Config == nil {
		return nil
	}
	return newStepConfigAliasError(map[string]any{
		"with":   s.With,
		"config": s.Config,
	})
}

func validateStepConfigAliasRaw(raw map[string]any) error {
	if raw == nil {
		return nil
	}
	_, hasWith := raw["with"]
	_, hasConfig := raw["config"]
	if !hasWith || !hasConfig {
		return nil
	}
	return newStepConfigAliasError(raw)
}

func newStepConfigAliasError(value any) error {
	return core.NewValidationError(
		"with",
		value,
		fmt.Errorf("fields %q and %q cannot be used together; use %q", "with", "config", "with"),
	)
}

// approvalConfig defines the approval configuration for a step.
type approvalConfig struct {
	// Prompt is the message displayed to the approver.
	Prompt string `yaml:"prompt,omitempty"`
	// Input is the list of expected input field names from the approver.
	Input []string `yaml:"input,omitempty"`
	// Required is the subset of Input fields that must be provided.
	Required []string `yaml:"required,omitempty"`
	// RewindTo is the step name or ID to restart from on push-back.
	RewindTo string `yaml:"rewind_to,omitempty"`
}

// repeatPolicy defines the repeat policy for a step.
type repeatPolicy struct {
	Repeat         types.RepeatMode   `yaml:"repeat,omitempty"`           // Flag to indicate if the step should be repeated, can be bool (legacy) or string ("while" or "until")
	IntervalSec    types.IntOrDynamic `yaml:"interval_sec,omitempty"`     // Interval in seconds to wait before repeating the step
	Limit          types.IntOrDynamic `yaml:"limit,omitempty"`            // Maximum number of times to repeat the step
	Condition      string             `yaml:"condition,omitempty"`        // Condition to check before repeating
	Expected       string             `yaml:"expected,omitempty"`         // Expected output to match before repeating
	ExitCode       []int              `yaml:"exit_code,omitempty"`        // List of exit codes to consider for repeating the step
	Backoff        types.BackoffValue `yaml:"backoff,omitempty"`          // Accepts bool or float
	MaxIntervalSec types.IntOrDynamic `yaml:"max_interval_sec,omitempty"` // Maximum interval in seconds
}

// retryPolicy defines the retry policy for a step.
type retryPolicy struct {
	Limit          any   `yaml:"limit,omitempty"`
	IntervalSec    any   `yaml:"interval_sec,omitempty"`
	ExitCode       []int `yaml:"exit_code,omitempty"`
	Backoff        any   `yaml:"backoff,omitempty"` // Accepts bool or float
	MaxIntervalSec int   `yaml:"max_interval_sec,omitempty"`
}

// llmConfig defines the LLM configuration for a step.
// thinkingConfig defines thinking/reasoning mode configuration for YAML parsing.
type thinkingConfig struct {
	// Enabled activates thinking mode for supported models.
	Enabled bool `yaml:"enabled,omitempty"`
	// Effort controls reasoning depth: low, medium, high, xhigh.
	Effort string `yaml:"effort,omitempty"`
	// BudgetTokens sets explicit token budget (provider-specific).
	BudgetTokens *int `yaml:"budget_tokens,omitempty"`
	// IncludeInOutput includes thinking blocks in stdout.
	IncludeInOutput bool `yaml:"include_in_output,omitempty"`
}

// webSearchConfig configures provider-native web search for LLM steps.
type webSearchConfig struct {
	// Enabled activates provider-native web search.
	Enabled bool `yaml:"enabled,omitempty"`
	// MaxUses limits search invocations per request.
	MaxUses *int `yaml:"max_uses,omitempty"`
	// AllowedDomains restricts results to these domains (Anthropic only).
	AllowedDomains []string `yaml:"allowed_domains,omitempty"`
	// BlockedDomains excludes results from these domains (Anthropic only).
	BlockedDomains []string `yaml:"blocked_domains,omitempty"`
	// UserLocation localizes search results.
	UserLocation *webSearchUserLocation `yaml:"user_location,omitempty"`
}

// webSearchUserLocation provides approximate location for search localization.
type webSearchUserLocation struct {
	City     string `yaml:"city,omitempty"`
	Region   string `yaml:"region,omitempty"`
	Country  string `yaml:"country,omitempty"`
	Timezone string `yaml:"timezone,omitempty"`
}

type llmConfig struct {
	// Provider is the LLM provider (openai, anthropic, gemini, openrouter, local).
	// Used for single model config (backward compatible).
	Provider string `yaml:"provider,omitempty"`
	// Model can be a string (single model) or array of model entries (fallback support).
	// String example: "gpt-4o"
	// Array example: [{provider: openai, name: gpt-4o}, {provider: anthropic, name: claude-sonnet-4-20250514}]
	Model types.ModelValue `yaml:"model,omitempty"`
	// System is the default system prompt for sessions.
	System string `yaml:"system,omitempty"`
	// Temperature controls randomness (0.0-2.0).
	Temperature *float64 `yaml:"temperature,omitempty"`
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens *int `yaml:"max_tokens,omitempty"`
	// TopP is the nucleus sampling parameter.
	TopP *float64 `yaml:"top_p,omitempty"`
	// BaseURL is a custom API endpoint.
	BaseURL string `yaml:"base_url,omitempty"`
	// APIKeyName is the name of the environment variable containing the API key.
	// If not specified, the default environment variable for the provider is used.
	APIKeyName string `yaml:"api_key_name,omitempty"`
	// Stream enables or disables streaming output.
	// Default is true.
	Stream *bool `yaml:"stream,omitempty"`
	// Thinking enables extended thinking/reasoning mode.
	Thinking *thinkingConfig `yaml:"thinking,omitempty"`
	// Tools is a list of DAG names to use as callable tools.
	Tools []string `yaml:"tools,omitempty"`
	// MaxToolIterations limits tool calling rounds (default: 10).
	MaxToolIterations *int `yaml:"max_tool_iterations,omitempty"`
	// WebSearch configures provider-native web search.
	WebSearch *webSearchConfig `yaml:"web_search,omitempty"`
}

// llmMessage defines a message in the LLM session.
type llmMessage struct {
	// Role is the message role (system, user, assistant, tool).
	Role string `yaml:"role,omitempty"`
	// Content is the message content. Supports variable substitution with ${VAR}.
	Content string `yaml:"content,omitempty"`
}

// agentConfig defines the agent configuration for an agent step.
type agentConfig struct {
	// Model overrides the global default model for this step.
	Model string `yaml:"model,omitempty"`
	// Tools configures which tools are available and their policies.
	Tools *agentToolsConfig `yaml:"tools,omitempty"`
	// Skills lists skill IDs the agent is allowed to use.
	// If omitted, falls back to globally enabled skills.
	Skills []string `yaml:"skills,omitempty"`
	// Soul is the soul ID for this step's agent identity.
	Soul string `yaml:"soul,omitempty"`
	// Memory controls whether persistent memory is loaded.
	Memory *agentMemoryConfig `yaml:"memory,omitempty"`
	// Prompt is additional instructions appended to the built-in system prompt.
	Prompt string `yaml:"prompt,omitempty"`
	// MaxIterations is the maximum number of tool call rounds.
	MaxIterations *int `yaml:"max_iterations,omitempty"`
	// SafeMode enables command approval via human review.
	SafeMode *bool `yaml:"safe_mode,omitempty"`
	// WebSearch configures provider-native web search for this agent step.
	// Overrides the global agent web search setting.
	WebSearch *webSearchConfig `yaml:"web_search,omitempty"`
}

// agentToolsConfig configures available tools and policies.
type agentToolsConfig struct {
	// Enabled lists the tools to enable.
	Enabled []string `yaml:"enabled,omitempty"`
	// BashPolicy configures bash command security rules.
	BashPolicy *agentBashPolicy `yaml:"bash_policy,omitempty"`
}

// agentBashPolicy configures bash command security enforcement.
type agentBashPolicy struct {
	// DefaultBehavior is the default action when no rule matches.
	DefaultBehavior string `yaml:"default_behavior,omitempty"`
	// DenyBehavior determines what happens when a command is denied.
	DenyBehavior string `yaml:"deny_behavior,omitempty"`
	// Rules is an ordered list of pattern-matching rules.
	Rules []agentBashRule `yaml:"rules,omitempty"`
}

// agentBashRule is a single bash command policy rule.
type agentBashRule struct {
	// Name is a human-readable name for the rule.
	Name string `yaml:"name,omitempty"`
	// Pattern is a regex pattern to match against commands.
	Pattern string `yaml:"pattern"`
	// Action is the action to take when the pattern matches.
	Action string `yaml:"action"`
}

// agentMemoryConfig configures memory for the agent step.
type agentMemoryConfig struct {
	// Enabled controls whether global and per-DAG memory is loaded.
	Enabled bool `yaml:"enabled,omitempty"`
}

// stepTransformer is a generic implementation for step field transformations
type stepTransformer[T any] struct {
	fieldName string
	builder   func(ctx StepBuildContext, s *step) (T, error)
}

func (t *stepTransformer[T]) Transform(ctx StepBuildContext, in *step, out reflect.Value) error {
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

// newStepTransformer creates a step transformer for a single field
func newStepTransformer[T any](fieldName string, builder func(StepBuildContext, *step) (T, error)) Transformer[StepBuildContext, *step] {
	return &stepTransformer[T]{
		fieldName: fieldName,
		builder:   builder,
	}
}

// stepTransform wraps a step transformer with its name for error reporting
type stepTransform struct {
	name        string
	transformer Transformer[StepBuildContext, *step]
}

// stepTransformers defines the ordered sequence of step transformers
var stepTransformers = []stepTransform{
	{"name", newStepTransformer("Name", buildStepName)},
	{"id", newStepTransformer("ID", buildStepID)},
	{"description", newStepTransformer("Description", buildStepDescription)},
	{"shell_packages", newStepTransformer("ShellPackages", buildStepShellPackages)},
	{"script", newStepTransformer("Script", buildStepScript)},
	{"stdout", newStepTransformer("Stdout", buildStepStdout)},
	{"stdout.artifact", newStepTransformer("StdoutArtifact", buildStepStdoutArtifact)},
	{"stderr", newStepTransformer("Stderr", buildStepStderr)},
	{"stderr.artifact", newStepTransformer("StderrArtifact", buildStepStderrArtifact)},
	{"log_output", newStepTransformer("LogOutput", buildStepLogOutput)},
	{"mail_on_error", newStepTransformer("MailOnError", buildStepMailOnError)},
	{"worker_selector", newStepTransformer("WorkerSelector", buildStepWorkerSelector)},
	{"working_dir", newStepTransformer("Dir", buildStepWorkingDir)},
	{"shell", newStepTransformer("Shell", buildStepShell)},
	{"shell_args", newStepTransformer("ShellArgs", buildStepShellArgs)},
	{"timeout", newStepTransformer("Timeout", buildStepTimeout)},
	{"depends", newStepTransformer("Depends", buildStepDepends)},
	{"explicitly_no_deps", newStepTransformer("ExplicitlyNoDeps", buildStepExplicitlyNoDeps)},
	{"continue_on", newStepTransformer("ContinueOn", buildStepContinueOn)},
	{"retry_policy", newStepTransformer("RetryPolicy", buildStepRetryPolicy)},
	{"repeat_policy", newStepTransformer("RepeatPolicy", buildStepRepeatPolicy)},
	{"signal_on_stop", newStepTransformer("SignalOnStop", buildStepSignalOnStop)},
	{"output", newStepTransformer("Output", buildStepOutput)},
	{"structured_output", newStepTransformer("StructuredOutput", buildStepStructuredOutput)},
	{"output_schema", newStepTransformer("OutputSchema", buildStepOutputSchema)},
	{"env", newStepTransformer("Env", buildStepEnvs)},
	{"preconditions", newStepTransformer("Preconditions", buildStepPreconditions)},
}

// runStepTransformers executes all step transformers
func runStepTransformers(ctx StepBuildContext, spec *step, result *core.Step) core.ErrorList {
	var errs core.ErrorList
	out := reflect.ValueOf(result).Elem()

	for _, t := range stepTransformers {
		if err := t.transformer.Transform(ctx, spec, out); err != nil {
			errs = append(errs, wrapTransformError(t.name, err))
		}
	}

	return errs
}

// build transforms the step specification into a core.Step.
func (s *step) build(ctx StepBuildContext) (*core.Step, error) {
	if err := validateStepConfigAliasStruct(s); err != nil {
		return nil, err
	}

	result := &core.Step{
		ExecutorConfig: core.ExecutorConfig{Config: make(map[string]any)},
	}

	// Run the transformer pipeline
	errs := runStepTransformers(ctx, s, result)

	// Action-defining transformations
	if err := buildStepContainer(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("container", err))
	}
	if err := buildStepParallel(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("parallel", err))
	}
	if err := buildStepSubDAG(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("subDAG", err))
	}
	if err := buildStepExecutor(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("executor", err))
		if isStepTypeValidationError(err) {
			return nil, errs
		}
	}
	// LLM must be after executor so we know if type supports LLM
	if err := buildStepLLM(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("llm", err))
	}
	if err := buildStepMessages(s, result); err != nil {
		errs = append(errs, wrapTransformError("messages", err))
	}
	if err := buildStepAgent(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("agent", err))
	}
	if err := buildStepRouter(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("router", err))
	}
	if err := buildStepApproval(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("approval", err))
	}
	if err := buildStepCommand(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("command", err))
	}
	if err := buildStepParamsField(ctx, s, result); err != nil {
		errs = append(errs, wrapTransformError("params", err))
	}

	// Final validators run after the executor type is determined
	// Capabilities-based validators handle all execution type conflicts
	if err := validateCommand(result); err != nil {
		errs = append(errs, wrapTransformError("command", err))
	}
	if err := validateMultipleCommands(result); err != nil {
		errs = append(errs, wrapTransformError("command", err))
	}
	if err := validateScript(result); err != nil {
		errs = append(errs, wrapTransformError("script", err))
	}
	if err := validateShell(result); err != nil {
		errs = append(errs, wrapTransformError("shell", err))
	}
	if err := validateContainer(result); err != nil {
		errs = append(errs, wrapTransformError("container", err))
	}
	if err := validateSubDAG(result); err != nil {
		errs = append(errs, wrapTransformError("dag", err))
	}
	if err := validateWorkerSelector(result); err != nil {
		errs = append(errs, wrapTransformError("worker_selector", err))
	}
	if err := validateLLM(result); err != nil {
		errs = append(errs, wrapTransformError("llm", err))
	}
	if err := validateMessages(result); err != nil {
		errs = append(errs, wrapTransformError("messages", err))
	}
	if err := validateAgent(result); err != nil {
		errs = append(errs, wrapTransformError("agent", err))
	}

	// Validate executor config against registered schema
	// Only validate when config has actual values (not just initialized as empty map)
	if len(result.ExecutorConfig.Config) > 0 {
		if err := core.ValidateExecutorConfig(result.ExecutorConfig.Type, result.ExecutorConfig.Config); err != nil {
			errs = append(errs, wrapTransformError(s.executorConfigFieldName(), err))
		}
	}

	// Validate that stdout and stderr don't point to the same file
	if err := validateStdoutStderr(result); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, errs
	}

	return result, nil
}

// validateStdoutStderr checks that stdout and stderr don't point to the same file.
// If both are specified and point to the same file, use log_output: merged instead.
func validateStdoutStderr(s *core.Step) error {
	if s.Stdout != "" && s.Stderr != "" && s.Stdout == s.Stderr {
		return fmt.Errorf("stdout and stderr cannot point to the same file %q; use 'log_output: merged' instead", s.Stdout)
	}
	return nil
}

// Simple field builders

func buildStepName(_ StepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.Name), nil
}

func buildStepID(_ StepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.ID), nil
}

func buildStepDescription(_ StepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.Description), nil
}

func buildStepShellPackages(_ StepBuildContext, s *step) ([]string, error) {
	return s.ShellPackages, nil
}

func buildStepScript(_ StepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.Script), nil
}

func buildStepStdout(_ StepBuildContext, s *step) (string, error) {
	path, _, err := buildStepOutputRedirect("stdout", s.Stdout)
	return path, err
}

func buildStepStdoutArtifact(_ StepBuildContext, s *step) (string, error) {
	_, artifact, err := buildStepOutputRedirect("stdout", s.Stdout)
	return artifact, err
}

func buildStepStderr(_ StepBuildContext, s *step) (string, error) {
	path, _, err := buildStepOutputRedirect("stderr", s.Stderr)
	return path, err
}

func buildStepStderrArtifact(_ StepBuildContext, s *step) (string, error) {
	_, artifact, err := buildStepOutputRedirect("stderr", s.Stderr)
	return artifact, err
}

func buildStepOutputRedirect(field string, raw any) (filePath string, artifactPath string, err error) {
	switch v := raw.(type) {
	case nil:
		return "", "", nil
	case string:
		return strings.TrimSpace(v), "", nil
	case map[string]any:
		return parseStepArtifactOutputRedirect(field, v)
	case map[any]any:
		converted := make(map[string]any, len(v))
		for key, value := range v {
			keyString, ok := key.(string)
			if !ok {
				return "", "", fmt.Errorf("%s object keys must be strings", field)
			}
			converted[keyString] = value
		}
		return parseStepArtifactOutputRedirect(field, converted)
	default:
		return "", "", fmt.Errorf("%s must be a string path or an object with artifact", field)
	}
}

func parseStepArtifactOutputRedirect(field string, raw map[string]any) (string, string, error) {
	if len(raw) != 1 {
		return "", "", fmt.Errorf("%s object must contain only artifact", field)
	}
	value, ok := raw["artifact"]
	if !ok {
		return "", "", fmt.Errorf("%s object must contain artifact", field)
	}
	artifact, ok := value.(string)
	if !ok {
		return "", "", fmt.Errorf("%s.artifact must be a string", field)
	}
	clean, err := cleanStepArtifactPath(artifact)
	if err != nil {
		return "", "", fmt.Errorf("%s.artifact: %w", field, err)
	}
	return "", clean, nil
}

func cleanStepArtifactPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(normalized, "/") ||
		strings.HasPrefix(normalized, "~/") ||
		normalized == "~" ||
		filepath.IsAbs(raw) ||
		hasWindowsDrive(raw) ||
		hasWindowsDrive(normalized) {
		return "", fmt.Errorf("artifact path must be relative")
	}
	if slices.Contains(strings.Split(normalized, "/"), "..") {
		return "", fmt.Errorf("artifact path must not contain parent directory segments")
	}

	clean := path.Clean(normalized)
	if clean == "." {
		return "", fmt.Errorf("artifact path must name a file")
	}
	if strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("artifact path must be relative")
	}
	return clean, nil
}

func hasWindowsDrive(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	ch := value[0]
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func buildStepLogOutput(_ StepBuildContext, s *step) (core.LogOutputMode, error) {
	if s.LogOutput.IsZero() {
		// Return empty string to indicate "inherit from DAG"
		return "", nil
	}
	return s.LogOutput.Mode(), nil
}

func buildStepMailOnError(_ StepBuildContext, s *step) (bool, error) {
	return s.MailOnError, nil
}

func buildStepWorkerSelector(_ StepBuildContext, s *step) (map[string]string, error) {
	return s.WorkerSelector, nil
}

func buildStepWorkingDir(_ StepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.WorkingDir), nil
}

// stepShellResult holds both shell and args for step
type stepShellResult struct {
	Shell string
	Args  []string
}

func parseStepShellInternal(_ StepBuildContext, s *step) (*stepShellResult, error) {
	if s.Shell.IsZero() {
		return &stepShellResult{}, nil
	}

	if s.Shell.IsArray() {
		return &stepShellResult{
			Shell: s.Shell.Command(),
			Args:  s.Shell.Arguments(),
		}, nil
	}

	// For string form, need to split command and args
	command := s.Shell.Command()
	if command == "" {
		return &stepShellResult{}, nil
	}

	shell, args, err := cmdutil.SplitCommand(command)
	if err != nil {
		return nil, core.NewValidationError("shell", s.Shell.Value(), fmt.Errorf("failed to parse shell command: %w", err))
	}
	return &stepShellResult{
		Shell: strings.TrimSpace(shell),
		Args:  args,
	}, nil
}

func buildStepShell(ctx StepBuildContext, s *step) (string, error) {
	result, err := parseStepShellInternal(ctx, s)
	if err != nil {
		return "", err
	}
	return result.Shell, nil
}

func buildStepShellArgs(ctx StepBuildContext, s *step) ([]string, error) {
	result, err := parseStepShellInternal(ctx, s)
	if err != nil {
		return nil, err
	}
	return append(result.Args, s.ShellArgs...), nil
}

func buildStepTimeout(_ StepBuildContext, s *step) (time.Duration, error) {
	if s.TimeoutSec < 0 {
		return 0, core.NewValidationError("timeout_sec", s.TimeoutSec, ErrTimeoutSecMustBeNonNegative)
	}
	return time.Second * time.Duration(s.TimeoutSec), nil
}

func buildStepDepends(_ StepBuildContext, s *step) ([]string, error) {
	return s.Depends.Values(), nil
}

func buildStepExplicitlyNoDeps(_ StepBuildContext, s *step) (bool, error) {
	return !s.Depends.IsZero() && s.Depends.IsEmpty(), nil
}

func buildStepContinueOn(_ StepBuildContext, s *step) (core.ContinueOn, error) {
	if s.ContinueOn.IsZero() {
		return core.ContinueOn{}, nil
	}

	return core.ContinueOn{
		Skipped:     s.ContinueOn.Skipped(),
		Failure:     s.ContinueOn.Failed(),
		MarkSuccess: s.ContinueOn.MarkSuccess(),
		ExitCode:    s.ContinueOn.ExitCode(),
		Output:      s.ContinueOn.Output(),
	}, nil
}

func buildStepRetryPolicy(_ StepBuildContext, s *step) (core.RetryPolicy, error) {
	if s.RetryPolicy == nil {
		return core.RetryPolicy{}, nil
	}

	var result core.RetryPolicy
	var err error

	// Step retry keeps string values so they can be resolved later at runtime.
	result.Limit, result.LimitStr, err = parseStepRetryLimit(s.RetryPolicy.Limit)
	if err != nil {
		return core.RetryPolicy{}, err
	}

	result.Interval, result.IntervalSecStr, err = parseStepRetryInterval(s.RetryPolicy.IntervalSec)
	if err != nil {
		return core.RetryPolicy{}, err
	}

	if s.RetryPolicy.ExitCode != nil {
		result.ExitCodes = s.RetryPolicy.ExitCode
	}

	// Parse backoff field
	backoff, err := parseBackoffValue(s.RetryPolicy.Backoff, "retry_policy.backoff")
	if err != nil {
		return core.RetryPolicy{}, core.NewValidationError("retry_policy.backoff", s.RetryPolicy.Backoff, err)
	}
	result.Backoff = backoff

	// Parse maxIntervalSec
	if s.RetryPolicy.MaxIntervalSec > 0 {
		result.MaxInterval = time.Second * time.Duration(s.RetryPolicy.MaxIntervalSec)
	}

	return result, nil
}

func parseStepRetryLimit(val any) (int, string, error) {
	switch v := val.(type) {
	case int:
		return v, "", nil
	case int64:
		return int(v), "", nil
	case uint64:
		if v > math.MaxInt {
			return 0, "", core.NewValidationError("retry_policy.limit", v, fmt.Errorf("value %d exceeds maximum int", v))
		}
		return int(v), "", nil
	case string:
		return 0, v, nil
	case nil:
		return 0, "", core.NewValidationError("retry_policy.limit", nil, fmt.Errorf("limit is required when retry_policy is specified"))
	default:
		return 0, "", core.NewValidationError("retry_policy.limit", v, fmt.Errorf("invalid type: %T", v))
	}
}

func parseStepRetryInterval(val any) (time.Duration, string, error) {
	switch v := val.(type) {
	case int:
		return time.Second * time.Duration(v), "", nil
	case int64:
		return time.Second * time.Duration(v), "", nil
	case uint64:
		if v > math.MaxInt64 {
			return 0, "", core.NewValidationError("retry_policy.interval_sec", v, fmt.Errorf("value %d exceeds maximum int64", v))
		}
		return time.Second * time.Duration(v), "", nil
	case string:
		return 0, v, nil
	case nil:
		return 0, "", core.NewValidationError("retry_policy.interval_sec", nil, fmt.Errorf("interval_sec is required when retry_policy is specified"))
	default:
		return 0, "", core.NewValidationError("retry_policy.interval_sec", v, fmt.Errorf("invalid type: %T", v))
	}
}

// parseBackoffValue parses a backoff value from various types (bool, int, float64).
// Returns the backoff multiplier and an error if validation fails.
func parseBackoffValue(val any, fieldName string) (float64, error) {
	if val == nil {
		return 0, nil
	}

	var backoff float64
	switch v := val.(type) {
	case bool:
		if v {
			backoff = 2.0 // Default multiplier when true
		}
	case int:
		backoff = float64(v)
	case int64:
		backoff = float64(v)
	case float64:
		backoff = v
	default:
		return 0, fmt.Errorf("invalid type for %s: %T (must be boolean or number)", fieldName, v)
	}

	// Validate backoff value
	if backoff > 0 && backoff <= 1.0 {
		return 0, fmt.Errorf("%s must be greater than 1.0 for exponential growth, got: %v", fieldName, backoff)
	}

	return backoff, nil
}

func buildStepRepeatPolicy(_ StepBuildContext, s *step) (core.RepeatPolicy, error) {
	if s.RepeatPolicy == nil {
		return core.RepeatPolicy{}, nil
	}
	rp := s.RepeatPolicy

	// Determine repeat mode from typed RepeatMode field
	var mode core.RepeatMode
	if rp.Repeat.IsSet() {
		switch rp.Repeat.String() {
		case "while":
			mode = core.RepeatModeWhile
		case "until":
			mode = core.RepeatModeUntil
		}
	}

	// Backward compatibility: infer mode if not set
	if mode == "" {
		if rp.Condition != "" && rp.Expected != "" {
			mode = core.RepeatModeUntil
		} else if rp.Condition != "" || len(rp.ExitCode) > 0 {
			mode = core.RepeatModeWhile
		}
	}

	// No repeat if mode is not determined
	if mode == "" {
		return core.RepeatPolicy{}, nil
	}

	// Validate that explicit string while/until modes have appropriate conditions
	// (bool true is allowed without conditions for backward compatibility)
	if rp.Repeat.IsSet() && !rp.Repeat.IsBool() {
		m := rp.Repeat.String()
		if (m == "while" || m == "until") && rp.Condition == "" && len(rp.ExitCode) == 0 {
			return core.RepeatPolicy{}, fmt.Errorf("repeat mode '%s' requires either 'condition' or 'exit_code' to be specified", m)
		}
	}

	var result core.RepeatPolicy
	result.RepeatMode = mode

	// Read interval_sec from typed field
	if intervalSec := rp.IntervalSec.Int(); intervalSec > 0 {
		result.Interval = time.Second * time.Duration(intervalSec)
	}
	result.IntervalStr = rp.IntervalSec.Str()

	// Read limit from typed field
	result.Limit = rp.Limit.Int()
	result.LimitStr = rp.Limit.Str()

	if rp.Condition != "" {
		result.Condition = &core.Condition{
			Condition: rp.Condition,
			Expected:  rp.Expected,
		}
	}
	result.ExitCode = rp.ExitCode

	// Read backoff from typed field
	result.Backoff = rp.Backoff.Multiplier()

	// Read max_interval_sec from typed field
	if maxIntervalSec := rp.MaxIntervalSec.Int(); maxIntervalSec > 0 {
		result.MaxInterval = time.Second * time.Duration(maxIntervalSec)
	}
	result.MaxIntervalStr = rp.MaxIntervalSec.Str()

	return result, nil
}

func buildStepSignalOnStop(_ StepBuildContext, s *step) (string, error) {
	if s.SignalOnStop == nil {
		return "", nil
	}
	sigOnStop := *s.SignalOnStop
	sig := signal.GetSignalNum(sigOnStop, 0)
	if sig == 0 {
		return "", fmt.Errorf("%w: %s", ErrInvalidSignal, sigOnStop)
	}
	return sigOnStop, nil
}

// outputConfig holds the parsed output configuration
type outputConfig struct {
	Name             string
	StructuredOutput map[string]core.StepOutputEntry
}

// parseOutputConfig parses the output field which can be string or object
func parseOutputConfig(output any) (*outputConfig, error) {
	if output == nil {
		return nil, nil
	}

	switch v := output.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		name := strings.TrimPrefix(strings.TrimSpace(v), "$")
		// Check for empty name after trimming and removing $ prefix
		if name == "" {
			return nil, nil
		}
		return &outputConfig{Name: name}, nil

	case map[string]any:
		structuredOutput, err := parseStructuredOutput(v)
		if err != nil {
			return nil, err
		}
		return &outputConfig{StructuredOutput: structuredOutput}, nil

	default:
		return nil, fmt.Errorf("output must be a string or object, got %T", output)
	}
}

var stepOutputReservedFields = map[string]struct{}{
	"value":  {},
	"from":   {},
	"path":   {},
	"decode": {},
	"select": {},
}

func parseStructuredOutput(raw map[string]any) (map[string]core.StepOutputEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	entries := make(map[string]core.StepOutputEntry, len(raw))
	for key, value := range raw {
		entry, err := parseStructuredOutputEntry(value)
		if err != nil {
			return nil, fmt.Errorf("output.%s: %w", key, err)
		}
		entries[key] = entry
	}
	return entries, nil
}

func parseStructuredOutputEntry(raw any) (core.StepOutputEntry, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return core.StepOutputEntry{
			HasValue: true,
			Value:    raw,
		}, nil
	}

	hasReservedField := false
	for key := range obj {
		if _, ok := stepOutputReservedFields[key]; ok {
			hasReservedField = true
			break
		}
	}
	if !hasReservedField {
		return core.StepOutputEntry{
			HasValue: true,
			Value:    obj,
		}, nil
	}

	var entry core.StepOutputEntry
	for key, value := range obj {
		switch key {
		case "value":
			entry.HasValue = true
			entry.Value = value
		case "from":
			str, ok := value.(string)
			if !ok {
				return core.StepOutputEntry{}, fmt.Errorf("from must be a string")
			}
			entry.From = strings.TrimSpace(str)
		case "path":
			str, ok := value.(string)
			if !ok {
				return core.StepOutputEntry{}, fmt.Errorf("path must be a string")
			}
			entry.Path = strings.TrimSpace(str)
		case "decode":
			str, ok := value.(string)
			if !ok {
				return core.StepOutputEntry{}, fmt.Errorf("decode must be a string")
			}
			entry.Decode = strings.TrimSpace(str)
		case "select":
			str, ok := value.(string)
			if !ok {
				return core.StepOutputEntry{}, fmt.Errorf("select must be a string")
			}
			entry.Select = strings.TrimSpace(str)
		default:
			return core.StepOutputEntry{}, fmt.Errorf("unknown field %q", key)
		}
	}

	if entry.HasValue && entry.From != "" {
		return core.StepOutputEntry{}, fmt.Errorf("value and from cannot be used together")
	}
	if !entry.HasValue && entry.From == "" {
		return core.StepOutputEntry{}, fmt.Errorf("entry must specify either a literal value or from")
	}
	if entry.HasValue {
		if entry.Path != "" || entry.Decode != "" || entry.Select != "" {
			return core.StepOutputEntry{}, fmt.Errorf("path, decode, and select are only valid with from")
		}
		return entry, nil
	}

	switch entry.From {
	case core.StepOutputSourceStdout, core.StepOutputSourceStderr:
		if entry.Path != "" {
			return core.StepOutputEntry{}, fmt.Errorf("path is only valid when from is file")
		}
	case core.StepOutputSourceFile:
		if entry.Path == "" {
			return core.StepOutputEntry{}, fmt.Errorf("path is required when from is file")
		}
	default:
		return core.StepOutputEntry{}, fmt.Errorf("from must be one of %q, %q, or %q",
			core.StepOutputSourceStdout, core.StepOutputSourceStderr, core.StepOutputSourceFile)
	}

	switch entry.Decode {
	case "", core.StepOutputDecodeText, core.StepOutputDecodeJSON, core.StepOutputDecodeYAML:
	default:
		return core.StepOutputEntry{}, fmt.Errorf("decode must be one of %q, %q, or %q",
			core.StepOutputDecodeText, core.StepOutputDecodeJSON, core.StepOutputDecodeYAML)
	}

	if entry.Select != "" && entry.Decode != core.StepOutputDecodeJSON && entry.Decode != core.StepOutputDecodeYAML {
		return core.StepOutputEntry{}, fmt.Errorf("select requires decode to be %q or %q",
			core.StepOutputDecodeJSON, core.StepOutputDecodeYAML)
	}

	return entry, nil
}

func buildStepOutput(_ StepBuildContext, s *step) (string, error) {
	cfg, err := s.parsedOutputConfig()
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", nil
	}
	return cfg.Name, nil
}

func buildStepStructuredOutput(_ StepBuildContext, s *step) (map[string]core.StepOutputEntry, error) {
	cfg, err := s.parsedOutputConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return cfg.StructuredOutput, nil
}

func buildStepOutputSchema(_ StepBuildContext, s *step) (map[string]any, error) {
	if s.OutputSchema == nil {
		return nil, nil
	}
	schemaMap, err := resolveOutputSchemaDeclaration("output_schema", s.OutputSchema)
	if err != nil {
		return nil, err
	}
	return schemaMap, nil
}

func buildStepEnvs(_ StepBuildContext, s *step) ([]string, error) {
	if s.Env.IsZero() {
		return nil, nil
	}
	var envs []string
	for _, entry := range s.Env.Entries() {
		envs = append(envs, fmt.Sprintf("%s=%s", entry.Key, entry.Value))
	}
	return envs, nil
}

func buildStepPreconditions(ctx StepBuildContext, s *step) ([]*core.Condition, error) {
	return parsePrecondition(ctx.BuildContext, s.Preconditions)
}

// buildStepCommand parses the command field in the step definition.
func buildStepCommand(_ StepBuildContext, s *step, result *core.Step) error {
	if s.Exec != nil {
		if s.Command != nil {
			return core.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with command"))
		}
		if strings.TrimSpace(s.Script) != "" {
			return core.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with script"))
		}
		if !s.Shell.IsZero() {
			return core.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with shell"))
		}
		if len(s.ShellPackages) > 0 {
			return core.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with shell_packages"))
		}
		if result.ExecutorConfig.Type != "" && result.ExecutorConfig.Type != "command" && result.ExecutorConfig.Type != "shell" {
			return core.NewValidationError("exec", s.Exec, fmt.Errorf("exec is only supported for direct command execution"))
		}
		return buildExecCommand(s.Exec, result)
	}

	command := s.Command

	// Case 1: command is nil
	if command == nil {
		return nil
	}

	switch val := command.(type) {
	case string:
		// Case 2: command is a string (single command)
		return buildSingleCommand(val, result)

	case []any:
		// Case 3: command is an array (multiple commands)
		return buildMultipleCommands(val, result)

	default:
		return core.NewValidationError("command", val, ErrStepCommandMustBeArrayOrString)
	}
}

func buildExecCommand(spec *execSpec, result *core.Step) error {
	if spec == nil {
		return nil
	}

	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return core.NewValidationError("exec.command", spec.Command, ErrStepCommandIsEmpty)
	}

	args := make([]string, 0, len(spec.Args))
	for i, arg := range spec.Args {
		switch v := arg.(type) {
		case string:
			args = append(args, v)
		case int, int64, uint64, float64, bool:
			args = append(args, fmt.Sprintf("%v", v))
		default:
			return core.NewValidationError(
				fmt.Sprintf("exec.args[%d]", i),
				arg,
				fmt.Errorf("exec args must be strings or primitive values, got %T", arg),
			)
		}
	}

	result.Shell = "direct"
	result.ShellArgs = nil
	result.Commands = []core.CommandEntry{{
		Command:     command,
		Args:        args,
		CmdWithArgs: command + buildDisplayArgsSuffix(args),
	}}
	return nil
}

func buildDisplayArgsSuffix(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return " " + strings.Join(args, " ")
}

// buildSingleCommand parses a single command string and populates the Step fields.
func buildSingleCommand(val string, result *core.Step) error {
	val = strings.TrimSpace(val)
	if val == "" {
		return core.NewValidationError("command", val, ErrStepCommandIsEmpty)
	}

	// Harness uses command as a prompt, so preserve multiline text as a single
	// command entry instead of reclassifying it as an inline script.
	if strings.Contains(val, "\n") && result.ExecutorConfig.Type == "harness" {
		result.Commands = []core.CommandEntry{
			{
				CmdWithArgs: val,
			},
		}
		return nil
	}

	// If the value is multi-line, treat it as a script
	if strings.Contains(val, "\n") {
		result.Script = val
		return nil
	}

	// We need to split the command into command and args.
	cmd, args, err := cmdutil.SplitCommand(val)
	if err != nil {
		return core.NewValidationError("command", val, fmt.Errorf("failed to parse command: %w", err))
	}

	cmd = strings.TrimSpace(cmd)

	result.Commands = []core.CommandEntry{
		{
			Command:     cmd,
			Args:        args,
			CmdWithArgs: val,
		},
	}

	return nil
}

// buildMultipleCommands parses an array of commands and populates the Step.Commands field.
// Each array element is treated as a separate command to be executed sequentially.
func buildMultipleCommands(val []any, result *core.Step) error {
	if len(val) == 0 {
		return core.NewValidationError("command", val, ErrStepCommandIsEmpty)
	}

	var commands []core.CommandEntry

	for i, v := range val {
		var strVal string
		switch tv := v.(type) {
		case string:
			strVal = tv
		case int, int64, uint64, float64, bool:
			strVal = fmt.Sprintf("%v", tv)
		case map[string]any:
			if len(tv) == 1 {
				for k, val := range tv {
					switch v2 := val.(type) {
					case string, int, int64, uint64, float64, bool:
						strVal = fmt.Sprintf("%s: %v", k, v2)
					default:
						// Nested maps or arrays are too complex, fall through to error
						return core.NewValidationError(
							fmt.Sprintf("command[%d]", i),
							v,
							fmt.Errorf("command array elements must be strings. If this contains a colon, wrap it in quotes. Got nested %T", v2),
						)
					}
				}
			} else {
				return core.NewValidationError(
					fmt.Sprintf("command[%d]", i),
					v,
					fmt.Errorf("command array elements must be strings. If this contains a colon, wrap it in quotes"),
				)
			}
		default:
			return core.NewValidationError(
				fmt.Sprintf("command[%d]", i),
				v,
				fmt.Errorf("command array elements must be strings or primitive types, got %T", v),
			)
		}
		strVal = strings.TrimSpace(strVal)

		if strVal == "" {
			continue // Skip empty commands
		}

		// Parse the command string to extract command and args
		cmd, args, err := cmdutil.SplitCommand(strVal)
		if err != nil {
			return core.NewValidationError(
				fmt.Sprintf("command[%d]", i),
				strVal,
				fmt.Errorf("failed to parse command: %w", err),
			)
		}

		commands = append(commands, core.CommandEntry{
			Command:     strings.TrimSpace(cmd),
			Args:        args,
			CmdWithArgs: strVal,
		})
	}

	if len(commands) == 0 {
		return core.NewValidationError("command", val, ErrStepCommandIsEmpty)
	}

	result.Commands = commands

	return nil
}

// validateCommand checks if the executor type supports the command field.
func validateCommand(result *core.Step) error {
	if len(result.Commands) == 0 {
		return nil
	}
	if !core.SupportsCommand(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"command",
			result.Commands,
			fmt.Errorf("action %q does not support command field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateMultipleCommands checks if the executor type supports multiple commands.
// Returns an error if multiple commands are specified for an executor that doesn't support them.
func validateMultipleCommands(result *core.Step) error {
	if len(result.Commands) <= 1 {
		return nil
	}
	if !core.SupportsMultipleCommands(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"command",
			result.Commands,
			multipleCommandsUnsupportedError{action: result.ExecutorConfig.Type},
		)
	}
	return nil
}

type multipleCommandsUnsupportedError struct {
	action string
}

func (e multipleCommandsUnsupportedError) Error() string {
	return fmt.Sprintf("action %q supports only one command", e.action)
}

func (e multipleCommandsUnsupportedError) Unwrap() error {
	return ErrExecutorDoesNotSupportMultipleCmd
}

func isStepTypeValidationError(err error) bool {
	var validationErr *core.ValidationError
	return errors.As(err, &validationErr) && validationErr.Field == "type"
}

// validateScript checks if the executor type supports the script field.
func validateScript(result *core.Step) error {
	if result.Script == "" {
		return nil
	}
	if !core.SupportsScript(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"script",
			result.Script,
			fmt.Errorf("action %q does not support script field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateShell checks if the executor type supports shell configuration.
func validateShell(result *core.Step) error {
	if result.Shell == "" && len(result.ShellArgs) == 0 && len(result.ShellPackages) == 0 {
		return nil
	}
	if !core.SupportsShell(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"shell",
			result.Shell,
			fmt.Errorf("action %q does not support shell configuration", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateContainer checks if the executor type supports the container field.
func validateContainer(result *core.Step) error {
	if result.Container == nil {
		return nil
	}
	if !core.SupportsContainer(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"container",
			result.Container,
			fmt.Errorf("action %q does not support container field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateSubDAG checks if the executor type supports sub-DAG execution.
func validateSubDAG(result *core.Step) error {
	if result.SubDAG == nil {
		return nil
	}
	if !core.SupportsSubDAG(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"call",
			result.SubDAG,
			fmt.Errorf("action %q does not support call field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateWorkerSelector checks if the executor type supports worker selection.
func validateWorkerSelector(result *core.Step) error {
	if len(result.WorkerSelector) == 0 {
		return nil
	}
	if !core.SupportsWorkerSelector(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"worker_selector",
			result.WorkerSelector,
			fmt.Errorf("action %q does not support worker_selector field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateLLM checks if the executor type supports the llm field.
func validateLLM(result *core.Step) error {
	if result.LLM == nil {
		return nil
	}
	if !core.SupportsLLM(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"llm",
			result.LLM,
			fmt.Errorf("action %q does not support llm field; use action: chat with llm configuration", result.ExecutorConfig.Type),
		)
	}

	// When Models array is used, Provider/Model fields are derived from the first entry
	hasModels := len(result.LLM.Models) > 0

	if !hasModels {
		// Single model config (legacy): require both provider and model
		if result.LLM.Provider == "" {
			return core.NewValidationError(
				"llm.provider",
				result.LLM.Provider,
				fmt.Errorf("provider is required (set at DAG or step level)"),
			)
		}
		if result.LLM.Model == "" {
			return core.NewValidationError(
				"llm.model",
				result.LLM.Model,
				fmt.Errorf("model is required (set at DAG or step level)"),
			)
		}
	}

	// Messages are required (at step level)
	if len(result.Messages) == 0 {
		return core.NewValidationError(
			"messages",
			result.Messages,
			fmt.Errorf("at least one message is required"),
		)
	}
	return nil
}

// validateMessages checks if the executor type supports the messages field.
func validateMessages(result *core.Step) error {
	if len(result.Messages) == 0 {
		return nil
	}
	if !core.SupportsLLM(result.ExecutorConfig.Type) && !core.SupportsAgent(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"messages",
			result.Messages,
			fmt.Errorf("action %q does not support messages field; use action: chat or action: agent", result.ExecutorConfig.Type),
		)
	}
	return nil
}

func buildStepParamsField(ctx StepBuildContext, s *step, result *core.Step) error {
	if s.Params == nil {
		return nil
	}

	// Parse params using existing parseParamValue function
	paramPairs, err := parseParamValue(ctx.BuildContext, s.Params)
	if err != nil {
		return core.NewValidationError("params", s.Params, err)
	}

	// Convert to map[string]string
	paramsData := make(map[string]string)
	for _, pair := range paramPairs {
		paramsData[pair.Name] = pair.Value
	}

	result.Params = core.NewSimpleParams(paramsData)
	return nil
}

// buildStepExecutor parses the executor configuration from step fields.
func buildStepExecutor(ctx StepBuildContext, s *step, result *core.Step) error {
	if err := validateStepConfigAliasStruct(s); err != nil {
		return err
	}

	// Step-level type and with/config fields
	if s.Type != "" {
		result.ExecutorConfig.Type = strings.TrimSpace(s.Type)
	}
	stepConfig := s.executorConfig()
	maps.Copy(result.ExecutorConfig.Config, stepConfig)

	// Infer type from container field
	if result.ExecutorConfig.Type == "" && result.Container != nil {
		result.ExecutorConfig.Type = "docker"
		return nil
	}

	// Publish-only steps with object-form output do not need a real executor.
	if shouldInferNoopStep(s, result) {
		result.ExecutorConfig.Type = "noop"
		return nil
	}

	// Infer type from DAG-level configuration
	if result.ExecutorConfig.Type == "" && ctx.dag != nil {
		if ctx.dag.Container != nil {
			result.ExecutorConfig.Type = "container"
		} else if ctx.dag.SSH != nil {
			result.ExecutorConfig.Type = "ssh"
		} else if ctx.dag.Redis != nil {
			result.ExecutorConfig.Type = "redis"
		} else if ctx.dag.Harness != nil {
			result.ExecutorConfig.Type = "harness"
		}
	}

	// Merge DAG-level Redis config into step config (step takes precedence)
	if result.ExecutorConfig.Type == "redis" && ctx.dag != nil && ctx.dag.Redis != nil {
		mergeRedisConfig(ctx.dag.Redis, result.ExecutorConfig.Config)
	}
	if result.ExecutorConfig.Type == "harness" && ctx.dag != nil && ctx.dag.Harness != nil {
		result.ExecutorConfig.Config = mergeHarnessConfig(ctx.dag.Harness, stepConfig)
	}
	if isKubernetesExecutorType(result.ExecutorConfig.Type) && ctx.dag != nil && ctx.dag.Kubernetes != nil {
		result.ExecutorConfig.Config = mergeKubernetesExecutorConfig(ctx.dag.Kubernetes, result.ExecutorConfig.Config)
	}
	if result.ExecutorConfig.Type != "" && !isBuiltinStepTypeName(result.ExecutorConfig.Type) {
		return core.NewValidationError(
			"type",
			result.ExecutorConfig.Type,
			fmt.Errorf("unknown action %q", result.ExecutorConfig.Type),
		)
	}
	if result.ExecutorConfig.Type == "harness" {
		var defs core.HarnessDefinitions
		if ctx.dag != nil {
			defs = ctx.dag.Harnesses
		}
		if err := validateHarnessProviderConfig(defs, result.ExecutorConfig.Config); err != nil {
			return err
		}
		fallbacks, err := extractHarnessFallback(cloneHarnessSpecMap(result.ExecutorConfig.Config))
		if err != nil {
			return err
		}
		for i := range fallbacks {
			if err := validateHarnessProviderConfig(defs, fallbacks[i]); err != nil {
				return fmt.Errorf("harness: invalid fallback[%d]: %w", i, err)
			}
		}
	}

	return nil
}

func shouldInferNoopStep(s *step, result *core.Step) bool {
	if result.ExecutorConfig.Type != "" || !result.HasStructuredOutput() {
		return false
	}
	if result.UsesStructuredOutputSource(core.StepOutputSourceStdout) ||
		result.UsesStructuredOutputSource(core.StepOutputSourceStderr) {
		return false
	}
	if result.Container != nil || result.SubDAG != nil || result.Parallel != nil {
		return false
	}
	if s == nil {
		return false
	}
	return s.Command == nil && s.Exec == nil && strings.TrimSpace(s.Script) == ""
}

// mergeRedisConfig merges DAG-level Redis defaults into step config.
// Step-level values take precedence over DAG-level defaults.
func mergeRedisConfig(dagRedis *core.RedisConfig, stepConfig map[string]any) {
	setIfMissing := func(key string, value any) {
		if _, exists := stepConfig[key]; !exists && !isRedisZeroValue(value) {
			stepConfig[key] = value
		}
	}

	setIfMissing("url", dagRedis.URL)
	setIfMissing("host", dagRedis.Host)
	setIfMissing("port", dagRedis.Port)
	setIfMissing("password", dagRedis.Password)
	setIfMissing("username", dagRedis.Username)
	setIfMissing("db", dagRedis.DB)
	setIfMissing("tls", dagRedis.TLS)
	setIfMissing("tls_skip_verify", dagRedis.TLSSkipVerify)
	setIfMissing("mode", dagRedis.Mode)
	setIfMissing("sentinel_master", dagRedis.SentinelMaster)
	setIfMissing("sentinel_addrs", dagRedis.SentinelAddrs)
	setIfMissing("cluster_addrs", dagRedis.ClusterAddrs)
	setIfMissing("max_retries", dagRedis.MaxRetries)
}

func mergeHarnessConfig(dagHarness *core.HarnessConfig, stepConfig map[string]any) map[string]any {
	effectiveProvider := harnessProviderName(stepConfig)
	if effectiveProvider == "" && dagHarness != nil {
		effectiveProvider = harnessProviderName(dagHarness.Config)
	}
	if core.IsBuiltinHarnessProvider(effectiveProvider) {
		stepConfig = core.NormalizeBuiltinHarnessFlagKeys(stepConfig)
	}

	merged := cloneHarnessSpecMap(stepConfig)
	if merged == nil {
		merged = make(map[string]any)
	}

	if dagHarness == nil {
		return merged
	}

	dagConfig := dagHarness.Config
	if core.IsBuiltinHarnessProvider(effectiveProvider) {
		dagConfig = core.NormalizeBuiltinHarnessFlagKeys(dagConfig)
	}

	for key, value := range dagConfig {
		if _, exists := merged[key]; !exists {
			merged[key] = cloneHarnessSpecValue(value)
		}
	}

	if _, exists := stepConfig["fallback"]; exists {
		merged["fallback"] = cloneHarnessSpecValue(stepConfig["fallback"])
	} else if dagHarness.Fallback != nil {
		merged["fallback"] = cloneHarnessSpecValue(dagHarness.Fallback)
	}

	return merged
}

func harnessProviderName(cfg map[string]any) string {
	if cfg == nil {
		return ""
	}
	provider, _ := cfg["provider"].(string)
	return strings.TrimSpace(provider)
}

// isRedisZeroValue checks if a value is a zero value for Redis config merging.
func isRedisZeroValue(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case int:
		return val == 0
	case bool:
		return !val
	case []string:
		return len(val) == 0
	default:
		return false
	}
}

// buildStepParallel parses the parallel field in the step definition.
func buildStepParallel(_ StepBuildContext, s *step, result *core.Step) error {
	if s.Parallel == nil {
		return nil
	}

	result.Parallel = &core.ParallelConfig{
		MaxConcurrent: core.DefaultMaxConcurrent,
	}

	switch v := s.Parallel.(type) {
	case string:
		// Direct variable reference like: parallel: ${ITEMS}
		result.Parallel.Variable = v

	case []any:
		// Static array: parallel: [item1, item2]
		items, err := parseParallelItems(v)
		if err != nil {
			return core.NewValidationError("parallel", v, err)
		}
		result.Parallel.Items = items

	case map[string]any:
		// Object configuration
		for key, val := range v {
			switch key {
			case "items":
				switch itemsVal := val.(type) {
				case string:
					result.Parallel.Variable = itemsVal
				case []any:
					items, err := parseParallelItems(itemsVal)
					if err != nil {
						return core.NewValidationError("parallel.items", itemsVal, err)
					}
					result.Parallel.Items = items
				default:
					return core.NewValidationError("parallel.items", val, fmt.Errorf("parallel.items must be string or array, got %T", val))
				}

			case "max_concurrent":
				switch mc := val.(type) {
				case int:
					result.Parallel.MaxConcurrent = mc
				case int64:
					result.Parallel.MaxConcurrent = int(mc)
				case uint64:
					if mc > math.MaxInt {
						return core.NewValidationError("parallel.max_concurrent", mc, fmt.Errorf("value %d exceeds maximum int", mc))
					}
					result.Parallel.MaxConcurrent = int(mc)
				case float64:
					result.Parallel.MaxConcurrent = int(mc)
				default:
					return core.NewValidationError("parallel.max_concurrent", val, fmt.Errorf("parallel.max_concurrent must be int, got %T", val))
				}
			}
		}

	default:
		return core.NewValidationError("parallel", v, fmt.Errorf("parallel must be string, array, or object, got %T", v))
	}

	return nil
}

// buildStepContainer parses the container field in the step definition.
func buildStepContainer(ctx StepBuildContext, s *step, result *core.Step) error {
	if s.Container == nil {
		return nil
	}

	ct, err := buildContainerField(ctx.BuildContext, s.Container)
	if err != nil {
		return err
	}

	result.Container = ct
	return nil
}

// buildStepLLM parses the LLM configuration in the step definition.
// Note: This only populates result.LLM. The executor type must be set explicitly
// via type: chat in YAML (no auto-detection).
// If step has no llm: config but DAG has one, the DAG config is inherited.
// If step has llm: config, it completely overrides DAG-level (full override pattern).
func buildStepLLM(ctx StepBuildContext, s *step, result *core.Step) error {
	// Only process LLM for executors that support it
	if !core.SupportsLLM(result.ExecutorConfig.Type) {
		return nil
	}

	// If step has no LLM config, inherit from DAG
	if s.LLM == nil {
		if ctx.dag != nil && ctx.dag.LLM != nil {
			result.LLM = ctx.dag.LLM
		}
		return nil
	}

	// Step has explicit llm: config - use it (full override of DAG-level)
	cfg := s.LLM

	// Validate provider if specified (for single model config)
	if cfg.Provider != "" {
		if _, err := llm.ParseProviderType(cfg.Provider); err != nil {
			return core.NewValidationError("llm.provider", cfg.Provider, err)
		}
	}

	// Model is required when llm config is provided
	if cfg.Model.IsZero() {
		return core.NewValidationError("llm.model", nil,
			fmt.Errorf("model must be specified when llm config is provided"))
	}

	// Get model string or entries from the parsed value
	var modelString string
	var models []core.ModelEntry

	if cfg.Model.IsArray() {
		var err error
		models, err = convertModelEntries(cfg.Model.Entries())
		if err != nil {
			return err
		}
	} else {
		modelString = cfg.Model.String()
		if modelString == "" {
			return core.NewValidationError("llm.model", cfg.Model.Value(),
				fmt.Errorf("model must be specified when llm config is provided"))
		}
	}

	// Validate temperature range
	if cfg.Temperature != nil {
		if *cfg.Temperature < 0.0 || *cfg.Temperature > 2.0 {
			return core.NewValidationError("llm.temperature", *cfg.Temperature,
				fmt.Errorf("temperature must be between 0.0 and 2.0"))
		}
	}

	// Validate max_tokens if specified
	if cfg.MaxTokens != nil {
		if *cfg.MaxTokens < 1 {
			return core.NewValidationError("llm.max_tokens", *cfg.MaxTokens,
				fmt.Errorf("max_tokens must be at least 1"))
		}
	}

	// Validate top_p range
	if cfg.TopP != nil {
		if *cfg.TopP < 0.0 || *cfg.TopP > 1.0 {
			return core.NewValidationError("llm.top_p", *cfg.TopP,
				fmt.Errorf("top_p must be between 0.0 and 1.0"))
		}
	}

	thinking, err := buildThinkingConfig(cfg.Thinking)
	if err != nil {
		return err
	}

	result.LLM = &core.LLMConfig{
		Provider:          cfg.Provider,
		Model:             modelString,
		Models:            models,
		System:            cfg.System,
		Temperature:       cfg.Temperature,
		MaxTokens:         cfg.MaxTokens,
		TopP:              cfg.TopP,
		BaseURL:           cfg.BaseURL,
		APIKeyName:        cfg.APIKeyName,
		Stream:            cfg.Stream,
		Thinking:          thinking,
		Tools:             cfg.Tools,
		MaxToolIterations: cfg.MaxToolIterations,
		WebSearch:         buildWebSearchConfig(cfg.WebSearch),
	}

	return nil
}

// buildWebSearchConfig converts webSearchConfig to core.WebSearchConfig.
func buildWebSearchConfig(cfg *webSearchConfig) *core.WebSearchConfig {
	if cfg == nil {
		return nil
	}
	result := &core.WebSearchConfig{
		Enabled:        cfg.Enabled,
		MaxUses:        cfg.MaxUses,
		AllowedDomains: cfg.AllowedDomains,
		BlockedDomains: cfg.BlockedDomains,
	}
	if cfg.UserLocation != nil {
		result.UserLocation = &core.WebSearchUserLocation{
			City:     cfg.UserLocation.City,
			Region:   cfg.UserLocation.Region,
			Country:  cfg.UserLocation.Country,
			Timezone: cfg.UserLocation.Timezone,
		}
	}
	return result
}

// convertModelEntries converts types.ModelEntry slice to core.ModelEntry slice with validation.
func convertModelEntries(entries []types.ModelEntry) ([]core.ModelEntry, error) {
	models := make([]core.ModelEntry, len(entries))
	for i, e := range entries {
		if _, err := llm.ParseProviderType(e.Provider); err != nil {
			return nil, core.NewValidationError(fmt.Sprintf("llm.model[%d].provider", i), e.Provider, err)
		}
		models[i] = core.ModelEntry{
			Provider:    e.Provider,
			Name:        e.Name,
			Temperature: e.Temperature,
			MaxTokens:   e.MaxTokens,
			TopP:        e.TopP,
			BaseURL:     e.BaseURL,
			APIKeyName:  e.APIKeyName,
		}
	}
	return models, nil
}

// buildThinkingConfig converts thinkingConfig to core.ThinkingConfig.
func buildThinkingConfig(cfg *thinkingConfig) (*core.ThinkingConfig, error) {
	if cfg == nil {
		return nil, nil
	}
	effort, err := core.ParseThinkingEffort(cfg.Effort)
	if err != nil {
		return nil, core.NewValidationError("thinking.effort", cfg.Effort, err)
	}
	return &core.ThinkingConfig{
		Enabled:         cfg.Enabled,
		Effort:          effort,
		BudgetTokens:    cfg.BudgetTokens,
		IncludeInOutput: cfg.IncludeInOutput,
	}, nil
}

// buildStepMessages parses the messages field for chat steps.
func buildStepMessages(s *step, result *core.Step) error {
	if len(s.Messages) == 0 {
		return nil
	}

	result.Messages = make([]core.LLMMessage, len(s.Messages))
	for i, msg := range s.Messages {
		if msg.Role == "" {
			return core.NewValidationError(
				fmt.Sprintf("messages[%d].role", i), msg.Role,
				fmt.Errorf("role is required"))
		}
		role, err := core.ParseLLMRole(msg.Role)
		if err != nil {
			return core.NewValidationError(
				fmt.Sprintf("messages[%d].role", i), msg.Role, err)
		}
		if msg.Content == "" {
			return core.NewValidationError(
				fmt.Sprintf("messages[%d].content", i), msg.Content,
				fmt.Errorf("content is required"))
		}
		result.Messages[i] = core.LLMMessage{
			Role:    role,
			Content: msg.Content,
		}
	}

	return nil
}

// buildStepRouter parses the router configuration from step fields.
func buildStepRouter(_ StepBuildContext, s *step, result *core.Step) error {
	if s.Type != "router" {
		return nil
	}

	// Trim and validate value
	s.Value = strings.TrimSpace(s.Value)
	if s.Value == "" {
		return core.NewValidationError("value", nil,
			fmt.Errorf("router step requires 'value' field"))
	}
	if len(s.Routes) == 0 {
		return core.NewValidationError("routes", nil,
			fmt.Errorf("router step requires at least one route"))
	}

	// Convert map to ordered entries
	var routes []core.RouteEntry
	for pattern, targets := range s.Routes {
		// Trim and validate pattern
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return core.NewValidationError("routes", nil,
				fmt.Errorf("route pattern cannot be empty"))
		}

		if len(targets) == 0 {
			return core.NewValidationError("routes", pattern,
				fmt.Errorf("route pattern %q has no targets", pattern))
		}

		// Trim and validate each target
		var trimmedTargets []string
		for _, target := range targets {
			target = strings.TrimSpace(target)
			if target == "" {
				return core.NewValidationError("routes", pattern,
					fmt.Errorf("route pattern %q has empty target", pattern))
			}
			trimmedTargets = append(trimmedTargets, target)
		}

		routes = append(routes, core.RouteEntry{
			Pattern: pattern,
			Targets: trimmedTargets,
		})
	}

	// Sort: exact matches first, then regex (catch-all "re:.*" last)
	sort.Slice(routes, func(i, j int) bool {
		iIsRegex := strings.HasPrefix(routes[i].Pattern, "re:")
		jIsRegex := strings.HasPrefix(routes[j].Pattern, "re:")
		if iIsRegex != jIsRegex {
			return !iIsRegex // exact matches first
		}
		// Catch-all patterns last
		if routes[i].Pattern == "re:.*" {
			return false
		}
		if routes[j].Pattern == "re:.*" {
			return true
		}
		return routes[i].Pattern < routes[j].Pattern
	})

	result.Router = &core.RouterConfig{
		Value:  s.Value,
		Routes: routes,
	}
	result.ExecutorConfig.Type = core.ExecutorTypeRouter

	return nil
}

// buildStepAgent parses the agent configuration from step fields.
func buildStepAgent(_ StepBuildContext, s *step, result *core.Step) error {
	if !core.SupportsAgent(result.ExecutorConfig.Type) {
		if s.Agent != nil {
			return core.NewValidationError("agent", result.ExecutorConfig.Type,
				fmt.Errorf("agent configuration is only valid for steps with type %q", core.ExecutorTypeAgent))
		}
		return nil
	}

	cfg := &core.AgentStepConfig{
		SafeMode:      true, // default: safe mode enabled
		MaxIterations: 50,   // default: 50 iterations
	}

	if s.Agent != nil {
		cfg.Model = strings.TrimSpace(s.Agent.Model)
		cfg.Prompt = s.Agent.Prompt

		if s.Agent.MaxIterations != nil {
			cfg.MaxIterations = *s.Agent.MaxIterations
			if cfg.MaxIterations < 1 {
				return core.NewValidationError("agent.max_iterations", cfg.MaxIterations,
					fmt.Errorf("must be at least 1"))
			}
		}
		if s.Agent.SafeMode != nil {
			cfg.SafeMode = *s.Agent.SafeMode
		}

		if s.Agent.Tools != nil {
			cfg.Tools = &core.AgentToolsConfig{
				Enabled: s.Agent.Tools.Enabled,
			}
			if s.Agent.Tools.BashPolicy != nil {
				bp := s.Agent.Tools.BashPolicy
				cfg.Tools.BashPolicy = &core.AgentBashPolicy{
					DefaultBehavior: bp.DefaultBehavior,
					DenyBehavior:    bp.DenyBehavior,
				}
				for _, r := range bp.Rules {
					cfg.Tools.BashPolicy.Rules = append(cfg.Tools.BashPolicy.Rules, core.AgentBashRule{
						Name:    r.Name,
						Pattern: r.Pattern,
						Action:  r.Action,
					})
				}
			}
		}

		if len(s.Agent.Skills) > 0 {
			cfg.Skills = s.Agent.Skills
		}

		cfg.Soul = strings.TrimSpace(s.Agent.Soul)

		if s.Agent.Memory != nil {
			cfg.Memory = &core.AgentMemoryConfig{
				Enabled: s.Agent.Memory.Enabled,
			}
		}

		cfg.WebSearch = buildWebSearchConfig(s.Agent.WebSearch)
	}

	result.Agent = cfg
	return nil
}

// validSlugIDRegexp matches a valid slug ID: lowercase alphanumeric segments separated by hyphens.
// Duplicated from agent.validSlugRegexp to avoid an import cycle (spec -> agent -> spec).
var validSlugIDRegexp = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxSlugIDLength = 128

// validateAgent checks that agent steps have required configuration.
func validateAgent(result *core.Step) error {
	if result.Agent == nil {
		return nil
	}
	if len(result.Messages) == 0 {
		return core.NewValidationError(
			"messages",
			result.Messages,
			fmt.Errorf("agent step requires at least one message"),
		)
	}
	for _, id := range result.Agent.Skills {
		if id == "" || len(id) > maxSlugIDLength || !validSlugIDRegexp.MatchString(id) {
			return core.NewValidationError("agent.skills", id,
				fmt.Errorf("invalid skill ID %q: must be lowercase alphanumeric with hyphens, max %d chars", id, maxSlugIDLength))
		}
	}
	if result.Agent.Soul != "" {
		if len(result.Agent.Soul) > maxSlugIDLength || !validSlugIDRegexp.MatchString(result.Agent.Soul) {
			return core.NewValidationError("agent.soul", result.Agent.Soul,
				fmt.Errorf("invalid soul ID %q: must be lowercase alphanumeric with hyphens, max %d chars", result.Agent.Soul, maxSlugIDLength))
		}
	}
	return nil
}

// buildStepApproval parses the approval configuration for a step.
func buildStepApproval(_ StepBuildContext, s *step, result *core.Step) error {
	if s.Approval == nil {
		return nil
	}
	result.Approval = &core.ApprovalConfig{
		Prompt:   s.Approval.Prompt,
		Input:    s.Approval.Input,
		Required: s.Approval.Required,
		RewindTo: strings.TrimSpace(s.Approval.RewindTo),
	}
	// Validate required fields are subset of input
	for _, req := range result.Approval.Required {
		if !slices.Contains(result.Approval.Input, req) {
			return fmt.Errorf("required field %q is not in input list", req)
		}
	}
	return nil
}

// buildStepSubDAG parses the child core.DAG definition and sets up the step to run a sub DAG.
func buildStepSubDAG(ctx StepBuildContext, s *step, result *core.Step) error {
	name := strings.TrimSpace(s.Call)

	// if the call field is not set, return nil.
	if name == "" {
		return nil
	}

	// Parse params similar to how core.DAG params are parsed
	var paramsStr string
	if s.Params != nil {
		// Parse the params to convert them to string format
		ctxCopy := ctx
		ctxCopy.opts.Flags |= BuildFlagNoEval // Disable evaluation for params parsing
		paramPairs, err := parseParamValue(ctxCopy.BuildContext, s.Params)
		if err != nil {
			return core.NewValidationError("params", s.Params, err)
		}

		// Convert to string format "key=value key=value ..."
		// For string-style params, positional params (no name) use SmartEscape
		// to avoid quoting variable references like ${ITEM.xxx} — their
		// expanded content should be re-split into separate KEY=VALUE pairs
		// at runtime. Named params always use Escaped to preserve their
		// values as single tokens after expansion.
		_, isStringParams := s.Params.(string)
		var paramsToJoin []string
		for _, paramPair := range paramPairs {
			if isStringParams && paramPair.Name == "" {
				paramsToJoin = append(paramsToJoin, paramPair.SmartEscape())
			} else {
				paramsToJoin = append(paramsToJoin, paramPair.Escaped())
			}
		}
		paramsStr = strings.Join(paramsToJoin, " ")
	}

	result.SubDAG = &core.SubDAG{Name: name, Params: paramsStr}

	// Set executor type based on whether parallel execution is configured
	if result.Parallel != nil {
		result.ExecutorConfig.Type = core.ExecutorTypeParallel
	} else {
		result.ExecutorConfig.Type = core.ExecutorTypeDAG
	}

	return nil
}

// parseParallelItems converts an array of any type to core.ParallelItem slice
func parseParallelItems(items []any) ([]core.ParallelItem, error) {
	var result []core.ParallelItem

	for _, item := range items {
		switch v := item.(type) {
		case string:
			result = append(result, core.ParallelItem{Value: v})

		case int, int64, uint64, float64:
			result = append(result, core.ParallelItem{Value: fmt.Sprintf("%v", v)})

		case map[string]any:
			params := make(collections.DeterministicMap)
			for key, val := range v {
				var strVal string
				switch pv := val.(type) {
				case string:
					strVal = pv
				case int:
					strVal = fmt.Sprintf("%d", pv)
				case int64:
					strVal = fmt.Sprintf("%d", pv)
				case uint64:
					strVal = fmt.Sprintf("%d", pv)
				case float64:
					strVal = fmt.Sprintf("%g", pv)
				case bool:
					strVal = fmt.Sprintf("%t", pv)
				default:
					return nil, fmt.Errorf("parameter values must be strings, numbers, or booleans, got %T for key %s", val, key)
				}
				params[key] = strVal
			}
			result = append(result, core.ParallelItem{Params: params})

		default:
			return nil, fmt.Errorf("parallel items must be strings, numbers, or objects, got %T", v)
		}
	}

	return result, nil
}
