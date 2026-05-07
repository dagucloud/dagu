// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestBody_WebSearch(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		config: llm.Config{APIKey: "test-key"},
	}

	t.Run("web search disabled - no tool added", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed messagesRequest
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Empty(t, parsed.Tools)
	})

	t.Run("web search enabled - tool appended", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			WebSearch: &llm.WebSearchRequest{
				Enabled: true,
			},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))

		tools, ok := parsed["tools"].([]any)
		require.True(t, ok, "tools should be an array")
		require.Len(t, tools, 1)

		wsTool := tools[0].(map[string]any)
		assert.Equal(t, "web_search_20260209", wsTool["type"])
		assert.Equal(t, "web_search", wsTool["name"])
	})

	t.Run("web search with max_uses and domain filters", func(t *testing.T) {
		t.Parallel()
		maxUses := 5
		req := &llm.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			WebSearch: &llm.WebSearchRequest{
				Enabled:        true,
				MaxUses:        &maxUses,
				AllowedDomains: []string{"example.com"},
				BlockedDomains: []string{"blocked.com"},
				UserLocation: &llm.UserLocation{
					City:    "Tokyo",
					Country: "JP",
				},
			},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))

		tools := parsed["tools"].([]any)
		require.Len(t, tools, 1)

		wsTool := tools[0].(map[string]any)
		assert.Equal(t, float64(5), wsTool["max_uses"])
		assert.Equal(t, []any{"example.com"}, wsTool["allowed_domains"])
		assert.Equal(t, []any{"blocked.com"}, wsTool["blocked_domains"])

		loc := wsTool["user_location"].(map[string]any)
		assert.Equal(t, "Tokyo", loc["city"])
		assert.Equal(t, "JP", loc["country"])
	})

	t.Run("web search alongside function tools", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			Tools: []llm.Tool{{
				Type: "function",
				Function: llm.ToolFunction{
					Name:        "test_tool",
					Description: "A test tool",
					Parameters:  map[string]any{"type": "object"},
				},
			}},
			WebSearch: &llm.WebSearchRequest{Enabled: true},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))

		tools := parsed["tools"].([]any)
		require.Len(t, tools, 2, "should have function tool + web search tool")

		// First tool is function tool
		funcTool := tools[0].(map[string]any)
		assert.Equal(t, "test_tool", funcTool["name"])

		// Second tool is web search
		wsTool := tools[1].(map[string]any)
		assert.Equal(t, "web_search_20260209", wsTool["type"])
	})

	t.Run("function tools preserve full input schema", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "claude-sonnet-4-20250514",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			Tools: []llm.Tool{{
				Type: "function",
				Function: llm.ToolFunction{
					Name:        "strict_tool",
					Description: "A strict tool",
					Parameters: map[string]any{
						"type": "object",
						"oneOf": []any{
							map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"operation":  map[string]any{"type": "string", "enum": []any{"replace"}},
									"old_string": map[string]any{"type": "string"},
									"new_string": map[string]any{"type": "string"},
								},
								"required": []any{"operation", "old_string", "new_string"},
							},
						},
					},
				},
			}},
		}
		body, err := provider.buildRequestBody(req, false)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(body, &parsed))

		tools := parsed["tools"].([]any)
		require.Len(t, tools, 1)
		inputSchema := tools[0].(map[string]any)["input_schema"].(map[string]any)
		assert.Contains(t, inputSchema, "oneOf")
		variants := inputSchema["oneOf"].([]any)
		require.Len(t, variants, 1)
		assert.Equal(t, false, variants[0].(map[string]any)["additionalProperties"])
	})
}

func TestChat_WebSearchResponseBlocks(t *testing.T) {
	t.Parallel()

	// Test that server_tool_use and web_search_tool_result blocks are skipped
	resp := messagesResponse{
		Content: []contentBlock{
			{Type: "server_tool_use", ID: "srvtu_1", Name: "web_search"},
			{Type: "web_search_tool_result", ID: "srvtr_1"},
			{Type: "text", Text: "The answer is 42."},
		},
		StopReason: "end_turn",
	}

	var content strings.Builder
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "server_tool_use", "web_search_tool_result":
			// Should be silently skipped
		}
	}
	assert.Equal(t, "The answer is 42.", content.String())
}

