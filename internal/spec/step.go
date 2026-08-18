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
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/signal"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/spec/types"
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
	// Outputs declares named step outputs.
	Outputs any `yaml:"outputs,omitempty"`
	// Inputs declares named regular-file inputs for build execution.
	Inputs any `yaml:"inputs,omitempty"`
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
	// Foreach specifies inline item-body iteration configuration.
	Foreach any `yaml:"foreach,omitempty"`
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

	// LLM contains the configuration for LLM-based executors.
	// Requires explicit type: chat.
	LLM *llmConfig `yaml:"llm,omitempty"`

	// Messages contains the session messages for chat steps.
	// Only valid when type is "chat".
	Messages []llmMessage `yaml:"messages,omitempty"`

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
	outputsSet         bool
	inputsSet          bool
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
	return ir.NewValidationError(
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
	// Array example: [{provider: openai, name: gpt-4o}, {provider: anthropic, name: claude-sonnet-4-6}]
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
	// MaxContextTokens activates agent observation aging at this prompt size.
	// Zero disables proactive aging.
	MaxContextTokens *int `yaml:"max_context_tokens,omitempty"`
	// ObservationMaxBytes limits one agent observation in bytes. Zero disables
	// the limit.
	ObservationMaxBytes *int `yaml:"observation_max_bytes,omitempty"`
	// ObservationKeepRecent controls how many recent observations remain complete.
	// Zero disables observation aging.
	ObservationKeepRecent *int `yaml:"observation_keep_recent,omitempty"`
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

// stepTransform builds one part of a step and applies it to the result. name
// identifies the spec field in build errors.
type stepTransform struct {
	name  string
	apply func(ctx stepBuildContext, in *step, out *ir.Step) error
}

// stepField describes a spec field whose built value is assigned to a single
// field of the result.
func stepField[T any](
	name string,
	build func(stepBuildContext, *step) (T, error),
	assign func(*ir.Step, T),
) stepTransform {
	return stepTransform{
		name: name,
		apply: func(ctx stepBuildContext, in *step, out *ir.Step) error {
			v, err := build(ctx, in)
			if err != nil {
				return err
			}
			assign(out, v)
			return nil
		},
	}
}

// stepOutputRedirectField parses one output redirect declaration and assigns
// every result field derived from it. Parsing once keeps a malformed
// declaration to a single error.
func stepOutputRedirectField(
	name string,
	raw func(*step) any,
	allowOutputs bool,
	assign func(*ir.Step, stepOutputRedirect),
) stepTransform {
	return stepTransform{
		name: name,
		apply: func(_ stepBuildContext, in *step, out *ir.Step) error {
			redirect, err := buildStepOutputRedirect(name, raw(in), allowOutputs)
			if err != nil {
				return err
			}
			assign(out, redirect)
			return nil
		},
	}
}

// stepShellField parses the shell declaration once and assigns both the shell
// and its arguments.
func stepShellField() stepTransform {
	return stepTransform{
		name: "shell",
		apply: func(ctx stepBuildContext, in *step, out *ir.Step) error {
			result, err := parseStepShellInternal(ctx, in)
			if err != nil {
				return err
			}
			out.Shell = result.Shell
			out.ShellArgs = result.Args
			if in.ShellArgs != nil {
				args := append([]string{}, result.Args...)
				out.ShellArgs = append(args, in.ShellArgs...)
			}
			return nil
		},
	}
}

type stepTransformStage []stepTransform

var stepIdentityStage = stepTransformStage{
	stepField("name", buildStepName, func(out *ir.Step, v string) { out.Name = v }),
	stepField("id", buildStepID, func(out *ir.Step, v string) { out.ID = v }),
	stepField("description", buildStepDescription, func(out *ir.Step, v string) { out.Description = v }),
}

var stepScriptStage = stepTransformStage{
	stepField("shell_packages", buildStepShellPackages, func(out *ir.Step, v []string) { out.ShellPackages = v }),
	stepField("script", buildStepScript, func(out *ir.Step, v string) { out.Script = v }),
}

var stepLogOutputStage = stepTransformStage{
	stepOutputRedirectField("stdout", func(s *step) any { return s.Stdout }, true,
		func(out *ir.Step, v stepOutputRedirect) {
			out.Stdout = v.filePath
			out.StdoutArtifact = v.artifactPath
			out.StdoutOutputs = v.outputs
		}),
	stepOutputRedirectField("stderr", func(s *step) any { return s.Stderr }, false,
		func(out *ir.Step, v stepOutputRedirect) {
			out.Stderr = v.filePath
			out.StderrArtifact = v.artifactPath
		}),
	stepField("log_output", buildStepLogOutput, func(out *ir.Step, v ir.LogOutputMode) { out.LogOutput = v }),
}

var stepExecutionPlacementStage = stepTransformStage{
	stepField("mail_on_error", buildStepMailOnError, func(out *ir.Step, v bool) { out.MailOnError = v }),
	stepField("worker_selector", buildStepWorkerSelector, func(out *ir.Step, v map[string]string) { out.WorkerSelector = v }),
	stepField("working_dir", buildStepWorkingDir, func(out *ir.Step, v string) { out.Dir = v }),
	stepShellField(),
	stepField("timeout", buildStepTimeout, func(out *ir.Step, v time.Duration) { out.Timeout = v }),
	stepField("depends", buildStepDepends, func(out *ir.Step, v []string) { out.Depends = v }),
	stepField("explicitly_no_deps", buildStepExplicitlyNoDeps, func(out *ir.Step, v bool) { out.ExplicitlyNoDeps = v }),
	stepField("continue_on", buildStepContinueOn, func(out *ir.Step, v ir.ContinueOn) { out.ContinueOn = v }),
	stepField("retry_policy", buildStepRetryPolicy, func(out *ir.Step, v ir.RetryPolicy) { out.RetryPolicy = v }),
	stepField("repeat_policy", buildStepRepeatPolicy, func(out *ir.Step, v ir.RepeatPolicy) { out.RepeatPolicy = v }),
	stepField("signal_on_stop", buildStepSignalOnStop, func(out *ir.Step, v string) { out.SignalOnStop = v }),
}

var stepStructuredOutputStage = stepTransformStage{
	stepField("output", buildStepOutput, func(out *ir.Step, v string) { out.Output = v }),
	stepField("structured_output", buildStepStructuredOutput, func(out *ir.Step, v map[string]ir.StepOutputEntry) { out.StructuredOutput = v }),
	stepField("output_schema", buildStepOutputSchema, func(out *ir.Step, v map[string]any) { out.OutputSchema = v }),
	stepField("outputs", buildStepDeclaredOutputs, func(out *ir.Step, v []ir.StepOutputDeclaration) { out.Outputs = v }),
	stepField("inputs", buildStepDeclaredInputs, func(out *ir.Step, v []ir.StepInputDeclaration) { out.Inputs = v }),
}

var stepEnvConditionStage = stepTransformStage{
	stepField("env", buildStepEnvs, func(out *ir.Step, v []string) { out.Env = v }),
	stepField("preconditions", buildStepPreconditions, func(out *ir.Step, v []*ir.Condition) { out.Preconditions = v }),
}

var stepTransformStages = []stepTransformStage{
	stepIdentityStage,
	stepScriptStage,
	stepLogOutputStage,
	stepExecutionPlacementStage,
	stepStructuredOutputStage,
	stepEnvConditionStage,
}

// runStepTransformers executes all step transformers
func runStepTransformers(ctx stepBuildContext, spec *step, result *ir.Step) ir.ErrorList {
	var errs ir.ErrorList

	for _, stage := range stepTransformStages {
		for _, t := range stage {
			if err := t.apply(ctx, spec, result); err != nil {
				errs = append(errs, wrapTransformError(t.name, err))
			}
		}
	}

	return errs
}

type stepActionBuilder struct {
	name                     string
	build                    func(stepBuildContext, *step, *ir.Step) error
	stopOnStepTypeValidation bool
}

type stepActionStage []stepActionBuilder

var stepExecutionTargetStage = stepActionStage{
	{"container", buildStepContainer, false},
	{"parallel", buildStepParallel, false},
	{"foreach", nil, false},
	{"subDAG", buildStepSubDAG, false},
	{"human_task", buildStepHumanTask, false},
	{"executor", buildStepExecutor, true},
}

func init() {
	for idx := range stepExecutionTargetStage {
		if stepExecutionTargetStage[idx].name == "foreach" {
			stepExecutionTargetStage[idx].build = buildStepForeach
			return
		}
	}
}

var stepInteractionActionStage = stepActionStage{
	// LLM must be after executor so we know if type supports LLM.
	{"llm", buildStepLLM, false},
	{"messages", func(_ stepBuildContext, s *step, result *ir.Step) error {
		return buildStepMessages(s, result)
	}, false},
	{"router", buildStepRouter, false},
	{"approval", buildStepApproval, false},
}

var stepCommandActionStage = stepActionStage{
	{"command", buildStepCommand, false},
	{"params", buildStepParamsField, false},
}

var stepActionStages = []stepActionStage{
	stepExecutionTargetStage,
	stepInteractionActionStage,
	stepCommandActionStage,
}

func runStepActionStages(ctx stepBuildContext, spec *step, result *ir.Step) (ir.ErrorList, bool) {
	var errs ir.ErrorList
	for _, stage := range stepActionStages {
		for _, builder := range stage {
			if err := builder.build(ctx, spec, result); err != nil {
				errs = append(errs, wrapTransformError(builder.name, err))
				if builder.stopOnStepTypeValidation && isStepTypeValidationError(err) {
					return errs, true
				}
			}
		}
	}
	return errs, false
}

type stepValidation struct {
	name     string
	validate func(*ir.Step) error
}

type stepValidationStage []stepValidation

var stepCommandValidationStage = stepValidationStage{
	{"command", validateCommand},
	{"command", validateMultipleCommands},
	{"script", validateScript},
	{"shell", validateShell},
}

var stepExecutionValidationStage = stepValidationStage{
	{"container", validateContainer},
	{"dag", validateSubDAG},
	{"worker_selector", validateWorkerSelector},
}

var stepInteractionValidationStage = stepValidationStage{
	{"llm", validateLLM},
	{"messages", validateMessages},
}

var stepValidationStages = []stepValidationStage{
	stepCommandValidationStage,
	stepExecutionValidationStage,
	stepInteractionValidationStage,
}

func runStepValidationStages(result *ir.Step) ir.ErrorList {
	var errs ir.ErrorList
	for _, stage := range stepValidationStages {
		for _, validation := range stage {
			if err := validation.validate(result); err != nil {
				errs = append(errs, wrapTransformError(validation.name, err))
			}
		}
	}
	return errs
}

// build transforms the step specification into a ir.Step.
func (s *step) build(ctx stepBuildContext) (*ir.Step, error) {
	if err := validateStepConfigAliasStruct(s); err != nil {
		return nil, err
	}

	result := &ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Config: make(map[string]any)},
	}

	// Run the transformer pipeline
	errs := runStepTransformers(ctx, s, result)

	actionErrs, stop := runStepActionStages(ctx, s, result)
	errs = append(errs, actionErrs...)
	if stop {
		return nil, errs
	}

	// Final validators run after the executor type is determined
	// Capabilities-based validators handle all execution type conflicts
	errs = append(errs, runStepValidationStages(result)...)

	// Validate executor config against registered schema
	// Only validate when config has actual values (not just initialized as empty map)
	if len(result.ExecutorConfig.Config) > 0 {
		if err := registry.ValidateExecutorConfig(result.ExecutorConfig.Type, result.ExecutorConfig.Config); err != nil {
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
func validateStdoutStderr(s *ir.Step) error {
	if s.Stdout != "" && s.Stderr != "" && s.Stdout == s.Stderr {
		return fmt.Errorf("stdout and stderr cannot point to the same file %q; use 'log_output: merged' instead", s.Stdout)
	}
	if s.StdoutArtifact != "" && s.StderrArtifact != "" && s.StdoutArtifact == s.StderrArtifact {
		return fmt.Errorf("stdout.artifact and stderr.artifact cannot point to the same file %q; use 'log_output: merged' instead", s.StdoutArtifact)
	}
	return nil
}

// Simple field builders

func buildStepName(_ stepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.Name), nil
}

func buildStepID(_ stepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.ID), nil
}

