// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package agenttools builds the effective tool catalog of a chat.completion
// step (Spec 090) and runs the calls of its tools.
package agenttools

import (
	"context"
	"fmt"
	"sort"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/toolschema"
	"github.com/dagucloud/dagu/v2/internal/runctx"
)

// toolParam represents a parsed parameter definition.
type toolParam = toolschema.Param

// ToolRegistry manages tool DAGs and converts them to LLM tool format.
type ToolRegistry struct {
	// tools maps the tool name to its tool info
	tools map[string]*toolInfo
	// dagNames maps the tool name to the DAG that the tool runs (for lookup)
	dagNames map[string]string
	// order lists the tool names in the order they joined the catalog. A
	// registry built without it lists the tools sorted by name.
	order []string
	// skills is the effective list of skills of the step. It is nil when the
	// step has none.
	skills *SkillSet
}

// toolInfo contains the parsed tool information from a DAG.
type toolInfo struct {
	Name        string
	Description string
	DAG         *ir.DAG
	Params      []toolParam
	// Source tells where the tool comes from: dag:<name>, mcp:<name> (the
	// label of the server) or skills.
	Source string
	// Fixed holds the inputs that the workflow supplies. The model neither sees
	// nor sets them.
	Fixed map[string]string
	// Schema replaces the schema built from Params when it is set.
	Schema map[string]any
	// MCP is set for a tool that an MCP server runs.
	MCP *mcpBinding
	// Skills is set for the tool load_skill.
	Skills *skillBinding
}

// loadToolDAG returns the DAG that a tool runs: a local DAG of the same file
// first, then a DAG that the loader finds by name.
func loadToolDAG(ctx context.Context, dagName string) (*ir.DAG, error) {
	rCtx := runctx.GetContext(ctx)

	// A local DAG defined in the same file (using --- separator) wins. This
	// follows the same pattern as SubDAGExecutor.NewSubDAGExecutor().
	if rCtx.DAG != nil && rCtx.DAG.LocalDAGs != nil {
		if localDAG, ok := rCtx.DAG.LocalDAGs[dagName]; ok {
			return localDAG, nil
		}
	}

	if rCtx.DAGLoader == nil {
		return nil, fmt.Errorf("loading non-local tool DAG %q requires DAGLoader", dagName)
	}
	dag, err := rCtx.DAGLoader.GetDAG(ctx, dagName)
	if err != nil {
		return nil, fmt.Errorf("failed to load tool DAG %q: %w", dagName, err)
	}
	return dag, nil
}

// toolParamsForDAG reads the input definitions of a tool DAG.
func toolParamsForDAG(dag *ir.DAG, dagName string) ([]toolParam, error) {
	params := toolschema.ParamsFromDefs(dag.ParamDefs)
	if len(params) == 0 {
		var err error
		params, err = toolschema.ParseParams(dag.DefaultParams)
		if err != nil {
			return nil, fmt.Errorf("failed to parse params for tool DAG %q: %w", dagName, err)
		}
	}
	return params, nil
}

// names lists the tool names in catalog order.
func (r *ToolRegistry) names() []string {
	if r == nil {
		return nil
	}
	if len(r.order) == len(r.tools) {
		return r.order
	}
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ToLLMTools converts the registry to LLM tool format.
func (r *ToolRegistry) ToLLMTools() []llmpkg.Tool {
	if r == nil {
		return nil
	}

	tools := make([]llmpkg.Tool, 0, len(r.tools))
	for _, name := range r.names() {
		info := r.tools[name]
		parameters := info.Schema
		if parameters == nil {
			parameters = toolschema.Build(info.Params)
		}
		tools = append(tools, llmpkg.Tool{
			Type: "function",
			Function: llmpkg.ToolFunction{
				Name:        info.Name,
				Description: info.Description,
				Parameters:  parameters,
			},
		})
	}

	return tools
}

// ToolDefinitions lists the offered tools for the record of the step.
func (r *ToolRegistry) ToolDefinitions() []ir.ToolDefinition {
	if r == nil {
		return nil
	}
	offered := r.ToLLMTools()
	definitions := make([]ir.ToolDefinition, 0, len(offered))
	for _, tool := range offered {
		definitions = append(definitions, ir.ToolDefinition{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
			Source:      r.tools[tool.Function.Name].Source,
		})
	}
	return definitions
}

// SkillSet returns the effective skills of the step. It is nil when the step
// has none.
func (r *ToolRegistry) SkillSet() *SkillSet {
	if r == nil {
		return nil
	}
	return r.skills
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

// HasTool reports whether the catalog has a tool of this name.
func (r *ToolRegistry) HasTool(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.tools[name]
	return ok
}

// HasTools returns true if any tools are registered.
func (r *ToolRegistry) HasTools() bool {
	return r != nil && len(r.tools) > 0
}