func TestBuildRequestBody_ThinkingTokens(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		config: llm.Config{APIKey: "test-key"},
	}

	tests := []struct {
		name                string
		maxTokens           *int
		thinking            *llm.ThinkingRequest
		expectedMaxTokens   int
		expectedHasThinking bool
		expectedBudget      int
	}{
		{
			name:                "no thinking, no max tokens - default 4096",
			maxTokens:           nil,
			thinking:            nil,
			expectedMaxTokens:   4096,
			expectedHasThinking: false,
		},
		{
			name:                "no thinking, explicit max tokens",
			maxTokens:           new(8192),
			thinking:            nil,
			expectedMaxTokens:   8192,
			expectedHasThinking: false,
		},
		{
			name:      "thinking enabled, no max tokens - auto adjust",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMedium, // 4096 budget
			},
			expectedMaxTokens:   4096 + 4096, // budget + buffer
			expectedHasThinking: true,
			expectedBudget:      4096,
		},
		{
			name:      "thinking enabled, max tokens less than budget - auto adjust",
			maxTokens: new(2000),
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMedium, // 4096 budget
			},
			expectedMaxTokens:   4096 + 4096, // budget + buffer
			expectedHasThinking: true,
			expectedBudget:      4096,
		},
		{
			name:      "thinking enabled, max tokens equal to budget - auto adjust",
			maxTokens: new(4096),
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMedium, // 4096 budget
			},
			expectedMaxTokens:   4096 + 4096, // budget + buffer
			expectedHasThinking: true,
			expectedBudget:      4096,
		},
		{
			name:      "thinking enabled, max tokens greater than budget - use provided",
			maxTokens: new(16000),
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMedium, // 4096 budget
			},
			expectedMaxTokens:   16000,
			expectedHasThinking: true,
			expectedBudget:      4096,
		},
		{
			name:      "thinking with explicit budget tokens",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled:      true,
				BudgetTokens: new(10000),
			},
			expectedMaxTokens:   10000 + 4096, // budget + buffer
			expectedHasThinking: true,
			expectedBudget:      10000,
		},
		{
			name:      "thinking high effort",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortHigh, // 16384 budget
			},
			expectedMaxTokens:   16384 + 4096, // budget + buffer
			expectedHasThinking: true,
			expectedBudget:      16384,
		},
		{
			name:      "thinking xhigh effort",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortXHigh, // 32768 budget
			},
			expectedMaxTokens:   32768 + 4096, // budget + buffer
			expectedHasThinking: true,
			expectedBudget:      32768,
		},
		{
			name:      "thinking low effort - default max tokens sufficient",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortLow, // 1024 budget
			},
			// Default 4096 is already > 1024, so no adjustment needed
			expectedMaxTokens:   4096,
			expectedHasThinking: true,
			expectedBudget:      1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &llm.ChatRequest{
				Model: "claude-sonnet-4-20250514",
				Messages: []llm.Message{
					{Role: llm.RoleUser, Content: "Hello"},
				},
				MaxTokens: tt.maxTokens,
				Thinking:  tt.thinking,
			}

			body, err := provider.buildRequestBody(req, false)
			require.NoError(t, err)

			var parsed messagesRequest
			err = json.Unmarshal(body, &parsed)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedMaxTokens, parsed.MaxTokens,
				"max_tokens should be %d, got %d", tt.expectedMaxTokens, parsed.MaxTokens)

			if tt.expectedHasThinking {
				require.NotNil(t, parsed.Thinking, "thinking should be present")
				assert.Equal(t, "enabled", parsed.Thinking.Type)
				assert.Equal(t, tt.expectedBudget, parsed.Thinking.BudgetToken,
					"budget_tokens should be %d, got %d", tt.expectedBudget, parsed.Thinking.BudgetToken)
				assert.Greater(t, parsed.MaxTokens, parsed.Thinking.BudgetToken,
					"max_tokens (%d) must be greater than budget_tokens (%d)",
					parsed.MaxTokens, parsed.Thinking.BudgetToken)
			} else {
				assert.Nil(t, parsed.Thinking, "thinking should not be present")
			}
		})
	}
}