func buildStepDescription(_ stepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.Description), nil
}

func buildStepShellPackages(_ stepBuildContext, s *step) ([]string, error) {
	return s.ShellPackages, nil
}

func buildStepScript(_ stepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.Script), nil
}

type stepOutputRedirect struct {
	filePath     string
	artifactPath string
	outputs      *ir.StepOutputsConfig
}

func buildStepOutputRedirect(
	field string,
	raw any,
	allowOutputs bool,
) (stepOutputRedirect, error) {
	switch v := raw.(type) {
	case nil:
		return stepOutputRedirect{}, nil
	case string:
		return stepOutputRedirect{filePath: strings.TrimSpace(v)}, nil
	case map[string]any:
		return parseStepObjectOutputRedirect(field, v, allowOutputs)
	case map[any]any:
		converted := make(map[string]any, len(v))
		for key, value := range v {
			keyString, ok := key.(string)
			if !ok {
				return stepOutputRedirect{}, fmt.Errorf("%s object keys must be strings", field)
			}
			converted[keyString] = value
		}
		return parseStepObjectOutputRedirect(field, converted, allowOutputs)
	default:
		return stepOutputRedirect{}, fmt.Errorf("%s must be a string path or an object", field)
	}
}

func parseStepObjectOutputRedirect(
	field string,
	raw map[string]any,
	allowOutputs bool,
) (stepOutputRedirect, error) {
	if len(raw) == 0 {
		return stepOutputRedirect{}, fmt.Errorf("%s object must not be empty", field)
	}
	var artifactPath string
	var outputs *ir.StepOutputsConfig
	for key, value := range raw {
		switch key {
		case "artifact":
			artifact, ok := value.(string)
			if !ok {
				return stepOutputRedirect{}, fmt.Errorf("%s.artifact must be a string", field)
			}
			clean, err := cleanStepArtifactPath(artifact)
			if err != nil {
				return stepOutputRedirect{}, fmt.Errorf("%s.artifact: %w", field, err)
			}
			artifactPath = clean
		case "outputs":
			if !allowOutputs {
				return stepOutputRedirect{}, fmt.Errorf("%s.outputs is not supported", field)
			}
			cfg, err := parseStdoutOutputsConfig(value)
			if err != nil {
				return stepOutputRedirect{}, fmt.Errorf("%s.outputs: %w", field, err)
			}
			outputs = cfg
		default:
			if allowOutputs {
				return stepOutputRedirect{}, fmt.Errorf("%s object supports only artifact and outputs", field)
			}
			return stepOutputRedirect{}, fmt.Errorf("%s object supports only artifact", field)
		}
	}
	if artifactPath == "" && outputs == nil {
		return stepOutputRedirect{}, fmt.Errorf("%s object must contain artifact or outputs", field)
	}
	return stepOutputRedirect{artifactPath: artifactPath, outputs: outputs}, nil
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

func buildStepLogOutput(_ stepBuildContext, s *step) (ir.LogOutputMode, error) {
	if s.LogOutput.IsZero() {
		// Return empty string to indicate "inherit from DAG"
		return "", nil
	}
	return s.LogOutput.Mode(), nil
}

func buildStepMailOnError(_ stepBuildContext, s *step) (bool, error) {
	return s.MailOnError, nil
}

func buildStepWorkerSelector(_ stepBuildContext, s *step) (map[string]string, error) {
	return s.WorkerSelector, nil
}

func buildStepWorkingDir(_ stepBuildContext, s *step) (string, error) {
	return strings.TrimSpace(s.WorkingDir), nil
}

// stepShellResult holds both shell and args for step
type stepShellResult struct {
	Shell string
	Args  []string
}

func parseStepShellInternal(_ stepBuildContext, s *step) (*stepShellResult, error) {
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
		return nil, ir.NewValidationError("shell", s.Shell.Value(), fmt.Errorf("failed to parse shell command: %w", err))
	}
	return &stepShellResult{
		Shell: strings.TrimSpace(shell),
		Args:  args,
	}, nil
}

func buildStepTimeout(_ stepBuildContext, s *step) (time.Duration, error) {
	if s.TimeoutSec < 0 {
		return 0, ir.NewValidationError("timeout_sec", s.TimeoutSec, ErrTimeoutSecMustBeNonNegative)
	}
	return time.Second * time.Duration(s.TimeoutSec), nil
}

func buildStepDepends(_ stepBuildContext, s *step) ([]string, error) {
	return s.Depends.Values(), nil
}

func buildStepExplicitlyNoDeps(_ stepBuildContext, s *step) (bool, error) {
	return !s.Depends.IsZero() && s.Depends.IsEmpty(), nil
}

func buildStepContinueOn(_ stepBuildContext, s *step) (ir.ContinueOn, error) {
	if s.ContinueOn.IsZero() {
		return ir.ContinueOn{}, nil
	}

	return ir.ContinueOn{
		Skipped:     s.ContinueOn.Skipped(),
		Failure:     s.ContinueOn.Failed(),
		MarkSuccess: s.ContinueOn.MarkSuccess(),
		ExitCode:    s.ContinueOn.ExitCode(),
		Output:      s.ContinueOn.Output(),
	}, nil
}

