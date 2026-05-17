// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Step contains the runtime information for a step in a DAG.
// A step is created from parsing a DAG file written in YAML.
// It marshals/unmarshals to/from JSON when it is saved in the execution history.
type Step struct {
	// ID is the optional unique identifier for the step.
	ID string `json:"id,omitempty"`
	// Name is the name of the step.
	Name string `json:"name"`
	// Description is the description of the step. This is optional.
	Description string `json:"description,omitempty"`
	// Shell is the shell program to execute the command. This is optional.
	Shell string `json:"shell,omitempty"`
	// ShellPackages is the list of packages to install. This is used only when the shell is `nix-shell`.
	ShellPackages []string `json:"shellPackages,omitempty"`
	// ShellArgs is the list of arguments for the shell program.
	ShellArgs []string `json:"shellArgs,omitempty"`
	// Dir is the working directory for the step.
	Dir string `json:"dir,omitempty"`
	// ExecutorConfig contains the configuration for the executor.
	ExecutorConfig ExecutorConfig `json:"executorConfig,omitzero"`
	// CmdWithArgs is the command with arguments for display purposes.
	// Deprecated: Use Commands[0].CmdWithArgs instead. Kept for JSON backward compatibility.
	CmdWithArgs string `json:"cmdWithArgs,omitempty"`
	// CmdArgsSys is the command with arguments for the system.
	// Deprecated: Kept for JSON backward compatibility.
	CmdArgsSys string `json:"cmdArgsSys,omitempty"`
	// Command specifies only the command without arguments.
	// Deprecated: Use Commands field instead. Kept for JSON backward compatibility.
	Command string `json:"command,omitempty"`
	// ShellCmdArgs is the shell command with arguments.
	ShellCmdArgs string `json:"shellCmdArgs,omitempty"`
	// Script is the script to be executed.
	Script string `json:"script,omitempty"`
	// Args contains the arguments for the command.
	// Deprecated: Use Commands field instead. Kept for JSON backward compatibility.
	Args []string `json:"args,omitempty"`
	// Commands is the source of truth for commands to execute.
	// Each entry represents a command to be executed sequentially.
	// For single commands, this will contain exactly one entry.
	Commands []CommandEntry `json:"commands,omitempty"`
	// Stdout is the file to store the standard output.
	Stdout string `json:"stdout,omitempty"`
	// StdoutArtifact is the artifact-relative file path to store standard output.
	StdoutArtifact string `json:"stdoutArtifact,omitempty"`
	// Stderr is the file to store the standard error.
	Stderr string `json:"stderr,omitempty"`
	// StderrArtifact is the artifact-relative file path to store standard error.
	StderrArtifact string `json:"stderrArtifact,omitempty"`
	// LogOutput specifies how stdout and stderr are handled in log files for this step.
	// Overrides the DAG-level LogOutput setting. Empty string means inherit from DAG.
	LogOutput LogOutputMode `json:"logOutput,omitempty"`
	// Output is the variable name to store captured stdout.
	Output string `json:"output,omitempty"`
	// StructuredOutput publishes post-processed step-scoped outputs for ${step.output.*} access.
	StructuredOutput map[string]StepOutputEntry `json:"structuredOutput,omitempty"`
	// OutputSchema validates stdout JSON before publishing step-scoped output.
	OutputSchema map[string]any `json:"outputSchema,omitzero"`
	// Depends contains the list of step names to depend on.
	Depends []string `json:"depends,omitempty"`
	// ExplicitlyNoDeps indicates the depends field was explicitly set to empty
	ExplicitlyNoDeps bool `json:"-"`
	// ContinueOn contains the conditions to continue on failure or skipped.
	ContinueOn ContinueOn `json:"continueOn,omitzero"`
	// RetryPolicy contains the retry policy for the step.
	RetryPolicy RetryPolicy `json:"retryPolicy,omitzero"`
	// RepeatPolicy contains the repeat policy for the step.
	RepeatPolicy RepeatPolicy `json:"repeatPolicy,omitzero"`
	// MailOnError is the flag to send mail on error.
	MailOnError bool `json:"mailOnError,omitempty"`
	// Preconditions contains the conditions to be met before running the step.
	Preconditions []*Condition `json:"preconditions,omitempty"`
	// SignalOnStop is the signal to send on stop.
	SignalOnStop string `json:"signalOnStop,omitempty"`
	// SubDAG contains the information about a sub DAG to be executed.
	SubDAG *SubDAG `json:"childDag,omitempty"`
	// WorkerSelector specifies required worker labels for execution.
	WorkerSelector map[string]string `json:"workerSelector,omitempty"`
	// Parallel contains the configuration for parallel execution.
	Parallel *ParallelConfig `json:"parallel,omitempty"`
	// Env contains environment variables for the step.
	Env []string `json:"env,omitempty"`
	// Params contains parameters/inputs for the step.
	Params Params `json:"params,omitzero"`
	// Timeout specifies the maximum execution time for the step.
	// If set, this timeout takes precedence over the DAG-level timeout for this step.
	Timeout time.Duration `json:"timeout,omitempty"`
	// Container specifies the container configuration for this step.
	// If set, the step runs in its own container instead of the DAG-level container.
	// This uses the same configuration format as the DAG-level container field.
	Container *Container `json:"container,omitempty"`
	// LLM contains the configuration for LLM-based executors (chat, agent, etc.).
	// Used with explicit type: chat (or future type: agent).
	LLM *LLMConfig `json:"llm,omitempty"`
	// Messages contains the session messages for chat executor.
	// Only used when type is "chat".
	Messages []LLMMessage `json:"messages,omitempty"`
	// Router contains the routing configuration for router-type steps.
	// Only used when type is "router".
	Router *RouterConfig `json:"router,omitempty"`
	// Agent contains the configuration for agent-type steps.
	// Only used when type is "agent".
	Agent *AgentStepConfig `json:"agent,omitempty"`
	// Approval configures a human approval gate after step execution.
	// When set, the step pauses in Waiting state after execution completes.
	Approval *ApprovalConfig `json:"approval,omitempty"`
}

