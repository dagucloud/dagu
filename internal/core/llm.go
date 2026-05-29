// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "fmt"

// LLMRole represents the role of a message sender in a session.
type LLMRole string

// LLM message role constants.
const (
	LLMRoleSystem    LLMRole = "system"
	LLMRoleUser      LLMRole = "user"
	LLMRoleAssistant LLMRole = "assistant"
	LLMRoleTool      LLMRole = "tool"
)

// ParseLLMRole validates and returns an LLMRole from a string.
// Returns error for invalid or empty role values.
func ParseLLMRole(s string) (LLMRole, error) {
	switch LLMRole(s) {
	case LLMRoleSystem, LLMRoleUser, LLMRoleAssistant, LLMRoleTool:
		return LLMRole(s), nil
	default:
		return "", fmt.Errorf("invalid role %q: must be one of: system, user, assistant, tool", s)
	}
}

// ThinkingEffort represents the reasoning depth level for thinking mode.
type ThinkingEffort string

// ThinkingEffort constants for reasoning/thinking depth.
const (
	ThinkingEffortLow    ThinkingEffort = "low"
	ThinkingEffortMedium ThinkingEffort = "medium"
	ThinkingEffortHigh   ThinkingEffort = "high"
	ThinkingEffortXHigh  ThinkingEffort = "xhigh"
)

// ParseThinkingEffort validates and returns a ThinkingEffort from a string.
// Returns empty string for empty input (no effort specified).
// Returns error for invalid effort values.
func ParseThinkingEffort(s string) (ThinkingEffort, error) {
	if s == "" {
		return "", nil
	}
	switch ThinkingEffort(s) {
	case ThinkingEffortLow, ThinkingEffortMedium, ThinkingEffortHigh, ThinkingEffortXHigh:
		return ThinkingEffort(s), nil
	default:
		return "", fmt.Errorf("invalid thinking effort %q: must be one of: low, medium, high, xhigh", s)
	}
}

// ThinkingConfig contains configuration for extended thinking/reasoning.
type ThinkingConfig struct {
	// Enabled activates thinking mode for supported models.
	Enabled bool `json:"enabled,omitempty"`
	// Effort controls reasoning depth: low, medium, high, xhigh.
	// Maps to provider-specific parameters.
	Effort ThinkingEffort `json:"effort,omitempty"`
	// BudgetTokens sets explicit token budget (provider-specific).
	// For Anthropic: minimum 1024, max 128K.
	// For Gemini 2.5: range 128-32768.
	BudgetTokens *int `json:"budgetTokens,omitempty"`
	// IncludeInOutput includes thinking blocks in stdout.
	// Default is false for consistency across providers.
	IncludeInOutput bool `json:"includeInOutput,omitempty"`
}

// ModelEntry represents a single model in the model array for fallback support.
// When multiple models are specified, they are tried in order until one succeeds.
type ModelEntry struct {
	// Provider is the LLM provider for this model.
	Provider string `json:"provider"`
	// Name is the model name (e.g., gpt-4o, claude-sonnet-4-6).
	Name string `json:"name"`
	// Temperature overrides the shared temperature for this model.
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens overrides the shared maxTokens for this model.
	MaxTokens *int `json:"maxTokens,omitempty"`
	// TopP overrides the shared topP for this model.
	TopP *float64 `json:"topP,omitempty"`
	// BaseURL is a custom API endpoint for this model.
	BaseURL string `json:"baseURL,omitempty"`
	// APIKeyName overrides the API key environment variable for this model.
	APIKeyName string `json:"apiKeyName,omitempty"`
}

