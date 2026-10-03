// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gemini_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolTurnParts is a model turn whose function call carries a thought
// signature, as Gemini 3 returns it.
const toolTurnParts = `[
	{"functionCall":{"name":"lookup","args":{"q":"a"}},"thoughtSignature":"c2lnLTE="},
	{"functionCall":{"name":"lookup","args":{"q":"b"}}}
]`

// A model turn goes back exactly as received, so its thought signatures
// reach the next request of a tool loop.
func TestChatReplaysModelTurn(t *testing.T) {
	t.Parallel()

	server := generateServer(
		`{"candidates":[{"content":{"role":"model","parts":`+toolTurnParts+`},"finishReason":"STOP"}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`,
	)
	provider, err := llm.NewProvider(llm.ProviderGemini, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
	require.NoError(t, err)

	user := llm.Message{Role: llm.RoleUser, Content: "look up a and b"}
	first, err := provider.Chat(context.Background(), &llm.ChatRequest{Model: "gemini-3.8-flash", Messages: []llm.Message{user}})
	require.NoError(t, err)
	require.Len(t, first.ToolCalls, 2)
	require.NotNil(t, first.ProviderState)
	assert.Equal(t, llm.ProviderGemini, first.ProviderState.Provider)

	_, err = provider.Chat(context.Background(), &llm.ChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []llm.Message{
			user,
			{Role: llm.RoleAssistant, ToolCalls: first.ToolCalls, ProviderState: first.ProviderState},
			{Role: llm.RoleTool, ToolCallID: first.ToolCalls[0].ID, Content: "A"},
			{Role: llm.RoleTool, ToolCallID: first.ToolCalls[1].ID, Content: "B"},
		},
	})
	require.NoError(t, err)

	requests := server.Requests()
	require.Len(t, requests, 2)
	contents := requests[1]["contents"].([]any)
	require.Len(t, contents, 3)
	turn, err := json.Marshal(contents[1])
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"model","parts":`+toolTurnParts+`}`, string(turn))
	results, err := json.Marshal(contents[2])
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"user","parts":[
		{"functionResponse":{"name":"lookup","response":{"result":"A"}}},
		{"functionResponse":{"name":"lookup","response":{"result":"B"}}}
	]}`, string(results))
}

// A record from another provider does not replace the turn; the turn is
// rebuilt from its tool calls.
func TestChatIgnoresOtherProviderState(t *testing.T) {
	t.Parallel()

	server := generateServer(`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)
	provider, err := llm.NewProvider(llm.ProviderGemini, llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
	require.NoError(t, err)

	_, err = provider.Chat(context.Background(), &llm.ChatRequest{
		Model: "gemini-3.8-flash",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "look up a"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID: "call_0", Type: "function",
					Function: llm.ToolCallFunction{Name: "lookup", Arguments: `{"q":"a"}`},
				}},
				ProviderState: &llm.ProviderState{Provider: llm.ProviderAnthropic, Data: []byte(`[{"type":"thinking"}]`)},
			},
			{Role: llm.RoleTool, ToolCallID: "call_0", Content: "A"},
		},
	})
	require.NoError(t, err)

	contents := server.Requests()[0]["contents"].([]any)
	turn, err := json.Marshal(contents[1])
	require.NoError(t, err)
	assert.JSONEq(t, `{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"q":"a"}}}]}`, string(turn))
}