const (
	StepOutputSourceStdout = "stdout"
	StepOutputSourceStderr = "stderr"
	StepOutputSourceFile   = "file"

	StepOutputDecodeText = "text"
	StepOutputDecodeJSON = "json"
	StepOutputDecodeYAML = "yaml"
)

// StepOutputEntry defines one structured object-form output entry.
type StepOutputEntry struct {
	// HasValue distinguishes literal/null values from source-based outputs.
	HasValue bool `json:"hasValue,omitempty"`
	// Value is the literal value to publish when HasValue is true.
	Value any `json:"value"`
	// From selects a runtime source to read from: stdout, stderr, or file.
	From string `json:"from,omitempty"`
	// Path is the file path used when From is file.
	Path string `json:"path,omitempty"`
	// Decode controls how the source content is decoded before selection.
	Decode string `json:"decode,omitempty"`
	// Select is an optional jq/gojq path applied after decode.
	Select string `json:"select,omitempty"`
}

// String returns a formatted string representation of the step
func (s *Step) String() string {
	return strings.Join([]string{
		fmt.Sprintf("Name: %s", s.Name),
		fmt.Sprintf("Dir: %s", s.Dir),
		fmt.Sprintf("Command: %s", s.Command),
		fmt.Sprintf("Args: %v", s.Args),
		fmt.Sprintf("Depends: [%s]", strings.Join(s.Depends, ", ")),
	}, "\t")
}

// SubDAG contains information about a sub DAG to be executed.
type SubDAG struct {
	Name   string `json:"name,omitempty"`
	Params string `json:"params,omitempty"`
}

// CommandEntry represents a single command in a multi-command step.
// Each entry contains a parsed command with its arguments.
type CommandEntry struct {
	// Command is the executable name or path.
	Command string `json:"command"`
	// Args contains the arguments for the command.
	Args []string `json:"args,omitempty"`
	// CmdWithArgs is the original command string for display purposes.
	CmdWithArgs string `json:"cmdWithArgs,omitempty"`
}

