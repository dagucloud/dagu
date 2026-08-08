// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
)

func TestLoadControllerDAG(t *testing.T) {
	t.Parallel()

	const valid = `
type: controller
llm:
  provider: anthropic
  model: claude-opus-5
steps:
  - name: design
    action: dag.run
    with: { dag: child }
tasks:
  - name: ship
    description: done when design ran
---
name: child
steps:
  - name: work
    run: echo work
`

	t.Run("BuildsAControllerStep", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(valid))
		require.NoError(t, err)

		require.True(t, dag.IsController())
		require.Len(t, dag.Tasks, 1)
		assert.Equal(t, "ship", dag.Tasks[0].Name)

		ctrl := dag.ControllerStep()
		require.NotNil(t, ctrl, "a controller DAG carries a synthesized controller step")
		assert.Equal(t, ir.ExecutorTypeController, ctrl.ExecutorConfig.Type)
		assert.Same(t, dag.LLM, ctrl.LLM)
	})

	t.Run("ActionsTolerateFailureSoTheControllerCanRecover", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(valid))
		require.NoError(t, err)

		for _, step := range dag.Steps {
			if ir.IsSynthesizedControllerStep(step.Name) {
				continue
			}
			assert.True(t, step.ContinueOn.Failure, "step %q should not abort the run", step.Name)
		}
	})
}

func TestLoadControllerDAG_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "MissingLLM",
			yaml: `
type: controller
steps:
  - name: a
    run: echo a
tasks:
  - name: t
    description: d
`,
			errContains: "requires an llm configuration",
		},
		{
			name: "NoTasks",
			yaml: `
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: a
    run: echo a
`,
			errContains: "requires at least one task",
		},
		{
			name: "DuplicateTaskNames",
			yaml: `
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: a
    run: echo a
tasks:
  - name: t
    description: one
  - name: t
    description: two
`,
			errContains: "duplicate task name",
		},
		{
			name: "TaskWithoutCompletionCriteria",
			yaml: `
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: a
    run: echo a
tasks:
  - name: t
`,
			errContains: "must declare a description",
		},
		{
			name: "DependsIsRejected",
			yaml: `
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: a
    run: echo a
  - name: b
    run: echo b
    depends: [a]
tasks:
  - name: t
    description: d
`,
			errContains: "depends is not allowed in type: controller",
		},
		{
			name: "NoSteps",
			yaml: `
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
tasks:
  - name: t
    description: d
`,
			errContains: "requires at least one step",
		},
		{
			name: "ReservedStepName",
			yaml: `
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: __controller__
    run: echo a
tasks:
  - name: t
    description: d
`,
			errContains: "is reserved by type: controller",
		},
		{
			name: "ReservedAskUserStepName",
			yaml: `
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: ask_user
    run: echo a
tasks:
  - name: t
    description: d
`,
			errContains: `"ask_user" is reserved by type: controller`,
		},
		{
			name: "TasksRequireControllerType",
			yaml: `
steps:
  - name: a
    run: echo a
tasks:
  - name: t
    description: d
`,
			errContains: "tasks require type: controller",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(tt.yaml))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

// TestControllerDAGStaysComposable guards sub-workflow use: the ask_user task
// every controller carries must not be mistaken for a declared human task, which
// would bar the DAG from running as somebody's child.
func TestControllerDAGStaysComposable(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: work
    run: echo work
tasks:
  - name: done
    description: Finished when work ran.
`))
	require.NoError(t, err)

	require.NotNil(t, dag.ControllerStep())
	assert.False(t, dag.HasHumanTaskSteps(),
		"the synthesized ask_user task must not count as a declared human task")

	withDeclared, err := spec.LoadYAML(t.Context(), []byte(`
type: controller
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - id: review
    name: review
    action: human.task
    with:
      prompt: ok?
tasks:
  - name: done
    description: Finished when review was answered.
`))
	require.NoError(t, err)
	assert.True(t, withDeclared.HasHumanTaskSteps(),
		"a declared human task still bars the DAG from being a child")
}

// TestControllerLLMKnobsReachTheDAG covers root llm fields a controller depends
// on: a dropped max_tool_iterations silently replaces an author's cost limit
// with the default.
func TestControllerLLMKnobsReachTheDAG(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
type: controller
llm:
  provider: anthropic
  model: claude-opus-5
  max_tool_iterations: 7
  max_context_tokens: 100000
  observation_max_bytes: 8192
  observation_keep_recent: 2
steps:
  - name: a
    run: echo a
tasks:
  - name: t
    description: d
`))
	require.NoError(t, err)
	require.NotNil(t, dag.LLM.MaxToolIterations)
	assert.Equal(t, 7, *dag.LLM.MaxToolIterations)
	assert.Equal(t, 7, dag.ControllerMaxIterations())
	assert.Equal(t, 100000, dag.ControllerMaxContextTokens())
	assert.Equal(t, 8192, dag.ControllerObservationMaxBytes())
	assert.Equal(t, 2, dag.ControllerObservationKeepRecent())
}

func TestControllerLLMObservationLimitsMustBeNonNegative(t *testing.T) {
	t.Parallel()

	for _, field := range []string{
		"max_context_tokens",
		"observation_max_bytes",
		"observation_keep_recent",
	} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(`
type: controller
llm:
  provider: anthropic
  model: claude-opus-5
  `+field+`: -1
steps:
  - name: a
    run: echo a
tasks:
  - name: t
    description: d
`))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "llm."+field)
		})
	}
}

func TestControllerContextLimitsCanBeDisabled(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
type: controller
llm:
  provider: anthropic
  model: claude-opus-5
  max_context_tokens: 0
  observation_max_bytes: 0
  observation_keep_recent: 0
steps:
  - name: a
    run: echo a
tasks:
  - name: t
    description: d
`))
	require.NoError(t, err)
	assert.Zero(t, dag.ControllerMaxContextTokens())
	assert.Zero(t, dag.ControllerObservationMaxBytes())
	assert.Zero(t, dag.ControllerObservationKeepRecent())
}

func TestControllerContextLimitsAreRestrictedToControllerRoot(t *testing.T) {
	t.Parallel()

	for _, field := range []string{
		"max_context_tokens",
		"observation_max_bytes",
		"observation_keep_recent",
	} {
		t.Run("dag_"+field, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(`
llm:
  provider: anthropic
  model: claude-opus-5
  `+field+`: 1
steps:
  - name: a
    run: echo a
`))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "controller DAG's root llm configuration")
		})

		t.Run("step_"+field, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(`
steps:
  - name: chat
    type: chat
    llm:
      provider: anthropic
      model: claude-opus-5
      `+field+`: 1
`))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "controller DAG's root llm configuration")
		})
	}
}

func TestControllerObservationDefaults(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{LLM: &ir.LLMConfig{}}
	assert.Equal(t, 200000, dag.ControllerMaxContextTokens())
	assert.Equal(t, 512*1024, dag.ControllerObservationMaxBytes())
	assert.Equal(t, 20, dag.ControllerObservationKeepRecent())
}

// TestReservedControllerNamesAreRejectedInGraphDAGs keeps the synthesized names
// unusable everywhere: the execution plan recognises a controller by them, and a
// human task carrying one would otherwise slip past the sub-DAG prohibition.
func TestReservedControllerNamesAreRejectedInGraphDAGs(t *testing.T) {
	t.Parallel()

	for _, name := range []string{ir.ControllerStepName, ir.AskUserStepName} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(
				"steps:\n  - name: "+name+"\n    run: echo a\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is reserved by type: controller")
		})
	}
}
