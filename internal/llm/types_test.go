// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected Role
	}{
		{"system", RoleSystem},
		{"sys", RoleSystem},
		{"user", RoleUser},
		{"human", RoleUser},
		{"assistant", RoleAssistant},
		{"ai", RoleAssistant},
		{"bot", RoleAssistant},
		{"tool", RoleTool},
		{"function", RoleTool},
		{"custom", Role("custom")},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, ParseRole(tc.input))
		})
	}
}

func TestParseThinkingEffort(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			effort, err := ParseThinkingEffort(input)
			require.NoError(t, err)
			assert.Equal(t, ThinkingEffort(input), effort)
		})
	}

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		_, err := ParseThinkingEffort("extreme")
		require.ErrorContains(t, err, "invalid thinking effort")
	})
}
