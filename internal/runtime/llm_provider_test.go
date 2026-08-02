// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
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

func TestEffectiveLLMConfigPreservesControllerContextLimits(t *testing.T) {
	t.Parallel()

	maxContextTokens := 100000
	observationMaxBytes := 8192
	observationKeepRecent := 2
	cfg := &core.LLMConfig{
		MaxContextTokens:      &maxContextTokens,
		ObservationMaxBytes:   &observationMaxBytes,
		ObservationKeepRecent: &observationKeepRecent,
	}

	got := runtime.EffectiveLLMConfig(cfg, core.ModelEntry{Provider: "openai", Name: "test"})
	assert.Equal(t, 100000, *got.MaxContextTokens)
	assert.Equal(t, 8192, *got.ObservationMaxBytes)
	assert.Equal(t, 2, *got.ObservationKeepRecent)
}
