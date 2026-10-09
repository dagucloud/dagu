// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamFinishReason(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tail string
	}{
		{name: "done marker", tail: "data: [DONE]\n"},
		{name: "end of input", tail: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := &Provider{}
			events := make(chan llm.StreamEvent, 2)
			body := io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n" + tc.tail))

			provider.streamResponse(context.Background(), body, events)

			var finishReason string
			for event := range events {
				if event.FinishReason != "" {
					finishReason = event.FinishReason
				}
			}
			assert.Equal(t, "stop", finishReason)
		})
	}
}

func TestBuildRequestBody_WebSearch(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		config: llm.Config{APIKey: "test-key"},
	}

	t.Run("web search disabled - no plugins", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "anthropic/claude-sonnet-4",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed chatCompletionRequest
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Empty(t, parsed.Plugins)
		assert.Nil(t, parsed.WebSearchOptions)
	})

	t.Run("web search enabled - plugin appended", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "anthropic/claude-sonnet-4",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			WebSearch: &llm.WebSearchRequest{
				Enabled: true,
			},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed chatCompletionRequest
		require.NoError(t, json.Unmarshal(body, &parsed))
		require.Len(t, parsed.Plugins, 1)
		assert.Equal(t, "web", parsed.Plugins[0].ID)
		assert.Nil(t, parsed.Plugins[0].MaxResults)
		require.NotNil(t, parsed.WebSearchOptions)
		assert.Equal(t, "medium", parsed.WebSearchOptions.SearchContextSize)
	})

	t.Run("web search with max_uses", func(t *testing.T) {
		t.Parallel()
		maxUses := 3
		req := &llm.ChatRequest{
			Model:    "anthropic/claude-sonnet-4",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			WebSearch: &llm.WebSearchRequest{
				Enabled: true,
				MaxUses: &maxUses,
			},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed chatCompletionRequest
		require.NoError(t, json.Unmarshal(body, &parsed))
		require.Len(t, parsed.Plugins, 1)
		assert.Equal(t, "web", parsed.Plugins[0].ID)
		require.NotNil(t, parsed.Plugins[0].MaxResults)
		assert.Equal(t, 3, *parsed.Plugins[0].MaxResults)
	})
}

func TestBuildRequestBody_ReasoningTokens(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		config: llm.Config{APIKey: "test-key"},
	}

	tests := []struct {
		name                    string
		maxTokens               *int
		thinking                *llm.ThinkingRequest
		expectedMaxTokens       *int
		expectedHasReasoning    bool
		expectedReasoningBudget *int
	}{
		{
			name:                 "no reasoning, no max tokens",
			maxTokens:            nil,
			thinking:             nil,
			expectedMaxTokens:    nil,
			expectedHasReasoning: false,
		},
		{
			name:                 "no reasoning, explicit max tokens",
			maxTokens:            new(8192),
			thinking:             nil,
			expectedMaxTokens:    new(8192),
			expectedHasReasoning: false,
		},
		{
			name:      "reasoning with budget, no max tokens - auto adjust",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled:      true,
				BudgetTokens: new(4096),
			},
			expectedMaxTokens:       new(4096 + 4096),
			expectedHasReasoning:    true,
			expectedReasoningBudget: new(4096),
		},
		{
			name:      "reasoning with budget, max tokens less than budget - auto adjust",
			maxTokens: new(2000),
			thinking: &llm.ThinkingRequest{
				Enabled:      true,
				BudgetTokens: new(4096),
			},
			expectedMaxTokens:       new(4096 + 4096),
			expectedHasReasoning:    true,
			expectedReasoningBudget: new(4096),
		},
		{
			name:      "reasoning with budget, max tokens greater than budget - use provided",
			maxTokens: new(16000),
			thinking: &llm.ThinkingRequest{
				Enabled:      true,
				BudgetTokens: new(4096),
			},
			expectedMaxTokens:       new(16000),
			expectedHasReasoning:    true,
			expectedReasoningBudget: new(4096),
		},
		{
			name:      "reasoning without budget (effort only) - no adjustment needed",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMedium,
			},
			// No explicit budget, so no max_tokens adjustment
			expectedMaxTokens:    nil,
			expectedHasReasoning: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &llm.ChatRequest{
				Model: "anthropic/claude-sonnet-4",
				Messages: []llm.Message{
					{Role: llm.RoleUser, Content: "Hello"},
				},
				MaxTokens: tt.maxTokens,
				Thinking:  tt.thinking,
			}

			body, err := provider.buildRequestBody(req, false)
			require.NoError(t, err)

			var parsed chatCompletionRequest
			err = json.Unmarshal(body, &parsed)
			require.NoError(t, err)

			if tt.expectedMaxTokens != nil {
				require.NotNil(t, parsed.MaxTokens, "max_tokens should be present")
				assert.Equal(t, *tt.expectedMaxTokens, *parsed.MaxTokens,
					"max_tokens should be %d, got %d", *tt.expectedMaxTokens, *parsed.MaxTokens)
			} else {
				assert.Nil(t, parsed.MaxTokens, "max_tokens should not be present")
			}

			if tt.expectedHasReasoning {
				require.NotNil(t, parsed.Reasoning, "reasoning should be present")
				if tt.expectedReasoningBudget != nil {
					require.NotNil(t, parsed.Reasoning.MaxTokens, "reasoning.max_tokens should be present")
					assert.Equal(t, *tt.expectedReasoningBudget, *parsed.Reasoning.MaxTokens)
					// Verify max_tokens > reasoning budget
					require.NotNil(t, parsed.MaxTokens, "max_tokens should be set when reasoning budget is set")
					assert.Greater(t, *parsed.MaxTokens, *parsed.Reasoning.MaxTokens,
						"max_tokens (%d) must be greater than reasoning.max_tokens (%d)",
						*parsed.MaxTokens, *parsed.Reasoning.MaxTokens)
				}
			} else {
				assert.Nil(t, parsed.Reasoning, "reasoning should not be present")
			}
		})
	}
}

