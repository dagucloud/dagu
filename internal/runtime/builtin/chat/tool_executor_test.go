// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/command"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			expected: `{"query":"test"}`,
		},
		{
			name: "SingleIntArg",
			args: map[string]any{
				"count": float64(10), // JSON numbers are float64
			},
			expected: `{"count":"10"}`,
		},
		{
			name: "SingleFloatArg",
			args: map[string]any{
				"temperature": 0.7,
			},
			expected: `{"temperature":"0.7"}`,
		},
		{
			name: "SingleBoolArg",
			args: map[string]any{
				"verbose": true,
			},
			expected: `{"verbose":"true"}`,
		},
		{
			name: "MultipleArgs",
			args: map[string]any{
				"query":  "hello",
				"count":  float64(5),
				"active": true,
			},
			// Keys are sorted alphabetically
			expected: `{"active":"true","count":"5","query":"hello"}`,
		},
		{
			name: "StringWithSpaces",
			args: map[string]any{
				"query": "hello world",
			},
			expected: `{"query":"hello world"}`,
		},
		{
			name: "NilValue",
			args: map[string]any{
				"empty": nil,
			},
			expected: `{"empty":""}`,
		},
		{
			name: "ArrayArg",
			args: map[string]any{
				"filters": []any{"a", "b"},
			},
			expected: `{"filters":"[\"a\",\"b\"]"}`,
		},
		{
			name: "ObjectArg",
			args: map[string]any{
				"config": map[string]any{"key": "value"},
			},
			expected: `{"config":"{\"key\":\"value\"}"}`,
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

// A value of the model with quotes, spaces or a new line cannot add a pair or
// change another input of the child run. The parser of the child run reads the
// same pairs.
func TestBuildParamStringKeepsOnePairForEachArg(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"CUSTOMER_ID": `x" TARGET="evil z`,
		"NOTE":        "a\nTARGET=evil",
	}
	dag, err := spec.LoadYAML(context.Background(), []byte("params:\n  - CUSTOMER_ID: \"\"\n  - NOTE: \"\"\n  - TARGET: prod\nsteps:\n  - run: echo\n"),
		spec.WithParams(buildParamString(args)))
	require.NoError(t, err)
	got := map[string]string{}
	for _, pair := range dag.Params {
		name, value, _ := strings.Cut(pair, "=")
		got[name] = value
	}
	assert.Equal(t, map[string]string{"CUSTOMER_ID": `x" TARGET="evil z`, "NOTE": "a\nTARGET=evil", "TARGET": "prod"}, got)
}

func TestUndeclaredArg(t *testing.T) {
	t.Parallel()
	info := &toolInfo{Params: []toolParam{{Name: "CUSTOMER_ID"}}}
	_, found := undeclaredArg(map[string]any{"CUSTOMER_ID": "1"}, info)
	assert.False(t, found)
	name, found := undeclaredArg(map[string]any{"CUSTOMER_ID": "1", "LD_PRELOAD": "/tmp/x.so"}, info)
	assert.True(t, found)
	assert.Equal(t, "LD_PRELOAD", name)
}

