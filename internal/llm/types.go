// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package llm provides a generic abstraction layer for interacting with
// Large Language Model providers.
package llm

import (
	"context"
	"fmt"
	"time"
)

// Role represents the role of a message sender in a session.
type Role string

const (
	// RoleSystem represents a system message that sets behavior/context.
	RoleSystem Role = "system"
	// RoleUser represents a message from the user.
	RoleUser Role = "user"
	// RoleAssistant represents a message from the AI assistant.
	RoleAssistant Role = "assistant"
	// RoleTool represents a tool/function call result.
	RoleTool Role = "tool"
)

// ThinkingEffort represents the reasoning depth level for thinking mode.
type ThinkingEffort string

const (
	// ThinkingEffortLow provides quick reasoning with minimal token budget.
	ThinkingEffortLow ThinkingEffort = "low"
	// ThinkingEffortMedium provides balanced reasoning (default).
	ThinkingEffortMedium ThinkingEffort = "medium"
	// ThinkingEffortHigh provides thorough analysis with larger token budget.
	ThinkingEffortHigh ThinkingEffort = "high"
	// ThinkingEffortXHigh provides maximum reasoning depth.
	// Note: Not all providers support this level.
	ThinkingEffortXHigh ThinkingEffort = "xhigh"
)

// ParseThinkingEffort validates and returns a ThinkingEffort from a string.
// Returns empty string for empty input (no effort specified).
func ParseThinkingEffort(s string) (ThinkingEffort, error) {
	switch ThinkingEffort(s) {
	case "":
		return "", nil
	case ThinkingEffortLow, ThinkingEffortMedium, ThinkingEffortHigh, ThinkingEffortXHigh:
		return ThinkingEffort(s), nil
	default:
		return "", fmt.Errorf("invalid thinking effort %q: must be one of: low, medium, high, xhigh", s)
	}
}

// ParseRole converts a string to a Role, with support for common aliases.
func ParseRole(s string) Role {
	switch s {
	case "system", "sys":
		return RoleSystem
	case "user", "human":
		return RoleUser
	case "assistant", "ai", "bot":
		return RoleAssistant
	case "tool", "function":
		return RoleTool
	default:
		return Role(s)
	}
}

