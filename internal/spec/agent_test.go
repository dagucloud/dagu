// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
)

func TestLoadAgentDAG(t *testing.T) {
	t.Parallel()

	const valid = `
type: agent
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

	t.Run("BuildsAAgentStep", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(valid))
		require.NoError(t, err)

		require.True(t, dag.IsAgent())
		require.Len(t, dag.Tasks, 1)
		assert.Equal(t, "ship", dag.Tasks[0].Name)

		agent := dag.AgentStep()
		require.NotNil(t, agent, "an agent DAG carries a synthesized agent step")
		assert.Equal(t, ir.ExecutorTypeAgent, agent.ExecutorConfig.Type)
		assert.Same(t, dag.LLM, agent.LLM)
	})

	t.Run("ActionsTolerateFailureSoTheAgentCanRecover", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(valid))
		require.NoError(t, err)

		for _, step := range dag.Steps {
			if ir.IsSynthesizedAgentStep(step.Name) {
				continue
			}
			assert.True(t, step.ContinueOn.Failure, "step %q should not abort the run", step.Name)
		}
	})
}

func TestLoadAgentDAG_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "MissingLLM",
			yaml: `
type: agent
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
type: agent
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
type: agent
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
type: agent
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
type: agent
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
			errContains: "depends is not allowed in type: agent",
		},
		{
			name: "NoSteps",
			yaml: `
type: agent
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
type: agent
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: __agent__
    run: echo a
tasks:
  - name: t
    description: d
`,
			errContains: "is reserved by type: agent",
		},
		{
			name: "ReservedAskUserStepName",
			yaml: `
type: agent
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: ask_user
    run: echo a
tasks:
  - name: t
    description: d
`,
			errContains: `"ask_user" is reserved by type: agent`,
		},
		{
			name: "TasksRequireAgentType",
			yaml: `
steps:
  - name: a
    run: echo a
tasks:
  - name: t
    description: d
`,
			errContains: "tasks require type: agent",
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

// TestAgentDAGStaysComposable guards sub-workflow use: the ask_user task
// every agent carries must not be mistaken for a declared human task, which
// would bar the DAG from running as somebody's child.
func TestAgentDAGStaysComposable(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
type: agent
llm: { provider: anthropic, model: claude-opus-5 }
steps:
  - name: work
    run: echo work
tasks:
  - name: done
    description: Finished when work ran.
`))
	require.NoError(t, err)

	require.NotNil(t, dag.AgentStep())
	assert.False(t, dag.HasHumanTaskSteps(),
		"the synthesized ask_user task must not count as a declared human task")

	withDeclared, err := spec.LoadYAML(t.Context(), []byte(`
type: agent
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

// TestAgentLLMKnobsReachTheDAG covers root llm fields an agent depends
// on: a dropped max_tool_iterations silently replaces an author's cost limit
// with the default.
func TestAgentLLMKnobsReachTheDAG(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
type: agent
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
	assert.Equal(t, 7, dag.AgentMaxIterations())
	assert.Equal(t, 100000, dag.AgentMaxContextTokens())
	assert.Equal(t, 8192, dag.AgentObservationMaxBytes())
	assert.Equal(t, 2, dag.AgentObservationKeepRecent())
}

func TestAgentLLMObservationLimitsMustBeNonNegative(t *testing.T) {
	t.Parallel()

	for _, field := range []string{
		"max_context_tokens",
		"observation_max_bytes",
		"observation_keep_recent",
	} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(`
type: agent
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

func TestAgentContextLimitsCanBeDisabled(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
type: agent
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
	assert.Zero(t, dag.AgentMaxContextTokens())
	assert.Zero(t, dag.AgentObservationMaxBytes())
	assert.Zero(t, dag.AgentObservationKeepRecent())
}

func TestAgentContextLimitsAreRestrictedToAgentRoot(t *testing.T) {
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
			assert.Contains(t, err.Error(), "agent DAG's root llm configuration")
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
			assert.Contains(t, err.Error(), "agent DAG's root llm configuration")
		})
	}
}

func TestAgentObservationDefaults(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{LLM: &ir.LLMConfig{}}
	assert.Equal(t, 200000, dag.AgentMaxContextTokens())
	assert.Equal(t, 512*1024, dag.AgentObservationMaxBytes())
	assert.Equal(t, 20, dag.AgentObservationKeepRecent())
}

// TestReservedAgentNamesAreRejectedInGraphDAGs keeps the synthesized names
// unusable everywhere: the execution plan recognises an agent by them, and a
// human task carrying one would otherwise slip past the sub-DAG prohibition.
func TestReservedAgentNamesAreRejectedInGraphDAGs(t *testing.T) {
	t.Parallel()

	for _, name := range []string{ir.AgentStepName, ir.AskUserStepName} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(
				"steps:\n  - name: "+name+"\n    run: echo a\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is reserved by type: agent")
		})
	}
}

// type: agent was called type: controller before the rename. Existing DAG files
// still using the old spelling must keep loading as agent DAGs.
func TestLegacyControllerTypeLoadsAsAgent(t *testing.T) {
	t.Parallel()

	const legacy = `
type: controller
llm:
  provider: anthropic
  model: claude-opus-5
steps:
  - name: design
    run: echo design
tasks:
  - name: ship
    description: done when design ran
`

	dag, err := spec.LoadYAML(t.Context(), []byte(legacy))
	require.NoError(t, err)

	assert.Equal(t, ir.TypeAgent, dag.Type)
	require.True(t, dag.IsAgent())
	require.NotNil(t, dag.AgentStep(), "an agent DAG carries a synthesized agent step")

	warnings := spec.DeprecatedSyntaxWarnings([]byte(legacy))
	assert.Contains(t, warnings, "Deprecated DAG syntax: type: controller is deprecated; use type: agent")
}
