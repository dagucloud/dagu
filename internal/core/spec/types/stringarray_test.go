// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/spec/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringOrArray_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantErr      bool
		errContains  string
		wantValues   []string
		checkIsEmpty bool
		wantIsEmpty  bool
		checkNotZero bool
	}{
		{
			name:         "SingleString",
			input:        "step1",
			wantValues:   []string{"step1"},
			checkNotZero: true,
		},
		{
			name:       "ArrayOfStringsInline",
			input:      `["step1", "step2", "step3"]`,
			wantValues: []string{"step1", "step2", "step3"},
		},
		{
			name:       "MultilineArray",
			input:      "- step1\n- step2",
			wantValues: []string{"step1", "step2"},
		},
		{
			name:         "EmptyString",
			input:        `""`,
			wantValues:   []string{""},
			checkIsEmpty: true,
			wantIsEmpty:  false,
			checkNotZero: true,
		},
		{
			name:         "EmptyArray",
			input:        "[]",
			wantValues:   nil,
			checkIsEmpty: true,
			wantIsEmpty:  true,
		},
		{
			name:        "InvalidTypeMap",
			input:       "{key: value}",
			wantErr:     true,
			errContains: "must be string or array",
		},
		{
			name:       "QuotedStringWithSpaces",
			input:      `"step with spaces"`,
			wantValues: []string{"step with spaces"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s types.StringOrArray
			err := yaml.Unmarshal([]byte(tt.input), &s)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.wantValues == nil {
				assert.Empty(t, s.Values())
			} else {
				assert.Equal(t, tt.wantValues, s.Values())
			}
			if tt.checkIsEmpty {
				assert.Equal(t, tt.wantIsEmpty, s.IsEmpty())
			}
			if tt.checkNotZero {
				assert.False(t, s.IsZero())
			}
		})
	}

	t.Run("ZeroValue", func(t *testing.T) {
		t.Parallel()
		var s types.StringOrArray
		assert.True(t, s.IsZero())
		assert.Nil(t, s.Values())
	})
}

func TestStringOrArray_InStruct(t *testing.T) {
	t.Parallel()

	type StepConfig struct {
		Name    string              `yaml:"name"`
		Depends types.StringOrArray `yaml:"depends"`
	}

	tests := []struct {
		name         string
		input        string
		wantValues   []string
		wantIsZero   bool
		checkIsEmpty bool
		wantIsEmpty  bool
	}{
		{
			name: "DependsAsString",
			input: `
name: step2
depends: step1
`,
			wantValues: []string{"step1"},
		},
		{
			name: "DependsAsArray",
			input: `
name: step3
depends:
  - step1
  - step2
`,
			wantValues: []string{"step1", "step2"},
		},
		{
			name:       "DependsNotSet",
			input:      "name: step1",
			wantIsZero: true,
		},
		{
			name: "DependsEmptyArray",
			input: `
name: step2
depends: []
`,
			checkIsEmpty: true,
			wantIsEmpty:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg StepConfig
			err := yaml.Unmarshal([]byte(tt.input), &cfg)
			require.NoError(t, err)
			if tt.wantValues != nil {
				assert.Equal(t, tt.wantValues, cfg.Depends.Values())
			}
			if tt.wantIsZero {
				assert.True(t, cfg.Depends.IsZero())
			}
			if tt.checkIsEmpty {
				assert.False(t, cfg.Depends.IsZero())
				assert.Equal(t, tt.wantIsEmpty, cfg.Depends.IsEmpty())
			}
		})
	}
}

func TestMailToValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		wantValues []string
	}{
		{
			name:       "SingleEmail",
			input:      "user@example.com",
			wantValues: []string{"user@example.com"},
		},
		{
			name:       "MultipleEmails",
			input:      `["user1@example.com", "user2@example.com"]`,
			wantValues: []string{"user1@example.com", "user2@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var m types.MailToValue
			err := yaml.Unmarshal([]byte(tt.input), &m)
			require.NoError(t, err)
			assert.Equal(t, tt.wantValues, m.Values())
		})
	}
}

func TestLabelsValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		wantValues []string
	}{
		{
			name:       "SingleLabel",
			input:      "production",
			wantValues: []string{"production"},
		},
		{
			name:       "MultipleLabels",
			input:      `["production", "critical", "monitored"]`,
			wantValues: []string{"production", "critical", "monitored"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var labels types.LabelsValue
			err := yaml.Unmarshal([]byte(tt.input), &labels)
			require.NoError(t, err)
			assert.Equal(t, tt.wantValues, labels.Values())
		})
	}
}

func TestStringOrArray_AdditionalCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains string
		wantValues  []string
		checkIsZero bool
	}{
		{
			name:       "ArrayWithNumericValues",
			input:      "[1, 2, 3]",
			wantValues: []string{"1", "2", "3"},
		},
		{
			name:       "ArrayWithMixedTypes",
			input:      `["step1", 123, true]`,
			wantValues: []string{"step1", "123", "true"},
		},
		{
			name:        "InvalidTypeNumber",
			input:       "123",
			wantErr:     true,
			errContains: "must be string or array",
		},
		{
			name:        "NullValue",
			input:       "null",
			checkIsZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s types.StringOrArray
			err := yaml.Unmarshal([]byte(tt.input), &s)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.wantValues != nil {
				assert.Equal(t, tt.wantValues, s.Values())
			}
			if tt.checkIsZero {
				assert.True(t, s.IsZero())
			}
		})
	}

	t.Run("ValueReturnsRawString", func(t *testing.T) {
		t.Parallel()
		var s types.StringOrArray
		err := yaml.Unmarshal([]byte("step1"), &s)
		require.NoError(t, err)
		assert.Equal(t, "step1", s.Value())
	})

	t.Run("ValueReturnsRawArray", func(t *testing.T) {
		t.Parallel()
		var s types.StringOrArray
		err := yaml.Unmarshal([]byte(`["step1", "step2"]`), &s)
		require.NoError(t, err)
		val, ok := s.Value().([]any)
		require.True(t, ok)
		assert.Len(t, val, 2)
	})
}
