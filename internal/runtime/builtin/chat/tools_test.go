// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	// The spec loader accepts a command for these executors only once they
	// are registered.
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/docker"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/ssh"
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
		{"DefaultedEnvCopy", "params:\n  - name: Q\n    type: string\n    default: x\nenv:\n  - QUERY: ${params.Q}\nsteps:\n  - id: s\n    run: echo ${env.QUERY}\n", `refers to ${env.QUERY}, which`},
		{"StepEnvCopy", params + "steps:\n  - id: s\n    env:\n      - C: $Q\n    run: echo ${env.C}\n", `refers to ${env.C}, which`},
		{"JSONPath", params + "shell: bash\nsteps:\n  - id: s\n    run: curl https://api.example.com/orders/${Q.id}\n", `the command of step "s" refers to ${Q.id}, which`},
		{"QuotedJSONPath", params + "shell: bash\nsteps:\n  - id: s\n    run: echo \"${Q.id}\"\n", `refers to ${Q.id}, which`},
		{"BareJSONPath", params + "shell: bash\nsteps:\n  - id: s\n    run: cat /data/$Q.txt\n", `refers to $Q.txt, which`},
		{"ParamsJSONPath", params + "shell: bash\nsteps:\n  - id: s\n    run: echo ${DAG_PARAMS_JSON.Q}\n", `refers to ${DAG_PARAMS_JSON.Q}, which`},
		{"ForeachItem", params + "shell: bash\nsteps:\n  - id: each\n    foreach:\n      items: ${params.Q}\n      steps:\n        - id: get\n          run: curl https://api.example.com/orders/${foreach.item}\n", `the command of step "get" refers to ${foreach.item}, which`},
		{"ForeachItemHint", params + "shell: bash\nsteps:\n  - id: each\n    foreach:\n      items: ${params.Q}\n      steps:\n        - run: echo ${foreach.item}\n", `copy it into an env entry of the step, such as VALUE: ${foreach.item}, and read "$VALUE"`},
		{"ForeachItemField", params + "shell: bash\nsteps:\n  - id: each\n    foreach:\n      items: ${params.Q}\n      as: order\n      steps:\n        - run: ./fetch.sh ${foreach.order.url}\n", `refers to ${foreach.order.url}, which`},
		{"ForeachStaticItem", params + "shell: bash\nsteps:\n  - id: each\n    foreach:\n      items: [\"${params.Q}\"]\n      steps:\n        - run: echo ${foreach.item}\n", `refers to ${foreach.item}, which`},
		{"ForeachKey", params + "shell: bash\nsteps:\n  - id: each\n    foreach:\n      items: [1]\n      key: ${params.Q}\n      steps:\n        - run: echo ${foreach.key}\n", `refers to ${foreach.key}, which`},
		{"NestedForeachOuterItems", params + "shell: bash\nsteps:\n  - id: orders\n    foreach:\n      items: ${params.Q}\n      as: order\n      steps:\n        - id: lines\n          foreach:\n            items: ${foreach.order.lines}\n            steps:\n              - id: body\n                run: echo ${foreach.item}\n", `the command of step "body" refers to ${foreach.item}, which`},
		{"ShellVariable", params + "shell: bash\nsteps:\n  - run: echo \"$Q\" \"$DAG_PARAMS_JSON\"\n", ""},
		{"ShellVariableBeforeDot", params + "shell: bash\nsteps:\n  - run: cat \"/data/$Q\".txt\n", ""},
		// Dagu fills $NAME.path only when the path parses as jq, and the
		// text up to the next space here does not.
		{"ShellVariableBeforeEllipsis", params + "shell: bash\nsteps:\n  - run: echo \"Looking up $Q...\"\n", ""},
		{"ShellVariableBeforeQuote", params + "shell: bash\nsteps:\n  - run: cat \"/data/$Q.txt\"\n", ""},
		{"ForeachItemEnvCopy", params + "shell: bash\nsteps:\n  - id: each\n    foreach:\n      items: ${params.Q}\n      steps:\n        - env:\n            - VALUE: ${foreach.item}\n          run: echo \"$VALUE\"\n", ""},
		{"ForeachLiteralItems", params + "shell: bash\nsteps:\n  - id: each\n    foreach:\n      items: [a, b]\n      steps:\n        - run: echo ${foreach.item} \"$Q\"\n", ""},
		// The item scope of an inner foreach replaces the outer one.
		{"NestedForeachLiteralItems", params + "shell: bash\nsteps:\n  - id: orders\n    foreach:\n      items: ${params.Q}\n      steps:\n        - id: lines\n          foreach:\n            items: [a, b]\n            steps:\n              - run: echo ${foreach.item}\n", ""},
		{"NestedForeachLiteralItemsEnvCopy", params + "shell: bash\nsteps:\n  - id: orders\n    foreach:\n      items: ${params.Q}\n      steps:\n        - id: lines\n          foreach:\n            items: [a, b]\n            steps:\n              - env:\n                  - COPY: $Q\n                run: echo ${foreach.item} \"$COPY\"\n", ""},
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
			// A queued, retried or restarted run reads its local DAGs back from
			// the run snapshot, which keeps fewer fields than a fresh load.
			for _, form := range []struct {
				name string
				dag  *ir.DAG
			}{{"Loaded", tool}, {"Snapshot", snapshotOf(t, tool)}} {
				parent := &ir.DAG{Name: "parent", LocalDAGs: map[string]*ir.DAG{"tool": form.dag}}
				ctx := runctx.NewContext(context.Background(), parent, "run-1", "/tmp/log")
				_, err := NewToolRegistry(ctx, []string{"tool"})
				if tc.want == "" {
					assert.NoError(t, err, form.name)
					continue
				}
				if assert.Error(t, err, form.name) {
					assert.Contains(t, err.Error(), `tool DAG "tool": `, form.name)
					assert.Contains(t, err.Error(), tc.want, form.name)
				}
			}
		})
	}
}