// An argument that the tool does not offer, of any JSON type and also a name
// that differs only by case, gets an error result and starts no child run.
// The other calls of the same response still run.
func TestExecuteToolCallsRefusesUndeclaredArg(t *testing.T) {
	t.Parallel()
	registry := &ToolRegistry{
		tools:    map[string]*toolInfo{"search": {Name: "search", Params: []toolParam{{Name: "query"}}}},
		dagNames: map[string]string{"search": "search"},
	}
	exec := NewToolExecutor(registry, t.TempDir())

	for _, args := range []string{
		`{"query":"a","QUERY":"b"}`,
		`{"query":"a","LD_PRELOAD":null}`,
		`{"query":"a","LD_PRELOAD":1}`,
		`{"query":"a","LD_PRELOAD":true}`,
		`{"query":"a","LD_PRELOAD":["/tmp/x.so"]}`,
		`{"query":"a","LD_PRELOAD":{"path":"/tmp/x.so"}}`,
	} {
		results := exec.ExecuteToolCalls(context.Background(), []llmpkg.ToolCall{
			{ID: "bad", Function: llmpkg.ToolCallFunction{Name: "search", Arguments: args}},
			{ID: "good", Function: llmpkg.ToolCallFunction{Name: "search", Arguments: `{"query":"a"}`}},
		})
		require.Len(t, results, 2, args)
		name := "LD_PRELOAD"
		if strings.Contains(args, "QUERY") {
			name = "QUERY"
		}
		assert.Equal(t, `tool "search" has no input "`+name+`"`, results[0].Result.Error, args)
		assert.Empty(t, results[0].SubRun.DAGRunID, args)
		// The declared call passes the check. It fails later only because
		// this test has no DAG store to start the child run from.
		assert.Equal(t, "good", results[1].Result.ToolCallID)
		assert.NotContains(t, results[1].Result.Error, "has no input")
	}
}

// Each form of params gives the model a schema. Arguments that follow it are
// accepted, and the child run reads back every value as the model sent it.
func TestToolArgsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.json"),
		[]byte(`{"type":"object","properties":{"A":{"type":"string"},"B":{"type":"string"}}}`), 0o600))
	forms := map[string]string{
		"None":       "",
		"String":     "params: A=x B=y\n",
		"Positional": "params: CITY\n",
		"List":       "params:\n  - A: \"\"\n  - B: \"\"\n",
		"Map":        "params:\n  A: \"\"\n  B: \"\"\n",
		"Defs":       "params:\n  - name: A\n    type: string\n  - name: B\n    type: string\n    default: d\n",
		"Inline":     "params:\n  type: object\n  properties:\n    A:\n      type: string\n    B:\n      type: string\n",
		"External":   "working_dir: " + dir + "\nparams:\n  schema: ./schema.json\n",
	}
	values := []string{
		`x" TARGET="evil`, `back\slash\`, "new\nline", "a=b", "$HOME ${HOME} $(id)", "`id`",
		"日本語", "", strings.Repeat("v", 100_000),
	}
	for name, params := range forms {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			yaml := []byte("name: tool\n" + params + "steps:\n  - run: echo\n")
			tool, err := spec.LoadYAML(context.Background(), yaml)
			require.NoError(t, err)
			parent := &ir.DAG{Name: "parent", LocalDAGs: map[string]*ir.DAG{"tool": tool}}
			registry, err := NewToolRegistry(runctx.NewContext(context.Background(), parent, "run-1", "/tmp/log"), []string{"tool"})
			require.NoError(t, err)
			schema := registry.ToLLMTools()[0].Function.Parameters["properties"].(map[string]any)

			for _, value := range values {
				args := map[string]any{}
				for input := range schema {
					args[input] = value
				}
				_, refused := undeclaredArg(args, registry.tools["tool"])
				require.False(t, refused)

				child, err := spec.LoadYAML(context.Background(), yaml, spec.WithParams(buildParamString(args)))
				require.NoError(t, err)
				got := map[string]string{}
				for _, pair := range child.Params {
					key, v, _ := strings.Cut(pair, "=")
					got[key] = v
				}
				for input := range schema {
					assert.Equal(t, value, got[input], "input %s", input)
				}
			}
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
			name:     "SmallFloatInDecimalForm",
			value:    0.00001,
			expected: "0.00001",
		},
		{
			name:     "LargeIntegerInDecimalForm",
			value:    1e21,
			expected: "1000000000000000000000",
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

		runStatus := &ir.RunStatus{
			Status: ir.Failed,
		}
		result := formatToolResult(runStatus)
		assert.Contains(t, result, "failed")
	})

	t.Run("SuccessWithOutputs", func(t *testing.T) {
		t.Parallel()

		runStatus := &ir.RunStatus{
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

		runStatus := &ir.RunStatus{
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
