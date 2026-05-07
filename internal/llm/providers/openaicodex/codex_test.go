// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openaicodex

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RequiresAccountIDForDirectToken(t *testing.T) {
	t.Parallel()

	_, err := New(llm.Config{APIKey: "token"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account ID")
}

func TestConvertMessages(t *testing.T) {
	t.Parallel()

	instructions, input := convertMessages([]llm.Message{
		{Role: llm.RoleSystem, Content: "system a"},
		{Role: llm.RoleSystem, Content: "system b"},
		{Role: llm.RoleUser, Content: "hello"},
		{
			Role:    llm.RoleAssistant,
			Content: "tool time",
			ToolCalls: []llm.ToolCall{{
				ID:   "call-1|item-1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "lookup",
					Arguments: `{"city":"tokyo"}`,
				},
			}},
		},
		{Role: llm.RoleTool, ToolCallID: "call-1|item-1", Content: "sunny"},
	})

	assert.Equal(t, "system a\n\nsystem b", instructions)
	require.Len(t, input, 4)
	assert.Equal(t, "user", input[0].(map[string]any)["role"])
	assert.Equal(t, "function_call", input[2].(map[string]any)["type"])
	assert.Equal(t, "function_call_output", input[3].(map[string]any)["type"])
}

func TestConvertMessages_OmitsEmptyFunctionCallItemID(t *testing.T) {
	t.Parallel()

	_, input := convertMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "lookup",
					Arguments: `{"city":"tokyo"}`,
				},
			}},
		},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: "sunny"},
	})

	require.Len(t, input, 3)
	toolCall, ok := input[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "function_call", toolCall["type"])
	assert.Equal(t, "call-1", toolCall["call_id"])
	_, hasItemID := toolCall["id"]
	assert.False(t, hasItemID)

	toolResult, ok := input[2].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "function_call_output", toolResult["type"])
	assert.Equal(t, "call-1", toolResult["call_id"])
}

func TestStreamResponse_CollectsTextToolCallsAndUsage(t *testing.T) {
	t.Parallel()

	body := io.NopCloser(strings.NewReader(strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"message"}}`,
		`data: {"type":"response.output_text.delta","delta":"Hello "}`,
		`data: {"type":"response.output_text.delta","delta":"world"}`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"lookup","arguments":"{\"city\":\"tokyo\"}"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"lookup","arguments":"{\"city\":\"tokyo\"}"}}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":20,"output_tokens":10,"total_tokens":30,"input_tokens_details":{"cached_tokens":5}}}}`,
		`data: [DONE]`,
	}, "\n")))

	provider := &Provider{}
	events := make(chan llm.StreamEvent, 16)

	go provider.streamResponse(context.Background(), body, events)

	var (
		deltas []string
		final  *llm.StreamEvent
	)
	for event := range events {
		if event.Delta != "" {
			deltas = append(deltas, event.Delta)
		}
		if event.Done {
			eventCopy := event
			final = &eventCopy
		}
	}

	require.NotNil(t, final)
	assert.Equal(t, []string{"Hello ", "world"}, deltas)
	require.Len(t, final.ToolCalls, 1)
	assert.Equal(t, "lookup", final.ToolCalls[0].Function.Name)
	assert.Equal(t, `{"city":"tokyo"}`, final.ToolCalls[0].Function.Arguments)
	require.NotNil(t, final.Usage)
	assert.Equal(t, 15, final.Usage.PromptTokens)
	assert.Equal(t, 10, final.Usage.CompletionTokens)
	assert.Equal(t, 30, final.Usage.TotalTokens)
	assert.Equal(t, "tool_calls", final.FinishReason)
}