func buildStepRetryPolicy(_ stepBuildContext, s *step) (ir.RetryPolicy, error) {
	if s.RetryPolicy == nil {
		return ir.RetryPolicy{}, nil
	}

	var result ir.RetryPolicy
	var err error

	// Step retry keeps string values so they can be resolved later at runtime.
	result.Limit, result.LimitStr, err = parseStepRetryLimit(s.RetryPolicy.Limit)
	if err != nil {
		return ir.RetryPolicy{}, err
	}

	result.Interval, result.IntervalSecStr, err = parseStepRetryInterval(s.RetryPolicy.IntervalSec)
	if err != nil {
		return ir.RetryPolicy{}, err
	}

	if s.RetryPolicy.ExitCode != nil {
		result.ExitCodes = s.RetryPolicy.ExitCode
	}

	// Parse backoff field
	backoff, err := parseBackoffValue(s.RetryPolicy.Backoff, "retry_policy.backoff")
	if err != nil {
		return ir.RetryPolicy{}, ir.NewValidationError("retry_policy.backoff", s.RetryPolicy.Backoff, err)
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
			return 0, "", ir.NewValidationError("retry_policy.limit", v, fmt.Errorf("value %d exceeds maximum int", v))
		}
		return int(v), "", nil
	case string:
		return 0, v, nil
	case nil:
		return 0, "", ir.NewValidationError("retry_policy.limit", nil, fmt.Errorf("limit is required when retry_policy is specified"))
	default:
		return 0, "", ir.NewValidationError("retry_policy.limit", v, fmt.Errorf("invalid type: %T", v))
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
			return 0, "", ir.NewValidationError("retry_policy.interval_sec", v, fmt.Errorf("value %d exceeds maximum int64", v))
		}
		return time.Second * time.Duration(v), "", nil
	case string:
		return 0, v, nil
	case nil:
		return 0, "", ir.NewValidationError("retry_policy.interval_sec", nil, fmt.Errorf("interval_sec is required when retry_policy is specified"))
	default:
		return 0, "", ir.NewValidationError("retry_policy.interval_sec", v, fmt.Errorf("invalid type: %T", v))
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

func buildStepRepeatPolicy(_ stepBuildContext, s *step) (ir.RepeatPolicy, error) {
	if s.RepeatPolicy == nil {
		return ir.RepeatPolicy{}, nil
	}
	rp := s.RepeatPolicy

	// Determine repeat mode from typed RepeatMode field
	var mode ir.RepeatMode
	if rp.Repeat.IsSet() {
		switch rp.Repeat.String() {
		case "while":
			mode = ir.RepeatModeWhile
		case "until":
			mode = ir.RepeatModeUntil
		}
	}

	// Backward compatibility: infer mode if not set
	if mode == "" {
		if rp.Condition != "" && rp.Expected != "" {
			mode = ir.RepeatModeUntil
		} else if rp.Condition != "" || len(rp.ExitCode) > 0 {
			mode = ir.RepeatModeWhile
		}
	}

	// No repeat if mode is not determined
	if mode == "" {
		return ir.RepeatPolicy{}, nil
	}

	// Validate that explicit string while/until modes have appropriate conditions
	// (bool true is allowed without conditions for backward compatibility)
	if rp.Repeat.IsSet() && !rp.Repeat.IsBool() {
		m := rp.Repeat.String()
		if (m == "while" || m == "until") && rp.Condition == "" && len(rp.ExitCode) == 0 {
			return ir.RepeatPolicy{}, fmt.Errorf("repeat mode '%s' requires either 'condition' or 'exit_code' to be specified", m)
		}
	}

	var result ir.RepeatPolicy
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
		result.Condition = &ir.Condition{
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

func buildStepSignalOnStop(_ stepBuildContext, s *step) (string, error) {
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
	StructuredOutput map[string]ir.StepOutputEntry
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

var stdoutOutputsConfigFields = map[string]struct{}{
	"field":  {},
	"decode": {},
	"select": {},
	"fields": {},
}

var declaredOutputNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

var declaredOutputFields = map[string]struct{}{
	"name": {},
	"type": {},
	"path": {},
}

func parseDeclaredOutputs(raw any) ([]ir.StepOutputDeclaration, error) {
	if raw == nil {
		return nil, fmt.Errorf("outputs must be a non-empty sequence")
	}

	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("outputs must be a non-empty sequence")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("outputs must be a non-empty sequence")
	}

	result := make([]ir.StepOutputDeclaration, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for idx, item := range items {
		obj, err := declaredOutputItemMap(item)
		if err != nil {
			return nil, fmt.Errorf("outputs[%d]: %w", idx, err)
		}
		for key := range obj {
			if _, ok := declaredOutputFields[key]; !ok {
				return nil, fmt.Errorf("outputs[%d]: unknown field %q", idx, key)
			}
		}

		nameRaw, ok := obj["name"]
		if !ok {
			return nil, fmt.Errorf("outputs[%d]: name is required", idx)
		}
		name, ok := nameRaw.(string)
		if !ok {
			return nil, fmt.Errorf("outputs[%d]: name must be a string", idx)
		}
		name = strings.TrimSpace(name)
		if !declaredOutputNamePattern.MatchString(name) {
			return nil, fmt.Errorf("outputs[%d]: name must match %q", idx, declaredOutputNamePattern.String())
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("outputs[%d]: duplicate output name %q", idx, name)
		}
		seen[name] = struct{}{}

		outputPath := ""
		if rawPath, ok := obj["path"]; ok {
			str, ok := rawPath.(string)
			if !ok {
				return nil, fmt.Errorf("outputs[%d]: path must be a string", idx)
			}
			outputPath = strings.TrimSpace(str)
			if outputPath == "" {
				return nil, fmt.Errorf("outputs[%d]: path must not be empty", idx)
			}
		}

		outputType := ir.StepDeclaredOutputTypeString
		if rawType, ok := obj["type"]; ok {
			if outputPath != "" {
				return nil, fmt.Errorf("outputs[%d]: type and path cannot be used together", idx)
			}
			str, ok := rawType.(string)
			if !ok {
				return nil, fmt.Errorf("outputs[%d]: type must be a string", idx)
			}
			outputType = strings.TrimSpace(str)
			switch outputType {
			case ir.StepDeclaredOutputTypeString, ir.StepDeclaredOutputTypeJSON:
			default:
				return nil, fmt.Errorf("outputs[%d]: type must be %q or %q",
					idx, ir.StepDeclaredOutputTypeString, ir.StepDeclaredOutputTypeJSON)
			}
		}
		if outputPath != "" {
			outputType = ""
		}

		result = append(result, ir.StepOutputDeclaration{
			Name: name,
			Type: outputType,
			Path: outputPath,
		})
	}
	return result, nil
}

func buildStepDeclaredInputs(_ stepBuildContext, s *step) ([]ir.StepInputDeclaration, error) {
	if !s.inputsSet {
		return nil, nil
	}
	if strings.TrimSpace(s.ID) == "" {
		return nil, fmt.Errorf("a step with inputs must define id")
	}
	items, ok := s.Inputs.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("inputs must be a non-empty sequence")
	}
	result := make([]ir.StepInputDeclaration, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for idx, item := range items {
		obj, err := declaredOutputItemMap(item)
		if err != nil {
			return nil, fmt.Errorf("inputs[%d]: %w", idx, err)
		}
		for key := range obj {
			if key != "name" && key != "path" {
				return nil, fmt.Errorf("inputs[%d]: unknown field %q", idx, key)
			}
		}
		nameRaw, ok := obj["name"]
		if !ok {
			return nil, fmt.Errorf("inputs[%d]: name is required", idx)
		}
		name, nameOK := nameRaw.(string)
		if !nameOK {
			return nil, fmt.Errorf("inputs[%d]: name must be a string", idx)
		}
		name = strings.TrimSpace(name)
		if !declaredOutputNamePattern.MatchString(name) {
			return nil, fmt.Errorf("inputs[%d]: name must match %q", idx, declaredOutputNamePattern.String())
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("inputs[%d]: duplicate input name %q", idx, name)
		}
		pathRaw, ok := obj["path"]
		if !ok {
			return nil, fmt.Errorf("inputs[%d]: path is required", idx)
		}
		pathValue, pathOK := pathRaw.(string)
		if !pathOK {
			return nil, fmt.Errorf("inputs[%d]: path must be a string", idx)
		}
		pathValue = strings.TrimSpace(pathValue)
		if pathValue == "" {
			return nil, fmt.Errorf("inputs[%d]: path must not be empty", idx)
		}
		seen[name] = struct{}{}
		result = append(result, ir.StepInputDeclaration{Name: name, Path: pathValue})
	}
	return result, nil
}

func declaredOutputItemMap(raw any) (map[string]any, error) {
	switch v := raw.(type) {
	case map[string]any:
		if len(v) == 0 {
			return nil, fmt.Errorf("item must not be empty")
		}
		return v, nil
	case map[any]any:
		if len(v) == 0 {
			return nil, fmt.Errorf("item must not be empty")
		}
		converted := make(map[string]any, len(v))
		for key, value := range v {
			keyString, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("item keys must be strings")
			}
			converted[keyString] = value
		}
		return converted, nil
	default:
		return nil, fmt.Errorf("item must be an object")
	}
}

func parseStdoutOutputsConfig(raw any) (*ir.StepOutputsConfig, error) {
	switch v := raw.(type) {
	case nil:
		return nil, fmt.Errorf("must not be null")
	case string:
		field := strings.TrimSpace(v)
		if field == "" {
			return nil, fmt.Errorf("field must not be empty")
		}
		return &ir.StepOutputsConfig{Field: field}, nil
	case map[string]any:
		return parseStdoutOutputsConfigMap(v)
	case map[any]any:
		converted := make(map[string]any, len(v))
		for key, value := range v {
			keyString, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("object keys must be strings")
			}
			converted[keyString] = value
		}
		return parseStdoutOutputsConfigMap(converted)
	default:
		return nil, fmt.Errorf("must be a string field name or object, got %T", raw)
	}
}

func parseStdoutOutputsConfigMap(raw map[string]any) (*ir.StepOutputsConfig, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("object must not be empty")
	}
	for key := range raw {
		if _, ok := stdoutOutputsConfigFields[key]; !ok {
			return nil, fmt.Errorf("unknown field %q", key)
		}
	}

	var cfg ir.StepOutputsConfig
	if field, ok := raw["field"]; ok {
		str, ok := field.(string)
		if !ok || strings.TrimSpace(str) == "" {
			return nil, fmt.Errorf("field must be a non-empty string")
		}
		cfg.Field = strings.TrimSpace(str)
	}
	if decode, ok := raw["decode"]; ok {
		str, ok := decode.(string)
		if !ok {
			return nil, fmt.Errorf("decode must be a string")
		}
		cfg.Decode = strings.TrimSpace(str)
	}
	if selectPath, ok := raw["select"]; ok {
		str, ok := selectPath.(string)
		if !ok {
			return nil, fmt.Errorf("select must be a string")
		}
		cfg.Select = strings.TrimSpace(str)
	}
	if fieldsRaw, ok := raw["fields"]; ok {
		fields, err := parseStdoutOutputsFields(fieldsRaw)
		if err != nil {
			return nil, err
		}
		cfg.Fields = fields
	}

	if len(cfg.Fields) > 0 && (cfg.Field != "" || cfg.Decode != "" || cfg.Select != "") {
		return nil, fmt.Errorf("fields cannot be used with field, decode, or select")
	}
	if cfg.Decode == "" && cfg.Select != "" {
		cfg.Decode = ir.StepOutputDecodeJSON
	}
	switch cfg.Decode {
	case "", ir.StepOutputDecodeText, ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML:
	default:
		return nil, fmt.Errorf("decode must be one of %q, %q, or %q",
			ir.StepOutputDecodeText, ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML)
	}
	if cfg.Select != "" && cfg.Decode != ir.StepOutputDecodeJSON && cfg.Decode != ir.StepOutputDecodeYAML {
		return nil, fmt.Errorf("select requires decode to be %q or %q",
			ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML)
	}
	return &cfg, nil
}

