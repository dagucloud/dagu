// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package chatusage reads the usage objects of OpenAI's chat completions and
// Responses APIs, and of the APIs compatible with them.
package chatusage

import "github.com/dagucloud/dagu/v2/internal/llm"

// Usage is the usage object of a chat completion or of a stream's final
// chunk. A provider reports the details it supports and leaves out the rest.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details,omitempty"`
	// Cost is what OpenRouter, and gateways that relay its answers, charged
	// for the call, in US dollars.
	Cost *float64 `json:"cost,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type completionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// LLM returns the usage as llm.Usage. The prompt and completion counts
// already include the cached and reasoning tokens their details name.
func (u Usage) LLM() llm.Usage {
	usage := llm.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		Cost:             u.Cost,
	}
	if u.PromptTokensDetails != nil {
		usage.CachedPromptTokens = u.PromptTokensDetails.CachedTokens
	}
	if u.CompletionTokensDetails != nil {
		usage.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return usage
}

// Responses is the usage object of the Responses API, which names its counts
// input and output.
type Responses struct {
	InputTokens         int                      `json:"input_tokens"`
	OutputTokens        int                      `json:"output_tokens"`
	TotalTokens         int                      `json:"total_tokens"`
	InputTokensDetails  *promptTokensDetails     `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *completionTokensDetails `json:"output_tokens_details,omitempty"`
}

// LLM returns the usage as llm.Usage. The input and output counts already
// include the cached and reasoning tokens their details name.
func (u Responses) LLM() llm.Usage {
	usage := llm.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.InputTokensDetails != nil {
		usage.CachedPromptTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		usage.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	return usage
}
