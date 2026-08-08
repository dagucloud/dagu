// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/toolschema"
)

const (
	// SetTaskStatusTool is the name of the tool the controller calls to record
	// where a task stands. It is reserved and cannot name a step.
	SetTaskStatusTool = "set_task_status"

	// AskUserTool is the name of the tool the controller calls to put a question
	// to a person. It is reserved and cannot name a step.
	AskUserTool = "ask_user"
)

// maxToolNameLen is the longest function name accepted across providers.
const maxToolNameLen = 64

var unsafeToolNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// Catalog is the set of actions a controller may choose from, expressed as LLM
// function-calling tools.
type Catalog struct {
	tools      []llmpkg.Tool
	stepByTool map[string]string
}

// NewCatalog builds the tool catalog from a controller DAG's declared steps. A
// step that runs a child DAG advertises that DAG's parameters, so the controller
// can pass arguments through.
func NewCatalog(ctx context.Context, dag *ir.DAG) (*Catalog, error) {
	c := &Catalog{stepByTool: make(map[string]string)}

	used := map[string]struct{}{SetTaskStatusTool: {}, AskUserTool: {}}
	for _, step := range dag.Steps {
		// The controller and its ask_user task are scaffolding, not actions the
		// model may pick.
		if ir.IsSynthesizedControllerStep(step.Name) {
			continue
		}

		name := uniqueToolName(toolName(step), used)
		used[name] = struct{}{}
		c.stepByTool[name] = step.Name

		var child *ir.DAG
		if step.SubDAG != nil {
			child = resolveChildDAG(ctx, dag, step.SubDAG.Name)
		}

		params, err := stepParameters(child, step)
		if err != nil {
			return nil, err
		}

		c.tools = append(c.tools, llmpkg.Tool{
			Type: "function",
			Function: llmpkg.ToolFunction{
				Name:        name,
				Description: stepDescription(step, child),
				Parameters:  params,
			},
		})
	}

	c.tools = append(c.tools, llmpkg.Tool{
		Type: "function",
		Function: llmpkg.ToolFunction{
			Name: AskUserTool,
			Description: "Put a question to a person and wait for their answer. The run pauses " +
				"until someone replies, so ask only when you genuinely cannot proceed and no " +
				"action would resolve the doubt.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type": "string",
						"description": "What you need to know, with enough context for someone " +
							"who has not been following the run.",
					},
				},
				"required": []string{"question"},
			},
		},
	})

	c.tools = append(c.tools, llmpkg.Tool{
		Type: "function",
		Function: llmpkg.ToolFunction{
			Name: SetTaskStatusTool,
			Description: "Record where a task from the task list stands. The run ends once no " +
				"task is open, and succeeds unless a task is failed.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task": map[string]any{
						"type":        "string",
						"description": "Name of the task to update.",
					},
					"status": map[string]any{
						"type": "string",
						"enum": []string{
							string(TaskCompleted),
							string(TaskSkipped),
							string(TaskFailed),
							string(TaskOpen),
						},
						"description": "completed: the task's criteria are now satisfied. " +
							"skipped: the task turned out to be unnecessary, so there is nothing to do. " +
							"failed: the task cannot be achieved, which fails the run. " +
							"open: undo an earlier decision because later work invalidated it.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Why the task is in this status.",
					},
				},
				"required": []string{"task", "status", "reason"},
			},
		},
	})

	return c, nil
}

// Tools returns the tool definitions to send to the model.
func (c *Catalog) Tools() []llmpkg.Tool {
	return c.tools
}

// StepFor resolves a tool name back to the step it runs. It returns false for
// CompleteTaskTool and for unknown names.
func (c *Catalog) StepFor(tool string) (string, bool) {
	step, ok := c.stepByTool[tool]
	return step, ok
}

