// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

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

// Test the wire shape through raw JSON so the production request structs do
// not define their own expected output.
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
				Effort:       llm.ThinkingEffortNone,
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
			expectedThinkingBudget:  new(1024),
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
			expectedThinkingBudget:  new(4096),
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
			expectedThinkingBudget:  new(8192),
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
			expectedThinkingBudget:  new(16384),
		},
		{
			name:      "thinking with effort level max",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMax,
			},
			expectedMaxOutputTokens: new(16384 + 4096),
			expectedHasThinking:     true,
			expectedThinkingBudget:  new(16384),
		},
		{
			name:      "thinking with effort level minimal",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortMinimal,
			},
			expectedMaxOutputTokens: new(1024 + 4096),
			expectedHasThinking:     true,
			expectedThinkingBudget:  new(1024),
		},
		{
			name:      "thinking with effort level none",
			maxTokens: nil,
			thinking: &llm.ThinkingRequest{
				Enabled: true,
				Effort:  llm.ThinkingEffortNone,
			},
			expectedMaxOutputTokens: nil,
			expectedHasThinking:     true,
			expectedThinkingBudget:  new(0),
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

			var request map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &request))

			if tt.expectedMaxOutputTokens != nil || tt.expectedHasThinking {
				var config map[string]json.RawMessage
				require.Contains(t, request, "generationConfig")
				require.NoError(t, json.Unmarshal(request["generationConfig"], &config))
				require.NotContains(t, config, "thinkingBudget")
				require.NotContains(t, config, "thinkingLevel")

				if tt.expectedMaxOutputTokens != nil {
					var maxOutputTokens int
					require.Contains(t, config, "maxOutputTokens")
					require.NoError(t, json.Unmarshal(config["maxOutputTokens"], &maxOutputTokens))
					assert.Equal(t, *tt.expectedMaxOutputTokens, maxOutputTokens)
				} else {
					assert.NotContains(t, config, "maxOutputTokens")
				}

				if tt.expectedThinkingBudget != nil {
					var thinking map[string]json.RawMessage
					require.Contains(t, config, "thinkingConfig")
					require.NoError(t, json.Unmarshal(config["thinkingConfig"], &thinking))
					var thinkingBudget int
					require.Contains(t, thinking, "thinkingBudget")
					require.NoError(t, json.Unmarshal(thinking["thinkingBudget"], &thinkingBudget))
					assert.Equal(t, *tt.expectedThinkingBudget, thinkingBudget)
				}
				if tt.expectedThinkingBudget != nil && *tt.expectedThinkingBudget > 0 {
					thinkingBudget := *tt.expectedThinkingBudget
					// Verify maxOutputTokens > thinkingBudget
					require.Contains(t, config, "maxOutputTokens", "maxOutputTokens should be set when thinkingBudget is set")
					var maxOutputTokens int
					require.NoError(t, json.Unmarshal(config["maxOutputTokens"], &maxOutputTokens))
					assert.Greater(t, maxOutputTokens, thinkingBudget,
						"maxOutputTokens (%d) must be greater than thinkingBudget (%d)",
						maxOutputTokens, thinkingBudget)
				}
			} else {
				assert.NotContains(t, request, "generationConfig")
			}
		})
	}
}

func TestThinkingConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		model         string
		thinking      *llm.ThinkingRequest
		temperature   *float64
		topP          *float64
		stop          []string
		wantThinking  string
		wantMaxTokens int
		noMaxTokens   bool
	}{
		{
			name:         "Flash Lite none disables thinking",
			model:        "gemini-2.5-flash-lite",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone},
			wantThinking: `{"thinkingBudget":0}`,
			noMaxTokens:  true,
		},
		{
			name:         "Pro none keeps a supported budget",
			model:        "gemini-2.5-pro",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone},
			wantThinking: `{"thinkingBudget":1024}`,
		},
		{
			name:         "budget model outside 2.5 omits level",
			model:        "gemini-robotics-er-1.6-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			wantThinking: `{"thinkingBudget":1024}`,
		},
		{
			name:         "Robotics 1.6 none disables thinking",
			model:        "gemini-robotics-er-1.6-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone},
			wantThinking: `{"thinkingBudget":0}`,
			noMaxTokens:  true,
		},
		{
			name:         "Robotics 1.5 uses a budget",
			model:        "gemini-robotics-er-1.5-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			wantThinking: `{"thinkingBudget":1024}`,
		},
		{
			name:         "later major version uses a level",
			model:        "gemini-4-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortHigh},
			wantThinking: `{"thinkingLevel":"HIGH"}`,
		},
		{
			name:         "unversioned alias uses a level",
			model:        "gemini-flash-latest",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortHigh},
			wantThinking: `{"thinkingLevel":"HIGH"}`,
		},
		{
			name:         "Robotics 2 uses a level",
			model:        "gemini-robotics-er-2-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3 series effort becomes uppercase level",
			model:        "gemini-3.8-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortHigh},
			wantThinking: `{"thinkingLevel":"HIGH"}`,
		},
		{
			name:         "3.1 Pro none falls back to low",
			model:        "gemini-3.1-pro-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3.1 Pro minimal falls back to low",
			model:        "gemini-3.1-pro-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3.8 Flash none falls back to low",
			model:        "gemini-3.8-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3.8 Flash minimal falls back to low",
			model:        "gemini-3.8-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3.7 Flash minimal falls back to low",
			model:        "gemini-3.7-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3 Pro minimal falls back to low",
			model:        "gemini-3-pro-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			// Medium sits between the two supported levels; the higher one wins.
			name:          "3 Pro medium rounds up to high",
			model:         "gemini-3-pro-preview",
			thinking:      &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMedium},
			wantThinking:  `{"thinkingLevel":"HIGH"}`,
			wantMaxTokens: 8192 + 4096,
		},
		{
			name:         "Flash Lite Image low falls back to minimal",
			model:        "gemini-3.1-flash-lite-image",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			wantThinking: `{"thinkingLevel":"MINIMAL"}`,
		},
		{
			name:          "Flash Lite Image medium falls back to high",
			model:         "gemini-3.1-flash-lite-image",
			thinking:      &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMedium},
			wantThinking:  `{"thinkingLevel":"HIGH"}`,
			wantMaxTokens: 8192 + 4096,
		},
		{
			name:         "Flash Image low falls back to minimal",
			model:        "gemini-3.1-flash-image",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			wantThinking: `{"thinkingLevel":"MINIMAL"}`,
		},
		{
			name:          "Flash Image medium falls back to high",
			model:         "gemini-3.1-flash-image",
			thinking:      &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMedium},
			wantThinking:  `{"thinkingLevel":"HIGH"}`,
			wantMaxTokens: 8192 + 4096,
		},
		{
			// Low sits between minimal and medium; the higher one wins.
			name:          "Nano Banana 2.1 low rounds up to medium",
			model:         "gemini-nano-banana-2.1",
			thinking:      &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			wantThinking:  `{"thinkingLevel":"MEDIUM"}`,
			wantMaxTokens: 4096 + 4096,
		},
		{
			name:         "Pro alias minimal falls back to low",
			model:        "gemini-pro-latest",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3 series minimal level",
			model:        "gemini-3-flash-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal},
			wantThinking: `{"thinkingLevel":"MINIMAL"}`,
		},
		{
			name:         "3 series low level",
			model:        "gemini-3.8-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			wantThinking: `{"thinkingLevel":"LOW"}`,
		},
		{
			name:         "3 series medium level",
			model:        "gemini-3.8-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMedium},
			wantThinking: `{"thinkingLevel":"MEDIUM"}`,
		},
		{
			name:         "3 series max maps to high",
			model:        "gemini-3-flash-preview",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMax},
			wantThinking: `{"thinkingLevel":"HIGH"}`,
		},
		{
			name:          "explicit budget on 3 series overrides effort",
			model:         "gemini-3.8-flash",
			thinking:      &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortHigh, BudgetTokens: new(4096)},
			wantThinking:  `{"thinkingBudget":4096}`,
			wantMaxTokens: 4096 + 4096,
		},
		{
			name:         "explicit zero budget is preserved",
			model:        "gemini-2.5-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, BudgetTokens: new(0)},
			wantThinking: `{"thinkingBudget":0}`,
			noMaxTokens:  true,
		},
		{
			name:         "dynamic budget is preserved",
			model:        "gemini-2.5-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, BudgetTokens: new(-1)},
			wantThinking: `{"thinkingBudget":-1}`,
		},
		{
			name:         "other generation settings remain alongside thinking",
			model:        "gemini-2.5-flash",
			thinking:     &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortLow},
			temperature:  new(0.2),
			topP:         new(0.8),
			stop:         []string{"END"},
			wantThinking: `{"thinkingBudget":1024}`,
		},
		{
			name:     "disabled thinking is omitted",
			model:    "gemini-2.5-flash",
			thinking: &llm.ThinkingRequest{Enabled: false, Effort: llm.ThinkingEffortHigh},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, err := (&Provider{}).buildRequestBody(&llm.ChatRequest{
				Model:       tt.model,
				Thinking:    tt.thinking,
				Temperature: tt.temperature,
				TopP:        tt.topP,
				Stop:        tt.stop,
				Messages:    []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			})
			require.NoError(t, err)
			var request map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &request))
			if tt.wantThinking == "" {
				assert.NotContains(t, request, "generationConfig")
				return
			}
			var config map[string]json.RawMessage
			require.Contains(t, request, "generationConfig")
			require.NoError(t, json.Unmarshal(request["generationConfig"], &config))
			require.Contains(t, config, "thinkingConfig")
			assert.JSONEq(t, tt.wantThinking, string(config["thinkingConfig"]))
			if tt.wantMaxTokens > 0 {
				var maxTokens int
				require.NoError(t, json.Unmarshal(config["maxOutputTokens"], &maxTokens))
				assert.Equal(t, tt.wantMaxTokens, maxTokens)
			}
			if tt.noMaxTokens {
				assert.NotContains(t, config, "maxOutputTokens")
			}
			if tt.temperature != nil {
				assert.JSONEq(t, `0.2`, string(config["temperature"]))
			}
			if tt.topP != nil {
				assert.JSONEq(t, `0.8`, string(config["topP"]))
			}
			if tt.stop != nil {
				assert.JSONEq(t, `["END"]`, string(config["stopSequences"]))
			}
		})
	}
}

