// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEnvVarExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "PlainVariableName",
			input:    "OPENAI_API_KEY",
			expected: "${OPENAI_API_KEY}",
		},
		{
			name:     "DollarPrefix",
			input:    "$ANTHROPIC_KEY",
			expected: "${ANTHROPIC_KEY}",
		},
		{
			name:     "BracedFormat",
			input:    "${MY_API_KEY}",
			expected: "${MY_API_KEY}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := runtime.NormalizeEnvVarExpr(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestEffectiveLLMConfigPreservesAgentContextLimits(t *testing.T) {
	t.Parallel()

	maxContextTokens := 100000
	observationMaxBytes := 8192
	observationKeepRecent := 2
	cfg := &ir.LLMConfig{
		MaxContextTokens:      &maxContextTokens,
		ObservationMaxBytes:   &observationMaxBytes,
		ObservationKeepRecent: &observationKeepRecent,
	}

	got := runtime.EffectiveLLMConfig(cfg, ir.ModelEntry{Provider: "openai", Name: "test"})
	assert.Equal(t, 100000, *got.MaxContextTokens)
	assert.Equal(t, 8192, *got.ObservationMaxBytes)
	assert.Equal(t, 2, *got.ObservationKeepRecent)
}

// A model entry's provider order replaces the shared one; an entry without
// one keeps the shared order.
func TestEffectiveLLMConfigProviderOrder(t *testing.T) {
	t.Parallel()

	cfg := &ir.LLMConfig{ProviderOrder: []string{"azure/eu"}}

	own := runtime.EffectiveLLMConfig(cfg, ir.ModelEntry{Provider: "openrouter", Name: "a", ProviderOrder: []string{"openai"}})
	assert.Equal(t, []string{"openai"}, own.ProviderOrder)
	shared := runtime.EffectiveLLMConfig(cfg, ir.ModelEntry{Provider: "openrouter", Name: "b"})
	assert.Equal(t, []string{"azure/eu"}, shared.ProviderOrder)
}

// Validation cannot check a provider named by a value reference, so the
// provider is refused once the reference has resolved to another provider.
func TestNewLLMProviderRefusesProviderOrder(t *testing.T) {
	t.Parallel()

	_, err := runtime.NewLLMProvider(context.Background(), &ir.LLMConfig{
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-6",
		ProviderOrder: []string{"azure/eu"},
	})
	require.ErrorContains(t, err, `provider_order is only supported by the openrouter provider, not "anthropic"`)
}
