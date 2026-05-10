// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"errors"
)

// Sentinel errors for model store operations.
var (
	ErrModelNotFound          = errors.New("model not found")
	ErrModelAlreadyExists     = errors.New("model already exists")
	ErrModelNameAlreadyExists = errors.New("model name already exists")
	ErrInvalidModelID         = errors.New("invalid model ID")
)

// Config holds the configuration for the AI agent feature.
type Config struct {
	Enabled        bool             `json:"enabled"`
	DefaultModelID string           `json:"defaultModelId,omitempty"`
	ToolPolicy     ToolPolicyConfig `json:"toolPolicy"`
	SelectedSoulID string           `json:"selectedSoulId,omitempty"`
	WebSearch      *WebSearchConfig `json:"webSearch,omitempty"`
	WebTools       *WebToolsConfig  `json:"webTools,omitempty"`
}

// WebSearchConfig configures provider-native web search for agent sessions.
type WebSearchConfig struct {
	// Enabled activates provider-native web search.
	Enabled bool `json:"enabled,omitempty"`
	// MaxUses limits search invocations per request.
	MaxUses *int `json:"maxUses,omitempty"`
}

// WebToolsBackend identifies a hosted web tool provider.
type WebToolsBackend string

const (
	// WebToolsBackendTavily uses Tavily's hosted Search and Extract APIs.
	WebToolsBackendTavily WebToolsBackend = "tavily"
	// WebToolsBackendFirecrawl uses Firecrawl's hosted Search and Scrape APIs.
	WebToolsBackendFirecrawl WebToolsBackend = "firecrawl"
)

// WebToolsConfig configures first-class agent web tools such as web_search and web_extract.
type WebToolsConfig struct {
	Enabled   bool                     `json:"enabled,omitempty"`
	Backend   WebToolsBackend          `json:"backend,omitempty"`
	Tavily    *TavilyWebToolsConfig    `json:"tavily,omitempty"`
	Firecrawl *FirecrawlWebToolsConfig `json:"firecrawl,omitempty"`
}

// TavilyWebToolsConfig configures Tavily-backed web tools.
type TavilyWebToolsConfig struct {
	APIKey      string `json:"apiKey,omitempty"`
	BaseURL     string `json:"baseUrl,omitempty"`
	MaxResults  int    `json:"maxResults,omitempty"`
	SearchDepth string `json:"searchDepth,omitempty"`
}

// FirecrawlWebToolsConfig configures Firecrawl-backed web tools.
type FirecrawlWebToolsConfig struct {
	APIKey     string `json:"apiKey,omitempty"`
	BaseURL    string `json:"baseUrl,omitempty"`
	MaxResults int    `json:"maxResults,omitempty"`
}

// BashRuleAction is the decision a bash rule applies when matched.
type BashRuleAction string

const (
	BashRuleActionAllow BashRuleAction = "allow"
	BashRuleActionDeny  BashRuleAction = "deny"
)

// BashDefaultBehavior defines behavior when no rule matches.
type BashDefaultBehavior string

const (
	BashDefaultBehaviorAllow BashDefaultBehavior = "allow"
	BashDefaultBehaviorDeny  BashDefaultBehavior = "deny"
)

// BashDenyBehavior defines behavior when a command is denied by policy.
type BashDenyBehavior string

const (
	BashDenyBehaviorAskUser BashDenyBehavior = "ask_user"
	BashDenyBehaviorBlock   BashDenyBehavior = "block"
)

// BashRule defines an ordered regex rule used for bash command policy checks.
type BashRule struct {
	Name    string         `json:"name,omitempty"`
	Pattern string         `json:"pattern"`
	Action  BashRuleAction `json:"action"`
	Enabled *bool          `json:"enabled,omitempty"`
}

// BashPolicyConfig configures granular bash command policy behavior.
type BashPolicyConfig struct {
	Rules           []BashRule          `json:"rules,omitempty"`
	DefaultBehavior BashDefaultBehavior `json:"defaultBehavior,omitempty"`
	DenyBehavior    BashDenyBehavior    `json:"denyBehavior,omitempty"`
}

// ToolPolicyConfig configures agent tool permissions.
// Tools is a map of tool name to enabled/disabled state.
type ToolPolicyConfig struct {
	Tools map[string]bool  `json:"tools,omitempty"`
	Bash  BashPolicyConfig `json:"bash"`
}

// LLMConfig holds LLM provider configuration for the agent.
// Supported providers: anthropic, openai, gemini, openrouter, or local.
type LLMConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseUrl,omitempty"`
}

// DefaultConfig returns the default agent configuration.
func DefaultConfig() *Config {
	return &Config{
		Enabled:    true,
		ToolPolicy: DefaultToolPolicy(),
		WebSearch:  &WebSearchConfig{Enabled: true},
	}
}

// DefaultToolPolicy returns the default agent tool policy.
// The default is permissive: all tools enabled and bash allowed unless rules deny.
// Tool names and default-enabled states are derived from the tool registry.
func DefaultToolPolicy() ToolPolicyConfig {
	regs := RegisteredTools()
	tools := make(map[string]bool, len(regs))
	for _, reg := range regs {
		tools[reg.Name] = reg.DefaultEnabled
	}
	return ToolPolicyConfig{
		Tools: tools,
		Bash: BashPolicyConfig{
			Rules:           []BashRule{},
			DefaultBehavior: BashDefaultBehaviorAllow,
			DenyBehavior:    BashDenyBehaviorAskUser,
		},
	}
}

// ModelConfig holds the configuration for a single LLM model.
type ModelConfig struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	APIKey           string  `json:"apiKey,omitempty"`
	BaseURL          string  `json:"baseUrl,omitempty"`
	ContextWindow    int     `json:"contextWindow,omitempty"`
	MaxOutputTokens  int     `json:"maxOutputTokens,omitempty"`
	InputCostPer1M   float64 `json:"inputCostPer1M,omitempty"`
	OutputCostPer1M  float64 `json:"outputCostPer1M,omitempty"`
	SupportsThinking bool    `json:"supportsThinking,omitempty"`
	// ThinkingEffort accepts "low", "medium", "high", or "xhigh"; empty uses the provider default.
	ThinkingEffort string `json:"thinkingEffort,omitempty"`
	Description    string `json:"description,omitempty"`
}

// ToLLMConfig converts a ModelConfig to an LLMConfig for provider creation.
func (m *ModelConfig) ToLLMConfig() LLMConfig {
	return LLMConfig{
		Provider: m.Provider,
		Model:    m.Model,
		APIKey:   m.APIKey,
		BaseURL:  m.BaseURL,
	}
}

// ValidateModelID validates that id is a safe, well-formed model identifier.
func ValidateModelID(id string) error {
	return validateSlugID(id, ErrInvalidModelID)
}

// MemoryContent holds loaded memory for system prompt injection.
type MemoryContent struct {
	GlobalMemory string // Contents of global MEMORY.md (truncated)
	DAGMemory    string // Contents of per-DAG MEMORY.md (truncated)
	DAGName      string // Name of the DAG (empty if no DAG context)
	MemoryDir    string // Root memory directory path
	ReadOnly     bool   // Memory is execution-scoped read-only context
}
