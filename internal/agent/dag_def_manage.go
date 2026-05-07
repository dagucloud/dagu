// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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
	Action  string              `json:"action"`
	DAGName string              `json:"dagName,omitempty"`
	Spec    string              `json:"spec,omitempty"`
	Path    string              `json:"path,omitempty"`
	Query   string              `json:"query,omitempty"`
	Labels  dagManageStringList `json:"labels,omitempty"`
	Limit   int                 `json:"limit,omitempty"`
	Cursor  string              `json:"cursor,omitempty"`
	Page    int                 `json:"page,omitempty"`
	Sort    string              `json:"sort,omitempty"`
	Order   string              `json:"order,omitempty"`
	Full    bool                `json:"full,omitempty"`
}

type dagDefManageDAGSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Group       string   `json:"group,omitempty"`
	Type        string   `json:"type,omitempty"`
	Location    string   `json:"location,omitempty"`
	SourceFile  string   `json:"sourceFile,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	StepCount   int      `json:"stepCount"`
	Steps       []string `json:"steps,omitempty"`
	Suspended   bool     `json:"suspended,omitempty"`
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
						"cursor":  map[string]any{"type": "string", "description": "Opaque cursor returned by list."},
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
	if err := json.Unmarshal(input, &args); err != nil {
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
	page, err := decodeDAGDefCursor(args.Cursor)
	if err != nil {
		return toolError("%v", err)
	}
	if page == 0 {
		page = args.Page
	}
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
		Labels:    []string(args.Labels),
		Sort:      args.Sort,
		Order:     args.Order,
	})
	if err != nil {
		return toolError("Failed to list DAG definitions: %v", err)
	}
	items := make([]dagDefManageDAGSummary, 0, len(result.Items))
	for _, dag := range result.Items {
		items = append(items, summarizeDAGDefinition(dag, store.IsSuspended(ctx, dag.Name)))
	}
	nextCursor := ""
	if result.HasNextPage {
		nextCursor = encodeDAGDefCursor(result.NextPage)
	}
	return dagManageJSON(map[string]any{
		"action":      "list",
		"items":       items,
		"limit":       limit,
		"cursor":      args.Cursor,
		"nextCursor":  nextCursor,
		"hasMore":     nextCursor != "",
		"currentPage": result.CurrentPage,
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
	summary := summarizeDAGDefinition(dag, store.IsSuspended(ctx, args.DAGName))
	return dagManageJSON(map[string]any{
		"action":      "get",
		"name":        summary.Name,
		"description": summary.Description,
		"group":       summary.Group,
		"type":        summary.Type,
		"location":    summary.Location,
		"sourceFile":  summary.SourceFile,
		"labels":      summary.Labels,
		"stepCount":   summary.StepCount,
		"steps":       summary.Steps,
		"suspended":   summary.Suspended,
		"spec":        raw,
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
		"dag":       summarizeDAGDefinition(dag, false),
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

func summarizeDAGDefinition(dag *core.DAG, suspended bool) dagDefManageDAGSummary {
	if dag == nil {
		return dagDefManageDAGSummary{}
	}
	steps := make([]string, 0, len(dag.Steps))
	for _, step := range dag.Steps {
		steps = append(steps, step.Name)
	}
	return dagDefManageDAGSummary{
		Name:        dag.Name,
		Description: dag.Description,
		Group:       dag.Group,
		Type:        dag.Type,
		Location:    dag.Location,
		SourceFile:  dag.SourceFile,
		Labels:      dag.Labels.Strings(),
		StepCount:   len(dag.Steps),
		Steps:       steps,
		Suspended:   suspended,
	}
}

func dagDefValidationErrors(err error) []string {
	var errList core.ErrorList
	if errors.As(err, &errList) {
		return errList.ToStringList()
	}
	return []string{err.Error()}
}

func encodeDAGDefCursor(page int) string {
	return fmt.Sprintf("page:%d", page)
}

func decodeDAGDefCursor(cursor string) (int, error) {
	if strings.TrimSpace(cursor) == "" {
		return 0, nil
	}
	page, ok := strings.CutPrefix(cursor, "page:")
	if !ok {
		return 0, fmt.Errorf("invalid cursor")
	}
	n, err := strconv.Atoi(page)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return n, nil
}
