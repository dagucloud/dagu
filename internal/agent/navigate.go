// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/llm"
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           "navigate",
		Label:          "Navigate",
		Description:    "Navigate Dagu UI pages",
		DefaultEnabled: true,
		Factory:        func(_ ToolConfig) *AgentTool { return NewNavigateTool() },
	})
}

// NavigateToolInput is the input schema for the navigate tool.
type NavigateToolInput struct {
	Path string `json:"path"`
}

const navigateDescription = "Navigate the user to a specific page in the Dagu UI. " +
	"Only use this when the user explicitly asks to navigate or view a page. " +
	"Available paths: '/dags' for DAG list, '/dags/<dag-name>' for DAG details, " +
	"'/dags/<dag-name>/<tab>' for specific tab (spec, history), '/dag-runs' for all DAG runs, " +
	"'/dag-runs/<dag-name>/<run-id>' for specific run details, '/queues' for queues, " +
	"'/docs' for the documents page, '/docs/<doc-id>' for a specific document, " +
	"'/system-status', '/webhooks', and '/audit-logs'. " +
	"Admin-only pages: '/users', '/api-keys', '/terminal', '/git-sync', '/agent-settings'."

var adminOnlyNavigatePaths = []string{
	"/users",
	"/api-keys",
	"/terminal",
	"/git-sync",
	"/agent-settings",
}

// NewNavigateTool creates a new navigate tool for UI navigation.
func NewNavigateTool() *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "navigate",
				Description: navigateDescription,
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "The path to navigate to (e.g., '/dags/my-dag', '/dags/my-dag/spec', '/dag-runs')",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		Run: navigateRun,
	}
}

func navigateRun(ctx ToolContext, input json.RawMessage) ToolOut {
	var args NavigateToolInput
	if err := decodeToolInput(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}

	if args.Path == "" {
		return toolError("Path is required")
	}
	if isAdminOnlyPath(args.Path) && ctx.Role.IsSet() && !ctx.Role.IsAdmin() {
		return toolError("Permission denied: navigation to %s requires admin role", args.Path)
	}

	if ctx.EmitUIAction != nil {
		ctx.EmitUIAction(UIAction{
			Type: UIActionNavigate,
			Path: args.Path,
		})
	}

	return ToolOut{Content: fmt.Sprintf("Navigating user to %s", args.Path)}
}

func isAdminOnlyPath(path string) bool {
	for _, prefix := range adminOnlyNavigatePaths {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
