// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestBuildParamString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{
			name:     "EmptyArgs",
			args:     nil,
			expected: "",
		},
		{
			name:     "EmptyMap",
			args:     map[string]any{},
			expected: "",
		},
		{
			name: "SingleStringArg",
			args: map[string]any{
				"query": "test",
			},
			expected: "query=test",
		},
		{
			name: "SingleIntArg",
			args: map[string]any{
				"count": float64(10), // JSON numbers are float64
			},
			expected: "count=10",
		},
		{
			name: "SingleFloatArg",
			args: map[string]any{
				"temperature": 0.7,
			},
			expected: "temperature=0.7",
		},
		{
			name: "SingleBoolArg",
			args: map[string]any{
				"verbose": true,
			},
			expected: "verbose=true",
		},
		{
			name: "MultipleArgs",
			args: map[string]any{
				"query":  "hello",
				"count":  float64(5),
				"active": true,
			},
			// Keys are sorted alphabetically
			expected: "active=true count=5 query=hello",
		},
		{
			name: "StringWithSpaces",
			args: map[string]any{
				"query": "hello world",
			},
			expected: `query="hello world"`,
		},
		{
			name: "NilValue",
			args: map[string]any{
				"empty": nil,
			},
			expected: "empty=",
		},
		{
			name: "ArrayArg",
			args: map[string]any{
				"filters": []any{"a", "b"},
			},
			expected: `filters=["a","b"]`,
		},
		{
			name: "ObjectArg",
			args: map[string]any{
				"config": map[string]any{"key": "value"},
			},
			expected: `config={"key":"value"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := buildParamString(tc.args)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestFormatArgValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    any
		expected string
	}{
		{
			name:     "String",
			value:    "hello",
			expected: "hello",
		},
		{
			name:     "Integer",
			value:    float64(42),
			expected: "42",
		},
		{
			name:     "Float",
			value:    3.14,
			expected: "3.14",
		},
		{
			name:     "BoolTrue",
			value:    true,
			expected: "true",
		},
		{
			name:     "BoolFalse",
			value:    false,
			expected: "false",
		},
		{
			name:     "Nil",
			value:    nil,
			expected: "",
		},
		{
			name:     "Array",
			value:    []any{1, 2, 3},
			expected: "[1,2,3]",
		},
		{
			name:     "Object",
			value:    map[string]any{"a": 1},
			expected: `{"a":1}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := formatArgValue(tc.value)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestFormatToolResult(t *testing.T) {
	t.Parallel()

	t.Run("NilResult", func(t *testing.T) {
		t.Parallel()

		result := formatToolResult(nil)
		assert.Contains(t, result, "no result")
	})

	t.Run("FailedStatus", func(t *testing.T) {
		t.Parallel()

		runStatus := &exec.RunStatus{
			Status: ir.Failed,
		}
		result := formatToolResult(runStatus)
		assert.Contains(t, result, "failed")
	})

	t.Run("SuccessWithOutputs", func(t *testing.T) {
		t.Parallel()

		runStatus := &exec.RunStatus{
			Status: ir.Succeeded,
			Outputs: map[string]string{
				"result": "test output",
			},
		}
		result := formatToolResult(runStatus)
		assert.Contains(t, result, "test output")
		assert.Contains(t, result, "result")
	})

	t.Run("SuccessNoOutputs", func(t *testing.T) {
		t.Parallel()

		runStatus := &exec.RunStatus{
			Status:  ir.Succeeded,
			Outputs: map[string]string{},
		}
		result := formatToolResult(runStatus)
		assert.Contains(t, result, "completed successfully")
	})
}

func TestNewToolExecutor(t *testing.T) {
	t.Parallel()

	t.Run("CreatesExecutor", func(t *testing.T) {
		t.Parallel()

		registry := &ToolRegistry{
			tools:    make(map[string]*toolInfo),
			dagNames: make(map[string]string),
		}

		executor := NewToolExecutor(registry, "/work/dir")
		assert.NotNil(t, executor)
		assert.Equal(t, registry, executor.registry)
		assert.Equal(t, "/work/dir", executor.parentWorkDir)
		assert.NotNil(t, executor.runningDAGs)
	})
}
