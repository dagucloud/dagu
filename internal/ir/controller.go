// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

const (
	// ControllerStepName is the reserved name of the synthesized step that drives
	// a controller DAG. It cannot be used as a step name or ID.
	ControllerStepName = "__controller__"

	// AskUserStepName is the reserved name and ID of the synthesized human task
	// a controller opens when it needs to ask a question no declared step
	// covers. It cannot be used as a step name or ID.
	//
	// The name is plain rather than underscore-wrapped because it is what an
	// operator types to answer:
	//
	//	dagu human-task complete <dag> --step ask_user --inputs-json '{"answer":"..."}'
	AskUserStepName = "ask_user"

	// AskUserAnswerField is the single form field an ask_user question collects.
	AskUserAnswerField = "answer"

	// DefaultControllerMaxIterations bounds the number of controller turns when
	// llm.max_tool_iterations is not set.
	DefaultControllerMaxIterations = 50

	// DefaultControllerMaxStepRuns caps how many times the controller may run a
	// single declared step within one DAG run.
	DefaultControllerMaxStepRuns = 5

	// DefaultControllerMaxQuestions caps how many questions a controller may put
	// to a person in one run. Each one suspends the run, so an unbounded
	// controller could pester someone indefinitely.
	DefaultControllerMaxQuestions = 5

	// DefaultControllerMaxContextTokens is the prompt size at which observation
	// aging starts.
	DefaultControllerMaxContextTokens = 200_000

	// DefaultControllerObservationMaxBytes bounds each tool result added to a
	// controller conversation.
	DefaultControllerObservationMaxBytes = 512 * 1024

	// DefaultControllerObservationKeepRecent is the number of tool results that
	// remain complete after observation aging starts.
	DefaultControllerObservationKeepRecent = 20
)

// ControllerTask is a goal the controller must satisfy. A controller DAG run
// concludes successfully once every task has been marked complete.
type ControllerTask struct {
	// Name identifies the task. It is unique within the DAG.
	Name string `json:"name"`
	// Description states the completion criteria in natural language.
	Description string `json:"description,omitempty"`
}

// IsController reports whether the DAG is driven by an LLM controller instead of
// a static dependency graph.
func (d *DAG) IsController() bool {
	return d != nil && d.Type == TypeController
}

// IsSynthesizedControllerStep reports whether a step name belongs to the
// scaffolding a controller DAG is built with rather than to a declared action.
func IsSynthesizedControllerStep(name string) bool {
	return name == ControllerStepName || name == AskUserStepName
}

// ControllerStep returns the synthesized controller step, or nil when the DAG is
// not a controller DAG.
func (d *DAG) ControllerStep() *Step {
	if d == nil {
		return nil
	}
	for i, step := range d.Steps {
		if step.Name == ControllerStepName {
			return &d.Steps[i]
		}
	}
	return nil
}

// ControllerMaxIterations returns the upper bound on controller turns for a
// single run.
func (d *DAG) ControllerMaxIterations() int {
	if d == nil || d.LLM == nil || d.LLM.MaxToolIterations == nil {
		return DefaultControllerMaxIterations
	}
	if n := *d.LLM.MaxToolIterations; n > 0 {
		return n
	}
	return DefaultControllerMaxIterations
}

// ControllerMaxContextTokens returns the prompt size at which observation aging
// starts. Zero disables proactive aging.
func (d *DAG) ControllerMaxContextTokens() int {
	if d == nil || d.LLM == nil || d.LLM.MaxContextTokens == nil {
		return DefaultControllerMaxContextTokens
	}
	return *d.LLM.MaxContextTokens
}

// ControllerObservationMaxBytes returns the maximum controller-facing size of
// one observation. Zero disables the size limit.
func (d *DAG) ControllerObservationMaxBytes() int {
	if d == nil || d.LLM == nil || d.LLM.ObservationMaxBytes == nil {
		return DefaultControllerObservationMaxBytes
	}
	return *d.LLM.ObservationMaxBytes
}

// ControllerObservationKeepRecent returns how many recent observations remain
// complete after aging starts. Zero disables observation aging.
func (d *DAG) ControllerObservationKeepRecent() int {
	if d == nil || d.LLM == nil || d.LLM.ObservationKeepRecent == nil {
		return DefaultControllerObservationKeepRecent
	}
	return *d.LLM.ObservationKeepRecent
}

// NewControllerStep builds the step that carries the controller's LLM config and
// task list. It is appended to a controller DAG at build time and is the node the
// runner drives the decision loop from.
func NewControllerStep(dag *DAG) Step {
	return Step{
		Name:        ControllerStepName,
		Description: "LLM controller",
		LLM:         dag.LLM,
		ExecutorConfig: ExecutorConfig{
			Type: ExecutorTypeController,
		},
	}
}
