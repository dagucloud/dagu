// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// Defaults of the keys of an agent.run step.
const (
	// DefaultAgentMaxToolCalls is the default of max_tool_calls.
	DefaultAgentMaxToolCalls = 100
	// AgentToolCallsOutput is the name of the output with the normalized
	// tool calls of an agent.run step.
	AgentToolCallsOutput = "tool_calls"
	// AgentStreamSuffix is the suffix of the file that holds the raw stream of
	// a CLI agent. The file is next to the stdout log.
	AgentStreamSuffix = ".stream.jsonl"
)

// AgentEffortLevels are the values of the key effort of an agent.run step, from
// the lowest to the highest. They are the values of --effort of the CLI claude.
var AgentEffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

// AgentFailureKind is the result class of one attempt of an agent.run step.
type AgentFailureKind string

// Failure kinds of an attempt.
const (
	AgentKindOK           AgentFailureKind = "ok"
	AgentKindAuthRequired AgentFailureKind = "auth_required"
	AgentKindRateLimited  AgentFailureKind = "rate_limited"
	AgentKindError        AgentFailureKind = "error"
)

// Exit codes of a step that fails with a kind other than error.
const (
	// AgentExitRateLimited is the exit code for rate_limited.
	AgentExitRateLimited = 75
	// AgentExitAuthRequired is the exit code for auth_required.
	AgentExitAuthRequired = 77
)

// AgentRun is the compiled form of an agent.run step. The loader keeps it with
// the step, next to the harness.run configuration that the step runs as.
type AgentRun struct {
	// Provider is the literal provider of the step. It is a CLI provider.
	Provider string `json:"provider,omitempty"`
	// Model is the model of the step. It is empty when no key sets it.
	Model string `json:"model,omitempty"`
	// Endpoint is the name of the AI endpoint of the step (Spec 094). It is empty
	// when the step names none. The name is a literal that matches the rule of the
	// name of an endpoint. Dagu checks that the endpoint exists when the step starts.
	Endpoint string `json:"endpoint,omitempty"`
	// Prompt is the prompt template. A value reference in it resolves when the
	// step runs.
	Prompt string `json:"prompt,omitempty"`
	// System is the system text of the step.
	System string `json:"system,omitempty"`
	// MaxToolIterations is the value of --max-turns of the CLI. Nil means that
	// the step passes no limit.
	MaxToolIterations *int `json:"maxToolIterations,omitempty"`
	// MaxToolCalls limits the calls of the tool bridge in one attempt. A call
	// over the limit gets an error result. Zero selects
	// DefaultAgentMaxToolCalls.
	MaxToolCalls int `json:"maxToolCalls,omitempty"`
	// CLI holds the tool keys of the step.
	CLI *AgentCLIConfig `json:"cli,omitempty"`
}

// EffectiveMaxToolCalls returns the limit of tool calls of one attempt.
func (r *AgentRun) EffectiveMaxToolCalls() int {
	if r == nil || r.MaxToolCalls <= 0 {
		return DefaultAgentMaxToolCalls
	}
	return r.MaxToolCalls
}

// AgentCLIConfig holds the tool keys and the skills of an agent.run step.
type AgentCLIConfig struct {
	// Tools lists the tool entries, in order: the DAG tools that the tool
	// bridge serves (the key dags), the mcp entries, then the builtin entry.
	Tools []ToolEntry `json:"tools,omitempty"`
	// Skills lists the skills of the step, in order. Dagu mounts each skill as
	// a file.
	Skills []SkillEntry `json:"skills,omitempty"`
}

// IsAgentRun reports whether the step is an agent.run step.
func (s Step) IsAgentRun() bool {
	return s.Agent != nil
}
