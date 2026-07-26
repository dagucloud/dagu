// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/masking"
	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	llmpkg "github.com/dagucloud/dagu/internal/llm"
)

// NewLLMProvider builds an LLM provider from a resolved DAG or step LLM config.
// The API key and base URL are evaluated against the current runtime env.
func NewLLMProvider(ctx context.Context, cfg *core.LLMConfig) (llmpkg.Provider, error) {
	providerType, err := llmpkg.ParseProviderType(cfg.Provider)
	if err != nil {
		// ParseProviderType already reports an invalid provider by name.
		return nil, err
	}

	apiKeyEnvVar := cfg.APIKeyName
	if apiKeyEnvVar == "" {
		apiKeyEnvVar = llmpkg.DefaultAPIKeyEnvVar(providerType)
	}

	var apiKey string
	if apiKeyEnvVar != "" {
		apiKey, err = ResolveString(ctx, NormalizeEnvVarExpr(apiKeyEnvVar), cmnvalue.WorkflowField("api_key"))
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate API key: %w", err)
		}
	}

	baseURL := cfg.BaseURL
	if baseURL != "" {
		baseURL, err = ResolveString(ctx, baseURL, cmnvalue.WorkflowField("base_url"))
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate baseURL: %w", err)
		}
	}
	if baseURL == "" {
		baseURL = llmpkg.DefaultBaseURL(providerType)
	}

	provider, err := llmpkg.NewProvider(providerType, llmpkg.Config{
		APIKey:          apiKey,
		BaseURL:         baseURL,
		Timeout:         5 * time.Minute,
		MaxRetries:      3,
		InitialInterval: 1 * time.Second,
		MaxInterval:     30 * time.Second,
		Multiplier:      2.0,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	return provider, nil
}

// ResolveModels evaluates variable substitution in the provider and model name of
// each entry. base_url is resolved later, when the provider is built, once shared
// config has been merged in; api_key_name is not value-resolved at all, since it
// names an environment variable the provider construction reads.
func ResolveModels(ctx context.Context, models []core.ModelEntry) ([]core.ModelEntry, error) {
	resolved := make([]core.ModelEntry, len(models))
	for i, model := range models {
		provider, err := ResolveString(ctx, model.Provider, cmnvalue.WorkflowField("llm.provider"))
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate provider: %w", err)
		}
		if provider == "" {
			return nil, emptyAfterResolution("provider", model.Provider)
		}
		name, err := ResolveString(ctx, model.Name, cmnvalue.WorkflowField("llm.model"))
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate model: %w", err)
		}
		if name == "" {
			return nil, emptyAfterResolution("model", model.Name)
		}
		resolved[i] = model
		resolved[i].Provider = provider
		resolved[i].Name = name
	}
	return resolved, nil
}

// emptyAfterResolution reports a required LLM field that has no value, naming the
// reference that produced nothing when the field carried one.
func emptyAfterResolution(field, raw string) error {
	if raw == "" {
		return fmt.Errorf("llm %s is required", field)
	}
	return fmt.Errorf("llm %s %q resolved to an empty value", field, raw)
}

// EffectiveLLMConfig folds one model entry into the shared LLM config, so the
// entry's own provider, name, and overrides win where it sets them.
func EffectiveLLMConfig(cfg *core.LLMConfig, model core.ModelEntry) *core.LLMConfig {
	return &core.LLMConfig{
		Provider:          model.Provider,
		Model:             model.Name,
		System:            cfg.System,
		Stream:            cfg.Stream,
		Thinking:          cfg.Thinking,
		Tools:             cfg.Tools,
		MaxToolIterations: cfg.MaxToolIterations,
		WebSearch:         cfg.WebSearch,
		Temperature:       coalescePtr(model.Temperature, cfg.Temperature),
		MaxTokens:         coalescePtr(model.MaxTokens, cfg.MaxTokens),
		TopP:              coalescePtr(model.TopP, cfg.TopP),
		BaseURL:           coalesceStr(model.BaseURL, cfg.BaseURL),
		APIKeyName:        coalesceStr(model.APIKeyName, cfg.APIKeyName),
	}
}

func coalescePtr[T any](override, fallback *T) *T {
	if override != nil {
		return override
	}
	return fallback
}

func coalesceStr(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// MaskSecretsForProvider replaces secret values in messages with a mask before
// they leave for an external model. Fields such as an LLM system prompt are
// resolved against the runtime scope, so a reference to a secret becomes the
// secret itself; only the copy sent to the provider is masked, and the run's own
// transcript keeps the resolved text.
func MaskSecretsForProvider(ctx context.Context, msgs []exec.LLMMessage) []exec.LLMMessage {
	rCtx := GetDAGContext(ctx)
	if rCtx.EnvScope == nil {
		return msgs
	}
	secrets := rCtx.EnvScope.AllSecrets()
	if len(secrets) == 0 {
		return msgs
	}

	envPairs := make([]string, 0, len(secrets))
	for key, value := range secrets {
		envPairs = append(envPairs, key+"="+value)
	}
	masker := masking.NewMasker(masking.SourcedEnvVars{Secrets: envPairs})

	result := make([]exec.LLMMessage, len(msgs))
	for i, msg := range msgs {
		result[i] = exec.LLMMessage{
			Role:       msg.Role,
			Content:    masker.MaskString(msg.Content),
			ToolCallID: msg.ToolCallID,
			ToolCalls:  msg.ToolCalls, // IDs and names carry no secrets
			Metadata:   msg.Metadata,
		}
	}
	return result
}

// NormalizeEnvVarExpr converts an environment variable reference to ${VAR} form,
// accepting VAR, $VAR, and ${VAR}.
func NormalizeEnvVarExpr(expr string) string {
	if expr == "" {
		return ""
	}
	if strings.HasPrefix(expr, "${") {
		return expr
	}
	if after, ok := strings.CutPrefix(expr, "$"); ok {
		return "${" + after + "}"
	}
	return "${" + expr + "}"
}
