// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "fmt"

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
	//	dagu human-task complete --step ask_user --inputs-json '{"answer":"..."}'
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

// ValidateController checks the DAG-level invariants of a controller DAG: an LLM
// must be configured, at least one uniquely named task must be declared, and the
// declared steps must form a tool catalog rather than a dependency graph.
func ValidateController(d *DAG) error {
	if d == nil || !d.IsController() {
		return nil
	}

	var errs ErrorList

	if d.LLM == nil {
		errs = append(errs, NewValidationError("llm", nil,
			fmt.Errorf("type: controller requires an llm configuration")))
	}

	if len(d.Tasks) == 0 {
		errs = append(errs, NewValidationError("tasks", nil,
			fmt.Errorf("type: controller requires at least one task")))
	}

	seen := make(map[string]struct{}, len(d.Tasks))
	for _, task := range d.Tasks {
		if task.Name == "" {
			errs = append(errs, NewValidationError("tasks.name", nil,
				fmt.Errorf("task name must not be empty")))
			continue
		}
		if _, dup := seen[task.Name]; dup {
			errs = append(errs, NewValidationError("tasks.name", task.Name,
				fmt.Errorf("duplicate task name: %s", task.Name)))
			continue
		}
		seen[task.Name] = struct{}{}
		if task.Description == "" {
			errs = append(errs, NewValidationError("tasks.description", task.Name,
				fmt.Errorf("task %q must declare a description stating when it is complete", task.Name)))
		}
	}

	actionable := 0
	for _, step := range d.Steps {
		if IsSynthesizedControllerStep(step.Name) {
			continue
		}
		actionable++
		if len(step.Depends) > 0 || step.ExplicitlyNoDeps {
			errs = append(errs, NewValidationError("depends", step.Depends,
				fmt.Errorf("step %q: depends is not allowed in type: controller; the controller decides step order", step.Name)))
		}
		if step.Router != nil {
			errs = append(errs, NewValidationError("router", step.Name,
				fmt.Errorf("step %q: router steps require type 'graph'", step.Name)))
		}
	}

	if actionable == 0 {
		errs = append(errs, NewValidationError("steps", nil,
			fmt.Errorf("type: controller requires at least one step for the controller to run")))
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}
