// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agentschema "github.com/dagucloud/dagu/internal/agent/schema"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	corespec "github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/llm"
)

const dagDefManageToolName = "dag_def_manage"

func init() {
	RegisterTool(ToolRegistration{
		Name:           dagDefManageToolName,
		Label:          "DAG Definition Manage",
		Description:    "List, inspect, validate, and navigate DAG definitions",
		DefaultEnabled: true,
		Factory: func(cfg ToolConfig) *AgentTool {
			return NewDAGDefManageTool(cfg.DAGStore)
		},
	})
}

type dagDefManageInput struct {
	Action  string   `json:"action"`
	DAGName string   `json:"dagName,omitempty"`
	Spec    string   `json:"spec,omitempty"`
	Path    string   `json:"path,omitempty"`
	Query   string   `json:"query,omitempty"`
	Labels  []string `json:"labels,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Page    int      `json:"page,omitempty"`
	Sort    string   `json:"sort,omitempty"`
	Order   string   `json:"order,omitempty"`
	Full    bool     `json:"full,omitempty"`
}

// NewDAGDefManageTool creates an agent tool for read-only DAG definition inspection.
func NewDAGDefManageTool(store exec.DAGStore) *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        dagDefManageToolName,
				Description: "Inspect and validate DAG definitions. Use list to discover DAGs, get to read the exact YAML and step summary, validate before or after changes, and schema to inspect valid DAG YAML fields.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"enum":        []string{"list", "get", "validate", "schema"},
							"description": "Operation to perform.",
						},
						"dagName": map[string]any{"type": "string", "description": "DAG name or file name for get/validate."},
						"spec":    map[string]any{"type": "string", "description": "Raw DAG YAML spec for validate."},
						"path":    map[string]any{"type": "string", "description": "DAG schema path for schema action, e.g. steps, params, schedule."},
						"query":   map[string]any{"type": "string", "description": "Optional name filter for list."},
						"labels":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional labels filter for list."},
						"limit":   map[string]any{"type": "integer", "description": "Maximum items for list (default 50, max 200)."},
						"page":    map[string]any{"type": "integer", "description": "Optional page number for list when no cursor is available."},
						"sort":    map[string]any{"type": "string", "description": "Optional list sort field, e.g. name, updated_at, created_at, nextRun."},
						"order":   map[string]any{"type": "string", "description": "Optional sort order: asc or desc."},
						"full":    map[string]any{"type": "boolean", "description": "For schema, include full descriptions instead of compact output."},
					},
					"required": []string{"action"},
				},
			},
		},
		Run: func(ctx ToolContext, input json.RawMessage) ToolOut {
			return dagDefManageRun(ctx, input, store)
		},
		Audit: &AuditInfo{
			Action:          dagDefManageToolName,
			DetailExtractor: ExtractFields("action", "dagName", "query", "path"),
		},
	}
}

func dagDefManageRun(toolCtx ToolContext, input json.RawMessage, store exec.DAGStore) ToolOut {
	if store == nil {
		return toolError("%s is unavailable: DAG store is not configured", dagDefManageToolName)
	}
	var args dagDefManageInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}
	if toolCtx.Context == nil {
		toolCtx.Context = context.Background()
	}

	switch strings.TrimSpace(args.Action) {
	case "list":
		return dagDefManageList(toolCtx.Context, store, args)
	case "get":
		return dagDefManageGet(toolCtx.Context, store, args)
	case "validate":
		return dagDefManageValidate(toolCtx.Context, store, args)
	case "schema":
		return dagDefManageSchema(args)
	default:
		return toolError("Unknown action: %s. Use list, get, validate, or schema.", args.Action)
	}
}

func dagDefManageList(ctx context.Context, store exec.DAGStore, args dagDefManageInput) ToolOut {
	limit := clampAgentLimit(args.Limit, 50, 200)
	page := args.Page
	if page <= 0 {
		page = 1
	}
	nameFilter := args.Query
	if nameFilter == "" {
		nameFilter = args.DAGName
	}
	paginator := exec.NewPaginator(page, limit)
	result, warnings, err := store.List(ctx, exec.ListDAGsOptions{
		Paginator: &paginator,
		Name:      nameFilter,
		Labels:    args.Labels,
		Sort:      args.Sort,
		Order:     args.Order,
	})
	if err != nil {
		return toolError("Failed to list DAG definitions: %v", err)
	}
	items := make([]map[string]any, 0, len(result.Items))
	for _, dag := range result.Items {
		if dag == nil {
			continue
		}
		items = append(items, dagDefManageSummary(dag, store.IsSuspended(ctx, dag.Name)))
	}
	return dagManageJSON(map[string]any{
		"action":      "list",
		"items":       items,
		"limit":       limit,
		"hasMore":     result.HasNextPage,
		"currentPage": result.CurrentPage,
		"nextPage":    result.NextPage,
		"totalPages":  result.TotalPages,
		"totalCount":  result.TotalCount,
		"warnings":    warnings,
	})
}

func dagDefManageGet(ctx context.Context, store exec.DAGStore, args dagDefManageInput) ToolOut {
	if args.DAGName == "" {
		return toolError("dagName is required")
	}
	dag, err := store.GetDetails(ctx, args.DAGName, corespec.WithoutEval())
	if err != nil {
		return toolError("Failed to get DAG details: %v", err)
	}
	raw, err := store.GetSpec(ctx, args.DAGName)
	if err != nil {
		return toolError("Failed to get DAG spec: %v", err)
	}
	summary := dagDefManageSummary(dag, store.IsSuspended(ctx, args.DAGName))
	return dagManageJSON(map[string]any{
		"action":  "get",
		"summary": summary,
		"dag":     dag,
		"spec":    raw,
	})
}

func dagDefManageValidate(ctx context.Context, store exec.DAGStore, args dagDefManageInput) ToolOut {
	raw := args.Spec
	var err error
	if raw == "" {
		if args.DAGName == "" {
			return toolError("spec or dagName is required for validate")
		}
		raw, err = store.GetSpec(ctx, args.DAGName)
		if err != nil {
			return toolError("Failed to get DAG spec: %v", err)
		}
	}
	dag, err := store.LoadSpec(ctx, []byte(raw), corespec.WithoutEval())
	if err != nil {
		return dagManageJSON(map[string]any{
			"action": "validate",
			"valid":  false,
			"errors": dagDefValidationErrors(err),
		})
	}
	return dagManageJSON(map[string]any{
		"action":    "validate",
		"valid":     true,
		"dag":       dag,
		"summary":   dagDefManageSummary(dag, false),
		"stepCount": len(dag.Steps),
	})
}

func dagDefManageSchema(args dagDefManageInput) ToolOut {
	var (
		out string
		err error
	)
	if args.Full {
		out, err = agentschema.DefaultRegistry.NavigateFull("dag", args.Path)
	} else {
		out, err = agentschema.DefaultRegistry.Navigate("dag", args.Path)
	}
	if err != nil {
		return toolError("Failed to navigate DAG schema: %v", err)
	}
	return dagManageJSON(map[string]any{
		"action": "schema",
		"path":   args.Path,
		"schema": out,
	})
}

func dagDefManageSummary(dag *core.DAG, suspended bool) map[string]any {
	if dag == nil {
		return map[string]any{}
	}
	steps := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		steps = append(steps, step.Name)
	}
	out := map[string]any{
		"name":      dag.Name,
		"stepCount": len(dag.Steps),
	}
	for key, value := range map[string]string{
		"description": dag.Description,
		"group":       dag.Group,
		"type":        dag.Type,
		"location":    dag.Location,
		"sourceFile":  dag.SourceFile,
	} {
		if value != "" {
			out[key] = value
		}
	}
	if labels := dag.Labels.Strings(); len(labels) > 0 {
		out["labels"] = labels
	}
	if len(steps) > 0 {
		out["steps"] = steps
	}
	if suspended {
		out["suspended"] = true
	}
	return out
}

func dagDefValidationErrors(err error) []string {
	var errList core.ErrorList
	if errors.As(err, &errList) {
		return errList.ToStringList()
	}
	return []string{err.Error()}
}
