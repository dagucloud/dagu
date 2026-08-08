// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLLMRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    LLMRole
		wantErr bool
	}{
		{
			name:  "system role",
			input: "system",
			want:  LLMRoleSystem,
		},
		{
			name:  "user role",
			input: "user",
			want:  LLMRoleUser,
		},
		{
			name:  "assistant role",
			input: "assistant",
			want:  LLMRoleAssistant,
		},
		{
			name:  "tool role",
			input: "tool",
			want:  LLMRoleTool,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid role",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseLLMRole(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid role")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseThinkingEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    ThinkingEffort
		wantErr bool
	}{
		{
			name:  "empty string returns empty",
			input: "",
			want:  "",
		},
		{
			name:  "low effort",
			input: "low",
			want:  ThinkingEffortLow,
		},
		{
			name:  "medium effort",
			input: "medium",
			want:  ThinkingEffortMedium,
		},
		{
			name:  "high effort",
			input: "high",
			want:  ThinkingEffortHigh,
		},
		{
			name:  "xhigh effort",
			input: "xhigh",
			want:  ThinkingEffortXHigh,
		},
		{
			name:    "invalid effort",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseThinkingEffort(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid thinking effort")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLLMConfig_StreamEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config *LLMConfig
		want   bool
	}{
		{
			name:   "nil Stream defaults to true",
			config: &LLMConfig{},
			want:   true,
		},
		{
			name:   "explicit true",
			config: &LLMConfig{Stream: new(true)},
			want:   true,
		},
		{
			name:   "explicit false",
			config: &LLMConfig{Stream: new(false)},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.config.StreamEnabled()
			assert.Equal(t, tt.want, got)
		})
	}
}
