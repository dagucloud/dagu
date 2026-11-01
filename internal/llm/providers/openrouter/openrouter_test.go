// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/llmtest"
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

// A stream's final chunk carries the usage OpenRouter reports, its cost
// included, which is what a gateway relaying OpenRouter charges.
func TestStreamUsageCost(t *testing.T) {
	t.Parallel()

	provider := &Provider{}
	events := make(chan llm.StreamEvent, 4)
	body := io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":30,\"total_tokens\":150,\"prompt_tokens_details\":{\"cached_tokens\":100},\"completion_tokens_details\":{\"reasoning_tokens\":12},\"cost\":0.0021}}\n" +
		"data: [DONE]\n"))

	provider.streamResponse(context.Background(), body, events)

	var usage *llm.Usage
	for event := range events {
		if event.Done {
			usage = event.Usage
		}
	}
	require.NotNil(t, usage)
	require.NotNil(t, usage.Cost)
	assert.InDelta(t, 0.0021, *usage.Cost, 1e-12)
	assert.Equal(t, 120, usage.PromptTokens)
	assert.Equal(t, 100, usage.CachedPromptTokens)
	assert.Equal(t, 12, usage.ReasoningTokens)
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

// Reasoning effort passes through, except that an unset effort is medium
// and max becomes xhigh.
func TestBuildRequestBody_ReasoningEffort(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	reasoning := func(effort string) map[string]any { return map[string]any{"effort": effort} }

	tests := []struct {
		name     string
		thinking *llm.ThinkingRequest
		expected any
	}{
		{name: "Disabled", thinking: &llm.ThinkingRequest{Effort: llm.ThinkingEffortHigh}},
		{name: "Unset", thinking: &llm.ThinkingRequest{Enabled: true}, expected: reasoning("medium")},
		{name: "None", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone}, expected: reasoning("none")},
		{name: "Minimal", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal}, expected: reasoning("minimal")},
		{name: "XHigh", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortXHigh}, expected: reasoning("xhigh")},
		{name: "Max", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMax}, expected: reasoning("xhigh")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &llm.ChatRequest{
				Model:    "openai/gpt-5.5",
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
				Thinking: tt.thinking,
			}
			body, err := provider.buildRequestBody(req, false)
			require.NoError(t, err)

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(body, &parsed))
			assert.Equal(t, tt.expected, parsed["reasoning"])
		})
	}
}

// A configured provider order reaches OpenRouter as its provider routing
// object; without one the request leaves routing to OpenRouter.
func TestChatProviderOrder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		order []string
		want  any
	}{
		{name: "set", order: []string{"azure/eu", "openai"}, want: map[string]any{"order": []any{"azure/eu", "openai"}}},
		{name: "unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bodies := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				bodies <- body
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()

			provider, err := New(llm.Config{APIKey: "test-key", BaseURL: server.URL, ProviderOrder: tc.order})
			require.NoError(t, err)
			_, err = provider.Chat(context.Background(), &llm.ChatRequest{
				Model:    "openai/gpt-6.1-terra",
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
			})
			require.NoError(t, err)

			routing, ok := (<-bodies)["provider"]
			assert.Equal(t, tc.want != nil, ok)
			assert.Equal(t, tc.want, routing)
		})
	}
}

const okAnswer = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

// fastRetry retries a logical request once without the default backoff.
var fastRetry = llm.LogicalRetryConfig{MaxAttempts: 2, InitialInterval: time.Millisecond, MaxInterval: time.Millisecond}

func chatWithRetry(t *testing.T, server *llmtest.Server) (*llm.ChatResponse, error) {
	t.Helper()
	provider, err := New(llm.Config{APIKey: "test-key", BaseURL: server.Start(t)})
	require.NoError(t, err)
	return llm.ChatWithRetry(context.Background(), provider, &llm.ChatRequest{
		Model:    "openai/gpt-6.1-terra",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
	}, fastRetry)
}

// OpenRouter answers 200 once the upstream provider accepts a request, so a
// later upstream failure arrives in the body. A rate limit or server error,
// or an answer without choices, is retried.
func TestChatRetriesTransientFailureInBody(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "error object", body: `{"id":"gen-1","error":{"code":502,"message":"Provider returned error"}}`},
		{name: "rate limit", body: `{"error":{"code":429,"message":"Rate limit exceeded","metadata":{"error_type":"rate_limit_exceeded"}}}`},
		// 520 is outside the statuses an HTTP error is retried on.
		{name: "other server error", body: `{"error":{"code":520,"message":"Unknown error"}}`},
		{name: "error on choice", body: `{"choices":[{"message":{"role":"assistant","content":"partial"},"finish_reason":"error","error":{"code":502,"message":"Provider disconnected mid-stream"}}]}`},
		{name: "no choices", body: `{"id":"gen-1","choices":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := &llmtest.Server{Path: defaultChatEndpoint, Responses: []string{tc.body, okAnswer}}

			resp, err := chatWithRetry(t, server)

			require.NoError(t, err)
			assert.Equal(t, "ok", resp.Content)
			assert.Len(t, server.Requests(), 2)
		})
	}
}

// A failure in the body whose code is a request error ends the request with
// the reported message, classified like the same error sent as a status.
func TestChatRequestErrorInBody(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		code      int
		message   string
		errorType string
		// kind is the sentinel the error classifies as, if any.
		kind error
	}{
		{name: "moderation", code: 403, message: "Your input was flagged for violence", errorType: "content_policy_violation"},
		{name: "context length message", code: 400, message: "This endpoint's maximum context length is 128000 tokens", kind: llm.ErrContextTooLong},
		{name: "context length type", code: 400, message: "Input validation error", errorType: "context_length_exceeded", kind: llm.ErrContextTooLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			failure := fmt.Sprintf(`{"error":{"code":%d,"message":%q,"metadata":{"error_type":%q}}}`, tc.code, tc.message, tc.errorType)
			server := &llmtest.Server{Path: defaultChatEndpoint, Responses: []string{failure, okAnswer}}

			_, err := chatWithRetry(t, server)

			require.ErrorContains(t, err, tc.message)
			if tc.kind != nil {
				assert.ErrorIs(t, err, tc.kind)
			}
			assert.Len(t, server.Requests(), 1)
		})
	}
}

// An error chunk ends the stream with the failure it reports.
func TestStreamErrorChunk(t *testing.T) {
	t.Parallel()

	provider := &Provider{}
	events := make(chan llm.StreamEvent, 3)
	body := io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n" +
		"data: {\"error\":{\"code\":502,\"message\":\"Provider disconnected\"},\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"error\"}]}\n"))

	provider.streamResponse(context.Background(), body, events)

	var last llm.StreamEvent
	for event := range events {
		last = event
	}
	require.True(t, last.Done)
	require.ErrorContains(t, last.Error, "Provider disconnected")
	assert.True(t, llm.IsRetryable(last.Error))
}
