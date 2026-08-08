// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogOutputValue_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMode    ir.LogOutputMode
		wantSet     bool
		wantErr     bool
		errContains string
	}{
		{
			name:     "separate mode",
			input:    "log_output: separate",
			wantMode: ir.LogOutputSeparate,
			wantSet:  true,
		},
		{
			name:     "merged mode",
			input:    "log_output: merged",
			wantMode: ir.LogOutputMerged,
			wantSet:  true,
		},
		{
			name:     "merged mode uppercase",
			input:    "log_output: MERGED",
			wantMode: ir.LogOutputMerged,
			wantSet:  true,
		},
		{
			name:     "separate mode mixed case",
			input:    "log_output: Separate",
			wantMode: ir.LogOutputSeparate,
			wantSet:  true,
		},
		{
			name:     "empty string defaults to separate",
			input:    "log_output: ''",
			wantMode: ir.LogOutputSeparate,
			wantSet:  true,
		},
		{
			name:        "invalid value",
			input:       "log_output: invalid",
			wantErr:     true,
			errContains: "invalid log_output value",
		},
		{
			name:        "invalid value - both",
			input:       "log_output: both",
			wantErr:     true,
			errContains: "invalid log_output value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result struct {
				LogOutput LogOutputValue `yaml:"log_output"`
			}

			err := yaml.Unmarshal([]byte(tt.input), &result)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMode, result.LogOutput.Mode())
			assert.Equal(t, tt.wantSet, !result.LogOutput.IsZero())
		})
	}
}

func TestLogOutputValue_UnmarshalYAML_InvalidType(t *testing.T) {
	var result struct {
		LogOutput LogOutputValue `yaml:"log_output"`
	}

	err := yaml.Unmarshal([]byte("log_output:\n  - item1\n  - item2"), &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a string")
}

func TestLogOutputValue_DefaultValue(t *testing.T) {
	var result struct {
		LogOutput LogOutputValue `yaml:"log_output"`
	}

	// When not set in YAML
	err := yaml.Unmarshal([]byte("other: value"), &result)
	require.NoError(t, err)

	// Should be zero
	assert.True(t, result.LogOutput.IsZero())
	// Default mode should be separate
	assert.Equal(t, ir.LogOutputSeparate, result.LogOutput.Mode())
	assert.True(t, result.LogOutput.IsSeparate())
	assert.False(t, result.LogOutput.IsMerged())
}

func TestLogOutputValue_Methods(t *testing.T) {
	t.Run("IsMerged", func(t *testing.T) {
		merged := LogOutputValue{mode: ir.LogOutputMerged, set: true}
		assert.True(t, merged.IsMerged())
		assert.False(t, merged.IsSeparate())

		separate := LogOutputValue{mode: ir.LogOutputSeparate, set: true}
		assert.False(t, separate.IsMerged())
		assert.True(t, separate.IsSeparate())
	})

	t.Run("String", func(t *testing.T) {
		merged := LogOutputValue{mode: ir.LogOutputMerged, set: true}
		assert.Equal(t, "merged", merged.String())

		separate := LogOutputValue{mode: ir.LogOutputSeparate, set: true}
		assert.Equal(t, "separate", separate.String())

		unset := LogOutputValue{}
		assert.Equal(t, "separate", unset.String()) // default
	})
}

func TestLogOutputMode_Constants(t *testing.T) {
	// Ensure constants have expected values
	assert.Equal(t, ir.LogOutputMode("separate"), ir.LogOutputSeparate)
	assert.Equal(t, ir.LogOutputMode("merged"), ir.LogOutputMerged)
}

func TestLogOutputValue_UnmarshalYAML_NilValue(t *testing.T) {
	var result struct {
		LogOutput LogOutputValue `yaml:"log_output"`
	}

	// Explicit null value in YAML
	err := yaml.Unmarshal([]byte("log_output: null"), &result)
	require.NoError(t, err)
	assert.True(t, result.LogOutput.IsZero())
	assert.Equal(t, ir.LogOutputSeparate, result.LogOutput.Mode())
}

func TestLogOutputValue_UnmarshalYAML_MapValue(t *testing.T) {
	var result struct {
		LogOutput LogOutputValue `yaml:"log_output"`
	}

	// Map value should fail with "must be a string" error
	err := yaml.Unmarshal([]byte("log_output:\n  key: value"), &result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a string")
}