func parseStdoutOutputsFields(raw any) (map[string]ir.StepOutputEntry, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		if anyMap, ok := raw.(map[any]any); ok {
			obj = make(map[string]any, len(anyMap))
			for key, value := range anyMap {
				keyString, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("fields object keys must be strings")
				}
				obj[keyString] = value
			}
		} else {
			return nil, fmt.Errorf("fields must be an object")
		}
	}
	if len(obj) == 0 {
		return nil, fmt.Errorf("fields must not be empty")
	}
	fields := make(map[string]ir.StepOutputEntry, len(obj))
	for key, value := range obj {
		name := strings.TrimSpace(key)
		if name == "" {
			return nil, fmt.Errorf("fields contains an empty name")
		}
		entry, err := parseStdoutOutputsFieldEntry(value)
		if err != nil {
			return nil, fmt.Errorf("fields.%s: %w", name, err)
		}
		fields[name] = entry
	}
	return fields, nil
}

func parseStdoutOutputsFieldEntry(raw any) (ir.StepOutputEntry, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		if anyMap, ok := raw.(map[any]any); ok {
			obj = make(map[string]any, len(anyMap))
			for key, value := range anyMap {
				keyString, ok := key.(string)
				if !ok {
					return ir.StepOutputEntry{}, fmt.Errorf("object keys must be strings")
				}
				obj[keyString] = value
			}
		} else {
			return ir.StepOutputEntry{HasValue: true, Value: raw}, nil
		}
	}
	if _, hasFrom := obj["from"]; hasFrom {
		entry, err := parseStructuredOutputEntry(obj)
		if err != nil {
			return ir.StepOutputEntry{}, err
		}
		if entry.From != ir.StepOutputSourceStdout {
			return ir.StepOutputEntry{}, fmt.Errorf("from must be %q", ir.StepOutputSourceStdout)
		}
		return entry, nil
	}
	if _, hasValue := obj["value"]; hasValue {
		return parseStructuredOutputEntry(obj)
	}

	entry := ir.StepOutputEntry{From: ir.StepOutputSourceStdout}
	for key, value := range obj {
		switch key {
		case "decode":
			str, ok := value.(string)
			if !ok {
				return ir.StepOutputEntry{}, fmt.Errorf("decode must be a string")
			}
			entry.Decode = strings.TrimSpace(str)
		case "select":
			str, ok := value.(string)
			if !ok {
				return ir.StepOutputEntry{}, fmt.Errorf("select must be a string")
			}
			entry.Select = strings.TrimSpace(str)
		default:
			return ir.StepOutputEntry{}, fmt.Errorf("unknown field %q", key)
		}
	}
	if entry.Decode == "" && entry.Select != "" {
		entry.Decode = ir.StepOutputDecodeJSON
	}
	switch entry.Decode {
	case "", ir.StepOutputDecodeText, ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML:
	default:
		return ir.StepOutputEntry{}, fmt.Errorf("decode must be one of %q, %q, or %q",
			ir.StepOutputDecodeText, ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML)
	}
	if entry.Select != "" && entry.Decode != ir.StepOutputDecodeJSON && entry.Decode != ir.StepOutputDecodeYAML {
		return ir.StepOutputEntry{}, fmt.Errorf("select requires decode to be %q or %q",
			ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML)
	}
	return entry, nil
}

func parseStructuredOutput(raw map[string]any) (map[string]ir.StepOutputEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	entries := make(map[string]ir.StepOutputEntry, len(raw))
	for key, value := range raw {
		entry, err := parseStructuredOutputEntry(value)
		if err != nil {
			return nil, fmt.Errorf("output.%s: %w", key, err)
		}
		entries[key] = entry
	}
	return entries, nil
}

