// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import "github.com/dagucloud/dagu/v2/internal/ir"

// LLM message role constants are aliases for the canonical IR constants.
const (
	RoleSystem    = ir.LLMRoleSystem
	RoleUser      = ir.LLMRoleUser
	RoleAssistant = ir.LLMRoleAssistant
	RoleTool      = ir.LLMRoleTool
)

// ToolCall represents an LLM's request to call a tool.
// This mirrors llmpkg.ToolCall for use in exec layer.
type ToolCall struct {
	// ID is a unique identifier for this tool call.
	ID string `json:"id"`
	// Type is always "function" for function calls.
	Type string `json:"type"`
	// Function contains the function call details.
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the details of a function call.
type ToolCallFunction struct {
	// Name is the name of the function to call.
	Name string `json:"name"`
	// Arguments is a JSON string containing the function arguments.
	Arguments string `json:"arguments"`
}

// LLMMessage represents a single message in the session.
type LLMMessage struct {
	// Role is the message role (system, user, assistant, tool).
	Role ir.LLMRole `json:"role"`
	// Content is the message content.
	Content string `json:"content"`
	// ToolCallID is the ID of the tool call this message is responding to.
	// Only set when Role is "tool".
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls contains tool calls made by the assistant.
	// Only set when Role is "assistant" and the model requested tool calls.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Metadata contains API call metadata (only set for assistant responses).
	Metadata *LLMMessageMetadata `json:"metadata,omitempty"`
}

// LLMMessageMetadata contains metadata about an LLM API call.
type LLMMessageMetadata struct {
	// Provider is the LLM provider used (openai, anthropic, etc.).
	Provider string `json:"provider,omitempty"`
	// Model is the model identifier used.
	Model string `json:"model,omitempty"`
	// PromptTokens is the number of tokens in the prompt.
	PromptTokens int `json:"promptTokens,omitempty"`
	// CompletionTokens is the number of tokens in the completion.
	CompletionTokens int `json:"completionTokens,omitempty"`
	// TotalTokens is the sum of prompt and completion tokens.
	TotalTokens int `json:"totalTokens,omitempty"`
	// Cost is the estimated USD cost for this API call.
	Cost float64 `json:"cost,omitempty"`
}

// ToolDefinition represents a tool that was available to the LLM.
// This is stored alongside messages to provide visibility into what
// tool definitions were sent to the LLM during execution.
type ToolDefinition struct {
	// Name is the tool/function name as presented to the LLM.
	Name string `json:"name"`
	// Description describes what the tool does.
	Description string `json:"description,omitempty"`
	// Parameters is the JSON Schema describing the tool's parameters.
	Parameters map[string]any `json:"parameters,omitempty"`
}

// DeduplicateSystemMessages keeps only the first system message.
func DeduplicateSystemMessages(messages []LLMMessage) []LLMMessage {
	if len(messages) == 0 {
		return nil
	}

	result := make([]LLMMessage, 0, len(messages))
	seenSystem := false

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if seenSystem {
				continue
			}
			seenSystem = true
		}
		result = append(result, msg)
	}

	return result
}
