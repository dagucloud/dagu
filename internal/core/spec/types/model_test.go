// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelValue_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains string
		wantString  string
		wantIsArray bool
		wantLen     int
	}{
		{
			name:       "SingleModelString",
			input:      "gpt-4o",
			wantString: "gpt-4o",
		},
		{
			name:       "QuotedModelString",
			input:      `"claude-sonnet-4-6"`,
			wantString: "claude-sonnet-4-6",
		},
		{
			name: "SingleEntryArray",
			input: `
- provider: openai
  name: gpt-4o
`,
			wantIsArray: true,
			wantLen:     1,
		},
		{
			name: "MultipleEntriesArray",
			input: `
- provider: openai
  name: gpt-4o
- provider: anthropic
  name: claude-sonnet-4-6
`,
			wantIsArray: true,
			wantLen:     2,
		},
		{
			name: "ArrayWithAllOptionalFields",
			input: `
- provider: openai
  name: gpt-4o
  temperature: 0.7
  max_tokens: 2000
  top_p: 0.9
  base_url: https://api.example.com
  api_key_name: MY_API_KEY
`,
			wantIsArray: true,
			wantLen:     1,
		},
		{
			name:        "EmptyArray",
			input:       "[]",
			wantErr:     true,
			errContains: "at least one entry",
		},
		{
			name:        "InvalidType",
			input:       "123",
			wantErr:     true,
			errContains: "must be string or array",
		},
		{
			name:        "InvalidTypeMap",
			input:       "{provider: openai}",
			wantErr:     true,
			errContains: "must be string or array",
		},
		{
			name: "MissingProvider",
			input: `
- name: gpt-4o
`,
			wantErr:     true,
			errContains: "provider: required",
		},
		{
			name: "MissingName",
			input: `
- provider: openai
`,
			wantErr:     true,
			errContains: "name: required",
		},
		{
			name: "InvalidEntryType",
			input: `
- "just a string"
`,
			wantErr:     true,
			errContains: "expected object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var m types.ModelValue
			err := yaml.Unmarshal([]byte(tt.input), &m)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			assert.False(t, m.IsZero())

			if tt.wantIsArray {
				assert.True(t, m.IsArray())
				assert.Equal(t, tt.wantLen, len(m.Entries()))
				assert.Empty(t, m.String())
			} else {
				assert.False(t, m.IsArray())
				assert.Equal(t, tt.wantString, m.String())
				assert.Nil(t, m.Entries())
			}
		})
	}

	t.Run("ZeroValue", func(t *testing.T) {
		t.Parallel()
		var m types.ModelValue
		assert.True(t, m.IsZero())
		assert.False(t, m.IsArray())
		assert.Empty(t, m.String())
		assert.Nil(t, m.Entries())
	})

	t.Run("NullValue", func(t *testing.T) {
		t.Parallel()
		var m types.ModelValue
		err := yaml.Unmarshal([]byte("null"), &m)
		require.NoError(t, err)
		assert.True(t, m.IsZero())
	})
}

