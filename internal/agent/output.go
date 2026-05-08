// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/dagucloud/dagu/internal/llm"
)

// NewOutputTool creates a tool that writes the final result to the given writer.
// This tool is used in agent step execution to capture the step's output variable.
func NewOutputTool(w io.Writer) *AgentTool {
	return &AgentTool{
		Tool: llm.Tool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "output",
				Description: "Write the final result of your task. Call this tool when you have completed your work and want to deliver the result. The content you provide will be captured as the step's output variable for use by subsequent workflow steps. Only call this once with your final answer.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{
							"type":        "string",
							"description": "The final result content to output",
						},
					},
					"required": []string{"content"},
				},
			},
		},
		Run: outputRunFunc(w),
	}
}

// outputRunFunc returns a ToolFunc that writes to the given writer.
func outputRunFunc(w io.Writer) ToolFunc {
	return func(_ ToolContext, input json.RawMessage) ToolOut {
		var params struct {
			Content string `json:"content"`
		}
		if err := decodeToolInput(input, &params); err != nil {
			return toolError("failed to parse output parameters: %v", err)
		}
		if params.Content == "" {
			return toolError("content is required")
		}
		if _, err := fmt.Fprint(w, params.Content); err != nil {
			return toolError("failed to write output: %v", err)
		}
		return ToolOut{Content: "Output written successfully."}
	}
}
