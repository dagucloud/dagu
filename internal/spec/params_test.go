// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParamPairString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pair     paramPair
		expected string
	}{
		{
			name:     "NamedParam",
			pair:     paramPair{Name: "foo", Value: "bar"},
			expected: "foo=bar",
		},
		{
			name:     "PositionalParam",
			pair:     paramPair{Name: "", Value: "value"},
			expected: "value",
		},
		{
			name:     "EmptyValue",
			pair:     paramPair{Name: "key", Value: ""},
			expected: "key=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.pair.String())
		})
	}
}

func TestParamPairEscaped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pair     paramPair
		expected string
	}{
		{
			name:     "NamedParam",
			pair:     paramPair{Name: "foo", Value: "bar"},
			expected: `foo="bar"`,
		},
		{
			name:     "PositionalParam",
			pair:     paramPair{Name: "", Value: "value"},
			expected: `"value"`,
		},
		{
			name:     "ValueWithSpaces",
			pair:     paramPair{Name: "msg", Value: "hello world"},
			expected: `msg="hello world"`,
		},
		{
			name:     "ValueWithQuotes",
			pair:     paramPair{Name: "json", Value: `{"key":"value"}`},
			expected: `json="{\"key\":\"value\"}"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.pair.Escaped())
		})
	}
}

func TestParamPairSmartEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pair     paramPair
		expected string
	}{
		{
			name:     "NamedSimpleValue",
			pair:     paramPair{Name: "foo", Value: "bar"},
			expected: `foo=bar`,
		},
		{
			name:     "NamedVariableRef",
			pair:     paramPair{Name: "NAME", Value: "${ITEM.name}"},
			expected: `NAME=${ITEM.name}`,
		},
		{
			name:     "PositionalVariableRef",
			pair:     paramPair{Name: "", Value: "${ITEM.extra}"},
			expected: `${ITEM.extra}`,
		},
		{
			name:     "NamedValueWithSpaces",
			pair:     paramPair{Name: "msg", Value: "hello world"},
			expected: `msg="hello world"`,
		},
		{
			name:     "PositionalValueWithSpaces",
			pair:     paramPair{Name: "", Value: "hello world"},
			expected: `"hello world"`,
		},
		{
			name:     "NamedEmptyValue",
			pair:     paramPair{Name: "key", Value: ""},
			expected: `key=""`,
		},
		{
			name:     "PositionalEmptyValue",
			pair:     paramPair{Name: "", Value: ""},
			expected: `""`,
		},
		{
			name:     "NamedValueWithQuotes",
			pair:     paramPair{Name: "json", Value: `{"key":"value"}`},
			expected: `json="{\"key\":\"value\"}"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.pair.SmartEscape())
		})
	}
}