func TestModelValue_ValidationRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains string
	}{
		{
			name: "TemperatureTooLow",
			input: `
- provider: openai
  name: gpt-4o
  temperature: -0.1
`,
			wantErr:     true,
			errContains: "temperature: must be between 0.0 and 2.0",
		},
		{
			name: "TemperatureTooHigh",
			input: `
- provider: openai
  name: gpt-4o
  temperature: 2.5
`,
			wantErr:     true,
			errContains: "temperature: must be between 0.0 and 2.0",
		},
		{
			name: "TemperatureAtBoundaries",
			input: `
- provider: openai
  name: gpt-4o
  temperature: 0.0
- provider: anthropic
  name: claude
  temperature: 2.0
`,
			wantErr: false,
		},
		{
			name: "TopPTooLow",
			input: `
- provider: openai
  name: gpt-4o
  top_p: -0.1
`,
			wantErr:     true,
			errContains: "top_p: must be between 0.0 and 1.0",
		},
		{
			name: "TopPTooHigh",
			input: `
- provider: openai
  name: gpt-4o
  top_p: 1.5
`,
			wantErr:     true,
			errContains: "top_p: must be between 0.0 and 1.0",
		},
		{
			name: "MaxTokensTooLow",
			input: `
- provider: openai
  name: gpt-4o
  max_tokens: 0
`,
			wantErr:     true,
			errContains: "max_tokens: must be at least 1",
		},
		{
			name: "MaxTokensValid",
			input: `
- provider: openai
  name: gpt-4o
  max_tokens: 1
`,
			wantErr: false,
		},
		{
			name: "TemperatureNotANumber",
			input: `
- provider: openai
  name: gpt-4o
  temperature: "hot"
`,
			wantErr:     true,
			errContains: "temperature: must be a number",
		},
		{
			name: "MaxTokensNotAnInteger",
			input: `
- provider: openai
  name: gpt-4o
  max_tokens: "many"
`,
			wantErr:     true,
			errContains: "max_tokens: must be an integer",
		},
		{
			name: "TopPNotANumber",
			input: `
- provider: openai
  name: gpt-4o
  top_p: "high"
`,
			wantErr:     true,
			errContains: "top_p: must be a number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var m types.ModelValue
			err := yaml.Unmarshal([]byte(tt.input), &m)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestModelValue_InStruct(t *testing.T) {
	t.Parallel()

	type LLMConfig struct {
		Provider string           `yaml:"provider"`
		Model    types.ModelValue `yaml:"model"`
	}

	tests := []struct {
		name        string
		input       string
		wantString  string
		wantIsArray bool
		wantIsZero  bool
	}{
		{
			name: "ModelAsString",
			input: `
provider: openai
model: gpt-4o
`,
			wantString: "gpt-4o",
		},
		{
			name: "ModelAsArray",
			input: `
provider: openai
model:
  - provider: openai
    name: gpt-4o
  - provider: anthropic
    name: claude-sonnet-4-6
`,
			wantIsArray: true,
		},
		{
			name:       "ModelNotSet",
			input:      "provider: openai",
			wantIsZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg LLMConfig
			err := yaml.Unmarshal([]byte(tt.input), &cfg)
			require.NoError(t, err)

			if tt.wantIsZero {
				assert.True(t, cfg.Model.IsZero())
				return
			}

			assert.False(t, cfg.Model.IsZero())
			if tt.wantIsArray {
				assert.True(t, cfg.Model.IsArray())
				assert.Len(t, cfg.Model.Entries(), 2)
			} else {
				assert.Equal(t, tt.wantString, cfg.Model.String())
			}
		})
	}
}

func TestModelValue_EntryFields(t *testing.T) {
	t.Parallel()

	input := `
- provider: openai
  name: gpt-4o
  temperature: 0.7
  max_tokens: 2000
  top_p: 0.9
  base_url: https://api.example.com
  api_key_name: MY_API_KEY
`
	var m types.ModelValue
	err := yaml.Unmarshal([]byte(input), &m)
	require.NoError(t, err)
	require.True(t, m.IsArray())
	require.Len(t, m.Entries(), 1)

	entry := m.Entries()[0]
	assert.Equal(t, "openai", entry.Provider)
	assert.Equal(t, "gpt-4o", entry.Name)
	require.NotNil(t, entry.Temperature)
	assert.InDelta(t, 0.7, *entry.Temperature, 0.001)
	require.NotNil(t, entry.MaxTokens)
	assert.Equal(t, 2000, *entry.MaxTokens)
	require.NotNil(t, entry.TopP)
	assert.InDelta(t, 0.9, *entry.TopP, 0.001)
	assert.Equal(t, "https://api.example.com", entry.BaseURL)
	assert.Equal(t, "MY_API_KEY", entry.APIKeyName)
}

func TestModelValue_Value(t *testing.T) {
	t.Parallel()

	t.Run("StringValue", func(t *testing.T) {
		t.Parallel()
		var m types.ModelValue
		err := yaml.Unmarshal([]byte("gpt-4o"), &m)
		require.NoError(t, err)
		assert.Equal(t, "gpt-4o", m.Value())
	})

	t.Run("ArrayValue", func(t *testing.T) {
		t.Parallel()
		var m types.ModelValue
		err := yaml.Unmarshal([]byte("- provider: openai\n  name: gpt-4o"), &m)
		require.NoError(t, err)
		val, ok := m.Value().([]any)
		require.True(t, ok)
		assert.Len(t, val, 1)
	})
}
