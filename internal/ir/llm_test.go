// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeduplicateSystemMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []LLMMessage
		want     []LLMMessage
	}{
		{name: "empty", messages: []LLMMessage{}, want: nil},
		{name: "nil", messages: nil, want: nil},
		{
			name: "no system messages",
			messages: []LLMMessage{
				{Role: LLMRoleUser, Content: "hello"},
				{Role: LLMRoleAssistant, Content: "hi"},
			},
			want: []LLMMessage{
				{Role: LLMRoleUser, Content: "hello"},
				{Role: LLMRoleAssistant, Content: "hi"},
			},
		},
		{
			name: "single system message",
			messages: []LLMMessage{
				{Role: LLMRoleSystem, Content: "be helpful"},
				{Role: LLMRoleUser, Content: "hello"},
			},
			want: []LLMMessage{
				{Role: LLMRoleSystem, Content: "be helpful"},
				{Role: LLMRoleUser, Content: "hello"},
			},
		},
		{
			name: "multiple system messages",
			messages: []LLMMessage{
				{Role: LLMRoleSystem, Content: "first"},
				{Role: LLMRoleUser, Content: "hello"},
				{Role: LLMRoleSystem, Content: "second"},
				{Role: LLMRoleAssistant, Content: "hi"},
				{Role: LLMRoleSystem, Content: "third"},
			},
			want: []LLMMessage{
				{Role: LLMRoleSystem, Content: "first"},
				{Role: LLMRoleUser, Content: "hello"},
				{Role: LLMRoleAssistant, Content: "hi"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DeduplicateSystemMessages(tt.messages))
		})
	}
}

func TestLLMMessageMetadataCostJSONRoundTrip(t *testing.T) {
	metadata := LLMMessageMetadata{
		Provider:         "openai",
		Model:            "gpt-4",
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
		Cost:             0.0042,
	}

	data, err := json.Marshal(metadata)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"cost":`)

	var decoded LLMMessageMetadata
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, metadata, decoded)

	data, err = json.Marshal(LLMMessageMetadata{Provider: "openai", Model: "gpt-4"})
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"cost"`)
}

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
