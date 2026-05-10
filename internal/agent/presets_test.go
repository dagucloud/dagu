// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetModelPresets(t *testing.T) {
	t.Parallel()

	presets := GetModelPresets()
	require.NotEmpty(t, presets)

	validProviders := map[string]bool{
		"anthropic":    true,
		"openai":       true,
		"openai-codex": true,
		"gemini":       true,
		"openrouter":   true,
		"local":        true,
		"zai":          true,
		"opencode":     true,
	}

	for _, p := range presets {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()

			assert.NotEmpty(t, p.Name, "Name must not be empty")
			assert.NotEmpty(t, p.Provider, "Provider must not be empty")
			assert.NotEmpty(t, p.Model, "Model must not be empty")
			assert.GreaterOrEqual(t, p.InputCostPer1M, 0.0, "InputCostPer1M should be >= 0")
			assert.GreaterOrEqual(t, p.OutputCostPer1M, 0.0, "OutputCostPer1M should be >= 0")
			assert.True(t, validProviders[p.Provider], "provider %q is not valid", p.Provider)
		})
	}
}

func TestGetModelPresets_ReturnsCopy(t *testing.T) {
	t.Parallel()

	t.Run("modifying returned slice does not affect subsequent calls", func(t *testing.T) {
		t.Parallel()

		first := GetModelPresets()
		originalName := first[0].Name

		// Mutate the returned slice
		first[0].Name = "MODIFIED"

		second := GetModelPresets()
		assert.Equal(t, originalName, second[0].Name, "second call should return unmodified data")
	})

	t.Run("returned slices are independent", func(t *testing.T) {
		t.Parallel()

		first := GetModelPresets()
		second := GetModelPresets()

		assert.Equal(t, len(first), len(second))

		// Append to first should not affect second
		first = append(first, ModelConfig{Name: "extra"})
		assert.NotEqual(t, len(first), len(second))
	})
}
