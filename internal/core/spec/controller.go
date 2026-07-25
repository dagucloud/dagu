// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
)

// controllerTask is the intermediate representation of a controller goal.
type controllerTask struct {
	// Name identifies the task.
	Name string `yaml:"name,omitempty"`
	// Description states when the task is considered complete.
	Description string `yaml:"description,omitempty"`
}

func buildTasks(_ BuildContext, d *dag) ([]core.ControllerTask, error) {
	if len(d.Tasks) == 0 {
		return nil, nil
	}

	tasks := make([]core.ControllerTask, 0, len(d.Tasks))
	for _, t := range d.Tasks {
		tasks = append(tasks, core.ControllerTask{
			Name:        strings.TrimSpace(t.Name),
			Description: strings.TrimSpace(t.Description),
		})
	}
	return tasks, nil
}

// injectControllerStep appends the synthesized controller step to a controller
// DAG. The controller step is the node the runner drives its decision loop from;
// the declared steps become the catalog of actions it may choose between.
func injectControllerStep(result *core.DAG) error {
	if !result.IsController() {
		if len(result.Tasks) > 0 {
			return core.NewValidationError("tasks", nil,
				fmt.Errorf("tasks require type: controller"))
		}
		return nil
	}

	for i, step := range result.Steps {
		if step.Name == core.ControllerStepName || step.ID == core.ControllerStepName {
			return core.NewValidationError("steps", step.Name,
				fmt.Errorf("%q is reserved for the controller step", core.ControllerStepName))
		}
		// A failed action never aborts a controller run: the failure is reported
		// to the controller, which decides whether to retry, route elsewhere, or
		// give up.
		result.Steps[i].ContinueOn.Failure = true
	}

	result.Steps = append(result.Steps, core.NewControllerStep(result))
	return nil
}
