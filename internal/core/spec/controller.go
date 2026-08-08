// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// controllerTask is the intermediate representation of a controller goal.
type controllerTask struct {
	// Name identifies the task.
	Name string `yaml:"name,omitempty"`
	// Description states when the task is considered complete.
	Description string `yaml:"description,omitempty"`
}

func buildTasks(_ buildContext, d *dag) ([]ir.ControllerTask, error) {
	if len(d.Tasks) == 0 {
		return nil, nil
	}

	tasks := make([]ir.ControllerTask, 0, len(d.Tasks))
	for _, t := range d.Tasks {
		tasks = append(tasks, ir.ControllerTask{
			Name:        strings.TrimSpace(t.Name),
			Description: strings.TrimSpace(t.Description),
		})
	}
	return tasks, nil
}

// injectControllerStep appends the synthesized controller step to a controller
// DAG. The controller step is the node the runner drives its decision loop from;
// the declared steps become the catalog of actions it may choose between.
func injectControllerStep(result *ir.DAG) error {
	if !result.IsController() {
		if len(result.Tasks) > 0 {
			return ir.NewValidationError("tasks", nil,
				fmt.Errorf("tasks require type: controller"))
		}
		return nil
	}

	for i, step := range result.Steps {
		for _, reserved := range []string{ir.ControllerStepName, ir.AskUserStepName} {
			if step.Name == reserved || step.ID == reserved {
				return ir.NewValidationError("steps", step.Name,
					fmt.Errorf("%q is reserved by type: controller", reserved))
			}
		}
		// A failed action never aborts a controller run: the failure is reported
		// to the controller, which decides whether to retry, route elsewhere, or
		// give up.
		result.Steps[i].ContinueOn.Failure = true
		// mark_success would rewrite a failed action as succeeded, hiding it from
		// the run status the controller's own decisions are meant to determine.
		result.Steps[i].ContinueOn.MarkSuccess = false
	}

	askUser, err := newAskUserStep()
	if err != nil {
		return err
	}
	result.Steps = append(result.Steps, ir.NewControllerStep(result), askUser)
	return nil
}

// newAskUserStep builds the human task a controller opens to ask a question of
// its own. The prompt is empty here because the controller writes it at runtime;
// only the shape of the answer is fixed.
func newAskUserStep() (ir.Step, error) {
	form, outputs, err := buildHumanTaskForm(map[string]any{
		"type": "object",
		"properties": map[string]any{
			ir.AskUserAnswerField: map[string]any{
				"type":        "string",
				"description": "Your answer to the controller's question.",
			},
		},
		"required": []any{ir.AskUserAnswerField},
	})
	if err != nil {
		return ir.Step{}, fmt.Errorf("failed to build the ask_user form: %w", err)
	}

	return ir.Step{
		Name:        ir.AskUserStepName,
		ID:          ir.AskUserStepName,
		Description: "Question asked by the controller",
		HumanTask:   &ir.HumanTaskConfig{Form: form},
		Outputs:     outputs,
	}, nil
}
