// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeduplicateSystemMessages(t *testing.T) {
	tests := []struct {
		name string
		msgs []LLMMessage
		want []LLMMessage
	}{
		{
			name: "empty",
			msgs: []LLMMessage{},
			want: nil,
		},
		{
			name: "nil",
			msgs: nil,
			want: nil,
		},
		{
			name: "no system messages",
			msgs: []LLMMessage{
				{Role: RoleUser, Content: "hello"},
				{Role: RoleAssistant, Content: "hi"},
			},
			want: []LLMMessage{
				{Role: RoleUser, Content: "hello"},
				{Role: RoleAssistant, Content: "hi"},
			},
		},
		{
			name: "single system message",
			msgs: []LLMMessage{
				{Role: RoleSystem, Content: "be helpful"},
				{Role: RoleUser, Content: "hello"},
			},
			want: []LLMMessage{
				{Role: RoleSystem, Content: "be helpful"},
				{Role: RoleUser, Content: "hello"},
			},
		},
		{
			name: "multiple system messages - keep first",
			msgs: []LLMMessage{
				{Role: RoleSystem, Content: "first"},
				{Role: RoleUser, Content: "hello"},
				{Role: RoleSystem, Content: "second"},
				{Role: RoleAssistant, Content: "hi"},
				{Role: RoleSystem, Content: "third"},
			},
			want: []LLMMessage{
				{Role: RoleSystem, Content: "first"},
				{Role: RoleUser, Content: "hello"},
				{Role: RoleAssistant, Content: "hi"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeduplicateSystemMessages(tt.msgs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLLMMessageMetadata_CostJSONRoundTrip(t *testing.T) {
	t.Run("CostSerializesWithKey", func(t *testing.T) {
		meta := LLMMessageMetadata{
			Provider:         "openai",
			Model:            "gpt-4",
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
			Cost:             0.0042,
		}

		data, err := json.Marshal(meta)
		require.NoError(t, err)

		// Verify "cost" key is present in JSON
		assert.Contains(t, string(data), `"cost":`)

		var decoded LLMMessageMetadata
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, meta, decoded)
	})

	t.Run("ZeroCostOmittedFromJSON", func(t *testing.T) {
		meta := LLMMessageMetadata{
			Provider: "openai",
			Model:    "gpt-4",
		}

		data, err := json.Marshal(meta)
		require.NoError(t, err)

		// "cost" should be omitted when zero (omitempty)
		assert.NotContains(t, string(data), `"cost"`)

		var decoded LLMMessageMetadata
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)
		assert.InDelta(t, 0.0, decoded.Cost, 1e-9)
	})
}