// ToolNames lists every advertised tool name.
func (c *Catalog) ToolNames() []string {
	names := make([]string, 0, len(c.tools))
	for _, tool := range c.tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

// Definitions returns the catalog in the form persisted for UI visibility.
func (c *Catalog) Definitions() []dagrun.ToolDefinition {
	defs := make([]dagrun.ToolDefinition, 0, len(c.tools))
	for _, tool := range c.tools {
		defs = append(defs, dagrun.ToolDefinition{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
		})
	}
	return defs
}

// stepParameters derives the JSON Schema of the arguments a step accepts. Only
// steps that launch a child DAG take arguments; everything else is a nullary
// action. A parameter the step already supplies a value for is left out: the
// author has decided it, so it is not the controller's to choose.
func stepParameters(child *ir.DAG, step ir.Step) (map[string]any, error) {
	if step.SubDAG != nil {
		if err := validateNamedSubDAGParams(step); err != nil {
			return nil, err
		}
	}
	if child == nil {
		return toolschema.Build(nil), nil
	}
	params, err := toolschema.ParamsForDAG(child)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", step.Name, err)
	}
	return toolschema.Build(omitPinned(params, PinnedParams(step))), nil
}

func validateNamedSubDAGParams(step ir.Step) error {
	values, err := step.Params.AsStringMap()
	if err != nil {
		return fmt.Errorf("step %q: invalid child DAG parameters: %w", step.Name, err)
	}
	if _, positional := values[""]; positional {
		return fmt.Errorf("step %q: controller child DAG parameters must be named", step.Name)
	}
	return nil
}

// PinnedParams lists the parameter names a step supplies itself.
func PinnedParams(step ir.Step) map[string]struct{} {
	values, err := step.Params.AsStringMap()
	if err != nil || len(values) == 0 {
		return nil
	}
	pinned := make(map[string]struct{}, len(values))
	for name := range values {
		if name != "" {
			pinned[name] = struct{}{}
		}
	}
	return pinned
}

// omitPinned drops the parameters the step already decided.
func omitPinned(params []toolschema.Param, pinned map[string]struct{}) []toolschema.Param {
	if len(pinned) == 0 {
		return params
	}
	kept := make([]toolschema.Param, 0, len(params))
	for _, param := range params {
		if _, ok := pinned[param.Name]; ok {
			continue
		}
		kept = append(kept, param)
	}
	return kept
}

// resolveChildDAG looks up a child DAG by name, preferring DAGs defined inline
// in the same file. An unresolvable name yields nil: the step itself reports the
// failure when the controller runs it.
func resolveChildDAG(ctx context.Context, dag *ir.DAG, name string) *ir.DAG {
	if name == "" {
		return nil
	}
	if local, ok := dag.LocalDAGs[name]; ok {
		return local
	}

	rCtx := exec.GetContext(ctx)
	if rCtx.DB == nil {
		return nil
	}
	child, err := rCtx.DB.GetDAG(ctx, name)
	if err != nil {
		return nil
	}
	return child
}

// stepDescription tells the model what an action does. An explicit step
// description wins; otherwise the target's own description is the next best
// thing the author has written down.
func stepDescription(step ir.Step, child *ir.DAG) string {
	if step.Description != "" {
		return step.Description
	}
	if step.HumanTask != nil {
		return "Ask a person: " + step.HumanTask.Prompt
	}
	if child != nil && child.Description != "" {
		return child.Description
	}
	if step.SubDAG != nil {
		return fmt.Sprintf("Run the %q workflow.", step.SubDAG.Name)
	}
	return fmt.Sprintf("Run the %q step.", step.Name)
}

func toolName(step ir.Step) string {
	if step.ID != "" {
		return step.ID
	}
	return step.Name
}

// uniqueToolName sanitizes a step identifier into a provider-safe function name
// and disambiguates it against names already in the catalog.
func uniqueToolName(raw string, used map[string]struct{}) string {
	name := unsafeToolNameChars.ReplaceAllString(strings.TrimSpace(raw), "_")
	name = strings.Trim(name, "_-")
	if name == "" {
		name = "step"
	}
	if len(name) > maxToolNameLen {
		name = name[:maxToolNameLen]
	}

	candidate := name
	for i := 2; ; i++ {
		if _, taken := used[candidate]; !taken {
			return candidate
		}
		suffix := fmt.Sprintf("_%d", i)
		trimmed := name
		if len(trimmed)+len(suffix) > maxToolNameLen {
			trimmed = trimmed[:maxToolNameLen-len(suffix)]
		}
		candidate = trimmed + suffix
	}
}
