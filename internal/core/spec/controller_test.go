// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/dagucloud/dagu/internal/runtime/builtin"
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
		assert.Equal(t, core.ExecutorTypeController, ctrl.ExecutorConfig.Type)
		assert.Same(t, dag.LLM, ctrl.LLM)
	})

	t.Run("ActionsTolerateFailureSoTheControllerCanRecover", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(t.Context(), []byte(valid))
		require.NoError(t, err)

		for _, step := range dag.Steps {
			if step.Name == core.ControllerStepName {
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
			errContains: "is reserved for the controller step",
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
