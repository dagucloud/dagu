// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package chat provides an executor for LLM-based session steps.
package chat

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/toolschema"
	"github.com/dagucloud/dagu/v2/internal/runctx"
)

// toolParam represents a parsed parameter definition.
type toolParam = toolschema.Param

// ToolRegistry manages tool DAGs and converts them to LLM tool format.
type ToolRegistry struct {
	// tools maps tool name (from DAG.Name) to tool info
	tools map[string]*toolInfo
	// dagNames maps tool name to original DAG name (for lookup)
	dagNames map[string]string
}

// toolInfo contains the parsed tool information from a DAG.
type toolInfo struct {
	Name        string
	Description string
	DAG         *ir.DAG
	Params      []toolParam
}

// NewToolRegistry creates a registry for the named tool DAGs.
func NewToolRegistry(ctx context.Context, dagNames []string) (*ToolRegistry, error) {
	if len(dagNames) == 0 {
		return nil, nil
	}

	rCtx := runctx.GetContext(ctx)

	registry := &ToolRegistry{
		tools:    make(map[string]*toolInfo),
		dagNames: make(map[string]string),
	}

	for _, dagName := range dagNames {
		var dag *ir.DAG

		// First, check if it's a local DAG defined in the same file (using --- separator)
		// This follows the same pattern as SubDAGExecutor.NewSubDAGExecutor()
		if rCtx.DAG != nil && rCtx.DAG.LocalDAGs != nil {
			if localDAG, ok := rCtx.DAG.LocalDAGs[dagName]; ok {
				dag = localDAG
			}
		}

		// Load the named DAG when no inline definition matched.
		if dag == nil {
			if rCtx.DAGLoader == nil {
				return nil, fmt.Errorf("loading non-local tool DAG %q requires DAGLoader", dagName)
			}
			var err error
			dag, err = rCtx.DAGLoader.GetDAG(ctx, dagName)
			if err != nil {
				return nil, fmt.Errorf("failed to load tool DAG %q: %w", dagName, err)
			}
		}

		// Use DAG.Name as the tool name (this is what the LLM will use)
		toolName := dag.Name
		if toolName == "" {
			toolName = dagName // Fallback to filename-based name
		}

		params := toolschema.ParamsFromDefs(dag.ParamDefs)
		if len(params) == 0 {
			var err error
			params, err = toolschema.ParseParams(dag.DefaultParams)
			if err != nil {
				return nil, fmt.Errorf("failed to parse params for tool DAG %q: %w", dagName, err)
			}
		}

		info := &toolInfo{
			Name:        toolName,
			Description: dag.Description,
			DAG:         dag,
			Params:      params,
		}

		registry.tools[toolName] = info
		registry.dagNames[toolName] = dagName
	}

	return registry, nil
}

// ToLLMTools converts the registry to LLM tool format.
func (r *ToolRegistry) ToLLMTools() []llmpkg.Tool {
	if r == nil {
		return nil
	}

	tools := make([]llmpkg.Tool, 0, len(r.tools))
	for _, info := range r.tools {
		tools = append(tools, llmpkg.Tool{
			Type: "function",
			Function: llmpkg.ToolFunction{
				Name:        info.Name,
				Description: info.Description,
				Parameters:  toolschema.Build(info.Params),
			},
		})
	}

	return tools
}

// GetDAGByToolName returns the DAG for a given tool name.
func (r *ToolRegistry) GetDAGByToolName(toolName string) (*ir.DAG, bool) {
	if r == nil {
		return nil, false
	}
	info, ok := r.tools[toolName]
	if !ok {
		return nil, false
	}
	return info.DAG, true
}

// GetDAGName returns the original DAG name for a tool name.
func (r *ToolRegistry) GetDAGName(toolName string) (string, bool) {
	if r == nil {
		return "", false
	}
	name, ok := r.dagNames[toolName]
	return name, ok
}

// HasTools returns true if any tools are registered.
func (r *ToolRegistry) HasTools() bool {
	return r != nil && len(r.tools) > 0
}
