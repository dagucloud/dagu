// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputerExtractAction(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
llm:
  provider: anthropic
  model: claude-opus-5
steps:
  - id: total
    action: computer.extract
    with:
      instruction: The invoice total in the open window
      timeout: 90s
      mode: generic
      schema:
        type: object
        properties:
          total:
            type: number
`))
	require.NoError(t, err)
	step := dag.Steps[0]

	assert.Equal(t, ir.ExecutorTypeComputer, step.ExecutorConfig.Type)
	assert.Equal(t, "generic", step.ExecutorConfig.Config["mode"])
	assert.Equal(t, []any{map[string]any{
		"extract": map[string]any{
			"instruction": "The invoice total in the open window",
			"schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"total": map[string]any{"type": "number"}},
			},
		},
		"timeout": "90s",
	}}, step.ExecutorConfig.Config["do"])

	require.NotNil(t, step.LLM, "the DAG llm block is inherited")
	assert.Equal(t, "claude-opus-5", step.LLM.Model)
	assert.Equal(t, []ir.StepOutputDeclaration{
		{Name: "total", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
	}, step.Outputs)
	require.NotNil(t, dag.Artifacts)
	assert.True(t, dag.Artifacts.Enabled, "screenshots are stored as artifacts")
}

func TestComputerRunAction(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - id: post
    action: computer.run
    with:
      llm:
        provider: openai
        model: gpt-5.6-sol
      variables:
        password: secret-value
      do:
        - launch: notepad.exe
        - act: Type %password% into the window
        - extract:
            instruction: The window title
            schema:
              type: object
              properties:
                title:
                  type: string
`))
	require.NoError(t, err)
	step := dag.Steps[0]

	assert.Equal(t, ir.ExecutorTypeComputer, step.ExecutorConfig.Type)
	assert.NotContains(t, step.ExecutorConfig.Config, "llm")
	require.NotNil(t, step.LLM)
	assert.Equal(t, "openai", step.LLM.Provider, "with.llm sets the step model")
	assert.Equal(t, []ir.StepOutputDeclaration{
		{Name: "title", Type: ir.StepDeclaredOutputTypeString, Source: ir.StepDeclaredOutputSourceCapture},
	}, step.Outputs)
}

func TestComputerActionErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "run without operations",
			yaml: `
steps:
  - action: computer.run
    with:
      mode: auto
`,
			want: "computer.run requires with.do",
		},
		{
			name: "extract without schema",
			yaml: `
steps:
  - action: computer.extract
    with:
      instruction: The title
`,
			want: "with.schema must be an object schema",
		},
		{
			name: "custom step type named computer",
			yaml: `
step_types:
  computer:
    type: command
    template:
      command: echo hi
steps:
  - type: computer
`,
			want: `definition name "computer" conflicts with a builtin action`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(tc.yaml))
			require.ErrorContains(t, err, tc.want)
		})
	}
}
