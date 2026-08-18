// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

const (
	// AgentStepName is the reserved name of the synthesized step that drives
	// an agent DAG. It cannot be used as a step name or ID.
	AgentStepName = "__agent__"

	// LegacyAgentStepName is the name the synthesized agent step carried while
	// agent DAGs were called controller DAGs. Run status files written then
	// still name the step this way, so reading them keeps recognizing it.
	LegacyAgentStepName = "__controller__"

	// AskUserStepName is the reserved name and ID of the synthesized human task
	// an agent opens when it needs to ask a question no declared step
	// covers. It cannot be used as a step name or ID.
	//
	// The name is plain rather than underscore-wrapped because it is what an
	// operator types to answer:
	//
	//	dagu human-task complete <dag> --step ask_user --inputs-json '{"answer":"..."}'
	AskUserStepName = "ask_user"

	// AskUserAnswerField is the single form field an ask_user question collects.
	AskUserAnswerField = "answer"

	// DefaultAgentMaxIterations bounds the number of agent turns when
	// llm.max_tool_iterations is not set.
	DefaultAgentMaxIterations = 50

	// DefaultAgentMaxStepRuns caps how many times the agent may run a
	// single declared step within one DAG run.
	DefaultAgentMaxStepRuns = 5

	// DefaultAgentMaxQuestions caps how many questions an agent may put
	// to a person in one run. Each one suspends the run, so an unbounded
	// agent could pester someone indefinitely.
	DefaultAgentMaxQuestions = 5

	// DefaultAgentMaxContextTokens is the prompt size at which observation
	// aging starts.
	DefaultAgentMaxContextTokens = 200_000

	// DefaultAgentObservationMaxBytes bounds each tool result added to a
	// agent conversation.
	DefaultAgentObservationMaxBytes = 512 * 1024

	// DefaultAgentObservationKeepRecent is the number of tool results that
	// remain complete after observation aging starts.
	DefaultAgentObservationKeepRecent = 20
)

// AgentTask is a goal an Agent DAG must settle before the run concludes.
type AgentTask struct {
	// Name identifies the task. It is unique within the DAG.
	Name string `json:"name"`
	// Description states the completion criteria in natural language.
	Description string `json:"description,omitempty"`
}

// IsAgent reports whether the DAG is driven by an LLM agent instead of
// a static dependency graph.
func (d *DAG) IsAgent() bool {
	return d != nil && d.Type == TypeAgent
}

// IsPersistedAgentStepName reports whether a step name recorded in a run
// status identifies the synthesized step that drives an agent DAG. It accepts
// the legacy spelling, so it suits reading run history but not building or
// validating a DAG, where only the current name is ever synthesized.
func IsPersistedAgentStepName(name string) bool {
	return name == AgentStepName || name == LegacyAgentStepName
}

// IsSynthesizedAgentStep reports whether a step name belongs to the
// scaffolding an agent DAG is built with rather than to a declared action.
func IsSynthesizedAgentStep(name string) bool {
	return name == AgentStepName || name == AskUserStepName
}

// AgentStep returns the synthesized agent step, or nil when the DAG is
// not an agent DAG.
func (d *DAG) AgentStep() *Step {
	if d == nil {
		return nil
	}
	for i, step := range d.Steps {
		if step.Name == AgentStepName {
			return &d.Steps[i]
		}
	}
	return nil
}

// AgentMaxIterations returns the upper bound on agent turns for a
// single run.
func (d *DAG) AgentMaxIterations() int {
	if d == nil || d.LLM == nil || d.LLM.MaxToolIterations == nil {
		return DefaultAgentMaxIterations
	}
	if n := *d.LLM.MaxToolIterations; n > 0 {
		return n
	}
	return DefaultAgentMaxIterations
}

// AgentMaxContextTokens returns the prompt size at which observation aging
// starts. Zero disables proactive aging.
func (d *DAG) AgentMaxContextTokens() int {
	if d == nil || d.LLM == nil || d.LLM.MaxContextTokens == nil {
		return DefaultAgentMaxContextTokens
	}
	return *d.LLM.MaxContextTokens
}

// AgentObservationMaxBytes returns the maximum agent-facing size of
// one observation. Zero disables the size limit.
func (d *DAG) AgentObservationMaxBytes() int {
	if d == nil || d.LLM == nil || d.LLM.ObservationMaxBytes == nil {
		return DefaultAgentObservationMaxBytes
	}
	return *d.LLM.ObservationMaxBytes
}

// AgentObservationKeepRecent returns how many recent observations remain
// complete after aging starts. Zero disables observation aging.
func (d *DAG) AgentObservationKeepRecent() int {
	if d == nil || d.LLM == nil || d.LLM.ObservationKeepRecent == nil {
		return DefaultAgentObservationKeepRecent
	}
	return *d.LLM.ObservationKeepRecent
}

// NewAgentStep builds the step that carries the agent's LLM config and
// task list. It is appended to an agent DAG at build time and is the node the
// runner drives the decision loop from.
func NewAgentStep(dag *DAG) Step {
	return Step{
		Name:        AgentStepName,
		Description: "LLM agent",
		LLM:         dag.LLM,
		ExecutorConfig: ExecutorConfig{
			Type: ExecutorTypeAgent,
		},
	}
}
