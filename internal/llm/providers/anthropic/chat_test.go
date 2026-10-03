// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolTurnContent is an assistant turn with thinking interleaved between tool
// calls, as models return it.
const toolTurnContent = `[
	{"type":"thinking","thinking":"","signature":"sig-1"},
	{"type":"tool_use","id":"tu-1","name":"lookup","input":{"q":"a"}},
	{"type":"thinking","thinking":"","signature":"sig-2"},
	{"type":"tool_use","id":"tu-2","name":"lookup","input":{"q":"b"}}
]`

// An assistant turn goes back exactly as the model produced it, which keeps
// signed thinking blocks valid in the next request of a tool loop.
func TestChatReplaysAssistantTurn(t *testing.T) {
	t.Parallel()

	server := messagesServer(
		`{"content":`+toolTurnContent+`,"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
	)
	provider, err := llm.NewProvider(llm.ProviderAnthropic, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
	require.NoError(t, err)

	user := llm.Message{Role: llm.RoleUser, Content: "look up a and b"}
	first, err := provider.Chat(context.Background(), &llm.ChatRequest{Model: "claude-opus-5-5", Messages: []llm.Message{user}})
	require.NoError(t, err)
	require.Len(t, first.ToolCalls, 2)
	require.NotNil(t, first.ProviderState)
	assert.Equal(t, llm.ProviderAnthropic, first.ProviderState.Provider)

	_, err = provider.Chat(context.Background(), &llm.ChatRequest{
		Model: "claude-opus-5-5",
		Messages: []llm.Message{
			user,
			{Role: llm.RoleAssistant, ToolCalls: first.ToolCalls, ProviderState: first.ProviderState},
			{Role: llm.RoleTool, ToolCallID: "tu-1", Content: "A"},
			{Role: llm.RoleTool, ToolCallID: "tu-2", Content: "B"},
		},
	})
	require.NoError(t, err)

	requests := server.Requests()
	require.Len(t, requests, 2)
	messages := requests[1]["messages"].([]any)
	replayed, err := json.Marshal(messages[1].(map[string]any)["content"])
	require.NoError(t, err)
	assert.JSONEq(t, toolTurnContent, string(replayed))
}

// A record from another provider does not replace the turn; the turn is
// rebuilt from its tool calls.
func TestChatIgnoresOtherProviderState(t *testing.T) {
	t.Parallel()

	server := messagesServer(`{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	provider, err := llm.NewProvider(llm.ProviderAnthropic, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
	require.NoError(t, err)

	_, err = provider.Chat(context.Background(), &llm.ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "look up a"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID: "tu-1", Type: "function",
					Function: llm.ToolCallFunction{Name: "lookup", Arguments: `{"q":"a"}`},
				}},
				ProviderState: &llm.ProviderState{Provider: llm.ProviderGemini, Data: []byte(`[{"text":"x"}]`)},
			},
			{Role: llm.RoleTool, ToolCallID: "tu-1", Content: "A"},
		},
	})
	require.NoError(t, err)

	messages := server.Requests()[0]["messages"].([]any)
	content, err := json.Marshal(messages[1].(map[string]any)["content"])
	require.NoError(t, err)
	assert.JSONEq(t, `[{"type":"tool_use","id":"tu-1","name":"lookup","input":{"q":"a"}}]`, string(content))
}

// The results of one turn's tool calls go back in a single user message, as
// the API expects for parallel tool use.
func TestChatGroupsToolResults(t *testing.T) {
	t.Parallel()

	server := messagesServer(`{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	provider, err := llm.NewProvider(llm.ProviderAnthropic, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
	require.NoError(t, err)

	_, err = provider.Chat(context.Background(), &llm.ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "look up a and b"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "tu-1", Type: "function", Function: llm.ToolCallFunction{Name: "lookup", Arguments: `{"q":"a"}`}},
				{ID: "tu-2", Type: "function", Function: llm.ToolCallFunction{Name: "lookup", Arguments: `{"q":"b"}`}},
			}},
			{Role: llm.RoleTool, ToolCallID: "tu-1", Content: "A"},
			{Role: llm.RoleTool, ToolCallID: "tu-2", Content: "B"},
		},
	})
	require.NoError(t, err)

	messages := server.Requests()[0]["messages"].([]any)
	require.Len(t, messages, 3)
	results, err := json.Marshal(messages[2])
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"tu-1","content":"A"},
		{"type":"tool_result","tool_use_id":"tu-2","content":"B"}
	]}`, string(results))
}
