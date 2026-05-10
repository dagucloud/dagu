// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"context"
	"fmt"
	"os"
)

// Provider is the interface that all LLM providers must implement.
type Provider interface {
	// Chat sends messages and returns the complete response.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream sends messages and streams the response.
	// Returns a channel that receives StreamEvents until Done is true or an error occurs.
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error)

	// Name returns the provider name for logging and error messages.
	Name() string
}

// ProviderType identifies the LLM provider.
type ProviderType string

const (
	// ProviderOpenAI is the OpenAI provider (GPT models).
	ProviderOpenAI ProviderType = "openai"
	// ProviderOpenAICodex is the ChatGPT/Codex subscription provider.
	ProviderOpenAICodex ProviderType = "openai-codex"
	// ProviderAnthropic is the Anthropic provider (Claude models).
	ProviderAnthropic ProviderType = "anthropic"
	// ProviderGemini is the Google Gemini provider.
	ProviderGemini ProviderType = "gemini"
	// ProviderOpenRouter is the OpenRouter provider (multi-model gateway).
	ProviderOpenRouter ProviderType = "openrouter"
	// ProviderLocal is for local OpenAI-compatible servers (Ollama, vLLM, etc).
	ProviderLocal ProviderType = "local"
	// ProviderZAI is the Z.AI provider (GLM models).
	ProviderZAI ProviderType = "zai"
	// ProviderOpenCode is the OpenCode provider (Kimi, DeepSeek, GLM, etc. via opencode.ai).
	ProviderOpenCode ProviderType = "opencode"
)

// ParseProviderType converts a string to a ProviderType.
func ParseProviderType(s string) (ProviderType, error) {
	switch s {
	case "openai":
		return ProviderOpenAI, nil
	case "openai-codex":
		return ProviderOpenAICodex, nil
	case "anthropic":
		return ProviderAnthropic, nil
	case "gemini", "google":
		return ProviderGemini, nil
	case "openrouter":
		return ProviderOpenRouter, nil
	case "local", "ollama", "vllm", "llama":
		return ProviderLocal, nil
	case "zai", "zhipu", "zhipuai", "glm":
		return ProviderZAI, nil
	case "opencode":
		return ProviderOpenCode, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrInvalidProvider, s)
	}
}

// DefaultAPIKeyEnvVar returns the standard environment variable name
// for the API key of a given provider.
func DefaultAPIKeyEnvVar(providerType ProviderType) string {
	switch providerType {
	case ProviderOpenAI:
		return "OPENAI_API_KEY"
	case ProviderOpenAICodex:
		return ""
	case ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case ProviderGemini:
		return "GOOGLE_API_KEY"
	case ProviderOpenRouter:
		return "OPENROUTER_API_KEY"
	case ProviderLocal:
		return "" // Local providers typically don't need an API key
	case ProviderZAI:
		return "ZAI_API_KEY"
	case ProviderOpenCode:
		return "OPENCODE_API_KEY"
	default:
		return ""
	}
}

// DefaultBaseURL returns the default API endpoint URL for a given provider.
func DefaultBaseURL(providerType ProviderType) string {
	switch providerType {
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	case ProviderOpenAICodex:
		return "https://chatgpt.com/backend-api"
	case ProviderAnthropic:
		return "https://api.anthropic.com"
	case ProviderGemini:
		return "https://generativelanguage.googleapis.com/v1beta"
	case ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	case ProviderLocal:
		return "http://localhost:11434/v1" // Default Ollama endpoint
	case ProviderZAI:
		return "https://api.z.ai/api/paas/v4"
	case ProviderOpenCode:
		return "https://opencode.ai/zen/go/v1"
	default:
		return ""
	}
}

// GetAPIKeyFromEnv attempts to retrieve the API key for a provider
// from environment variables.
func GetAPIKeyFromEnv(providerType ProviderType) string {
	envVar := DefaultAPIKeyEnvVar(providerType)
	if envVar == "" {
		return ""
	}
	return os.Getenv(envVar)
}

// ProviderFactory is a function type that creates a new Provider instance.
type ProviderFactory func(cfg Config) (Provider, error)

// registry holds the registered provider factories.
var registry = make(map[ProviderType]ProviderFactory)

// RegisterProvider registers a provider factory for a given type.
// This is typically called in the init() function of each provider package.
func RegisterProvider(providerType ProviderType, factory ProviderFactory) {
	registry[providerType] = factory
}

// NewProvider creates a new Provider instance based on the provider type and configuration.
func NewProvider(providerType ProviderType, cfg Config) (Provider, error) {
	factory, ok := registry[providerType]
	if !ok {
		return nil, fmt.Errorf("%w: %s (not registered)", ErrInvalidProvider, providerType)
	}

	// Apply defaults
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultConfig().Timeout
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = DefaultConfig().MaxRetries
	}
	if cfg.InitialInterval == 0 {
		cfg.InitialInterval = DefaultConfig().InitialInterval
	}
	if cfg.MaxInterval == 0 {
		cfg.MaxInterval = DefaultConfig().MaxInterval
	}
	if cfg.Multiplier == 0 {
		cfg.Multiplier = DefaultConfig().Multiplier
	}

	// Try to get API key from environment if not provided
	if cfg.APIKey == "" {
		cfg.APIKey = GetAPIKeyFromEnv(providerType)
	}

	// Set default base URL if not provided
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL(providerType)
	}

	return factory(cfg)
}

// NewProviderWithAPIKey creates a new Provider with the given API key.
// This is a convenience function for simple use cases.
func NewProviderWithAPIKey(providerType ProviderType, apiKey string) (Provider, error) {
	cfg := DefaultConfig()
	cfg.APIKey = apiKey
	return NewProvider(providerType, cfg)
}

// NewProviderFromEnv creates a new Provider using API key from environment.
// This is a convenience function for simple use cases.
func NewProviderFromEnv(providerType ProviderType) (Provider, error) {
	return NewProvider(providerType, DefaultConfig())
}