func TestParseStringParams(t *testing.T) {
	t.Parallel()

	ctx := buildContext{
		ctx:  context.Background(),
		opts: buildOpts{Flags: buildFlagNoEval},
	}

	tests := []struct {
		name     string
		input    string
		expected []paramPair
	}{
		{
			name:     "SinglePositionalParam",
			input:    "value",
			expected: []paramPair{{Name: "", Value: "value"}},
		},
		{
			name:     "SingleNamedParam",
			input:    "key=value",
			expected: []paramPair{{Name: "key", Value: "value"}},
		},
		{
			name:  "MultipleNamedParams",
			input: "foo=bar baz=qux",
			expected: []paramPair{
				{Name: "foo", Value: "bar"},
				{Name: "baz", Value: "qux"},
			},
		},
		{
			name:  "MixedParams",
			input: "positional key=value",
			expected: []paramPair{
				{Name: "", Value: "positional"},
				{Name: "key", Value: "value"},
			},
		},
		{
			name:     "QuotedValue",
			input:    `msg="hello world"`,
			expected: []paramPair{{Name: "msg", Value: "hello world"}},
		},
		{
			name:     "QuotedValueWithEscapedQuotes",
			input:    `msg="say \"hello\""`,
			expected: []paramPair{{Name: "msg", Value: `say "hello"`}},
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: nil,
		},
		{
			name:  "MultiplePositionalParams",
			input: "one two three",
			expected: []paramPair{
				{Name: "", Value: "one"},
				{Name: "", Value: "two"},
				{Name: "", Value: "three"},
			},
		},
		{
			name:     "MultilineEscapeSequence",
			input:    `msg="line1\nline2"`,
			expected: []paramPair{{Name: "msg", Value: "line1\nline2"}},
		},
		{
			name:     "EscapedBackslash",
			input:    `path="C:\\Users"`,
			expected: []paramPair{{Name: "path", Value: `C:\Users`}},
		},
		{
			name:     "LiteralBackslashN",
			input:    `val="a\\nb"`,
			expected: []paramPair{{Name: "val", Value: `a\nb`}},
		},
		{
			name:     "TabEscape",
			input:    `data="col1\tcol2"`,
			expected: []paramPair{{Name: "data", Value: "col1\tcol2"}},
		},
		{
			name:     "MixedEscapes",
			input:    `msg="line1\nline2\\end"`,
			expected: []paramPair{{Name: "msg", Value: "line1\nline2\\end"}},
		},
		{
			name:     "BacktickValue",
			input:    "cmd=`echo hello`",
			expected: []paramPair{{Name: "cmd", Value: "`echo hello`"}},
		},
		{
			name:     "BacktickValueWithDoubleQuotes",
			input:    `cmd=` + "`echo \"hello world\"`",
			expected: []paramPair{{Name: "cmd", Value: "`echo \"hello world\"`"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := parseStringParams(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseStringParams_NoEval_Matrix(t *testing.T) {
	t.Parallel()

	ctx := buildContext{
		ctx:  context.Background(),
		opts: buildOpts{Flags: buildFlagNoEval},
	}

	tests := []struct {
		name     string
		input    string
		expected []paramPair
	}{
		{
			name:  "NamedBacktickWithInnerDoubleQuotes",
			input: `cmd=` + "`echo \"hello world\"`",
			expected: []paramPair{
				{Name: "cmd", Value: "`echo \"hello world\"`"},
			},
		},
		{
			name:  "PositionalBacktickToken",
			input: "`echo \"hello\"`",
			expected: []paramPair{
				{Name: "", Value: "`echo \"hello\"`"},
			},
		},
		{
			name:  "MixedNamedBacktickQuotedAndPositional",
			input: `A=1 cmd=` + "`echo \"x\"`" + ` B="y z" bare`,
			expected: []paramPair{
				{Name: "A", Value: "1"},
				{Name: "cmd", Value: "`echo \"x\"`"},
				{Name: "B", Value: "y z"},
				{Name: "", Value: "bare"},
			},
		},
		{
			name:  "EscapedQuotesInDoubleQuotedToken",
			input: `X="a \"b\"" Y=2`,
			expected: []paramPair{
				{Name: "X", Value: `a "b"`},
				{Name: "Y", Value: "2"},
			},
		},
		{
			name:     "EmptyInput",
			input:    "",
			expected: nil,
		},
		{
			name:     "WhitespaceInput",
			input:    "   ",
			expected: nil,
		},
		{
			name:  "UnterminatedDoubleQuoteFallback",
			input: `A="unterminated B=2`,
			expected: []paramPair{
				{Name: "", Value: "A="},
				{Name: "", Value: "unterminated"},
				{Name: "B", Value: "2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStringParams(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestParseStringParamsWithJSON(t *testing.T) {
	t.Parallel()

	ctx := buildContext{
		ctx:  context.Background(),
		opts: buildOpts{Flags: buildFlagNoEval},
	}

	tests := []struct {
		name     string
		input    string
		expected []paramPair
	}{
		{
			name:  "JSONObject",
			input: `{"key1": "test1", "key2": "test2"}`,
			expected: []paramPair{
				{Name: "key1", Value: "test1"},
				{Name: "key2", Value: "test2"},
			},
		},
		{
			name:  "JSONArray",
			input: `["val1", "val2", "val3"]`,
			expected: []paramPair{
				{Name: "", Value: "val1"},
				{Name: "", Value: "val2"},
				{Name: "", Value: "val3"},
			},
		},
		{
			name:  "JSONArrayOfNamedObjects",
			input: `[{"region":"us-west-2"},{"count":"5"}]`,
			expected: []paramPair{
				{Name: "region", Value: "us-west-2"},
				{Name: "count", Value: "5"},
			},
		},
		{
			name:  "JSONWithNonStringValues",
			input: `{"count": 42, "enabled": true}`,
			expected: []paramPair{
				{Name: "count", Value: "42"},
				{Name: "enabled", Value: "true"},
			},
		},
		{
			name:     "InvalidJSONFallsBackToRegex",
			input:    `{invalid json`,
			expected: []paramPair{{Name: "", Value: "{invalid"}, {Name: "", Value: "json"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := parseStringParams(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseStringParamsBackticksLiteral(t *testing.T) {
	t.Run("BacktickValuesPreservedLiterally", func(t *testing.T) {
		ctx := buildContext{
			ctx:  context.Background(),
			opts: buildOpts{},
		}

		result, err := parseStringParams(ctx, "val=`echo hello`")
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "val", result[0].Name)
		assert.Equal(t, "`echo hello`", result[0].Value)
	})
}

func TestParseListParams(t *testing.T) {
	t.Parallel()

	ctx := buildContext{
		ctx:  context.Background(),
		opts: buildOpts{Flags: buildFlagNoEval},
	}

	tests := []struct {
		name     string
		input    []string
		expected []paramPair
	}{
		{
			name:     "EmptyList",
			input:    []string{},
			expected: nil,
		},
		{
			name:     "SingleItem",
			input:    []string{"foo=bar"},
			expected: []paramPair{{Name: "foo", Value: "bar"}},
		},
		{
			name:  "MultipleItems",
			input: []string{"foo=bar", "baz=qux"},
			expected: []paramPair{
				{Name: "foo", Value: "bar"},
				{Name: "baz", Value: "qux"},
			},
		},
		{
			name:  "ItemsWithMultipleParams",
			input: []string{"a=1 b=2", "c=3"},
			expected: []paramPair{
				{Name: "a", Value: "1"},
				{Name: "b", Value: "2"},
				{Name: "c", Value: "3"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := parseListParams(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseMapParams(t *testing.T) {
	t.Parallel()

	ctx := buildContext{
		ctx:  context.Background(),
		opts: buildOpts{Flags: buildFlagNoEval},
	}

	t.Run("EmptySlice", func(t *testing.T) {
		t.Parallel()
		result, err := parseMapParams(ctx, []any{})
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("SingleMap", func(t *testing.T) {
		t.Parallel()
		input := []any{
			map[string]any{"foo": "bar"},
		}
		result, err := parseMapParams(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, []paramPair{{Name: "foo", Value: "bar"}}, result)
	})

	t.Run("MapWithMultipleKeys", func(t *testing.T) {
		t.Parallel()
		input := []any{
			map[string]any{"a": "1", "b": "2"},
		}
		result, err := parseMapParams(ctx, input)
		require.NoError(t, err)
		// Keys are sorted alphabetically
		assert.Equal(t, []paramPair{
			{Name: "a", Value: "1"},
			{Name: "b", Value: "2"},
		}, result)
	})

	t.Run("MultipleMaps", func(t *testing.T) {
		t.Parallel()
		input := []any{
			map[string]any{"foo": "bar"},
			map[string]any{"baz": "qux"},
		}
		result, err := parseMapParams(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
			{Name: "baz", Value: "qux"},
		}, result)
	})

	t.Run("MixedMapAndString", func(t *testing.T) {
		t.Parallel()
		input := []any{
			map[string]any{"foo": "bar"},
			"baz=qux",
		}
		result, err := parseMapParams(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
			{Name: "baz", Value: "qux"},
		}, result)
	})

	t.Run("IntegerValue", func(t *testing.T) {
		t.Parallel()
		input := []any{
			map[string]any{"count": 42},
		}
		result, err := parseMapParams(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, []paramPair{{Name: "count", Value: "42"}}, result)
	})

	t.Run("BooleanValue", func(t *testing.T) {
		t.Parallel()
		input := []any{
			map[string]any{"debug": true},
		}
		result, err := parseMapParams(ctx, input)
		require.NoError(t, err)
		assert.Equal(t, []paramPair{{Name: "debug", Value: "true"}}, result)
	})

	t.Run("InvalidType", func(t *testing.T) {
		t.Parallel()
		input := []any{123}
		_, err := parseMapParams(ctx, input)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameter value")
	})
}

func TestParseParamValue(t *testing.T) {
	t.Parallel()

	ctx := buildContext{
		ctx:  context.Background(),
		opts: buildOpts{Flags: buildFlagNoEval},
	}

	t.Run("Nil", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, nil)
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, "foo=bar baz=qux")
		require.NoError(t, err)
		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
			{Name: "baz", Value: "qux"},
		}, result)
	})

	t.Run("StringSlice", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, []string{"foo=bar", "baz=qux"})
		require.NoError(t, err)
		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
			{Name: "baz", Value: "qux"},
		}, result)
	})

	t.Run("AnySlice", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, []any{
			map[string]any{"foo": "bar"},
		})
		require.NoError(t, err)
		assert.Equal(t, []paramPair{{Name: "foo", Value: "bar"}}, result)
	})

	t.Run("MapWithoutSchema", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, map[string]any{
			"foo": "bar",
			"baz": "qux",
		})
		require.NoError(t, err)
		// Keys are sorted
		assert.Len(t, result, 2)
	})

	t.Run("MapWithSchemaNoValues", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, map[string]any{
			"schema": "schema.json",
		})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("MapWithPlainSchemaKeyRemainsLegacy", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, map[string]any{
			"schema": "prod",
			"region": "us",
		})
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("MapWithSchemaAndValues", func(t *testing.T) {
		t.Parallel()
		result, err := parseParamValue(ctx, map[string]any{
			"schema": "schema.json",
			"values": map[string]any{"foo": "bar"},
		})
		require.NoError(t, err)
		assert.Equal(t, []paramPair{{Name: "foo", Value: "bar"}}, result)
	})

	t.Run("InvalidType", func(t *testing.T) {
		t.Parallel()
		_, err := parseParamValue(ctx, 123)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameter value")
	})
}

func TestOverrideParams(t *testing.T) {
	t.Parallel()

	t.Run("OverrideByName", func(t *testing.T) {
		t.Parallel()
		params := []paramPair{
			{Name: "foo", Value: "original"},
			{Name: "bar", Value: "keep"},
		}
		override := []paramPair{
			{Name: "foo", Value: "overridden"},
		}
		require.NoError(t, overrideParams(&params, override))
		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "overridden"},
			{Name: "bar", Value: "keep"},
		}, params)
	})

	t.Run("RejectsUnknownNamedParam", func(t *testing.T) {
		t.Parallel()
		params := []paramPair{
			{Name: "foo", Value: "bar"},
		}
		override := []paramPair{
			{Name: "baz", Value: "qux"},
		}
		err := overrideParams(&params, override)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"baz"`)
		assert.Contains(t, err.Error(), "accepted parameters are: foo")
	})

	t.Run("OverrideByPosition", func(t *testing.T) {
		t.Parallel()
		params := []paramPair{
			{Name: "", Value: "first"},
			{Name: "", Value: "second"},
		}
		override := []paramPair{
			{Name: "", Value: "new-first"},
		}
		require.NoError(t, overrideParams(&params, override))
		assert.Equal(t, []paramPair{
			{Name: "", Value: "new-first"},
			{Name: "", Value: "second"},
		}, params)
	})

	t.Run("AddPositionalBeyondLength", func(t *testing.T) {
		t.Parallel()
		params := []paramPair{
			{Name: "", Value: "first"},
		}
		override := []paramPair{
			{Name: "", Value: "new-first"},
			{Name: "", Value: "new-second"},
		}
		require.NoError(t, overrideParams(&params, override))
		assert.Equal(t, []paramPair{
			{Name: "", Value: "new-first"},
			{Name: "", Value: "new-second"},
		}, params)
	})

	t.Run("EmptyOverride", func(t *testing.T) {
		t.Parallel()
		params := []paramPair{
			{Name: "foo", Value: "bar"},
		}
		require.NoError(t, overrideParams(&params, []paramPair{}))
		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
		}, params)
	})

	t.Run("EmptyParams", func(t *testing.T) {
		t.Parallel()
		params := []paramPair{}
		override := []paramPair{
			{Name: "foo", Value: "bar"},
		}
		require.NoError(t, overrideParams(&params, override))
		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
		}, params)
	})

	t.Run("PositionalOnlyDefaultsAcceptNamedOverrides", func(t *testing.T) {
		t.Parallel()
		params := []paramPair{
			{Name: "", Value: "val1"},
			{Name: "", Value: "val2"},
		}
		override := []paramPair{
			{Name: "foo", Value: "bar"},
		}
		require.NoError(t, overrideParams(&params, override))
		assert.Len(t, params, 3)
	})
}

func TestParseParams(t *testing.T) {
	t.Parallel()

	t.Run("SimpleParams", func(t *testing.T) {
		t.Parallel()

		var params []paramPair
		var envs []string

		err := parseParams("foo=bar baz=qux", &params, &envs)
		require.NoError(t, err)

		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
			{Name: "baz", Value: "qux"},
		}, params)
		assert.Equal(t, []string{"foo=bar", "baz=qux"}, envs)
	})

	t.Run("PositionalParamsGetNames", func(t *testing.T) {
		t.Parallel()

		var params []paramPair
		var envs []string

		err := parseParams("one two three", &params, &envs)
		require.NoError(t, err)

		// Positional params get numbered names
		assert.Equal(t, []paramPair{
			{Name: "1", Value: "one"},
			{Name: "2", Value: "two"},
			{Name: "3", Value: "three"},
		}, params)
	})

	t.Run("NamedParamsAddedToEnvs", func(t *testing.T) {
		var params []paramPair
		var envs []string

		err := parseParams("foo=bar baz=qux", &params, &envs)
		require.NoError(t, err)

		assert.Equal(t, []paramPair{
			{Name: "foo", Value: "bar"},
			{Name: "baz", Value: "qux"},
		}, params)
		assert.Equal(t, []string{"foo=bar", "baz=qux"}, envs)
	})

	t.Run("VariableRefsPreservedLiterally", func(t *testing.T) {
		var params []paramPair
		var envs []string

		err := parseParams([]any{
			map[string]any{"BASE": "/opt"},
			map[string]any{"PATH_VAR": "${BASE}/bin"},
		}, &params, &envs)
		require.NoError(t, err)

		assert.Equal(t, "/opt", params[0].Value)
		assert.Equal(t, "${BASE}/bin", params[1].Value)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()

		var params []paramPair
		var envs []string

		err := parseParams(nil, &params, &envs)
		require.NoError(t, err)
		assert.Empty(t, params)
		assert.Empty(t, envs)
	})

	t.Run("InvalidInput", func(t *testing.T) {
		t.Parallel()

		var params []paramPair
		var envs []string

		err := parseParams(123, &params, &envs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parameter value")
	})
}

func TestMultilineParamRoundTrip(t *testing.T) {
	t.Parallel()
	pair := paramPair{Name: "MSG", Value: "line1\nline2"}
	escaped := pair.Escaped() // MSG="line1\nline2" with \n escape
	ctx := buildContext{ctx: context.Background(), opts: buildOpts{Flags: buildFlagNoEval}}
	result, err := parseStringParams(ctx, escaped)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "MSG", result[0].Name)
	assert.Equal(t, "line1\nline2", result[0].Value)
}

func TestJSONParamsSkipBacktickSubstitution(t *testing.T) {
	t.Parallel()

	// JSON params from the UI should not execute backtick commands.
	ctx := buildContext{
		ctx:  context.Background(),
		opts: buildOpts{},
	}

	// Value containing backticks — should be treated as literal, not executed.
	input := "[{\"topic\":\"`echo pwned`\"}]"
	result, err := parseStringParams(ctx, input)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "topic", result[0].Name)
	// tryParseJSONParams does not execute backtick commands;
	// the value is returned as-is from JSON parsing.
	assert.Equal(t, "`echo pwned`", result[0].Value)
}
