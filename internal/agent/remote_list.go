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
		Name:           "list_contexts",
		Label:          "List Contexts",
		Description:    "List available remote CLI contexts for remote_agent",
		DefaultEnabled: true,
		Factory: func(cfg ToolConfig) *AgentTool {
			if cfg.RemoteContextResolver == nil {
				return nil
			}
			return NewListContextsTool(cfg.RemoteContextResolver)
		},
	})
}

type listContextsInput struct {
	NameFilter string `json:"name_filter,omitempty"`
}

func NewListContextsTool(resolver RemoteContextResolver) *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "list_contexts",
				Description: "List available remote CLI contexts that can be targeted by remote_agent.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name_filter": map[string]any{
							"type":        "string",
							"description": "Optional substring filter to match context names",
						},
					},
				},
			},
		},
		Run: makeListContextsRun(resolver),
		Audit: &AuditInfo{
			Action:          "contexts_list",
			DetailExtractor: ExtractFields("name_filter"),
		},
	}
}

func makeListContextsRun(resolver RemoteContextResolver) ToolFunc {
	return func(ctx ToolContext, input json.RawMessage) ToolOut {
		if ctx.Role.IsSet() && !ctx.Role.CanExecute() {
			return toolError("Permission denied: list_contexts requires execute permission")
		}

		var args listContextsInput
		if err := decodeToolInput(input, &args); err != nil {
			return toolError("Failed to parse input: %v", err)
		}

		contexts, err := resolver.ListRemoteContexts(ctx.Context)
		if err != nil {
			return toolError("Failed to list remote contexts: %v", err)
		}

		if args.NameFilter != "" {
			filter := strings.ToLower(args.NameFilter)
			var filtered []RemoteContextInfo
			for _, item := range contexts {
				if strings.Contains(strings.ToLower(item.Name), filter) {
					filtered = append(filtered, item)
				}
			}
			contexts = filtered
		}

		if len(contexts) == 0 {
			return ToolOut{Content: "No remote contexts found."}
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Found %d remote context(s):\n\n", len(contexts))
		for _, item := range contexts {
			fmt.Fprintf(&sb, "- **%s**", item.Name)
			if item.Description != "" {
				fmt.Fprintf(&sb, ": %s", item.Description)
			}
			sb.WriteByte('\n')
		}

		return ToolOut{Content: sb.String()}
	}
}
