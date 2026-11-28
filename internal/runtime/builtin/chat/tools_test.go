// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/spec"
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

func TestNewToolRegistry_LocalDAGs(t *testing.T) {
	t.Parallel()

	t.Run("LoadsFromLocalDAGs", func(t *testing.T) {
		t.Parallel()

		// Create a parent DAG with LocalDAGs
		localDAG := &ir.DAG{
			Name:          "search_tool",
			Description:   "Search the web",
			DefaultParams: "query",
		}

		parentDAG := &ir.DAG{
			Name: "parent",
			LocalDAGs: map[string]*ir.DAG{
				"search_tool": localDAG,
			},
		}

		// Create context with parent DAG
		ctx := runctx.NewContext(context.Background(), parentDAG, "run-1", "/tmp/log")

		registry, err := NewToolRegistry(ctx, []string{"search_tool"})
		require.NoError(t, err)
		require.NotNil(t, registry)

		// Verify the tool was loaded from LocalDAGs
		assert.True(t, registry.HasTools())
		dag, ok := registry.GetDAGByToolName("search_tool")
		assert.True(t, ok)
		assert.Equal(t, "search_tool", dag.Name)
		assert.Equal(t, "Search the web", dag.Description)
	})

	t.Run("EmptyDagNames", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		registry, err := NewToolRegistry(ctx, []string{})
		assert.NoError(t, err)
		assert.Nil(t, registry)
	})

	t.Run("NilDagNames", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		registry, err := NewToolRegistry(ctx, nil)
		assert.NoError(t, err)
		assert.Nil(t, registry)
	})
}

// A tool DAG that would run text of the model as code is refused when the
// step starts: a command that Dagu fills with an input, or an eval that reads
// one. Reading the input as a variable of a POSIX shell is accepted.
func TestNewToolRegistryRefusesEvaluatedInput(t *testing.T) {
	t.Parallel()
	const params = "params:\n  - name: Q\n    type: string\n"
	tests := []struct {
		name string
		body string
		want string
	}{
		{"ParamsRef", params + "steps:\n  - id: s\n    run: echo ${params.Q}\n", `the command of step "s" refers to ${params.Q}`},
		{"EnvRef", params + "steps:\n  - id: s\n    run: echo ${env.Q}\n", `refers to ${env.Q}`},
		{"WholeParams", params + "shell: bash\nsteps:\n  - id: s\n    run: printf '%s' '${params}'\n", `read it as "$DAG_PARAMS_JSON"`},
		{"Script", params + "steps:\n  - id: s\n    script: |\n      echo ${params.Q}\n", `refers to ${params.Q}`},
		{"Handler", params + "handler_on:\n  exit:\n    run: echo ${params.Q}\nsteps:\n  - run: echo\n", `refers to ${params.Q}`},
		{"Foreach", params + "steps:\n  - id: s\n    foreach:\n      items: [1]\n      steps:\n        - id: body\n          run: echo ${params.Q}\n", `step "body"`},
		{"ParamEval", params + "  - name: X\n    type: string\n    eval: \"`echo $Q`\"\nsteps:\n  - run: echo\n", `the eval of param "X" refers to $Q`},
		{"PreconditionCommand", params + "preconditions:\n  - condition: test -n ${params.Q}\nsteps:\n  - run: echo\n", `a precondition refers to ${params.Q}`},
		{"PreconditionEval", params + "steps:\n  - id: s\n    preconditions:\n      - eval: \"`echo $Q`\"\n        expected: x\n    run: echo\n", `a precondition of step "s" refers to $Q`},
		{"PreconditionShellVariable", params + "preconditions:\n  - condition: test -n \"$Q\"\nsteps:\n  - run: echo\n", `a precondition refers to $Q, which`},
		{"PowerShell", params + "shell: pwsh\nsteps:\n  - id: s\n    run: Write-Output \"$Q\"\n", `the command of step "s" refers to $Q, which`},
		{"StepShell", params + "shell: bash\nsteps:\n  - id: s\n    with:\n      shell: cmd\n    run: echo $Q\n", `the command of step "s" refers to $Q, which`},
		{"HereDoc", params + "shell: bash\nsteps:\n  - id: s\n    run: |\n      cat <<EOF\n      {\"q\": \"${Q}\"}\n      EOF\n", `refers to "${Q}", which`},
		{"EnvCopy", params + "env:\n  - QUERY: ${params.Q}\nsteps:\n  - id: s\n    run: echo ${env.QUERY}\n", `refers to ${env.QUERY}, which`},
		{"StepEnvCopy", params + "steps:\n  - id: s\n    env:\n      - C: $Q\n    run: echo ${env.C}\n", `refers to ${env.C}, which`},
		{"ShellVariable", params + "shell: bash\nsteps:\n  - run: echo \"$Q\" \"$DAG_PARAMS_JSON\"\n", ""},
		{"StepPOSIXShell", params + "shell: pwsh\nsteps:\n  - with:\n      shell: bash\n    run: echo \"$Q\"\n", ""},
		{"EnvCopyAsVariable", params + "shell: bash\nenv:\n  - QUERY: ${params.Q}\nsteps:\n  - run: echo \"$QUERY\"\n", ""},
		{"ValueMatch", params + "preconditions:\n  - condition: ${params.Q}\n    expected: yes\nsteps:\n  - run: echo\n", ""},
		{"NoInputs", "steps:\n  - run: echo ${params}\n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool, err := spec.LoadYAML(context.Background(), []byte("name: tool\n"+tc.body))
			require.NoError(t, err)
			parent := &ir.DAG{Name: "parent", LocalDAGs: map[string]*ir.DAG{"tool": tool}}
			ctx := runctx.NewContext(context.Background(), parent, "run-1", "/tmp/log")
			_, err = NewToolRegistry(ctx, []string{"tool"})
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), `tool DAG "tool": `)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