// Message represents a single message in a session.
type Message struct {
	// Role identifies who sent the message (system, user, assistant, or tool).
	Role Role `json:"role"`
	// Content is the text content of the message.
	Content string `json:"content"`
	// ReasoningContent holds the model's internal reasoning/thinking text from a
	// previous assistant turn. Some providers (e.g. Moonshot/KIMI via OpenCode)
	// require this to be echoed back in subsequent turns when reasoning is enabled.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// Name is an optional identifier for the message sender.
	// Useful in multi-agent scenarios or for tool messages.
	Name string `json:"name,omitempty"`
	// ToolCallID is the ID of the tool call this message is responding to.
	// Required when Role is "tool".
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls contains tool calls made by the assistant.
	// Only set when Role is "assistant" and the model requests tool calls.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Tool represents a function/tool available to the LLM.
type Tool struct {
	// Type is always "function" for function tools.
	Type string `json:"type"`
	// Function contains the function definition.
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a callable function.
type ToolFunction struct {
	// Name is the function name (must match [a-zA-Z0-9_-]+).
	Name string `json:"name"`
	// Description explains what the function does.
	Description string `json:"description"`
	// Parameters is a JSON Schema describing the function parameters.
	Parameters map[string]any `json:"parameters"`
}

// ToolCall represents an LLM's request to call a tool.
type ToolCall struct {
	// ID is a unique identifier for this tool call.
	ID string `json:"id"`
	// Type is always "function" for function calls.
	Type string `json:"type"`
	// Function contains the function call details.
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the details of a function call.
type ToolCallFunction struct {
	// Name is the name of the function to call.
	Name string `json:"name"`
	// Arguments is a JSON string containing the function arguments.
	Arguments string `json:"arguments"`
}

// Usage contains token usage information from an LLM API call.
type Usage struct {
	// PromptTokens is the number of tokens in the prompt.
	PromptTokens int `json:"prompt_tokens"`
	// CompletionTokens is the number of tokens in the completion.
	CompletionTokens int `json:"completion_tokens"`
	// TotalTokens is the sum of prompt and completion tokens.
	TotalTokens int `json:"total_tokens"`
}

// OAuthCredential contains dynamic bearer-auth details resolved at request time.
type OAuthCredential struct {
	AccessToken string
	AccountID   string
}

// OAuthCredentialProvider resolves dynamic bearer credentials for providers that
// cannot safely persist a static API key inside model configuration.
type OAuthCredentialProvider interface {
	ResolveCredential(ctx context.Context) (OAuthCredential, error)
}

// Config contains configuration for an LLM provider.
type Config struct {
	// APIKey is the authentication key for the provider.
	APIKey string
	// AccountID carries provider-specific account metadata when required.
	AccountID string
	// BaseURL is the API endpoint URL. If empty, the provider's default is used.
	BaseURL string
	// OAuthCredentialProvider resolves dynamic bearer credentials on demand.
	OAuthCredentialProvider OAuthCredentialProvider
	// Timeout is the maximum time to wait for a response.
	// Default is 60 seconds if not specified unless DisableRequestTimeout is true.
	Timeout time.Duration
	// DisableRequestTimeout leaves the underlying request client without a
	// response timeout. Callers must cancel through the request context.
	DisableRequestTimeout bool

	// Retry configuration
	// MaxRetries is the maximum number of retry attempts for transient errors.
	// Default is 3 if not specified.
	MaxRetries int
	// InitialInterval is the initial backoff interval before the first retry.
	// Default is 1 second if not specified.
	InitialInterval time.Duration
	// MaxInterval is the maximum backoff interval between retries.
	// Default is 30 seconds if not specified.
	MaxInterval time.Duration
	// Multiplier is the factor by which the interval increases after each retry.
	// Default is 2.0 if not specified.
	Multiplier float64
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Timeout:         60 * time.Second,
		MaxRetries:      3,
		InitialInterval: 1 * time.Second,
		MaxInterval:     30 * time.Second,
		Multiplier:      2.0,
	}
}

// ThinkingRequest contains configuration for extended thinking/reasoning mode.
// Each provider maps this to their native format.
type ThinkingRequest struct {
	// Enabled activates thinking mode.
	Enabled bool
	// Effort controls reasoning depth: low, medium, high, xhigh.
	Effort ThinkingEffort
	// BudgetTokens sets explicit token budget (provider-specific).
	BudgetTokens *int
	// IncludeInOutput includes thinking blocks in the response content.
	IncludeInOutput bool
}

// WebSearchRequest contains configuration for provider-native web search.
// Each provider maps this to its native format during request building.
type WebSearchRequest struct {
	// Enabled activates provider-native web search.
	Enabled bool
	// MaxUses limits search invocations per request.
	// Anthropic: maps to max_uses. OpenRouter: maps to max_results.
	MaxUses *int
	// AllowedDomains restricts results to these domains (Anthropic only).
	AllowedDomains []string
	// BlockedDomains excludes results from these domains (Anthropic only).
	BlockedDomains []string
	// UserLocation localizes search results.
	UserLocation *UserLocation
}

// UserLocation provides approximate location for search result localization.
type UserLocation struct {
	// City is the city name.
	City string
	// Region is the region or state.
	Region string
	// Country is the ISO country code (e.g., "US").
	Country string
	// Timezone is the IANA timezone (e.g., "America/New_York").
	Timezone string
}

// ChatRequest contains the input for a chat completion request.
type ChatRequest struct {
	// Model is the identifier of the model to use.
	Model string
	// Messages is the session history to send to the model.
	Messages []Message
	// Temperature controls randomness in the response (0.0 to 2.0).
	// Lower values make output more deterministic.
	Temperature *float64
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens *int
	// TopP is the nucleus sampling parameter (0.0 to 1.0).
	TopP *float64
	// Stop is a list of sequences where the model will stop generating.
	Stop []string
	// Thinking enables extended thinking/reasoning mode.
	// Provider-specific handling in each provider implementation.
	Thinking *ThinkingRequest
	// Tools is a list of tools available for the model to call.
	Tools []Tool
	// ToolChoice controls how the model uses tools.
	// Values: "auto" (default), "required", "none", or a specific tool name.
	ToolChoice string
	// WebSearch enables provider-native web search when non-nil and Enabled.
	// Provider-specific handling in each provider implementation.
	WebSearch *WebSearchRequest
}

// ChatResponse contains the output from a chat completion request.
type ChatResponse struct {
	// Content is the generated text content.
	Content string
	// ReasoningContent holds the model's internal reasoning/thinking text.
	// Must be preserved and echoed back in the next assistant message when
	// reasoning is enabled, as required by some providers (e.g. Moonshot/KIMI).
	ReasoningContent string
	// FinishReason indicates why the model stopped generating.
	// Common values: "stop", "length", "content_filter", "tool_calls".
	FinishReason string
	// Usage contains token usage statistics.
	Usage Usage
	// ToolCalls contains tool calls requested by the model.
	// Only populated when FinishReason is "tool_calls".
	ToolCalls []ToolCall
}

// StreamEvent represents a single event in a streaming response.
type StreamEvent struct {
	// Delta is the incremental content received in this event.
	Delta string
	// Done indicates whether the stream has completed.
	Done bool
	// Error contains any error that occurred during streaming.
	Error error
	// Usage contains token usage statistics (only set when Done is true).
	Usage *Usage
	// ToolCalls contains tool calls accumulated during streaming.
	// Only populated when Done is true and the model requested tool calls.
	ToolCalls []ToolCall
	// FinishReason indicates why the model stopped (only set when Done is true).
	FinishReason string
}
