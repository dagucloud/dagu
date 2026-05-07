// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/llm"
	"github.com/google/uuid"
)

func init() {
	RegisterTool(ToolRegistration{
		Name:           "ask_user",
		Label:          "Ask User",
		Description:    "Ask user questions",
		DefaultEnabled: true,
		Factory:        func(_ ToolConfig) *AgentTool { return NewAskUserTool() },
	})
}

// AskUserToolInput is the input schema for the ask_user tool.
type AskUserToolInput struct {
	Question            string   `json:"question"`
	Options             []string `json:"options"`
	AllowFreeText       bool     `json:"allow_free_text"`
	FreeTextPlaceholder string   `json:"free_text_placeholder,omitempty"`
	MultiSelect         bool     `json:"multi_select"`
}

const askUserDescription = "Ask the user a question and wait for their response. " +
	"Use when you need clarification or the user needs to make a choice. " +
	"Provide 2-4 predefined options when possible. " +
	"Set allow_free_text to true if the user might want to provide a custom answer. " +
	"Set multi_select to true if the user can select multiple options."

// NewAskUserTool creates a new ask_user tool for interactive user prompts.
func NewAskUserTool() *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "ask_user",
				Description: askUserDescription,
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"question": map[string]any{
							"type":        "string",
							"description": "The question to ask the user",
						},
						"options": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"minItems":    2,
							"maxItems":    4,
							"description": "2-4 predefined options for the user to choose from",
						},
						"allow_free_text": map[string]any{
							"type":        "boolean",
							"default":     false,
							"description": "Whether to allow the user to enter custom text",
						},
						"free_text_placeholder": map[string]any{
							"type":        "string",
							"description": "Placeholder text for the free text input field",
						},
						"multi_select": map[string]any{
							"type":        "boolean",
							"default":     false,
							"description": "Whether the user can select multiple options",
						},
					},
					"required": []string{"question"},
				},
			},
		},
		Run: askUserRun,
	}
}

func askUserRun(ctx ToolContext, input json.RawMessage) ToolOut {
	var args AskUserToolInput
	if err := json.Unmarshal(input, &args); err != nil {
		return toolError("Failed to parse input: %v", err)
	}

	if args.Question == "" {
		return toolError("Question is required")
	}

	if len(args.Options) > 0 && (len(args.Options) < 2 || len(args.Options) > 4) {
		return toolError("Options must have 2-4 items if provided")
	}

	if ctx.EmitUserPrompt == nil || ctx.WaitUserResponse == nil {
		return toolError("User prompt functionality is not available")
	}

	promptID := uuid.Must(uuid.NewV7()).String()

	var options []UserPromptOption
	for i, opt := range args.Options {
		options = append(options, UserPromptOption{
			ID:    fmt.Sprintf("opt_%d", i),
			Label: opt,
		})
	}

	prompt := UserPrompt{
		PromptID:            promptID,
		Question:            args.Question,
		Options:             options,
		AllowFreeText:       args.AllowFreeText,
		FreeTextPlaceholder: args.FreeTextPlaceholder,
		MultiSelect:         args.MultiSelect,
	}

	ctx.EmitUserPrompt(prompt)

	response, err := ctx.WaitUserResponse(ctx.Context, promptID)
	if err != nil {
		return toolError("Failed to get user response: %v", err)
	}

	if response.Cancelled {
		return ToolOut{Content: "User skipped this question"}
	}

	return ToolOut{Content: formatUserResponse(args, response)}
}

func formatUserResponse(args AskUserToolInput, response UserPromptResponse) string {
	var parts []string

	if len(response.SelectedOptionIDs) > 0 {
		var selectedLabels []string
		optionMap := make(map[string]string)
		for i, opt := range args.Options {
			optionMap[fmt.Sprintf("opt_%d", i)] = opt
		}
		for _, id := range response.SelectedOptionIDs {
			if label, ok := optionMap[id]; ok {
				selectedLabels = append(selectedLabels, label)
			}
		}
		if len(selectedLabels) > 0 {
			parts = append(parts, fmt.Sprintf("User selected: %s", strings.Join(selectedLabels, ", ")))
		}
	}

	if response.FreeTextResponse != "" {
		parts = append(parts, fmt.Sprintf("User responded: %s", response.FreeTextResponse))
	}

	if len(parts) == 0 {
		return "User provided no response"
	}

	return strings.Join(parts, "\n")
}
