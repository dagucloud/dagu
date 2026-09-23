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

func TestBrowserExtractAction(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
llm:
  provider: anthropic
  model: claude-sonnet-5
steps:
  - id: hn
    action: browser.extract
    with:
      url: https://news.ycombinator.com
      instruction: The top stories
      timeout: 90s
      schema:
        type: object
        properties:
          stories:
            type: array
          count:
            type: integer
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	step := dag.Steps[0]

	assert.Equal(t, ir.ExecutorTypeBrowser, step.ExecutorConfig.Type)
	assert.Equal(t, "https://news.ycombinator.com", step.ExecutorConfig.Config["url"])
	assert.Equal(t, []any{map[string]any{
		"extract": map[string]any{
			"instruction": "The top stories",
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"stories": map[string]any{"type": "array"},
					"count":   map[string]any{"type": "integer"},
				},
			},
		},
		"timeout": "90s",
	}}, step.ExecutorConfig.Config["do"])
	assert.NotContains(t, step.ExecutorConfig.Config, "instruction")

	require.NotNil(t, step.LLM, "the DAG llm block is inherited")
	assert.Equal(t, "claude-sonnet-5", step.LLM.Model)

	assert.ElementsMatch(t, []ir.StepOutputDeclaration{
		{Name: "count", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
		{Name: "stories", Type: ir.StepDeclaredOutputTypeJSON, Source: ir.StepDeclaredOutputSourceCapture},
	}, step.Outputs)
	require.NotNil(t, dag.Artifacts)
	assert.True(t, dag.Artifacts.Enabled, "screenshots are stored as artifacts")
}

func TestBrowserRunAction(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
llm:
  provider: anthropic
  model: claude-sonnet-5
artifacts:
  enabled: false
steps:
  - id: invoice
    action: browser.run
    with:
      llm:
        provider: openai
        model: gpt-5
      url: https://portal.example.com
      do:
        - act: Sign in
        - extract:
            instruction: The latest invoice
            schema:
              type: object
              properties:
                invoice_number:
                  type: string
`))
	require.NoError(t, err)
	step := dag.Steps[0]

	assert.Equal(t, ir.ExecutorTypeBrowser, step.ExecutorConfig.Type)
	assert.NotContains(t, step.ExecutorConfig.Config, "llm")
	require.NotNil(t, step.LLM)
	assert.Equal(t, "openai", step.LLM.Provider, "with.llm replaces the DAG llm block")
	assert.Equal(t, "gpt-5", step.LLM.Model)
	assert.Equal(t, []ir.StepOutputDeclaration{
		{Name: "invoice_number", Type: ir.StepDeclaredOutputTypeString, Source: ir.StepDeclaredOutputSourceCapture},
	}, step.Outputs)
	require.NotNil(t, dag.Artifacts)
	assert.False(t, dag.Artifacts.Enabled, "an explicit opt-out still loads")
}

func TestBrowserActionErrors(t *testing.T) {
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
  - action: browser.run
    with:
      url: https://example.com
`,
			want: "browser.run requires with.do",
		},
		{
			name: "extract without schema",
			yaml: `
steps:
  - action: browser.extract
    with:
      url: https://example.com
      instruction: The title
`,
			want: "with.schema must be an object schema",
		},
		{
			name: "extract without url",
			yaml: `
steps:
  - action: browser.extract
    with:
      instruction: The title
      schema: {type: object}
`,
			want: "with.url",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(context.Background(), []byte(tc.yaml))
			require.ErrorContains(t, err, tc.want)
		})
	}
}
