// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatusage_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/providers/chatusage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A chat completion's usage keeps its counts, which already include the
// cached and reasoning tokens its details name, and the cost a provider such
// as OpenRouter reports; a provider that reports no cost leaves it unknown.
func TestUsage(t *testing.T) {
	t.Parallel()

	cost := 0.00042
	for _, tc := range []struct {
		name string
		body string
		want llm.Usage
	}{
		{
			name: "details and cost",
			body: `{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":100},"completion_tokens_details":{"reasoning_tokens":12},"cost":0.00042}`,
			want: llm.Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150, CachedPromptTokens: 100, ReasoningTokens: 12, Cost: &cost},
		},
		{
			name: "counts alone",
			body: `{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}`,
			want: llm.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var usage chatusage.Usage
			require.NoError(t, json.Unmarshal([]byte(tc.body), &usage))
			assert.Equal(t, tc.want, usage.LLM())
		})
	}
}

// A free call's cost is a reported zero, not an unknown cost.
func TestUsageFreeCall(t *testing.T) {
	t.Parallel()

	var usage chatusage.Usage
	require.NoError(t, json.Unmarshal([]byte(`{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"cost":0}`), &usage))
	cost := usage.LLM().Cost
	require.NotNil(t, cost)
	assert.Zero(t, *cost)
}

// The Responses API names its counts input and output, and its details the
// same way.
func TestResponses(t *testing.T) {
	t.Parallel()

	var usage chatusage.Responses
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":20,"output_tokens":10,"total_tokens":30,"input_tokens_details":{"cached_tokens":5},"output_tokens_details":{"reasoning_tokens":4}}`), &usage))
	assert.Equal(t, llm.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30, CachedPromptTokens: 5, ReasoningTokens: 4}, usage.LLM())
}
