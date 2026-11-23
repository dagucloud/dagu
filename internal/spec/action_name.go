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
// browser.extract as browser.run, returns the other action's name. A legacy
// step whose operation no action has, such as type archive with command
// pack, returns the name that operation would have, archive.pack. A step
// whose executor type no action names, such as a run step, returns its
// executor type.
func ActionName(step *ir.Step) string {
	cfg := step.ExecutorConfig
	if custom, _ := cfg.Metadata["custom_type"].(string); custom != "" {
		return custom
	}
	if step.HumanTask != nil {
		return "human.task"
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
	actions := builtinActionsByExecutor[executorType]
	if actions.variant != nil {
		if variant := actions.variant(step); variant != "" {
			return executorType + "." + variant
		}
	}
	if actions.defaultName != "" {
		return actions.defaultName
	}
	return cfg.Type
}

// executorActions are the builtin actions whose steps load as one executor
// type.
type executorActions struct {
	// variant reads the variant of every action of the type but the default.
	variant stepVariant
	// defaultName names the action of a step with no variant.
	defaultName string
}

var builtinActionsByExecutor = indexBuiltinActions()

func indexBuiltinActions() map[string]executorActions {
	index := make(map[string]executorActions)
	// Name order keeps the index independent of map order.
	for _, name := range slices.Sorted(maps.Keys(builtinActions)) {
		action := builtinActions[name]
		if action.executorType == "" {
			continue
		}
		actions := index[action.executorType]
		if action.variant != nil {
			actions.variant = action.variant
		} else {
			actions.defaultName = name
		}
		index[action.executorType] = actions
	}
	return index
}

// stepVariant returns the part of a loaded step's action name after its
// executor type, or "" when the step sets none.
type stepVariant func(step *ir.Step) string

// operationVariant reads the operation an operation action writes as the
// step's command.
func operationVariant(step *ir.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return step.Commands[0].Command
}

// configValueVariant reads the string value of key in the step's executor
// config.
func configValueVariant(key string) stepVariant {
	return func(step *ir.Step) string {
		value, _ := step.ExecutorConfig.Config[key].(string)
		return value
	}
}

// configKeyVariant reads key itself when the step's executor config sets it.
func configKeyVariant(key string) stepVariant {
	return func(step *ir.Step) string {
		if _, ok := step.ExecutorConfig.Config[key]; ok {
			return key
		}
		return ""
	}
}
