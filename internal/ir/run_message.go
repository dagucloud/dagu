// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

// ToolCall represents an LLM's request to call a tool.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the details of a function call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// LLMMessage represents a persisted message in an LLM session.
type LLMMessage struct {
	Role       LLMRole            `json:"role"`
	Content    string             `json:"content"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall         `json:"tool_calls,omitempty"`
	Metadata   *LLMMessageMetadata `json:"metadata,omitempty"`
}

// LLMMessageMetadata contains metadata about an LLM API call.
type LLMMessageMetadata struct {
	Provider         string  `json:"provider,omitempty"`
	Model            string  `json:"model,omitempty"`
	PromptTokens     int     `json:"promptTokens,omitempty"`
	CompletionTokens int     `json:"completionTokens,omitempty"`
	TotalTokens      int     `json:"totalTokens,omitempty"`
	Cost             float64 `json:"cost,omitempty"`
}

// ToolDefinition represents a tool that was available to the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// DeduplicateSystemMessages keeps only the first system message.
func DeduplicateSystemMessages(messages []LLMMessage) []LLMMessage {
	if len(messages) == 0 {
		return nil
	}

	result := make([]LLMMessage, 0, len(messages))
	seenSystem := false
	for _, message := range messages {
		if message.Role == LLMRoleSystem {
			if seenSystem {
				continue
			}
			seenSystem = true
		}
		result = append(result, message)
	}
	return result
}