// String returns a display string for the command entry.
func (c CommandEntry) String() string {
	if c.CmdWithArgs != "" {
		return c.CmdWithArgs
	}
	if c.Command == "" {
		return ""
	}
	if len(c.Args) == 0 {
		return c.Command
	}
	return c.Command + " " + strings.Join(c.Args, " ")
}

// HasMultipleCommands returns true if the step has multiple commands to execute.
func (s *Step) HasMultipleCommands() bool {
	return len(s.Commands) > 1
}

// HasStructuredOutput reports whether the step publishes object-form output.
func (s Step) HasStructuredOutput() bool {
	return len(s.StructuredOutput) > 0
}

// HasOutputSchema reports whether the step validates stdout JSON with an output schema.
func (s Step) HasOutputSchema() bool {
	return s.OutputSchema != nil
}

// UsesStructuredOutputSource reports whether any structured output entry reads from source.
func (s Step) UsesStructuredOutputSource(source string) bool {
	for _, entry := range s.StructuredOutput {
		if entry.From == source {
			return true
		}
	}
	return false
}

// UnmarshalJSON implements json.Unmarshaler for backward compatibility.
// It handles old JSON format where command/args fields were used instead of commands.
func (s *Step) UnmarshalJSON(data []byte) error {
	type alias Step
	aux := &struct {
		*alias
	}{
		alias: (*alias)(s),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	if len(s.Commands) > 0 || (s.Command == "" && len(s.Args) == 0 && s.CmdWithArgs == "") {
		return nil
	}

	s.Commands = []CommandEntry{s.legacyCommandEntry()}
	return nil
}

func (s *Step) legacyCommandEntry() CommandEntry {
	return CommandEntry{
		Command:     s.Command,
		Args:        slices.Clone(s.Args),
		CmdWithArgs: s.CmdWithArgs,
	}
}

// ExecutorConfig contains the configuration for the executor.
type ExecutorConfig struct {
	// Type represents one of the registered executors.
	// See `executor.Register` in `internal/executor/executor.go`.
	Type   string         `json:"type,omitempty"`
	Config map[string]any `json:"config,omitempty"` // Config contains executor-specific configuration.
	// Metadata contains additional metadata for the executor that is not passed to the executor itself.
	// This is used internally for optimization purposes.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// IsCommand returns true if the executor is a command
func (e ExecutorConfig) IsCommand() bool {
	return e.Type == "" || e.Type == "command"
}

// RetryPolicy contains the retry policy for a step.
type RetryPolicy struct {
	// Limit is the number of retries allowed.
	Limit int `json:"limit,omitempty"`
	// Interval is the time to wait between retries.
	Interval time.Duration `json:"interval,omitempty"`
	// LimitStr is the string representation of the limit.
	LimitStr string `json:"limitStr,omitempty"`
	// IntervalSecStr is the string representation of the interval.
	IntervalSecStr string `json:"intervalSecStr,omitempty"`
	// ExitCodes is the list of exit codes that should trigger a retry.
	ExitCodes []int `json:"exitCode,omitempty"`
	// Backoff is the exponential backoff multiplier (e.g., 2.0 for doubling).
	Backoff float64 `json:"backoff,omitempty"`
	// MaxInterval is the maximum interval cap for exponential backoff.
	MaxInterval time.Duration `json:"maxInterval,omitempty"`
}

// RepeatMode is the type for the repeat mode.
type RepeatMode string

const (
	// RepeatModeWhile repeats the step while the condition is met.
	RepeatModeWhile RepeatMode = "while"
	// RepeatModeUntil repeats the step until the condition is met.
	RepeatModeUntil RepeatMode = "until"
)

// RepeatPolicy contains the repeat policy for a step.
type RepeatPolicy struct {
	// RepeatMode determines if and how the step should be repeated.
	// It can be 'while' or 'until'.
	RepeatMode RepeatMode `json:"repeatMode,omitempty"`
	// Interval is the time to wait between repeats.
	Interval time.Duration `json:"interval,omitempty"`
	// IntervalStr is the string representation of interval_sec for deferred evaluation.
	IntervalStr string `json:"intervalStr,omitempty"`
	// Limit is the maximum number of times to repeat the step.
	Limit int `json:"limit,omitempty"`
	// LimitStr is the string representation of the limit for deferred evaluation.
	LimitStr string `json:"limitStr,omitempty"`
	// Backoff is the exponential backoff multiplier (e.g., 2.0 for doubling).
	Backoff float64 `json:"backoff,omitempty"`
	// MaxInterval is the maximum interval cap for exponential backoff.
	MaxInterval time.Duration `json:"maxInterval,omitempty"`
	// MaxIntervalStr is the string representation of max_interval_sec for deferred evaluation.
	MaxIntervalStr string `json:"maxIntervalStr,omitempty"`
	// Condition is the condition object to be met for the repeat.
	Condition *Condition `json:"condition,omitempty"`
	// ExitCode is the list of exit codes that should trigger a repeat.
	ExitCode []int `json:"exitCode,omitempty"`
}

// UnmarshalJSON implements the json.Unmarshaler interface for RepeatPolicy.
// It handles the legacy boolean repeat field and the new string repeat modes.
func (r *RepeatPolicy) UnmarshalJSON(data []byte) error {
	// Use a type alias to avoid infinite recursion
	type Alias RepeatPolicy

	// First, unmarshal into the alias to get the new format fields
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Copy the fields
	r.RepeatMode = aux.RepeatMode
	r.Interval = aux.Interval
	r.IntervalStr = aux.IntervalStr
	r.Limit = aux.Limit
	r.LimitStr = aux.LimitStr
	r.Condition = aux.Condition
	r.ExitCode = aux.ExitCode
	r.Backoff = aux.Backoff
	r.MaxInterval = aux.MaxInterval
	r.MaxIntervalStr = aux.MaxIntervalStr

	// If RepeatMode is already set, we're done (new format)
	if r.RepeatMode != "" {
		return nil
	}

	// Otherwise, check for legacy format
	var legacy struct {
		Repeat bool `json:"repeat"`
	}

	if err := json.Unmarshal(data, &legacy); err == nil && data != nil {
		// Successfully parsed legacy format
		if legacy.Repeat {
			// Legacy repeat: true -> while mode
			r.RepeatMode = RepeatModeWhile
		} else {
			// Legacy repeat: false -> infer based on conditions
			if r.Condition != nil && r.Condition.Expected != "" {
				// Condition with expected value -> "until" mode
				r.RepeatMode = RepeatModeUntil
			} else if r.Condition != nil || len(r.ExitCode) > 0 {
				// Just condition or exit code -> "while" mode
				r.RepeatMode = RepeatModeWhile
			}
			// Otherwise leave RepeatMode empty (no repeat)
		}
	}

	return nil
}

// ContinueOn contains the conditions to continue on failure or skipped.
// Failure is the flag to continue to the next step on failure.
// Skipped is the flag to continue to the next step on skipped.
// A step can be skipped when the preconditions are not met.
// Then if the ContinueOn.Skip is set, the step will continue to the next step.
type ContinueOn struct {
	Failure     bool     `json:"failure,omitempty"`     // Failure is the flag to continue to the next step on failure.
	Skipped     bool     `json:"skipped,omitempty"`     // Skipped is the flag to continue to the next step on skipped.
	ExitCode    []int    `json:"exitCode,omitempty"`    // ExitCode is the list of exit codes to continue to the next step.
	Output      []string `json:"output,omitempty"`      // Output is the list of output (stdout/stderr) to continue to the next step.
	MarkSuccess bool     `json:"markSuccess,omitempty"` // MarkSuccess is the flag to mark the step as success when the condition is met.
}

// ApprovalConfig configures the approval gate for a step.
// When a step has an ApprovalConfig, it pauses in Waiting state after execution
// completes, allowing a human to approve, push back (re-run with feedback), or reject.
type ApprovalConfig struct {
	// Prompt is the message displayed to the approver.
	Prompt string `json:"prompt,omitempty"`
	// Input is the list of expected input field names from the approver.
	Input []string `json:"input,omitempty"`
	// Required is the subset of Input fields that must be provided.
	Required []string `json:"required,omitempty"`
	// RewindTo is the step name or ID to restart from on push-back.
	// When empty, push-back re-executes the approval step itself.
	RewindTo string `json:"rewindTo,omitempty"`
}

const (
	// ExecutorTypeDAG is the executor type for a sub DAG.
	ExecutorTypeDAG = "dag"

	// ExecutorTypeDAGEnqueue is the executor type for asynchronously queueing a sub DAG.
	ExecutorTypeDAGEnqueue = "dag_enqueue"

	// ExecutorTypeParallel is the executor type for parallel steps.
	ExecutorTypeParallel = "parallel"

	// ExecutorTypeRouter is the executor type for router steps.
	ExecutorTypeRouter = "router"

	// ExecutorTypeAgent is the executor type for agent steps.
	ExecutorTypeAgent = "agent"

	// ExecutorTypeAction is the executor type for external Dagu actions.
	ExecutorTypeAction = "action"
)

// AgentStepConfig contains the configuration for an agent step.
type AgentStepConfig struct {
	// Model overrides the global default model for this step.
	Model string `json:"model,omitempty"`
	// Tools configures which tools are available and their policies.
	Tools *AgentToolsConfig `json:"tools,omitempty"`
	// Skills lists skill IDs the agent is allowed to use.
	// If empty, falls back to globally enabled skills from agent config.
	Skills []string `json:"skills,omitempty"`
	// Soul is the soul ID for this step's agent identity.
	Soul string `json:"soul,omitempty"`
	// Memory controls whether persistent memory is loaded.
	Memory *AgentMemoryConfig `json:"memory,omitempty"`
	// Prompt is additional instructions appended to the built-in system prompt.
	Prompt string `json:"prompt,omitempty"`
	// MaxIterations is the maximum number of tool call rounds (default: 50).
	MaxIterations int `json:"maxIterations,omitempty"`
	// SafeMode enables command approval via human review (default: true).
	SafeMode bool `json:"safeMode"`
	// WebSearch configures provider-native web search for this step.
	// Overrides the global agent web search setting.
	WebSearch *WebSearchConfig `json:"webSearch,omitempty"`
}

// AgentToolsConfig configures available tools and policies.
type AgentToolsConfig struct {
	// Enabled lists the tools to enable. When empty, all available tools are enabled.
	Enabled []string `json:"enabled,omitempty"`
	// BashPolicy configures bash command security rules.
	BashPolicy *AgentBashPolicy `json:"bashPolicy,omitempty"`
}

// AgentBashPolicy configures bash command security enforcement.
type AgentBashPolicy struct {
	// DefaultBehavior is the default action when no rule matches ("allow" or "deny").
	DefaultBehavior string `json:"defaultBehavior,omitempty"`
	// DenyBehavior determines what happens when a command is denied ("block" or "ask_user").
	DenyBehavior string `json:"denyBehavior,omitempty"`
	// Rules is an ordered list of pattern-matching rules.
	Rules []AgentBashRule `json:"rules,omitempty"`
}

// AgentBashRule is a single bash command policy rule.
type AgentBashRule struct {
	// Name is a human-readable name for the rule.
	Name string `json:"name,omitempty"`
	// Pattern is a regex pattern to match against commands.
	Pattern string `json:"pattern"`
	// Action is the action to take when the pattern matches ("allow" or "deny").
	Action string `json:"action"`
}

// AgentMemoryConfig configures memory for the agent step.
type AgentMemoryConfig struct {
	// Enabled controls whether global and per-DAG memory is loaded.
	Enabled bool `json:"enabled,omitempty"`
}

// RouterConfig contains routing configuration for router-type steps.
type RouterConfig struct {
	Value  string       `json:"value"`  // Value expression to evaluate (e.g., "${STATUS}")
	Routes []RouteEntry `json:"routes"` // Ordered list of pattern → targets
}

// RouteEntry represents a single routing rule.
type RouteEntry struct {
	Pattern string   `json:"pattern"` // Match pattern (exact or "re:regex")
	Targets []string `json:"targets"` // Step names to route to when pattern matches
}
