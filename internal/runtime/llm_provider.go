// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
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
