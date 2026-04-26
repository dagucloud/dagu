// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
)

// convertMessage converts an agent.Message to one or more exec.LLMMessage values.
// User messages with tool results expand to one message per result (Role=Tool).
func convertMessage(msg agent.Message, modelCfg *agent.ModelConfig) []exec.LLMMessage {
	switch msg.Type {
	case agent.MessageTypeAssistant:
		return []exec.LLMMessage{convertAssistantMessage(msg, modelCfg)}

	case agent.MessageTypeUser:
		// When ToolResults are present, the message is a tool-result payload; Content is unused.
		if len(msg.ToolResults) > 0 {
			return convertToolResultMessages(msg)
		}
		return []exec.LLMMessage{{
			Role:    exec.RoleUser,
			Content: msg.Content,
		}}

	case agent.MessageTypeError:
		return []exec.LLMMessage{{
			Role:    exec.RoleAssistant,
			Content: msg.Content,
		}}

	default:
		return nil
	}
}

// convertAssistantMessage converts an assistant agent.Message to an exec.LLMMessage.
func convertAssistantMessage(msg agent.Message, modelCfg *agent.ModelConfig) exec.LLMMessage {
	m := exec.LLMMessage{
		Role:    exec.RoleAssistant,
		Content: msg.Content,
	}

	// Convert tool calls if present.
	if len(msg.ToolCalls) > 0 {
		m.ToolCalls = make([]exec.ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			m.ToolCalls[i] = exec.ToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: exec.ToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			}
		}
	}

	// Build metadata with provider/model and optional usage/cost.
	metadata := &exec.LLMMessageMetadata{
		Provider: modelCfg.Provider,
		Model:    modelCfg.Model,
	}
	if msg.Usage != nil {
		metadata.PromptTokens = msg.Usage.PromptTokens
		metadata.CompletionTokens = msg.Usage.CompletionTokens
		metadata.TotalTokens = msg.Usage.TotalTokens
	}
	if msg.Cost != nil {
		metadata.Cost = *msg.Cost
	}
	m.Metadata = metadata

	return m
}

// contextToLLMHistory converts context messages to llm.Message for LoopConfig.History.
// System messages are filtered out since the loop handles system prompt separately.
func contextToLLMHistory(msgs []exec.LLMMessage) []llm.Message {
	if len(msgs) == 0 {
		return nil
	}
	var result []llm.Message
	for _, msg := range msgs {
		if msg.Role == exec.RoleSystem {
			continue
		}
		m := llm.Message{
			Role:       llm.Role(msg.Role),
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			m.ToolCalls = make([]llm.ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				m.ToolCalls[j] = llm.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: llm.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		result = append(result, m)
	}
	return result
}

// formatPushBackFeedback creates a user message from push-back inputs,
// filtering to only declared approval input fields.
func formatPushBackFeedback(inputs map[string]string, approval *core.ApprovalConfig) string {
	var sb strings.Builder
	sb.WriteString("The reviewer has requested changes to your previous work.\n")

	keys := make([]string, 0, len(inputs))
	if len(inputs) > 0 {
		var allowed map[string]struct{}
		if approval != nil {
			// Filter to declared input fields only (consistent with env var allowlist).
			allowed = make(map[string]struct{}, len(approval.Input))
			for _, key := range approval.Input {
				allowed[key] = struct{}{}
			}
		}

		for k := range inputs {
			if allowed != nil {
				if _, ok := allowed[k]; !ok {
					continue
				}
			}
			keys = append(keys, k)
		}
	}

	if len(keys) > 0 {
		sb.WriteString("\nReviewer feedback:\n")
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "- %s: %s\n", k, inputs[k])
		}
	}

	sb.WriteString("\nPlease revise your response based on this feedback.")
	return sb.String()
}

// convertToolResultMessages converts a user message with tool results
// into one exec.LLMMessage per tool result (Role=Tool).
func convertToolResultMessages(msg agent.Message) []exec.LLMMessage {
	msgs := make([]exec.LLMMessage, len(msg.ToolResults))
	for i, tr := range msg.ToolResults {
		msgs[i] = exec.LLMMessage{
			Role:       exec.RoleTool,
			Content:    tr.Content,
			ToolCallID: tr.ToolCallID,
		}
	}
	return msgs
}