func TestBuildRequestBody_Images(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	body, err := provider.buildRequestBody(&llm.ChatRequest{
		Model: "anthropic/claude-sonnet-4",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "plain"},
			{Role: llm.RoleUser, Content: "describe", Images: []llm.Image{{MediaType: "image/png", Data: []byte{1, 2}}}},
		},
	}, false)
	require.NoError(t, err)

	var parsed struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Messages, 2)
	assert.JSONEq(t, `"plain"`, string(parsed.Messages[0].Content))
	assert.JSONEq(t, `[
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AQI="}},
		{"type":"text","text":"describe"}
	]`, string(parsed.Messages[1].Content))
}

// Claude models refuse a forced tool choice while reasoning and from Claude 5
// on, so those requests ask for auto. Other models keep the forced choice.
func TestBuildRequestBody_ToolChoice(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	tests := []struct {
		name     string
		model    string
		choice   string
		thinking bool
		expected string
	}{
		{name: "Claude5", model: "anthropic/claude-sonnet-5.5", choice: "required", expected: "auto"},
		{name: "Fable", model: "anthropic/claude-fable-5.1", choice: "required", expected: "auto"},
		{name: "Claude4", model: "anthropic/claude-sonnet-4.6", choice: "required", expected: "required"},
		{name: "Claude4Reasoning", model: "anthropic/claude-sonnet-4.6", choice: "required", thinking: true, expected: "auto"},
		{name: "OtherModel", model: "openai/gpt-5.4-mini", choice: "required", expected: "required"},
		{name: "OtherModelReasoning", model: "google/gemini-3.8-flash", choice: "required", thinking: true, expected: "required"},
		{name: "Auto", model: "anthropic/claude-sonnet-5.5", choice: "auto", expected: "auto"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &llm.ChatRequest{
				Model:    tt.model,
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
				Tools: []llm.Tool{{
					Type:     "function",
					Function: llm.ToolFunction{Name: "respond", Parameters: map[string]any{"type": "object"}},
				}},
				ToolChoice: tt.choice,
			}
			if tt.thinking {
				req.Thinking = &llm.ThinkingRequest{Enabled: true}
			}
			body, err := provider.buildRequestBody(req, false)
			require.NoError(t, err)

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(body, &parsed))
			assert.Equal(t, tt.expected, parsed["tool_choice"])
		})
	}
}
