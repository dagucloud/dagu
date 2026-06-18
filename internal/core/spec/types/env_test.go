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

func TestEnvValue_UnmarshalYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		wantErr        bool
		errContains    string
		wantEntryCount int
		wantEntries    map[string]string
		checkIsZero    bool
		checkNotZero   bool
		checkEmpty     bool
	}{
		{
			name: "MapForm",
			input: `
KEY1: value1
KEY2: value2
`,
			wantEntryCount: 2,
			wantEntries:    map[string]string{"KEY1": "value1", "KEY2": "value2"},
		},
		{
			name: "ArrayOfMapsPreservesOrder",
			input: `
- KEY1: value1
- KEY2: value2
- KEY3: value3
`,
			wantEntryCount: 3,
			wantEntries:    map[string]string{"KEY1": "value1", "KEY2": "value2", "KEY3": "value3"},
		},
		{
			name: "ArrayOfStrings",
			input: `
- KEY1=value1
- KEY2=value2
`,
			wantEntryCount: 2,
			wantEntries:    map[string]string{"KEY1": "value1"},
		},
		{
			name: "MixedArrayMapsAndStrings",
			input: `
- KEY1: value1
- KEY2=value2
- KEY3: value3
`,
			wantEntryCount: 3,
		},
		{
			name: "NumericValuesStringified",
			input: `
PORT: 8080
ENABLED: true
RATIO: 0.5
`,
			wantEntryCount: 3,
			wantEntries:    map[string]string{"PORT": "8080", "ENABLED": "true", "RATIO": "0.5"},
		},
		{
			name: "ValueWithEqualsSign",
			input: `
- CONNECTION_STRING=host=localhost;port=5432
`,
			wantEntryCount: 1,
			wantEntries:    map[string]string{"CONNECTION_STRING": "host=localhost;port=5432"},
		},
		{
			name:        "InvalidStringFormatNoEquals",
			input:       `["invalid_no_equals"]`,
			wantErr:     true,
			errContains: "expected KEY=value",
		},
		{
			name:        "InvalidTypeScalarString",
			input:       `"just a string"`,
			wantErr:     true,
			errContains: "must be map or array",
		},
		{
			name:         "EmptyMap",
			input:        "{}",
			checkNotZero: true,
			checkEmpty:   true,
		},
		{
			name:         "EmptyArray",
			input:        "[]",
			checkNotZero: true,
			checkEmpty:   true,
		},
		{
			name: "EnvironmentVariableReference",
			input: `
- PATH: ${HOME}/bin
- DERIVED: ${OTHER_VAR}
`,
			wantEntryCount: 2,
			wantEntries:    map[string]string{"PATH": "${HOME}/bin", "DERIVED": "${OTHER_VAR}"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var e types.EnvValue
			err := yaml.Unmarshal([]byte(tt.input), &e)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.checkIsZero {
				assert.True(t, e.IsZero())
				return
			}
			if tt.checkNotZero {
				assert.False(t, e.IsZero())
			}
			if tt.checkEmpty {
				assert.Empty(t, e.Entries())
				return
			}
			if tt.wantEntryCount > 0 {
				assert.Len(t, e.Entries(), tt.wantEntryCount)
			}
			if tt.wantEntries != nil {
				entries := e.Entries()
				keys := make(map[string]string)
				for _, entry := range entries {
					keys[entry.Key] = entry.Value
				}
				for k, v := range tt.wantEntries {
					assert.Equal(t, v, keys[k], "key %s should have value %s", k, v)
				}
			}
		})
	}

	t.Run("ZeroValue", func(t *testing.T) {
		t.Parallel()
		var e types.EnvValue
		assert.True(t, e.IsZero())
		assert.Nil(t, e.Entries())
	})
}

func TestEnvValue_MapFormPreservesSourceOrder(t *testing.T) {
	t.Parallel()

	var env types.EnvValue
	err := yaml.Unmarshal([]byte(`
SERVICE: api
HOST: ${env.SERVICE}.internal
THIRD: ${env.HOST}/v1
`), &env)
	require.NoError(t, err)

	require.Equal(t, []types.EnvEntry{
		{Key: "SERVICE", Value: "api"},
		{Key: "HOST", Value: "${env.SERVICE}.internal"},
		{Key: "THIRD", Value: "${env.HOST}/v1"},
	}, env.Entries())
}