// snapshotOf returns dag as a run snapshot holds it: written as JSON and read
// back.
func snapshotOf(t *testing.T, dag *ir.DAG) *ir.DAG {
	t.Helper()
	data, err := json.Marshal(dag)
	require.NoError(t, err)
	var restored ir.DAG
	require.NoError(t, json.Unmarshal(data, &restored))
	return &restored
}

// Dagu writes $NAME into the command of a step that is not a local command,
// such as one that a DAG-level ssh or container block runs, whatever shell
// runs it there. The error gives no "$NAME" hint for such a step.
func TestNewToolRegistryRefusesShellVariableOfRemoteStep(t *testing.T) {
	t.Parallel()
	steps := map[string]string{
		"SSH":       "ssh:\n  user: ops\n  host: app.internal\nsteps:\n  - id: s\n    run: %s\n",
		"SSHRun":    "steps:\n  - id: s\n    action: ssh.run\n    with:\n      user: ops\n      host: app.internal\n      command: %s\n",
		"Container": "container:\n  image: alpine:3\nsteps:\n  - id: s\n    run: %s\n",
	}
	commands := map[string]string{
		`grep "$Q" /var/log/app.log`:        `$Q`,
		`grep ${params.Q} /var/log/app.log`: `${params.Q}`,
	}
	for name, step := range steps {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for command, ref := range commands {
				yaml := "name: logs\nparams:\n  - name: Q\n    type: string\n" + fmt.Sprintf(step, command)
				tool, err := spec.LoadYAML(context.Background(), []byte(yaml))
				require.NoError(t, err)
				parent := &ir.DAG{Name: "parent", LocalDAGs: map[string]*ir.DAG{"logs": tool}}
				ctx := runctx.NewContext(context.Background(), parent, "run-1", "/tmp/log")
				_, err = NewToolRegistry(ctx, []string{"logs"})
				if assert.Error(t, err, command) {
					assert.Equal(t, `tool DAG "logs": the command of step "s" refers to `+ref+`, which holds text that the model sets, and that text would run as code`, err.Error())
				}
			}
		})
	}
}

// A params schema next to the tool DAG file resolves without a working_dir.
func TestNewToolRegistryRelativeSchema(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "schema.json"),
		[]byte(`{"type":"object","properties":{"Q":{"type":"string"}}}`), 0o600))
	file := filepath.Join(dir, "lookup.yaml")
	require.NoError(t, os.WriteFile(file,
		[]byte("name: lookup\nparams:\n  schema: ./schema.json\nsteps:\n  - run: echo ok\n"), 0o600))
	tool, err := spec.Load(context.Background(), file)
	require.NoError(t, err)
	parent := &ir.DAG{Name: "parent", LocalDAGs: map[string]*ir.DAG{"lookup": tool}}
	ctx := runctx.NewContext(context.Background(), parent, "run-1", "/tmp/log")

	registry, err := NewToolRegistry(ctx, []string{"lookup"})
	require.NoError(t, err)
	assert.True(t, registry.HasTools())
}
