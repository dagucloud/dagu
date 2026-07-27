// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package toolschema_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/llm/toolschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []toolschema.Param
	}{
		{
			name:     "EmptyString",
			input:    "",
			expected: nil,
		},
		{
			name:  "SingleRequiredParam",
			input: "query",
			expected: []toolschema.Param{
				{Name: "query", Type: "string", Required: true},
			},
		},
		{
			name:  "SingleParamWithDefault",
			input: "max_results=10",
			expected: []toolschema.Param{
				{Name: "max_results", Type: "integer", Default: int64(10), Required: false},
			},
		},
		{
			name:  "MultipleParams",
			input: "query max_results=10 include_images=true",
			expected: []toolschema.Param{
				{Name: "query", Type: "string", Required: true},
				{Name: "max_results", Type: "integer", Default: int64(10), Required: false},
				{Name: "include_images", Type: "boolean", Default: true, Required: false},
			},
		},
		{
			name:  "StringWithQuotes",
			input: `name="default value"`,
			expected: []toolschema.Param{
				{Name: "name", Type: "string", Default: "default value", Required: false},
			},
		},
		{
			name:  "FloatDefault",
			input: "temperature=0.7",
			expected: []toolschema.Param{
				{Name: "temperature", Type: "number", Default: 0.7, Required: false},
			},
		},
		{
			name:  "BooleanFalse",
			input: "verbose=false",
			expected: []toolschema.Param{
				{Name: "verbose", Type: "boolean", Default: false, Required: false},
			},
		},
		{
			name:  "EmptyArrayDefault",
			input: "filters=[]",
			expected: []toolschema.Param{
				{Name: "filters", Type: "array", Default: []any{}, Required: false},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := toolschema.ParseParams(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestParamsFromDefs_PreservesRequiredWithDefault(t *testing.T) {
	t.Parallel()

	params := toolschema.ParamsFromDefs([]core.ParamDef{
		{
			Name:     "query",
			Type:     core.ParamDefTypeString,
			Required: true,
			Default:  "latest",
		},
	})

	require.Len(t, params, 1)
	assert.True(t, params[0].Required)
	assert.Equal(t, "latest", params[0].Default)
}

func TestInferTypeFromDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		value        string
		expectedVal  any
		expectedType string
	}{
		{
			name:         "Integer",
			value:        "42",
			expectedVal:  int64(42),
			expectedType: "integer",
		},
		{
			name:         "NegativeInteger",
			value:        "-5",
			expectedVal:  int64(-5),
			expectedType: "integer",
		},
		{
			name:         "Float",
			value:        "3.14",
			expectedVal:  3.14,
			expectedType: "number",
		},
		{
			name:         "BooleanTrue",
			value:        "true",
			expectedVal:  true,
			expectedType: "boolean",
		},
		{
			name:         "BooleanFalse",
			value:        "false",
			expectedVal:  false,
			expectedType: "boolean",
		},
		{
			name:         "DoubleQuotedString",
			value:        `"hello world"`,
			expectedVal:  "hello world",
			expectedType: "string",
		},
		{
			name:         "SingleQuotedString",
			value:        `'test'`,
			expectedVal:  "test",
			expectedType: "string",
		},
		{
			name:         "PlainString",
			value:        "hello",
			expectedVal:  "hello",
			expectedType: "string",
		},
		{
			name:         "EmptyArray",
			value:        "[]",
			expectedVal:  []any{},
			expectedType: "array",
		},
		{
			name:         "ArrayWithValues",
			value:        `["a","b"]`,
			expectedVal:  []any{"a", "b"},
			expectedType: "array",
		},
		{
			name:         "EmptyObject",
			value:        "{}",
			expectedVal:  map[string]any{},
			expectedType: "object",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			val, typ := toolschema.InferTypeFromDefault(tc.value)
			assert.Equal(t, tc.expectedVal, val)
			assert.Equal(t, tc.expectedType, typ)
		})
	}
}

func TestSplitParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "EmptyString",
			input:    "",
			expected: nil,
		},
		{
			name:     "SingleParam",
			input:    "query",
			expected: []string{"query"},
		},
		{
			name:     "MultipleParams",
			input:    "a b c",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "ParamWithQuotedValue",
			input:    `name="hello world" count=5`,
			expected: []string{`name="hello world"`, "count=5"},
		},
		{
			name:     "SingleQuotedValue",
			input:    `greeting='Hello, World!'`,
			expected: []string{`greeting='Hello, World!'`},
		},
		{
			name:     "MultipleSpaces",
			input:    "a   b    c",
			expected: []string{"a", "b", "c"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := toolschema.SplitParams(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestBuild(t *testing.T) {
	t.Parallel()

	t.Run("EmptyParams", func(t *testing.T) {
		t.Parallel()

		result := toolschema.Build(nil)
		assert.Equal(t, "object", result["type"])
		assert.Empty(t, result["properties"])
		assert.Nil(t, result["required"])
	})

	t.Run("RequiredParam", func(t *testing.T) {
		t.Parallel()

		params := []toolschema.Param{
			{Name: "query", Type: "string", Required: true},
		}
		result := toolschema.Build(params)

		props := result["properties"].(map[string]any)
		assert.Contains(t, props, "query")

		queryProp := props["query"].(map[string]any)
		assert.Equal(t, "string", queryProp["type"])
		assert.Contains(t, queryProp["description"], "query parameter")

		required := result["required"].([]string)
		assert.Contains(t, required, "query")
	})

	t.Run("OptionalParamWithDefault", func(t *testing.T) {
		t.Parallel()

		params := []toolschema.Param{
			{Name: "limit", Type: "integer", Default: int64(10), Required: false},
		}
		result := toolschema.Build(params)

		props := result["properties"].(map[string]any)
		limitProp := props["limit"].(map[string]any)
		assert.Equal(t, "integer", limitProp["type"])
		assert.Equal(t, int64(10), limitProp["default"])

		assert.Nil(t, result["required"])
	})

	t.Run("MixedParams", func(t *testing.T) {
		t.Parallel()

		params := []toolschema.Param{
			{Name: "query", Type: "string", Required: true},
			{Name: "limit", Type: "integer", Default: int64(10), Required: false},
			{Name: "format", Type: "string", Required: true},
		}
		result := toolschema.Build(params)

		props := result["properties"].(map[string]any)
		assert.Len(t, props, 3)

		required := result["required"].([]string)
		assert.Len(t, required, 2)
		assert.Contains(t, required, "query")
		assert.Contains(t, required, "format")
	})
}

// TestInferTypeFromDefault_MalformedQuotes covers default-params a DAG author
// mistyped: an unterminated quote must not take the process down.
func TestInferTypeFromDefault_MalformedQuotes(t *testing.T) {
	t.Parallel()

	for _, value := range []string{`"`, `'`, ``, `"unterminated`, `trailing"`} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			decoded, kind := toolschema.InferTypeFromDefault(value)
			assert.Equal(t, "string", kind)
			assert.Equal(t, value, decoded, "a value that is not a quoted literal is kept as is")
		})
	}
}
