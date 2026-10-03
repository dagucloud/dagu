// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package claudemodel_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/llm/claudemodel"
	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		expected claudemodel.Model
		ok       bool
	}{
		{id: "claude-opus-4-8", expected: claudemodel.Model{Family: "opus", Major: 4, Minor: 8}, ok: true},
		{id: "anthropic.claude-sonnet-5", expected: claudemodel.Model{Family: "sonnet", Major: 5}, ok: true},
		{id: "anthropic/claude-sonnet-5.5", expected: claudemodel.Model{Family: "sonnet", Major: 5, Minor: 5}, ok: true},
		{id: "claude-opus-4-5@20251101", expected: claudemodel.Model{Family: "opus", Major: 4, Minor: 5}, ok: true},
		{id: "claude-opus-4-20250514", expected: claudemodel.Model{Family: "opus", Major: 4}, ok: true},
		{id: "claude-fable-5-1", expected: claudemodel.Model{Family: "fable", Major: 5, Minor: 1}, ok: true},
		{id: "claude-3-5-sonnet-20241022", expected: claudemodel.Model{Major: 3}, ok: true},
		{id: "anthropic/claude-3.7-sonnet", expected: claudemodel.Model{Major: 3}, ok: true},
		{id: "gpt-5", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			model, ok := claudemodel.Parse(tt.id)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.expected, model)
		})
	}
}

func TestForcedToolChoiceSupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id       string
		expected bool
	}{
		{id: "claude-sonnet-4-6", expected: true},
		{id: "anthropic/claude-sonnet-4.6", expected: true},
		{id: "claude-haiku-4-5", expected: true},
		{id: "claude-3-5-sonnet-20241022", expected: true},
		{id: "claude-opus-5", expected: false},
		{id: "anthropic/claude-sonnet-5.5", expected: false},
		{id: "claude-fable-5-1", expected: false},
		{id: "claude-mythos-5-1", expected: false},
		{id: "my-deployment", expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, claudemodel.ForcedToolChoiceSupported(tt.id))
		})
	}
}
