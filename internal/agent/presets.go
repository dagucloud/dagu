// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

// modelPresets contains hardcoded model presets with metadata.
// These are shown in the frontend as "quick add" options.
// No API key or base URL is included — admin fills those in.
//
// Sources:
//
//	Anthropic (verified 2026-02-11): https://platform.claude.com/docs/en/docs/about-claude/models
//	OpenAI API (verified 2026-05-10): https://developers.openai.com/api/docs/models/all
//	OpenAI pricing (verified 2026-05-10): https://developers.openai.com/api/docs/pricing
//	Gemini (verified 2026-02-11): https://ai.google.dev/gemini-api/docs/models
var modelPresets = []ModelConfig{
	// --- Anthropic ---
	// https://platform.claude.com/docs/en/docs/about-claude/models
	// https://platform.claude.com/docs/en/docs/about-claude/pricing
	{Name: "Claude Opus 4.6", Provider: "anthropic", Model: "claude-opus-4-6",
		ContextWindow: 200_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 5, OutputCostPer1M: 25, SupportsThinking: true,
		Description: "Most intelligent. Best for complex agents and coding."},
	{Name: "Claude Sonnet 4.6", Provider: "anthropic", Model: "claude-sonnet-4-6",
		ContextWindow: 200_000, MaxOutputTokens: 64_000,
		InputCostPer1M: 3, OutputCostPer1M: 15, SupportsThinking: true,
		Description: "Best balance of speed and intelligence."},
	{Name: "Claude Sonnet 4.5", Provider: "anthropic", Model: "claude-sonnet-4-5",
		ContextWindow: 200_000, MaxOutputTokens: 64_000,
		InputCostPer1M: 3, OutputCostPer1M: 15, SupportsThinking: true,
		Description: "Previous generation Sonnet. Still capable."},
	{Name: "Claude Haiku 4.5", Provider: "anthropic", Model: "claude-haiku-4-5",
		ContextWindow: 200_000, MaxOutputTokens: 64_000,
		InputCostPer1M: 1, OutputCostPer1M: 5, SupportsThinking: true,
		Description: "Fastest with near-frontier intelligence."},
	// --- OpenAI ---
	// https://developers.openai.com/api/docs/models/gpt-5.5
	// https://developers.openai.com/api/docs/models/gpt-5.4
	// https://developers.openai.com/api/docs/models/gpt-5.4-mini
	// https://developers.openai.com/api/docs/models/gpt-5.4-nano
	// https://developers.openai.com/api/docs/models/o3
	// https://developers.openai.com/api/docs/models/o4-mini
	{Name: "GPT-5.5", Provider: "openai", Model: "gpt-5.5",
		ContextWindow: 1_050_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 5, OutputCostPer1M: 30, SupportsThinking: true,
		Description: "Most intelligent GPT. Best for complex reasoning and coding. 1.05M context."},
	{Name: "GPT-5.4", Provider: "openai", Model: "gpt-5.4",
		ContextWindow: 1_050_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 2.50, OutputCostPer1M: 15, SupportsThinking: true,
		Description: "Flagship GPT for professional work. 1.05M context."},
	{Name: "GPT-5.4 mini", Provider: "openai", Model: "gpt-5.4-mini",
		ContextWindow: 400_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 0.75, OutputCostPer1M: 4.50, SupportsThinking: true,
		Description: "Fast mini GPT for coding, computer use, and subagents. 400K context."},
	{Name: "GPT-5.4 nano", Provider: "openai", Model: "gpt-5.4-nano",
		ContextWindow: 400_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 0.20, OutputCostPer1M: 1.25, SupportsThinking: true,
		Description: "Cheapest GPT-5.4-class model for simple high-volume tasks. 400K context."},
	{Name: "o4-mini", Provider: "openai", Model: "o4-mini",
		ContextWindow: 200_000, MaxOutputTokens: 100_000,
		InputCostPer1M: 1.10, OutputCostPer1M: 4.40, SupportsThinking: true,
		Description: "Affordable reasoning model for coding and visual tasks. 200K context."},
	{Name: "o3", Provider: "openai", Model: "o3",
		ContextWindow: 200_000, MaxOutputTokens: 100_000,
		InputCostPer1M: 2, OutputCostPer1M: 8, SupportsThinking: true,
		Description: "Powerful reasoning model for math, science, and coding. 200K context."},
	// --- OpenAI Codex Subscription ---
	{Name: "GPT-5.5 Codex", Provider: "openai-codex", Model: "gpt-5.5",
		ContextWindow: 1_050_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 0, OutputCostPer1M: 0, SupportsThinking: true,
		Description: "Most intelligent Codex model via your ChatGPT Plus/Pro subscription."},
	{Name: "GPT-5.4 Codex", Provider: "openai-codex", Model: "gpt-5.4",
		ContextWindow: 1_050_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 0, OutputCostPer1M: 0, SupportsThinking: true,
		Description: "Codex model via your ChatGPT Plus/Pro subscription."},
	{Name: "GPT-5.4 Codex Mini", Provider: "openai-codex", Model: "gpt-5.4-mini",
		ContextWindow: 400_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 0, OutputCostPer1M: 0, SupportsThinking: true,
		Description: "Faster lower-cost Codex model via ChatGPT subscription."},
	// --- Google Gemini ---
	// https://ai.google.dev/gemini-api/docs/models
	// https://ai.google.dev/gemini-api/docs/pricing
	{Name: "Gemini 3 Pro", Provider: "gemini", Model: "gemini-3-pro-preview",
		ContextWindow: 1_048_576, MaxOutputTokens: 65_536,
		InputCostPer1M: 2, OutputCostPer1M: 12, SupportsThinking: true,
		Description: "Google's latest flagship. 1M context."},
	{Name: "Gemini 3 Flash", Provider: "gemini", Model: "gemini-3-flash-preview",
		ContextWindow: 1_048_576, MaxOutputTokens: 65_536,
		InputCostPer1M: 0.50, OutputCostPer1M: 3, SupportsThinking: true,
		Description: "Latest Gemini Flash. Fast and capable. 1M context."},
	{Name: "Gemini 2.5 Flash", Provider: "gemini", Model: "gemini-2.5-flash",
		ContextWindow: 1_048_576, MaxOutputTokens: 65_536,
		InputCostPer1M: 0.30, OutputCostPer1M: 2.50, SupportsThinking: true,
		Description: "Stable and fast with thinking. 1M context."},
	// --- OpenCode ---
	// https://opencode.ai/docs/go/
	{Name: "OpenCode Kimi K2.6", Provider: "opencode", Model: "kimi-k2.6",
		ContextWindow: 262_144, MaxOutputTokens: 65_536,
		SupportsThinking: true,
		Description:      "Moonshot Kimi K2.6 via OpenCode subscription."},
	{Name: "OpenCode Kimi K2.5", Provider: "opencode", Model: "kimi-k2.5",
		ContextWindow: 262_144, MaxOutputTokens: 32_768,
		SupportsThinking: true,
		Description:      "Moonshot Kimi K2.5 via OpenCode subscription."},
	{Name: "OpenCode DeepSeek V4 Pro", Provider: "opencode", Model: "deepseek-v4-pro",
		ContextWindow: 1_000_000, MaxOutputTokens: 384_000,
		SupportsThinking: true,
		Description:      "DeepSeek V4 Pro via OpenCode subscription."},
	{Name: "OpenCode DeepSeek V4 Flash", Provider: "opencode", Model: "deepseek-v4-flash",
		ContextWindow: 1_000_000, MaxOutputTokens: 384_000,
		SupportsThinking: true,
		Description:      "DeepSeek V4 Flash via OpenCode subscription."},
	{Name: "OpenCode GLM-5.1", Provider: "opencode", Model: "glm-5.1",
		ContextWindow: 204_800, MaxOutputTokens: 131_072,
		SupportsThinking: true,
		Description:      "Z.AI GLM-5.1 via OpenCode subscription."},
	{Name: "OpenCode GLM-5", Provider: "opencode", Model: "glm-5",
		ContextWindow: 204_800, MaxOutputTokens: 131_072,
		SupportsThinking: true,
		Description:      "Z.AI GLM-5 via OpenCode subscription."},
	{Name: "OpenCode Qwen3.6 Plus", Provider: "opencode", Model: "qwen3.6-plus",
		ContextWindow: 1_000_000, MaxOutputTokens: 65_536,
		SupportsThinking: true,
		Description:      "Alibaba Qwen3.6 Plus via OpenCode subscription."},
	// --- Z.AI ---
	// https://docs.z.ai/guides/overview/pricing
	{Name: "GLM-5", Provider: "zai", Model: "glm-5",
		ContextWindow: 200_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 1, OutputCostPer1M: 3.2, SupportsThinking: true,
		Description: "Z.AI flagship. 200K context with deep thinking."},
	{Name: "GLM-4.6", Provider: "zai", Model: "glm-4.6",
		ContextWindow: 200_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 0.6, OutputCostPer1M: 2.2, SupportsThinking: true,
		Description: "Strong reasoning and coding. 200K context."},
	{Name: "GLM-4.7-Flash", Provider: "zai", Model: "glm-4.7-flash",
		ContextWindow: 200_000, MaxOutputTokens: 128_000,
		InputCostPer1M: 0, OutputCostPer1M: 0, SupportsThinking: false,
		Description: "Free tier. Fast responses."},
}

// GetModelPresets returns a copy of the built-in model presets.
func GetModelPresets() []ModelConfig {
	result := make([]ModelConfig, len(modelPresets))
	copy(result, modelPresets)
	return result
}
