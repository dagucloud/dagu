// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolRegistry_ToLLMTools(t *testing.T) {
	t.Parallel()

	t.Run("NilRegistry", func(t *testing.T) {
		t.Parallel()

		var r *ToolRegistry
		result := r.ToLLMTools()
		assert.Nil(t, result)
	})

	t.Run("EmptyRegistry", func(t *testing.T) {
		t.Parallel()

		r := &ToolRegistry{
			tools: make(map[string]*toolInfo),
		}
		result := r.ToLLMTools()
		assert.Empty(t, result)
	})

	t.Run("WithTools", func(t *testing.T) {
		t.Parallel()

		r := &ToolRegistry{
			tools: map[string]*toolInfo{
				"search": {
					Name:        "search",
					Description: "Search the web",
					Params: []toolParam{
						{Name: "query", Type: "string", Required: true},
					},
				},
			},
		}
		result := r.ToLLMTools()

		require.Len(t, result, 1)
		assert.Equal(t, "function", result[0].Type)
		assert.Equal(t, "search", result[0].Function.Name)
		assert.Equal(t, "Search the web", result[0].Function.Description)
		assert.Contains(t, result[0].Function.Parameters, "type")
		assert.Contains(t, result[0].Function.Parameters, "properties")
	})
}

func TestToolRegistry_GetDAGByToolName(t *testing.T) {
	t.Parallel()

	t.Run("NilRegistry", func(t *testing.T) {
		t.Parallel()

		var r *ToolRegistry
		dag, ok := r.GetDAGByToolName("test")
		assert.Nil(t, dag)
		assert.False(t, ok)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()

		r := &ToolRegistry{
			tools: make(map[string]*toolInfo),
		}
		dag, ok := r.GetDAGByToolName("unknown")
		assert.Nil(t, dag)
		assert.False(t, ok)
	})

	t.Run("Found", func(t *testing.T) {
		t.Parallel()

		testDAG := &ir.DAG{Name: "test-dag"}
		r := &ToolRegistry{
			tools: map[string]*toolInfo{
				"test": {
					Name: "test",
					DAG:  testDAG,
				},
			},
		}
		dag, ok := r.GetDAGByToolName("test")
		assert.Equal(t, testDAG, dag)
		assert.True(t, ok)
	})
}

func TestToolRegistry_GetDAGName(t *testing.T) {
	t.Parallel()

	t.Run("NilRegistry", func(t *testing.T) {
		t.Parallel()

		var r *ToolRegistry
		name, ok := r.GetDAGName("test")
		assert.Empty(t, name)
		assert.False(t, ok)
	})

	t.Run("Found", func(t *testing.T) {
		t.Parallel()

		r := &ToolRegistry{
			dagNames: map[string]string{
				"search_tool": "search-tool-dag",
			},
		}
		name, ok := r.GetDAGName("search_tool")
		assert.Equal(t, "search-tool-dag", name)
		assert.True(t, ok)
	})
}

func TestToolRegistry_HasTools(t *testing.T) {
	t.Parallel()

	t.Run("NilRegistry", func(t *testing.T) {
		t.Parallel()

		var r *ToolRegistry
		assert.False(t, r.HasTools())
	})

	t.Run("EmptyRegistry", func(t *testing.T) {
		t.Parallel()

		r := &ToolRegistry{
			tools: make(map[string]*toolInfo),
		}
		assert.False(t, r.HasTools())
	})

	t.Run("WithTools", func(t *testing.T) {
		t.Parallel()

		r := &ToolRegistry{
			tools: map[string]*toolInfo{
				"test": {},
			},
		}
		assert.True(t, r.HasTools())
	})
}
