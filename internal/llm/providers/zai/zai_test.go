// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package zai

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Z.AI thinking is a toggle: effort none disables it and any other level
// enables it.
func TestBuildRequestBody_Thinking(t *testing.T) {
	t.Parallel()

	provider := &Provider{config: llm.Config{APIKey: "test-key"}}
	tests := []struct {
		name     string
		thinking *llm.ThinkingRequest
		expected any
	}{
		{name: "NoThinking"},
		{name: "Disabled", thinking: &llm.ThinkingRequest{Effort: llm.ThinkingEffortHigh}},
		{name: "Unset", thinking: &llm.ThinkingRequest{Enabled: true}, expected: map[string]any{"type": "enabled"}},
		{name: "Minimal", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMinimal}, expected: map[string]any{"type": "enabled"}},
		{name: "Max", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortMax}, expected: map[string]any{"type": "enabled"}},
		{name: "None", thinking: &llm.ThinkingRequest{Enabled: true, Effort: llm.ThinkingEffortNone}, expected: map[string]any{"type": "disabled"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &llm.ChatRequest{
				Model:    "glm-5",
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hello"}},
				Thinking: tt.thinking,
			}
			body, err := provider.buildRequestBody(req, false)
			require.NoError(t, err)

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(body, &parsed))
			assert.Equal(t, tt.expected, parsed["thinking"])
		})
	}
}