func parseStructuredOutputEntry(raw any) (ir.StepOutputEntry, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return ir.StepOutputEntry{
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
		return ir.StepOutputEntry{
			HasValue: true,
			Value:    obj,
		}, nil
	}

	var entry ir.StepOutputEntry
	for key, value := range obj {
		switch key {
		case "value":
			entry.HasValue = true
			entry.Value = value
		case "from":
			str, ok := value.(string)
			if !ok {
				return ir.StepOutputEntry{}, fmt.Errorf("from must be a string")
			}
			entry.From = strings.TrimSpace(str)
		case "path":
			str, ok := value.(string)
			if !ok {
				return ir.StepOutputEntry{}, fmt.Errorf("path must be a string")
			}
			entry.Path = strings.TrimSpace(str)
		case "decode":
			str, ok := value.(string)
			if !ok {
				return ir.StepOutputEntry{}, fmt.Errorf("decode must be a string")
			}
			entry.Decode = strings.TrimSpace(str)
		case "select":
			str, ok := value.(string)
			if !ok {
				return ir.StepOutputEntry{}, fmt.Errorf("select must be a string")
			}
			entry.Select = strings.TrimSpace(str)
		default:
			return ir.StepOutputEntry{}, fmt.Errorf("unknown field %q", key)
		}
	}

	if entry.HasValue && entry.From != "" {
		return ir.StepOutputEntry{}, fmt.Errorf("value and from cannot be used together")
	}
	if !entry.HasValue && entry.From == "" {
		return ir.StepOutputEntry{}, fmt.Errorf("entry must specify either a literal value or from")
	}
	if entry.HasValue {
		if entry.Path != "" || entry.Decode != "" || entry.Select != "" {
			return ir.StepOutputEntry{}, fmt.Errorf("path, decode, and select are only valid with from")
		}
		return entry, nil
	}

	switch entry.From {
	case ir.StepOutputSourceStdout, ir.StepOutputSourceStderr:
		if entry.Path != "" {
			return ir.StepOutputEntry{}, fmt.Errorf("path is only valid when from is file")
		}
	case ir.StepOutputSourceFile:
		if entry.Path == "" {
			return ir.StepOutputEntry{}, fmt.Errorf("path is required when from is file")
		}
	default:
		return ir.StepOutputEntry{}, fmt.Errorf("from must be one of %q, %q, or %q",
			ir.StepOutputSourceStdout, ir.StepOutputSourceStderr, ir.StepOutputSourceFile)
	}

	switch entry.Decode {
	case "", ir.StepOutputDecodeText, ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML:
	default:
		return ir.StepOutputEntry{}, fmt.Errorf("decode must be one of %q, %q, or %q",
			ir.StepOutputDecodeText, ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML)
	}

	if entry.Select != "" && entry.Decode != ir.StepOutputDecodeJSON && entry.Decode != ir.StepOutputDecodeYAML {
		return ir.StepOutputEntry{}, fmt.Errorf("select requires decode to be %q or %q",
			ir.StepOutputDecodeJSON, ir.StepOutputDecodeYAML)
	}

	return entry, nil
}

func buildStepOutput(_ stepBuildContext, s *step) (string, error) {
	cfg, err := s.parsedOutputConfig()
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", nil
	}
	return cfg.Name, nil
}

func buildStepStructuredOutput(_ stepBuildContext, s *step) (map[string]ir.StepOutputEntry, error) {
	cfg, err := s.parsedOutputConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return cfg.StructuredOutput, nil
}

func buildStepOutputSchema(_ stepBuildContext, s *step) (map[string]any, error) {
	if s.OutputSchema == nil {
		return nil, nil
	}
	schemaMap, err := resolveOutputSchemaDeclaration("output_schema", s.OutputSchema)
	if err != nil {
		return nil, err
	}
	return schemaMap, nil
}

func buildStepDeclaredOutputs(_ stepBuildContext, s *step) ([]ir.StepOutputDeclaration, error) {
	if !s.outputsSet {
		return nil, nil
	}
	if strings.TrimSpace(s.ID) == "" {
		return nil, fmt.Errorf("a step with outputs must define id")
	}
	return parseDeclaredOutputs(s.Outputs)
}

func buildStepEnvs(_ stepBuildContext, s *step) ([]string, error) {
	if s.Env.IsZero() {
		return nil, nil
	}
	var envs []string
	for i, entry := range s.Env.Entries() {
		if !cmnvalue.ValidEnvName(entry.Key) {
			return nil, ir.NewValidationError(
				"env",
				entry.Key,
				fmt.Errorf("%w: invalid environment variable name %q at env[%d]", ErrInvalidEnvValue, entry.Key, i),
			)
		}
		envs = append(envs, fmt.Sprintf("%s=%s", entry.Key, entry.Value))
	}
	return envs, nil
}

func buildStepPreconditions(ctx stepBuildContext, s *step) ([]*ir.Condition, error) {
	return parsePrecondition(ctx.buildContext, s.Preconditions)
}

// buildStepCommand parses the command field in the step definition.
func buildStepCommand(_ stepBuildContext, s *step, result *ir.Step) error {
	if s.Exec != nil {
		if s.Command != nil {
			return ir.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with command"))
		}
		if strings.TrimSpace(s.Script) != "" {
			return ir.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with script"))
		}
		if !s.Shell.IsZero() {
			return ir.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with shell"))
		}
		if len(s.ShellPackages) > 0 {
			return ir.NewValidationError("exec", s.Exec, fmt.Errorf("exec cannot be used together with shell_packages"))
		}
		if result.ExecutorConfig.Type != "" && result.ExecutorConfig.Type != "command" && result.ExecutorConfig.Type != "shell" {
			return ir.NewValidationError("exec", s.Exec, fmt.Errorf("exec is only supported for direct command execution"))
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
		return ir.NewValidationError("command", val, ErrStepCommandMustBeArrayOrString)
	}
}

func buildExecCommand(spec *execSpec, result *ir.Step) error {
	if spec == nil {
		return nil
	}

	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return ir.NewValidationError("exec.command", spec.Command, ErrStepCommandIsEmpty)
	}

	args := make([]string, 0, len(spec.Args))
	for i, arg := range spec.Args {
		switch v := arg.(type) {
		case string:
			args = append(args, v)
		case int, int64, uint64, float64, bool:
			args = append(args, fmt.Sprintf("%v", v))
		default:
			return ir.NewValidationError(
				fmt.Sprintf("exec.args[%d]", i),
				arg,
				fmt.Errorf("exec args must be strings or primitive values, got %T", arg),
			)
		}
	}

	result.Shell = "direct"
	result.ShellArgs = nil
	result.Commands = []ir.CommandEntry{{
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
func buildSingleCommand(val string, result *ir.Step) error {
	raw := val
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ir.NewValidationError("command", raw, ErrStepCommandIsEmpty)
	}

	// Harness uses command as a prompt, so preserve multiline text as a single
	// command entry instead of reclassifying it as an inline script.
	if strings.Contains(raw, "\n") && result.ExecutorConfig.Type == "harness" {
		result.Commands = []ir.CommandEntry{
			{
				CmdWithArgs: raw,
			},
		}
		return nil
	}

	// If the value is multi-line, treat it as a script
	if strings.Contains(raw, "\n") {
		result.Script = raw
		return nil
	}

	val = trimmed

	// We need to split the command into command and args.
	cmd, args, err := cmdutil.SplitCommand(val)
	if err != nil {
		return ir.NewValidationError("command", val, fmt.Errorf("failed to parse command: %w", err))
	}

	cmd = strings.TrimSpace(cmd)

	result.Commands = []ir.CommandEntry{
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
func buildMultipleCommands(val []any, result *ir.Step) error {
	if len(val) == 0 {
		return ir.NewValidationError("command", val, ErrStepCommandIsEmpty)
	}

	var commands []ir.CommandEntry

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
						return ir.NewValidationError(
							fmt.Sprintf("command[%d]", i),
							v,
							fmt.Errorf("command array elements must be strings. If this contains a colon, wrap it in quotes. Got nested %T", v2),
						)
					}
				}
			} else {
				return ir.NewValidationError(
					fmt.Sprintf("command[%d]", i),
					v,
					fmt.Errorf("command array elements must be strings. If this contains a colon, wrap it in quotes"),
				)
			}
		default:
			return ir.NewValidationError(
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
			return ir.NewValidationError(
				fmt.Sprintf("command[%d]", i),
				strVal,
				fmt.Errorf("failed to parse command: %w", err),
			)
		}

		commands = append(commands, ir.CommandEntry{
			Command:     strings.TrimSpace(cmd),
			Args:        args,
			CmdWithArgs: strVal,
		})
	}

	if len(commands) == 0 {
		return ir.NewValidationError("command", val, ErrStepCommandIsEmpty)
	}

	result.Commands = commands

	return nil
}

