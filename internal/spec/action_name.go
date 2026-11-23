// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"maps"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ActionName returns the action a loaded step runs, as written in a step's
// action field, such as ssh.run, file.copy, or the name of a custom action.
// An action whose step loads as another action's step, such as
// browser.extract as browser.run, returns the other action's name. A step
// whose executor type no action names, such as a run step, returns its
// executor type.
func ActionName(step *ir.Step) string {
	cfg := step.ExecutorConfig
	if custom, _ := cfg.Metadata["custom_type"].(string); custom != "" {
		return custom
	}
	executorType := cfg.Type
	switch executorType {
	case ir.ExecutorTypeAction:
		if ref, _ := cfg.Config["ref"].(string); ref != "" {
			return ref
		}
	case "redis":
		if command, _ := cfg.Config["command"].(string); command != "" {
			return "redis." + strings.ToLower(command)
		}
	case ir.ExecutorTypeParallel, ir.ExecutorTypeSubworkflow:
		// A dag.run step with parallel loads as parallel, and subworkflow is
		// the legacy name of the dag executor.
		executorType = ir.ExecutorTypeDAG
	}
	var fallback string
	for _, name := range builtinActionsByExecutor[executorType] {
		selects := builtinActions[name].selects
		switch {
		case selects == nil:
			fallback = name
		case selects(step):
			return name
		}
	}
	if fallback != "" {
		return fallback
	}
	return cfg.Type
}

// builtinActionsByExecutor lists the builtin actions by the executor type of
// their loaded steps, in name order so a lookup does not depend on map order.
var builtinActionsByExecutor = indexBuiltinActions()

func indexBuiltinActions() map[string][]string {
	index := make(map[string][]string)
	for _, name := range slices.Sorted(maps.Keys(builtinActions)) {
		action := builtinActions[name]
		if action.executorType != "" || action.selects != nil {
			index[action.executorType] = append(index[action.executorType], name)
		}
	}
	return index
}

// stepSelector reports whether a loaded step came from one builtin action.
type stepSelector func(step *ir.Step) bool

// operationIs selects the step whose command is operation, as an operation
// action writes it.
func operationIs(operation string) stepSelector {
	return func(step *ir.Step) bool {
		return len(step.Commands) > 0 && step.Commands[0].Command == operation
	}
}

// configHas selects the step whose executor config sets key.
func configHas(key string) stepSelector {
	return func(step *ir.Step) bool {
		_, ok := step.ExecutorConfig.Config[key]
		return ok
	}
}

// configIs selects the step whose executor config sets key to value.
func configIs(key, value string) stepSelector {
	return func(step *ir.Step) bool {
		got, _ := step.ExecutorConfig.Config[key].(string)
		return got == value
	}
}

// isHumanTask selects the step that waits for a person to complete a task.
func isHumanTask(step *ir.Step) bool {
	return step.HumanTask != nil
}
