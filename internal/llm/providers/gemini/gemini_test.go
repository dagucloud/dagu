// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestBody_WebSearch(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		config: llm.Config{APIKey: "test-key"},
	}

	t.Run("web search disabled - no google_search tool", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "gemini-2.5-flash",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
		}
		body, err := provider.buildRequestBody(req)
		require.NoError(t, err)

		var parsed generateContentRequest
		require.NoError(t, json.Unmarshal(body, &parsed))
		assert.Empty(t, parsed.Tools)
	})

	t.Run("web search enabled - google_search tool appended", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "gemini-2.5-flash",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			WebSearch: &llm.WebSearchRequest{
				Enabled: true,
			},
		}
		body, err := provider.buildRequestBody(req)
		require.NoError(t, err)

		var parsed generateContentRequest
		require.NoError(t, json.Unmarshal(body, &parsed))
		require.Len(t, parsed.Tools, 1)
		assert.NotNil(t, parsed.Tools[0].GoogleSearch, "should have google_search tool")
		assert.Empty(t, parsed.Tools[0].FunctionDeclarations, "google_search tool should have no function declarations")
	})

	t.Run("web search alongside function tools", func(t *testing.T) {
		t.Parallel()
		req := &llm.ChatRequest{
			Model:    "gemini-2.5-flash",
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
		body, err := provider.buildRequestBody(req)
		require.NoError(t, err)

		var parsed generateContentRequest
		require.NoError(t, json.Unmarshal(body, &parsed))
		require.Len(t, parsed.Tools, 2, "should have function tool group + google_search tool")

		// First entry has function declarations
		assert.NotEmpty(t, parsed.Tools[0].FunctionDeclarations)
		assert.Equal(t, "test_tool", parsed.Tools[0].FunctionDeclarations[0].Name)

		// Second entry is google_search
		assert.NotNil(t, parsed.Tools[1].GoogleSearch)
	})
}

func TestBuildRequestBody_ThinkingTokens(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		config: llm.Config{APIKey: "test-key"},
	}

	tests := []struct {
		name                    string
		maxTokens               *int
		thinking                *llm.ThinkingRequest
		expectedMaxOutputTokens *int
		expectedHasThinking     bool
		expectedThinkingBudget  *int
		expectedThinkingLevel   string
	}{
		{
			name:                    "no thinking, no max tokens",
			maxTokens:               nil,
			thinking:                nil,
			expectedMaxOutputTokens: nil,
			expectedHasThinking:     false,
		},
		{
			name:                    "no thinking, explicit max tokens",
			maxTokens:               new(8192),
			thinking:                nil,
			expectedMaxOutputTokens: new(8192),
			expectedHasThinking:     false,
		},
		{
			name:      "thinking with budget, no max tokens - auto adjust",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled:      true,
				BudgetTokens: new(4096),
			},
			expectedMaxOutputTokens: new(4096 + 4096),
			expectedHasThinking:     true,
			expectedThinkingBudget:  new(4096),
		},
		{
			name:      "thinking with budget, max tokens less than budget - auto adjust",
			maxTokens: new(2000),
			thinking: &llm.ThinkingRequest{
				Enabled:      true,
				BudgetTokens: new(4096),
			},
			expectedMaxOutputTokens: new(4096 + 4096),
			expectedHasThinking:     true,
			expectedThinkingBudget:  new(4096),
		},
		{
			name:      "thinking with budget, max tokens greater than budget - use provided",
			maxTokens: new(16000),
			thinking: &llm.ThinkingRequest{
				Enabled:      true,
				BudgetTokens: new(4096),
			},
			expectedMaxOutputTokens: new(16000),
			expectedHasThinking:     true,
			expectedThinkingBudget:  new(4096),
		},
		{
			name:      "thinking with effort level low",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortLow,
			},
			// Estimated budget 1024, so no adjustment needed with default
			expectedMaxOutputTokens: new(1024 + 4096),
			expectedHasThinking:     true,
			expectedThinkingLevel:   "low",
		},
		{
			name:      "thinking with effort level medium",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMedium,
			},
			// Estimated budget 4096, auto-adjust to 8192
			expectedMaxOutputTokens: new(4096 + 4096),
			expectedHasThinking:     true,
			expectedThinkingLevel:   "medium",
		},
		{
			name:      "thinking with effort level high",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortHigh,
			},
			// Estimated budget 8192, auto-adjust to 12288
			expectedMaxOutputTokens: new(8192 + 4096),
			expectedHasThinking:     true,
			expectedThinkingLevel:   "high",
		},
		{
			name:      "thinking with effort level xhigh",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortXHigh,
			},
			// Estimated budget 16384, auto-adjust to 20480
			expectedMaxOutputTokens: new(16384 + 4096),
			expectedHasThinking:     true,
			expectedThinkingLevel:   "high", // xhigh maps to high for Gemini
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &llm.ChatRequest{
				Model: "gemini-2.5-flash",
				Messages: []llm.Message{
					{Role: llm.RoleUser, Content: "Hello"},
				},
				MaxTokens: tt.maxTokens,
				Thinking:  tt.thinking,
			}

			body, err := provider.buildRequestBody(req)
			require.NoError(t, err)

			var parsed generateContentRequest
			err = json.Unmarshal(body, &parsed)
			require.NoError(t, err)

			if tt.expectedMaxOutputTokens != nil || tt.expectedHasThinking {
				require.NotNil(t, parsed.GenerationConfig, "generationConfig should be present")

				if tt.expectedMaxOutputTokens != nil {
					require.NotNil(t, parsed.GenerationConfig.MaxOutputTokens, "maxOutputTokens should be present")
					assert.Equal(t, *tt.expectedMaxOutputTokens, *parsed.GenerationConfig.MaxOutputTokens,
						"maxOutputTokens should be %d, got %d", *tt.expectedMaxOutputTokens, *parsed.GenerationConfig.MaxOutputTokens)
				}

				if tt.expectedThinkingBudget != nil {
					require.NotNil(t, parsed.GenerationConfig.ThinkingBudget, "thinkingBudget should be present")
					assert.Equal(t, *tt.expectedThinkingBudget, *parsed.GenerationConfig.ThinkingBudget)
					// Verify maxOutputTokens > thinkingBudget
					require.NotNil(t, parsed.GenerationConfig.MaxOutputTokens, "maxOutputTokens should be set when thinkingBudget is set")
					assert.Greater(t, *parsed.GenerationConfig.MaxOutputTokens, *parsed.GenerationConfig.ThinkingBudget,
						"maxOutputTokens (%d) must be greater than thinkingBudget (%d)",
						*parsed.GenerationConfig.MaxOutputTokens, *parsed.GenerationConfig.ThinkingBudget)
				}

				if tt.expectedThinkingLevel != "" {
					assert.Equal(t, tt.expectedThinkingLevel, parsed.GenerationConfig.ThinkingLevel)
				}
			} else {
				assert.Nil(t, parsed.GenerationConfig, "generationConfig should not be present")
			}
		})
	}
}