func TestBuildRequestBody_Images(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	body, err := provider.buildRequestBody(&llm.ChatRequest{
		Model: "gemini-3.5-flash",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "describe", Images: []llm.Image{{MediaType: "image/png", Data: []byte{1, 2}}}},
		},
	})
	require.NoError(t, err)

	var parsed struct {
		Contents []struct {
			Parts json.RawMessage `json:"parts"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Contents, 1)
	assert.JSONEq(t, `[
		{"inlineData":{"mimeType":"image/png","data":"AQI="}},
		{"text":"describe"}
	]`, string(parsed.Contents[0].Parts))
}

// Tool results name the function they answer, found through the call ID,
// and the results of one turn's calls travel in one content, as Gemini
// requires for parallel calls.
func TestBuildRequestBody_FunctionResponses(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	body, err := provider.buildRequestBody(&llm.ChatRequest{
		Model: "gemini-2.5-flash",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "weather in Tokyo and Paris"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
				{ID: "call_0", Type: "function", Function: llm.ToolCallFunction{Name: "get_weather", Arguments: `{"city":"Tokyo"}`}},
				{ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "get_time", Arguments: `{"city":"Paris"}`}},
			}},
			{Role: llm.RoleTool, ToolCallID: "call_0", Content: `{"celsius":21}`},
			{Role: llm.RoleTool, ToolCallID: "call_1", Content: "noon"},
			{Role: llm.RoleUser, Content: "thanks"},
		},
	})
	require.NoError(t, err)

	var parsed struct {
		Contents []struct {
			Role  string          `json:"role"`
			Parts json.RawMessage `json:"parts"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Contents, 4)
	assert.Equal(t, "user", parsed.Contents[2].Role)
	assert.JSONEq(t, `[
		{"functionResponse":{"name":"get_weather","response":{"celsius":21}}},
		{"functionResponse":{"name":"get_time","response":{"result":"noon"}}}
	]`, string(parsed.Contents[2].Parts))
	assert.JSONEq(t, `[{"text":"thanks"}]`, string(parsed.Contents[3].Parts))
}

