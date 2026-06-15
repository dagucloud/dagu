// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec/types"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseKeyValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]any
		expected []pair
	}{
		{
			name:     "EmptyMap",
			input:    map[string]any{},
			expected: nil,
		},
		{
			name:     "SingleStringValue",
			input:    map[string]any{"FOO": "bar"},
			expected: []pair{{key: "FOO", val: "bar"}},
		},
		{
			name:     "IntegerValue",
			input:    map[string]any{"COUNT": 42},
			expected: []pair{{key: "COUNT", val: "42"}},
		},
		{
			name:     "BooleanValue",
			input:    map[string]any{"DEBUG": true},
			expected: []pair{{key: "DEBUG", val: "true"}},
		},
		{
			name:     "FloatValue",
			input:    map[string]any{"RATIO": 3.14},
			expected: []pair{{key: "RATIO", val: "3.14"}},
		},
		{
			name:  "MultipleValues",
			input: map[string]any{"A": "1", "B": "2"},
			expected: []pair{
				{key: "A", val: "1"},
				{key: "B", val: "2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var pairs []pair
			err := parseKeyValue(tt.input, &pairs)
			require.NoError(t, err)

			if tt.expected == nil {
				assert.Empty(t, pairs)
				return
			}

			// Since map iteration order is not guaranteed, check by content
			assert.Len(t, pairs, len(tt.expected))
			for _, exp := range tt.expected {
				found := false
				for _, p := range pairs {
					if p.key == exp.key && p.val == exp.val {
						found = true
						break
					}
				}
				assert.True(t, found, "expected pair %v not found", exp)
			}
		})
	}
}

func TestLoadVariables(t *testing.T) {
	t.Parallel()

	t.Run("MapInput", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			input    map[string]any
			expected map[string]string
		}{
			{
				name:     "SingleVariable",
				input:    map[string]any{"FOO": "bar"},
				expected: map[string]string{"FOO": "bar"},
			},
			{
				name:     "MultipleVariables",
				input:    map[string]any{"A": "1", "B": "2"},
				expected: map[string]string{"A": "1", "B": "2"},
			},
			{
				name:     "IntegerValue",
				input:    map[string]any{"PORT": 8080},
				expected: map[string]string{"PORT": "8080"},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := BuildContext{
					ctx:  context.Background(),
					opts: BuildOpts{Flags: BuildFlagNoEval},
				}
				result, err := loadVariables(ctx, tt.input)
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("ArrayInput", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			input    []any
			expected map[string]string
		}{
			{
				name: "ArrayOfMaps",
				input: []any{
					map[string]any{"FOO": "bar"},
					map[string]any{"BAZ": "qux"},
				},
				expected: map[string]string{"FOO": "bar", "BAZ": "qux"},
			},
			{
				name: "ArrayOfStrings",
				input: []any{
					"FOO=bar",
					"BAZ=qux",
				},
				expected: map[string]string{"FOO": "bar", "BAZ": "qux"},
			},
			{
				name: "MixedArrayOfMapsAndStrings",
				input: []any{
					map[string]any{"FOO": "bar"},
					"BAZ=qux",
				},
				expected: map[string]string{"FOO": "bar", "BAZ": "qux"},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := BuildContext{
					ctx:  context.Background(),
					opts: BuildOpts{Flags: BuildFlagNoEval},
				}
				result, err := loadVariables(ctx, tt.input)
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("ErrorCases", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name        string
			input       any
			errContains string
		}{
			{
				name:        "InvalidStringFormat",
				input:       []any{"INVALID_NO_EQUALS"},
				errContains: "env config should be map of strings or array of key=value",
			},
			{
				name:        "InvalidTypeInArray",
				input:       []any{123},
				errContains: "env config should be map of strings or array of key=value",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := BuildContext{
					ctx:  context.Background(),
					opts: BuildOpts{Flags: BuildFlagNoEval},
				}
				_, err := loadVariables(ctx, tt.input)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			})
		}
	})

	t.Run("WithEvaluation", func(t *testing.T) {
		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{},
		}

		input := map[string]any{"GREETING": "hello"}
		result, err := loadVariables(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, "hello", result["GREETING"])
	})

	t.Run("PreservesBacktickSubstitution", func(t *testing.T) {
		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{},
		}

		input := map[string]any{"CMD": "`echo hello`"}
		result, err := loadVariables(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, "`echo hello`", result["CMD"])
	})

	t.Run("NoEvalFlag", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		// With NoEval, command substitution should not be executed
		input := map[string]any{"CMD": "$(echo hello)"}
		result, err := loadVariables(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, "$(echo hello)", result["CMD"])
	})

	t.Run("VariableReference", func(t *testing.T) {
		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{},
		}

		// Test that later variables can reference earlier ones
		input := []any{
			map[string]any{"BASE": "/opt"},
			map[string]any{"PATH_VAR": "${BASE}/bin"},
		}
		result, err := loadVariables(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, "/opt", result["BASE"])
		assert.Equal(t, "/opt/bin", result["PATH_VAR"])
	})

	t.Run("EmptyValue", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		input := map[string]any{"EMPTY": ""}
		result, err := loadVariables(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, "", result["EMPTY"])
	})

	t.Run("ValueWithEqualsSign", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		input := []any{"KEY=value=with=equals"}
		result, err := loadVariables(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, "value=with=equals", result["KEY"])
	})
}

// Helper to create EnvValue from YAML string
func envValueFromYAML(t *testing.T, yamlStr string) types.EnvValue {
	t.Helper()
	var env types.EnvValue
	err := yaml.Unmarshal([]byte(yamlStr), &env)
	require.NoError(t, err)
	return env
}

func TestLoadVariablesFromEnvValue(t *testing.T) {
	t.Parallel()

	t.Run("EmptyEnvValue", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		var env types.EnvValue
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("MapFormat", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		env := envValueFromYAML(t, `
FOO: bar
BAZ: qux
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "bar", result["FOO"])
		assert.Equal(t, "qux", result["BAZ"])
	})

	t.Run("ArrayFormat", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		env := envValueFromYAML(t, `
- FOO: bar
- BAZ: qux
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "bar", result["FOO"])
		assert.Equal(t, "qux", result["BAZ"])
	})

	t.Run("WithEvaluation", func(t *testing.T) {
		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{},
		}

		env := envValueFromYAML(t, `
GREETING: hello
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "hello", result["GREETING"])
	})

	t.Run("PreservesBacktickSubstitution", func(t *testing.T) {
		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{},
		}

		env := envValueFromYAML(t, `
CMD: "`+"`echo hello`"+`"
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "`echo hello`", result["CMD"])
	})

	t.Run("NoEvalFlag", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		env := envValueFromYAML(t, `
CMD: "$(echo hello)"
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "$(echo hello)", result["CMD"])
	})

	t.Run("VariableReference", func(t *testing.T) {
		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{},
		}

		env := envValueFromYAML(t, `
- BASE: /opt
- PATH_VAR: "${BASE}/bin"
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "/opt", result["BASE"])
		assert.Equal(t, "/opt/bin", result["PATH_VAR"])
	})

	t.Run("IntegerValue", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		env := envValueFromYAML(t, `
PORT: 8080
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "8080", result["PORT"])
	})

	t.Run("BooleanValue", func(t *testing.T) {
		t.Parallel()

		ctx := BuildContext{
			ctx:  context.Background(),
			opts: BuildOpts{Flags: BuildFlagNoEval},
		}

		env := envValueFromYAML(t, `
DEBUG: true
`)
		result, err := loadVariablesFromEnvValue(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "true", result["DEBUG"])
	})
}