func TestEnvValue_InStruct(t *testing.T) {
	t.Parallel()

	type StepConfig struct {
		Name string         `yaml:"name"`
		Env  types.EnvValue `yaml:"env"`
	}

	tests := []struct {
		name           string
		input          string
		wantName       string
		wantEntryCount int
		wantIsZero     bool
		checkNotZero   bool
	}{
		{
			name: "EnvSetAsMap",
			input: `
name: my-step
env:
  DEBUG: "true"
  LOG_LEVEL: info
`,
			wantName:       "my-step",
			wantEntryCount: 2,
			checkNotZero:   true,
		},
		{
			name: "EnvSetAsArray",
			input: `
name: my-step
env:
  - DEBUG: "true"
  - LOG_LEVEL: info
`,
			wantEntryCount: 2,
		},
		{
			name:       "EnvNotSet",
			input:      "name: my-step",
			wantIsZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cfg StepConfig
			err := yaml.Unmarshal([]byte(tt.input), &cfg)
			require.NoError(t, err)
			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, cfg.Name)
			}
			if tt.wantEntryCount > 0 {
				assert.Len(t, cfg.Env.Entries(), tt.wantEntryCount)
			}
			if tt.wantIsZero {
				assert.True(t, cfg.Env.IsZero())
			}
			if tt.checkNotZero {
				assert.False(t, cfg.Env.IsZero())
			}
		})
	}

	t.Run("EnvSetAsArrayPreservesOrder", func(t *testing.T) {
		t.Parallel()
		var cfg StepConfig
		err := yaml.Unmarshal([]byte(`
name: my-step
env:
  - DEBUG: "true"
  - LOG_LEVEL: info
`), &cfg)
		require.NoError(t, err)
		entries := cfg.Env.Entries()
		require.Len(t, entries, 2)
		assert.Equal(t, "DEBUG", entries[0].Key)
		assert.Equal(t, "LOG_LEVEL", entries[1].Key)
	})
}

func TestEnvValue_AdditionalCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantErr     bool
		errContains string
		checkIsZero bool
	}{
		{
			name:        "InvalidTypeInArrayNumber",
			input:       "[123]",
			wantErr:     true,
			errContains: "expected map or string",
		},
		{
			name:        "InvalidTypeInArrayBoolean",
			input:       "[true]",
			wantErr:     true,
			errContains: "expected map or string",
		},
		{
			name:        "InvalidTypeNumber",
			input:       "123",
			wantErr:     true,
			errContains: "must be map or array",
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
			var e types.EnvValue
			err := yaml.Unmarshal([]byte(tt.input), &e)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if tt.checkIsZero {
				assert.True(t, e.IsZero())
			}
		})
	}

	t.Run("ValueReturnsRawMap", func(t *testing.T) {
		t.Parallel()
		var e types.EnvValue
		err := yaml.Unmarshal([]byte("KEY: value"), &e)
		require.NoError(t, err)
		val, ok := e.Value().(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "value", val["KEY"])
	})

	t.Run("ValueReturnsRawArray", func(t *testing.T) {
		t.Parallel()
		var e types.EnvValue
		err := yaml.Unmarshal([]byte("[KEY=value]"), &e)
		require.NoError(t, err)
		val, ok := e.Value().([]any)
		require.True(t, ok)
		assert.Len(t, val, 1)
	})
}

func TestEnvValue_Prepend(t *testing.T) {
	t.Parallel()

	parseEnv := func(t *testing.T, input string) types.EnvValue {
		t.Helper()
		var e types.EnvValue
		require.NoError(t, yaml.Unmarshal([]byte(input), &e))
		return e
	}

	t.Run("PrependEntries", func(t *testing.T) {
		t.Parallel()
		base := parseEnv(t, "- STEP_KEY=step_val")
		other := parseEnv(t, "- DEFAULT_KEY=default_val")

		result := base.Prepend(other)
		entries := result.Entries()
		require.Len(t, entries, 2)
		require.Equal(t, "DEFAULT_KEY", entries[0].Key)
		require.Equal(t, "default_val", entries[0].Value)
		require.Equal(t, "STEP_KEY", entries[1].Key)
		require.Equal(t, "step_val", entries[1].Value)
		require.False(t, result.IsZero())
	})

	t.Run("PrependZeroValue_NoChange", func(t *testing.T) {
		t.Parallel()
		base := parseEnv(t, "- KEY=val")
		var zero types.EnvValue // IsZero() == true

		result := base.Prepend(zero)
		entries := result.Entries()
		require.Len(t, entries, 1)
		require.Equal(t, "KEY", entries[0].Key)
	})

	t.Run("PrependToZeroValue", func(t *testing.T) {
		t.Parallel()
		var base types.EnvValue
		other := parseEnv(t, "- KEY=val")

		result := base.Prepend(other)
		entries := result.Entries()
		require.Len(t, entries, 1)
		require.Equal(t, "KEY", entries[0].Key)
		require.False(t, result.IsZero())
	})

	t.Run("PrependMultipleEntries", func(t *testing.T) {
		t.Parallel()
		base := parseEnv(t, "- C=3")
		other := parseEnv(t, "- A=1\n- B=2")

		result := base.Prepend(other)
		entries := result.Entries()
		require.Len(t, entries, 3)
		require.Equal(t, "A", entries[0].Key)
		require.Equal(t, "B", entries[1].Key)
		require.Equal(t, "C", entries[2].Key)
	})
}