// Tool schemas are sent as JSON Schema, which keeps keywords such as
// additionalProperties and $defs that the OpenAPI parameters field rejects.
func TestBuildRequestBody_FunctionDeclarationSchema(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	parameters := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"category": map[string]any{"$ref": "#/$defs/category"},
		},
		"$defs": map[string]any{
			"category": map[string]any{"type": "string", "enum": []any{"refund", "question"}},
		},
	}
	body, err := provider.buildRequestBody(&llm.ChatRequest{
		Model:    "gemini-2.5-flash",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
		Tools: []llm.Tool{{
			Type:     "function",
			Function: llm.ToolFunction{Name: "respond", Description: "Answer", Parameters: parameters},
		}},
	})
	require.NoError(t, err)

	var parsed struct {
		Tools []struct {
			FunctionDeclarations []map[string]json.RawMessage `json:"functionDeclarations"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed.Tools, 1)
	require.Len(t, parsed.Tools[0].FunctionDeclarations, 1)
	declaration := parsed.Tools[0].FunctionDeclarations[0]

	expected, err := json.Marshal(parameters)
	require.NoError(t, err)
	assert.JSONEq(t, string(expected), string(declaration["parametersJsonSchema"]))
	assert.NotContains(t, declaration, "parameters", "parameters and parametersJsonSchema are mutually exclusive")
}

// A single SSE data line larger than bufio.Scanner's default 64 KiB limit must
// not terminate the stream.
func TestStreamResponse_LargeEvent(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("a", 200*1024)
	stream := "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"" + big + "\"}]},\"finishReason\":\"STOP\"}]}\n\n"

	provider := &Provider{}
	events := make(chan llm.StreamEvent)
	go provider.streamResponse(context.Background(), io.NopCloser(strings.NewReader(stream)), events)

	var deltas []string
	done := false
	for event := range events {
		require.NoError(t, event.Error)
		if event.Done {
			done = true
			continue
		}
		deltas = append(deltas, event.Delta)
	}
	require.True(t, done)
	require.Equal(t, []string{big}, deltas)
}

// A response's completion count includes the thinking tokens, which Gemini
// reports apart from the candidates, and its cached content is named.
func TestStreamResponse_Usage(t *testing.T) {
	t.Parallel()

	stream := "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":50,\"candidatesTokenCount\":10,\"thoughtsTokenCount\":5,\"cachedContentTokenCount\":20,\"totalTokenCount\":65}}\n\n"

	provider := &Provider{}
	events := make(chan llm.StreamEvent)
	go provider.streamResponse(context.Background(), io.NopCloser(strings.NewReader(stream)), events)

	var usage *llm.Usage
	for event := range events {
		require.NoError(t, event.Error)
		if event.Done {
			usage = event.Usage
		}
	}
	require.NotNil(t, usage)
	assert.Equal(t, llm.Usage{PromptTokens: 50, CompletionTokens: 15, TotalTokens: 65, CachedPromptTokens: 20, ReasoningTokens: 5}, *usage)
}