// validateCommand checks if the executor type supports the command field.
func validateCommand(result *ir.Step) error {
	if len(result.Commands) == 0 {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).Command {
		return ir.NewValidationError(
			"command",
			result.Commands,
			fmt.Errorf("action %q does not support command field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateMultipleCommands checks if the executor type supports multiple commands.
// Returns an error if multiple commands are specified for an executor that doesn't support them.
func validateMultipleCommands(result *ir.Step) error {
	if len(result.Commands) <= 1 {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).MultipleCommands {
		return ir.NewValidationError(
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
	var validationErr *ir.ValidationError
	return errors.As(err, &validationErr) && validationErr.Field == "type"
}

// validateScript checks if the executor type supports the script field.
func validateScript(result *ir.Step) error {
	if result.Script == "" {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).Script {
		return ir.NewValidationError(
			"script",
			result.Script,
			fmt.Errorf("action %q does not support script field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateShell checks if the executor type supports shell configuration.
func validateShell(result *ir.Step) error {
	if result.Shell == "" && len(result.ShellArgs) == 0 && len(result.ShellPackages) == 0 {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).Shell {
		return ir.NewValidationError(
			"shell",
			result.Shell,
			fmt.Errorf("action %q does not support shell configuration", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateContainer checks if the executor type supports the container field.
func validateContainer(result *ir.Step) error {
	if result.Container == nil {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).Container {
		return ir.NewValidationError(
			"container",
			result.Container,
			fmt.Errorf("action %q does not support container field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateSubDAG checks if the executor type supports sub-DAG execution.
func validateSubDAG(result *ir.Step) error {
	if result.SubDAG == nil {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).SubDAG {
		return ir.NewValidationError(
			"call",
			result.SubDAG,
			fmt.Errorf("action %q does not support call field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateWorkerSelector checks if the executor type supports worker selection.
func validateWorkerSelector(result *ir.Step) error {
	if len(result.WorkerSelector) == 0 {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).WorkerSelector {
		return ir.NewValidationError(
			"worker_selector",
			result.WorkerSelector,
			fmt.Errorf("action %q does not support worker_selector field", result.ExecutorConfig.Type),
		)
	}
	return nil
}

// validateLLM checks if the executor type supports the llm field.
func validateLLM(result *ir.Step) error {
	if result.LLM == nil {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).LLM {
		return ir.NewValidationError(
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
			return ir.NewValidationError(
				"llm.provider",
				result.LLM.Provider,
				fmt.Errorf("provider is required (set at DAG or step level)"),
			)
		}
		if result.LLM.Model == "" {
			return ir.NewValidationError(
				"llm.model",
				result.LLM.Model,
				fmt.Errorf("model is required (set at DAG or step level)"),
			)
		}
	}

	// Messages are required (at step level)
	if len(result.Messages) == 0 {
		return ir.NewValidationError(
			"messages",
			result.Messages,
			fmt.Errorf("at least one message is required"),
		)
	}
	return nil
}

// validateMessages checks if the executor type supports the messages field.
func validateMessages(result *ir.Step) error {
	if len(result.Messages) == 0 {
		return nil
	}
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).LLM {
		return ir.NewValidationError(
			"messages",
			result.Messages,
			fmt.Errorf("action %q does not support messages field; use action: chat", result.ExecutorConfig.Type),
		)
	}
	return nil
}

func buildStepParamsField(ctx stepBuildContext, s *step, result *ir.Step) error {
	if s.Params == nil {
		return nil
	}

	// Parse params using existing parseParamValue function
	paramPairs, err := parseParamValue(ctx.buildContext, s.Params)
	if err != nil {
		return ir.NewValidationError("params", s.Params, err)
	}

	// Convert to map[string]string
	paramsData := make(map[string]string)
	for _, pair := range paramPairs {
		paramsData[pair.Name] = pair.Value
	}

	result.Params = ir.NewSimpleParams(paramsData)
	return nil
}

// buildStepExecutor parses the executor configuration from step fields.
func buildStepExecutor(ctx stepBuildContext, s *step, result *ir.Step) error {
	if err := validateStepConfigAliasStruct(s); err != nil {
		return err
	}
	if result.HumanTask != nil {
		return nil
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
		}
		// A DAG-level harness: block is not inferred as a step type. Unlike
		// container and ssh it is not a transport: it reads the step's command
		// as a prompt, so inferring it turns a command into agent input. A
		// harness step names itself with action: harness.run.
	}

	// Merge DAG-level Redis config into step config (step takes precedence)
	if result.ExecutorConfig.Type == "redis" && ctx.dag != nil && ctx.dag.Redis != nil {
		mergeRedisConfig(ctx.dag.Redis, result.ExecutorConfig.Config)
	}
	if result.ExecutorConfig.Type == "harness" && ctx.dag != nil && ctx.dag.Harness != nil {
		result.ExecutorConfig.Config = mergeHarnessConfig(ctx.dag.Harness, ctx.dag.Harnesses, stepConfig)
	}
	if isKubernetesExecutorType(result.ExecutorConfig.Type) && ctx.dag != nil && ctx.dag.Kubernetes != nil {
		result.ExecutorConfig.Config = mergeKubernetesExecutorConfig(ctx.dag.Kubernetes, result.ExecutorConfig.Config)
	}
	if result.ExecutorConfig.Type != "" && !isBuiltinStepTypeName(result.ExecutorConfig.Type) {
		return ir.NewValidationError(
			"type",
			result.ExecutorConfig.Type,
			fmt.Errorf("unknown action %q", result.ExecutorConfig.Type),
		)
	}
	if result.ExecutorConfig.Type == "harness" {
		var defs ir.HarnessDefinitions
		if ctx.dag != nil {
			defs = ctx.dag.Harnesses
		}
		if err := validateHarnessProviderConfig(defs, result.ExecutorConfig.Config); err != nil {
			return err
		}
		fallbacks, err := extractHarnessFallback(cloneMap(result.ExecutorConfig.Config))
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

func shouldInferNoopStep(s *step, result *ir.Step) bool {
	if result.ExecutorConfig.Type != "" || !result.HasStructuredOutput() {
		return false
	}
	if result.UsesStructuredOutputSource(ir.StepOutputSourceStdout) ||
		result.UsesStructuredOutputSource(ir.StepOutputSourceStderr) {
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
func mergeRedisConfig(dagRedis *ir.RedisConfig, stepConfig map[string]any) {
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

func mergeHarnessConfig(dagHarness *ir.HarnessConfig, defs ir.HarnessDefinitions, stepConfig map[string]any) map[string]any {
	effectiveProvider := harnessProviderName(stepConfig)
	if effectiveProvider == "" && dagHarness != nil {
		effectiveProvider = harnessProviderName(dagHarness.Config)
	}
	if ir.IsEffectiveBuiltinCLIHarnessProvider(effectiveProvider, defs) {
		stepConfig = ir.NormalizeBuiltinHarnessFlagKeys(stepConfig)
	}

	merged := cloneMap(stepConfig)
	if merged == nil {
		merged = make(map[string]any)
	}

	if dagHarness == nil {
		return merged
	}

	dagConfig := dagHarness.Config
	if ir.IsEffectiveBuiltinCLIHarnessProvider(effectiveProvider, defs) {
		dagConfig = ir.NormalizeBuiltinHarnessFlagKeys(dagConfig)
	}

	for key, value := range dagConfig {
		if _, exists := merged[key]; !exists {
			merged[key] = cloneAny(value)
		}
	}

	if _, exists := stepConfig["fallback"]; exists {
		merged["fallback"] = cloneAny(stepConfig["fallback"])
	} else if dagHarness.Fallback != nil {
		merged["fallback"] = cloneAny(dagHarness.Fallback)
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
func buildStepParallel(_ stepBuildContext, s *step, result *ir.Step) error {
	if s.Parallel == nil {
		return nil
	}

	result.Parallel = &ir.ParallelConfig{
		MaxConcurrent: ir.DefaultMaxConcurrent,
	}

	switch v := s.Parallel.(type) {
	case string:
		// Direct variable reference like: parallel: ${ITEMS}
		result.Parallel.Variable = v

	case []any:
		// Static array: parallel: [item1, item2]
		items, err := parseParallelItems(v)
		if err != nil {
			return ir.NewValidationError("parallel", v, err)
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
						return ir.NewValidationError("parallel.items", itemsVal, err)
					}
					result.Parallel.Items = items
				default:
					return ir.NewValidationError("parallel.items", val, fmt.Errorf("parallel.items must be string or array, got %T", val))
				}

			case "max_concurrent":
				switch mc := val.(type) {
				case int:
					result.Parallel.MaxConcurrent = mc
				case int64:
					if mc > math.MaxInt || mc < math.MinInt {
						return ir.NewValidationError("parallel.max_concurrent", mc, fmt.Errorf("value %d exceeds integer range", mc))
					}
					result.Parallel.MaxConcurrent = int(mc)
				case uint64:
					if mc > math.MaxInt {
						return ir.NewValidationError("parallel.max_concurrent", mc, fmt.Errorf("value %d exceeds maximum int", mc))
					}
					result.Parallel.MaxConcurrent = int(mc)
				default:
					return ir.NewValidationError("parallel.max_concurrent", val, fmt.Errorf("parallel.max_concurrent must be an integer, got %T", val))
				}
			default:
				return ir.NewValidationError("parallel", v, fmt.Errorf("unknown parallel field %q", key))
			}
		}

	default:
		return ir.NewValidationError("parallel", v, fmt.Errorf("parallel must be string, array, or object, got %T", v))
	}

	return nil
}

var foreachIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// buildStepForeach parses the foreach field in the step definition.
func buildStepForeach(ctx stepBuildContext, s *step, result *ir.Step) error {
	if s.Foreach == nil {
		return nil
	}

	if err := validateForeachExecutionTarget(s); err != nil {
		return err
	}

	cfg, err := parseForeachConfig(ctx, s.Foreach)
	if err != nil {
		return err
	}

	result.Foreach = cfg
	result.ExecutorConfig.Type = ir.ExecutorTypeForeach
	return nil
}

func validateForeachExecutionTarget(s *step) error {
	targets := map[string]bool{
		"run":      s.Run != nil,
		"action":   s.Action != "",
		"command":  s.Command != nil,
		"exec":     s.Exec != nil,
		"script":   s.Script != "",
		"call":     s.Call != "",
		"parallel": s.Parallel != nil,
		"type":     s.Type != "",
	}
	for name, present := range targets {
		if present {
			return ir.NewValidationError("foreach", s.Foreach,
				fmt.Errorf("foreach cannot be combined with %q on the same step", name))
		}
	}
	return nil
}

func parseForeachConfig(ctx stepBuildContext, raw any) (*ir.ForeachConfig, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError("foreach", raw,
			fmt.Errorf("foreach must be an object, got %T", raw))
	}

	cfg := &ir.ForeachConfig{
		As:            "item",
		MaxConcurrent: ir.DefaultMaxConcurrent,
	}

	for key, value := range obj {
		switch key {
		case "items":
			if err := parseForeachItems(value, cfg); err != nil {
				return nil, err
			}
		case "as":
			alias, ok := value.(string)
			if !ok {
				return nil, ir.NewValidationError("foreach.as", value,
					fmt.Errorf("foreach.as must be a string, got %T", value))
			}
			if err := validateForeachIdentifier("foreach.as", alias); err != nil {
				return nil, ir.NewValidationError("foreach.as", alias, err)
			}
			if alias == "index" || alias == "key" {
				return nil, ir.NewValidationError("foreach.as", alias,
					fmt.Errorf("foreach.as %q is reserved", alias))
			}
			cfg.As = alias
		case "key":
			keyExpr, ok := value.(string)
			if !ok {
				return nil, ir.NewValidationError("foreach.key", value,
					fmt.Errorf("foreach.key must be a string, got %T", value))
			}
			cfg.Key = keyExpr
		case "max_concurrent":
			maxConcurrent, err := parseForeachMaxConcurrent(value)
			if err != nil {
				return nil, err
			}
			cfg.MaxConcurrent = maxConcurrent
		case "steps":
			steps, err := parseForeachSteps(ctx, value)
			if err != nil {
				return nil, err
			}
			cfg.Steps = steps
		case "collect":
			collect, err := parseForeachCollect(value)
			if err != nil {
				return nil, err
			}
			cfg.Collect = collect
		default:
			return nil, ir.NewValidationError("foreach", raw,
				fmt.Errorf("unknown foreach field %q", key))
		}
	}

	if cfg.ItemsExpr == "" && cfg.Items == nil {
		return nil, ir.NewValidationError("foreach.items", nil,
			fmt.Errorf("foreach.items is required"))
	}
	if len(cfg.Steps) == 0 {
		return nil, ir.NewValidationError("foreach.steps", nil,
			fmt.Errorf("foreach.steps must contain at least one step"))
	}
	return cfg, nil
}

func parseForeachItems(value any, cfg *ir.ForeachConfig) error {
	switch items := value.(type) {
	case string:
		cfg.ItemsExpr = items
	case []any:
		cfg.Items = slices.Clone(items)
	default:
		return ir.NewValidationError("foreach.items", value,
			fmt.Errorf("foreach.items must be string or array, got %T", value))
	}
	return nil
}

func parseForeachMaxConcurrent(value any) (int, error) {
	var maxConcurrent int
	switch mc := value.(type) {
	case int:
		maxConcurrent = mc
	case int64:
		if mc > math.MaxInt || mc < math.MinInt {
			return 0, ir.NewValidationError("foreach.max_concurrent", mc,
				fmt.Errorf("value %d exceeds integer range", mc))
		}
		maxConcurrent = int(mc)
	case uint64:
		if mc > math.MaxInt {
			return 0, ir.NewValidationError("foreach.max_concurrent", mc,
				fmt.Errorf("value %d exceeds maximum int", mc))
		}
		maxConcurrent = int(mc)
	default:
		return 0, ir.NewValidationError("foreach.max_concurrent", value,
			fmt.Errorf("foreach.max_concurrent must be an integer, got %T", value))
	}
	if maxConcurrent < 1 || maxConcurrent > ir.MaxExpansionConcurrency {
		return 0, ir.NewValidationError("foreach.max_concurrent", value,
			fmt.Errorf("max_concurrent must be an integer from 1 through %d", ir.MaxExpansionConcurrency))
	}
	return maxConcurrent, nil
}

func parseForeachSteps(ctx stepBuildContext, value any) ([]ir.Step, error) {
	rawSteps, ok := value.([]any)
	if !ok {
		return nil, ir.NewValidationError("foreach.steps", value,
			fmt.Errorf("foreach.steps must be an array, got %T", value))
	}
	if len(rawSteps) == 0 {
		return nil, ir.NewValidationError("foreach.steps", value,
			fmt.Errorf("foreach.steps must contain at least one step"))
	}

	steps := make([]ir.Step, 0, len(rawSteps))
	names := map[string]struct{}{}
	for idx, rawStep := range rawSteps {
		stepMap, ok := rawStep.(map[string]any)
		if !ok {
			return nil, ir.NewValidationError("foreach.steps", rawStep,
				fmt.Errorf("foreach.steps[%d] must be an object, got %T", idx, rawStep))
		}
		builtStep, err := buildStepFromRaw(ctx, idx, stepMap, names, nil)
		if err != nil {
			return nil, ir.NewValidationError("foreach.steps", rawStep, err)
		}
		steps = append(steps, *builtStep)
	}
	return steps, nil
}

func parseForeachCollect(value any) (map[string]string, error) {
	rawCollect, ok := value.(map[string]any)
	if !ok {
		return nil, ir.NewValidationError("foreach.collect", value,
			fmt.Errorf("foreach.collect must be an object, got %T", value))
	}

	collect := make(map[string]string, len(rawCollect))
	for name, rawExpr := range rawCollect {
		if err := validateForeachIdentifier("foreach.collect", name); err != nil {
			return nil, ir.NewValidationError("foreach.collect", name, err)
		}
		expr, ok := rawExpr.(string)
		if !ok {
			return nil, ir.NewValidationError("foreach.collect", rawExpr,
				fmt.Errorf("foreach.collect.%s must be a string, got %T", name, rawExpr))
		}
		collect[name] = expr
	}
	return collect, nil
}

func validateForeachIdentifier(fieldName, value string) error {
	if !foreachIdentifierPattern.MatchString(value) {
		return fmt.Errorf("%s must match %s", fieldName, foreachIdentifierPattern.String())
	}
	return nil
}

// buildStepContainer parses the container field in the step definition.
func buildStepContainer(ctx stepBuildContext, s *step, result *ir.Step) error {
	if s.Container == nil {
		return nil
	}

	ct, err := buildContainerField(ctx.buildContext, s.Container)
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
func buildStepLLM(ctx stepBuildContext, s *step, result *ir.Step) error {
	// Only process LLM for executors that support it
	if !registry.ExecutorCapabilitiesFor(result.ExecutorConfig.Type).LLM {
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
	if err := validateLLMProvider(cfg.Provider); err != nil {
		return ir.NewValidationError("llm.provider", cfg.Provider, err)
	}

	// Model is required when llm config is provided
	if cfg.Model.IsZero() {
		return ir.NewValidationError("llm.model", nil,
			fmt.Errorf("model must be specified when llm config is provided"))
	}

	// Get model string or entries from the parsed value
	var modelString string
	var models []ir.ModelEntry

	if cfg.Model.IsArray() {
		var err error
		models, err = convertModelEntries(cfg.Model.Entries())
		if err != nil {
			return err
		}
	} else {
		modelString = cfg.Model.String()
		if modelString == "" {
			return ir.NewValidationError("llm.model", cfg.Model.Value(),
				fmt.Errorf("model must be specified when llm config is provided"))
		}
	}

	// Validate temperature range
	if cfg.Temperature != nil {
		if *cfg.Temperature < 0.0 || *cfg.Temperature > 2.0 {
			return ir.NewValidationError("llm.temperature", *cfg.Temperature,
				fmt.Errorf("temperature must be between 0.0 and 2.0"))
		}
	}

	// Validate max_tokens if specified
	if cfg.MaxTokens != nil {
		if *cfg.MaxTokens < 1 {
			return ir.NewValidationError("llm.max_tokens", *cfg.MaxTokens,
				fmt.Errorf("max_tokens must be at least 1"))
		}
	}
	if err := validateAgentLLMLimits(cfg, false); err != nil {
		return err
	}

	// Validate top_p range
	if cfg.TopP != nil {
		if *cfg.TopP < 0.0 || *cfg.TopP > 1.0 {
			return ir.NewValidationError("llm.top_p", *cfg.TopP,
				fmt.Errorf("top_p must be between 0.0 and 1.0"))
		}
	}

	thinking, err := buildThinkingConfig(cfg.Thinking)
	if err != nil {
		return err
	}

	result.LLM = &ir.LLMConfig{
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

func validateAgentLLMLimits(cfg *llmConfig, agentRoot bool) error {
	limits := []struct {
		path  string
		value *int
	}{
		{path: "llm.max_context_tokens", value: cfg.MaxContextTokens},
		{path: "llm.observation_max_bytes", value: cfg.ObservationMaxBytes},
		{path: "llm.observation_keep_recent", value: cfg.ObservationKeepRecent},
	}
	for _, limit := range limits {
		if limit.value == nil {
			continue
		}
		if !agentRoot {
			return ir.NewValidationError(limit.path, *limit.value,
				fmt.Errorf("field is only valid in an agent DAG's root llm configuration"))
		}
		if *limit.value < 0 {
			return ir.NewValidationError(limit.path, *limit.value,
				fmt.Errorf("value must be non-negative"))
		}
	}
	return nil
}

// buildWebSearchConfig converts webSearchConfig to ir.WebSearchConfig.
func buildWebSearchConfig(cfg *webSearchConfig) *ir.WebSearchConfig {
	if cfg == nil {
		return nil
	}
	result := &ir.WebSearchConfig{
		Enabled:        cfg.Enabled,
		MaxUses:        cfg.MaxUses,
		AllowedDomains: cfg.AllowedDomains,
		BlockedDomains: cfg.BlockedDomains,
	}
	if cfg.UserLocation != nil {
		result.UserLocation = &ir.WebSearchUserLocation{
			City:     cfg.UserLocation.City,
			Region:   cfg.UserLocation.Region,
			Country:  cfg.UserLocation.Country,
			Timezone: cfg.UserLocation.Timezone,
		}
	}
	return result
}

// validateLLMProvider reports whether provider names a supported LLM provider.
// An empty provider is accepted; so is one carrying a value reference, since its
// final value is only known once the step runs.
func validateLLMProvider(provider string) error {
	if provider == "" || cmnvalue.HasValueReference(provider) {
		return nil
	}
	_, err := llm.ParseProviderType(provider)
	return err
}

// convertModelEntries converts types.ModelEntry slice to ir.ModelEntry slice with validation.
func convertModelEntries(entries []types.ModelEntry) ([]ir.ModelEntry, error) {
	models := make([]ir.ModelEntry, len(entries))
	for i, e := range entries {
		if err := validateLLMProvider(e.Provider); err != nil {
			return nil, ir.NewValidationError(fmt.Sprintf("llm.model[%d].provider", i), e.Provider, err)
		}
		models[i] = ir.ModelEntry{
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

// buildThinkingConfig converts thinkingConfig to ir.ThinkingConfig.
func buildThinkingConfig(cfg *thinkingConfig) (*ir.ThinkingConfig, error) {
	if cfg == nil {
		return nil, nil
	}
	effort, err := ir.ParseThinkingEffort(cfg.Effort)
	if err != nil {
		return nil, ir.NewValidationError("thinking.effort", cfg.Effort, err)
	}
	return &ir.ThinkingConfig{
		Enabled:         cfg.Enabled,
		Effort:          effort,
		BudgetTokens:    cfg.BudgetTokens,
		IncludeInOutput: cfg.IncludeInOutput,
	}, nil
}

// buildStepMessages parses the messages field for chat steps.
func buildStepMessages(s *step, result *ir.Step) error {
	if len(s.Messages) == 0 {
		return nil
	}

	result.Messages = make([]ir.PromptMessage, len(s.Messages))
	for i, msg := range s.Messages {
		if msg.Role == "" {
			return ir.NewValidationError(
				fmt.Sprintf("messages[%d].role", i), msg.Role,
				fmt.Errorf("role is required"))
		}
		role, err := ir.ParseLLMRole(msg.Role)
		if err != nil {
			return ir.NewValidationError(
				fmt.Sprintf("messages[%d].role", i), msg.Role, err)
		}
		if msg.Content == "" {
			return ir.NewValidationError(
				fmt.Sprintf("messages[%d].content", i), msg.Content,
				fmt.Errorf("content is required"))
		}
		result.Messages[i] = ir.PromptMessage{
			Role:    role,
			Content: msg.Content,
		}
	}

	return nil
}

// buildStepRouter parses the router configuration from step fields.
func buildStepRouter(_ stepBuildContext, s *step, result *ir.Step) error {
	if s.Type != "router" {
		return nil
	}

	// Trim and validate value
	s.Value = strings.TrimSpace(s.Value)
	if s.Value == "" {
		return ir.NewValidationError("value", nil,
			fmt.Errorf("router step requires 'value' field"))
	}
	if len(s.Routes) == 0 {
		return ir.NewValidationError("routes", nil,
			fmt.Errorf("router step requires at least one route"))
	}

	// Convert map to ordered entries
	var routes []ir.RouteEntry
	for pattern, targets := range s.Routes {
		// Trim and validate pattern
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return ir.NewValidationError("routes", nil,
				fmt.Errorf("route pattern cannot be empty"))
		}

		if len(targets) == 0 {
			return ir.NewValidationError("routes", pattern,
				fmt.Errorf("route pattern %q has no targets", pattern))
		}

		// Trim and validate each target
		var trimmedTargets []string
		for _, target := range targets {
			target = strings.TrimSpace(target)
			if target == "" {
				return ir.NewValidationError("routes", pattern,
					fmt.Errorf("route pattern %q has empty target", pattern))
			}
			trimmedTargets = append(trimmedTargets, target)
		}

		routes = append(routes, ir.RouteEntry{
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

	result.Router = &ir.RouterConfig{
		Value:  s.Value,
		Routes: routes,
	}
	result.ExecutorConfig.Type = ir.ExecutorTypeRouter

	return nil
}

// buildStepApproval parses the approval configuration for a step.
func buildStepApproval(_ stepBuildContext, s *step, result *ir.Step) error {
	if s.Approval == nil {
		return nil
	}
	approval := ir.ApprovalConfig(*s.Approval)
	approval.RewindTo = strings.TrimSpace(approval.RewindTo)
	result.Approval = &approval
	// Validate required fields are subset of input
	for _, req := range result.Approval.Required {
		if !slices.Contains(result.Approval.Input, req) {
			return fmt.Errorf("required field %q is not in input list", req)
		}
	}
	return nil
}

// buildStepSubDAG parses the child ir.DAG definition and sets up the step to run a sub DAG.
func buildStepSubDAG(ctx stepBuildContext, s *step, result *ir.Step) error {
	name := strings.TrimSpace(s.Call)

	// if the call field is not set, return nil.
	if name == "" {
		return nil
	}

	// Parse params similar to how ir.DAG params are parsed
	var paramsStr string
	if s.Params != nil {
		// Parse the params to convert them to string format
		ctxCopy := ctx
		ctxCopy.opts.Flags |= buildFlagNoEval // Disable evaluation for params parsing
		paramPairs, err := parseParamValue(ctxCopy.buildContext, s.Params)
		if err != nil {
			return ir.NewValidationError("params", s.Params, err)
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

	result.SubDAG = &ir.SubDAG{Name: name, Params: paramsStr}

	// Set executor type based on whether parallel execution is configured
	if result.Parallel != nil {
		result.ExecutorConfig.Type = ir.ExecutorTypeParallel
	} else {
		result.ExecutorConfig.Type = ir.ExecutorTypeDAG
	}

	return nil
}

// parseParallelItems converts an array of any type to ir.ParallelItem slice
func parseParallelItems(items []any) ([]ir.ParallelItem, error) {
	var result []ir.ParallelItem

	for _, item := range items {
		switch v := item.(type) {
		case string:
			result = append(result, ir.ParallelItem{Value: v})

		case int, int64, uint64, float64:
			result = append(result, ir.ParallelItem{Value: fmt.Sprintf("%v", v)})

		case map[string]any:
			params := make(map[string]string)
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
			result = append(result, ir.ParallelItem{Params: params})

		default:
			return nil, fmt.Errorf("parallel items must be strings, numbers, or objects, got %T", v)
		}
	}

	return result, nil
}