// LLMConfig contains the configuration for LLM-based executors (chat, agent, etc.).
type LLMConfig struct {
	// Provider is the LLM provider (openai, anthropic, gemini, openrouter, local).
	// Used for single model config (backward compatible).
	Provider string `json:"provider,omitempty"`
	// Model is the model to use (e.g., gpt-4o, claude-sonnet-4-6).
	// Used for single model config (backward compatible).
	Model string `json:"model,omitempty"`
	// Models is an array of models for fallback support.
	// First model is primary, rest are tried in order if primary fails.
	// When set, Provider/Model fields are ignored.
	Models []ModelEntry `json:"models,omitempty"`
	// System is the default system prompt for sessions.
	System string `json:"system,omitempty"`
	// Temperature controls randomness (0.0-2.0).
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens *int `json:"maxTokens,omitempty"`
	// TopP is the nucleus sampling parameter.
	TopP *float64 `json:"topP,omitempty"`
	// BaseURL is a custom API endpoint.
	BaseURL string `json:"baseURL,omitempty"`
	// APIKeyName is the name of the environment variable containing the API key.
	// If not specified, the default environment variable for the provider is used.
	APIKeyName string `json:"apiKeyName,omitempty"`
	// Stream enables or disables streaming output.
	// Default is true.
	Stream *bool `json:"stream,omitempty"`
	// Thinking enables extended thinking/reasoning mode.
	// Provider-specific: Anthropic uses budget_tokens, OpenAI uses reasoning.effort,
	// Gemini uses thinkingLevel/thinkingBudget, OpenRouter normalizes across providers.
	Thinking *ThinkingConfig `json:"thinking,omitempty"`
	// Tools is a list of DAG names to use as callable tools.
	// Tool names, descriptions, and parameters are auto-discovered from DAG definitions.
	// Example: ["search-tool", "analyzer-tool"]
	Tools []string `json:"tools,omitempty"`
	// MaxToolIterations limits the number of tool calling rounds.
	// Default is 10 if not specified.
	MaxToolIterations *int `json:"maxToolIterations,omitempty"`
	// WebSearch configures provider-native web search.
	WebSearch *WebSearchConfig `json:"webSearch,omitempty"`
}

// LLMMessage represents a message in the LLM session.
type LLMMessage struct {
	// Role is the message role (system, user, assistant, tool).
	Role LLMRole `json:"role,omitempty"`
	// Content is the message content. Supports variable substitution with ${VAR}.
	Content string `json:"content,omitempty"`
}

// ToolCall represents an LLM's request to invoke a tool.
type ToolCall struct {
	// ID is the unique identifier for this tool call.
	ID string `json:"id"`
	// Name is the name of the tool to invoke (matches DAG name field).
	Name string `json:"name"`
	// Arguments contains the tool arguments as key-value pairs.
	Arguments map[string]any `json:"arguments"`
}

// ToolResult represents the result of a tool execution.
type ToolResult struct {
	// ToolCallID is the ID of the tool call this result corresponds to.
	ToolCallID string `json:"tool_call_id"`
	// Name is the name of the tool that was executed.
	Name string `json:"name"`
	// Content is the result content from the tool execution.
	Content string `json:"content"`
	// Error contains any error message if the tool execution failed.
	Error string `json:"error,omitempty"`
}

// StreamEnabled returns true if streaming is enabled.
// Default is true if Stream is nil.
func (c *LLMConfig) StreamEnabled() bool {
	if c.Stream == nil {
		return true
	}
	return *c.Stream
}

// GetMaxToolIterations returns the maximum number of tool calling iterations.
// Default is 10 if not specified.
func (c *LLMConfig) GetMaxToolIterations() int {
	if c.MaxToolIterations == nil {
		return 10
	}
	return *c.MaxToolIterations
}

// HasTools returns true if tools are configured.
func (c *LLMConfig) HasTools() bool {
	return len(c.Tools) > 0
}

// GetModels returns the ordered list of models to try.
// If Models array is set, returns it. Otherwise, creates single-entry list from Provider/Model.
func (c *LLMConfig) GetModels() []ModelEntry {
	if len(c.Models) > 0 {
		return c.Models
	}
	// Backward compatibility: single model from Provider/Model fields
	return []ModelEntry{{
		Provider: c.Provider,
		Name:     c.Model,
	}}
}

// HasFallback returns true if there are multiple models configured.
func (c *LLMConfig) HasFallback() bool {
	return len(c.Models) > 1
}

// WebSearchConfig contains configuration for provider-native web search.
type WebSearchConfig struct {
	// Enabled activates provider-native web search.
	Enabled bool `json:"enabled,omitempty"`
	// MaxUses limits search invocations per request.
	MaxUses *int `json:"maxUses,omitempty"`
	// AllowedDomains restricts results to these domains (Anthropic only).
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	// BlockedDomains excludes results from these domains (Anthropic only).
	BlockedDomains []string `json:"blockedDomains,omitempty"`
	// UserLocation localizes search results.
	UserLocation *WebSearchUserLocation `json:"userLocation,omitempty"`
}

// WebSearchUserLocation provides approximate location for search localization.
type WebSearchUserLocation struct {
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	Country  string `json:"country,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// ExecutorTypeChat is the executor type for chat steps.
const ExecutorTypeChat = "chat"
