// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gemini_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThinkingRequests(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		model string
		want  string
	}{
		{model: "gemini-2.5-flash", want: `{"thinkingBudget":8192}`},
		{model: "gemini-3.8-flash", want: `{"thinkingLevel":"HIGH"}`},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(tt.model+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				var requestBody []byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wantPath := "/models/" + tt.model
					if stream {
						wantPath += ":streamGenerateContent"
					} else {
						wantPath += ":generateContent"
					}
					assert.Equal(t, wantPath, r.URL.Path)
					assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
					var err error
					requestBody, err = io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, "failed to read request", http.StatusBadRequest)
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
						return
					}
					_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
				}))
				defer server.Close()

				provider, err := llm.NewProvider(llm.ProviderGemini, llm.Config{
					APIKey: "test-key", BaseURL: server.URL,
				})
				require.NoError(t, err)
				req := &llm.ChatRequest{
					Model:    tt.model,
					Thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortHigh},
					Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
				}

				if stream {
					events, err := provider.ChatStream(context.Background(), req)
					require.NoError(t, err)
					var text string
					for event := range events {
						require.NoError(t, event.Error)
						text += event.Delta
					}
					assert.Equal(t, "ok", text)
				} else {
					_, err = provider.Chat(context.Background(), req)
					require.NoError(t, err)
				}

				var request map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(requestBody, &request))
				var config map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(request["generationConfig"], &config))
				require.Contains(t, config, "thinkingConfig")
				assert.JSONEq(t, tt.want, string(config["thinkingConfig"]))
			})
		}
	}
}

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
