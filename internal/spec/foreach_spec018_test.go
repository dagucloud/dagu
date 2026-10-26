// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForeachSpec018LoadsModel(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(`
steps:
  - id: summarize
    foreach:
      items: [alpha, beta]
      as: episode
      key: ${foreach.episode}
      max_concurrent: 2
      steps:
        - id: write
          run: echo ${foreach.episode}
          outputs:
            - name: value
      collect:
        value: ${steps.write.outputs.value}
    output: FOREACH_RESULTS
`)), spec.SkipSchemaValidation(), spec.WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)

	step := dag.Steps[0]
	require.NotNil(t, step.Foreach)
	assert.Equal(t, ir.ExecutorTypeForeach, step.ExecutorConfig.Type)
	assert.Equal(t, "episode", step.Foreach.As)
	assert.Equal(t, "${foreach.episode}", step.Foreach.Key)
	assert.Equal(t, 2, step.Foreach.MaxConcurrent)
	assert.Len(t, step.Foreach.Items, 2)
	assert.Len(t, step.Foreach.Steps, 1)
	assert.Equal(t, "write", step.Foreach.Steps[0].ID)
	assert.Equal(t, map[string]string{"value": "${steps.write.outputs.value}"}, step.Foreach.Collect)
}

func TestForeachSpec018ItemScopeReferencesDoNotEmitStaticNotices(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(strings.TrimSpace(`
steps:
  - id: summarize
    foreach:
      items: [{slug: alpha, url: https://example.test/alpha}]
      as: episode
      key: ${foreach.episode.slug}
      steps:
        - id: write
          env:
            - EPISODE_URL=${foreach.episode.url}
          run: echo ${foreach.episode.url}
      collect:
        slug: ${foreach.episode.slug}
`)))

	require.NoError(t, err)
	require.Empty(t, result.ValueReferenceNotices)
}

func TestForeachSpec018StepEnvIsInScope(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(strings.TrimSpace(`
steps:
  - id: loop
    foreach:
      items: [one]
      steps:
        - id: call
          env:
            - MY_ENDPOINT=https://example.internal/api
          action: http.request
          with:
            method: GET
            url: ${env.MY_ENDPOINT}
`)))

	require.NoError(t, err)
	require.Empty(t, result.ValueReferenceNotices)
}

// Text that only resembles an item field without the foreach namespace keeps
// loading: shell variables, names the DAG defines for ${name.path} lookup,
// escaped text, and scripts passed to their executor as written.
func TestForeachSpec018AcceptsItemLikeReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "shell variables",
			yaml: `
steps:
  - foreach:
      items: [one]
      steps:
        - run: for item in a b; do echo "${item}" $item.txt; done`,
		},
		{
			name: "param",
			yaml: `
params:
  - item: '{"wallet":"A"}'
steps:
  - foreach:
      items: [one]
      steps:
        - run: echo "${item.wallet}"`,
		},
		{
			name: "env",
			yaml: `
env:
  - item: '{"wallet":"A"}'
steps:
  - foreach:
      items: [one]
      steps:
        - run: echo "${item.wallet}"`,
		},
		{
			name: "output variable",
			yaml: `
steps:
  - foreach:
      items: [one]
      steps:
        - id: plan
          run: echo '{"wallet":"A"}'
          output: item
        - depends: [plan]
          run: echo "${item.wallet}"`,
		},
		{
			name: "parallel item",
			yaml: `
steps:
  - foreach:
      items: [one]
      as: ITEM
      steps:
        - action: dag.run
          with:
            dag: child
            params:
              account: ${ITEM.account}
          parallel:
            items:
              - account: a`,
		},
		{
			name: "escaped",
			yaml: `
steps:
  - foreach:
      items: [one]
      steps:
        - action: js.run
          with:
            input: '\${item.wallet}'
            script: return input;`,
		},
		{
			name: "verbatim script",
			yaml: `
steps:
  - foreach:
      items: [{wallet: A}]
      steps:
        - action: js.run
          with:
            input: ${foreach.item}
            script: |
              const item = input;
              return ` + "`${item.wallet}`" + `;`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(tt.yaml)),
				spec.SkipSchemaValidation(), spec.WithoutEval())
			require.NoError(t, err)
		})
	}
}

func TestForeachSpec018Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantParts []string
	}{
		{
			name: "rejects mixed run selector",
			body: `
    run: echo no
    foreach:
      items: [one]
      steps:
        - run: echo ${foreach.item}`,
			wantParts: []string{"foreach", "run"},
		},
		{
			name: "requires items",
			body: `
    foreach:
      steps:
        - run: echo ${foreach.item}`,
			wantParts: []string{"foreach.items"},
		},
		{
			name: "requires non empty steps",
			body: `
    foreach:
      items: [one]
      steps: []`,
			wantParts: []string{"foreach.steps"},
		},
		{
			name: "rejects reserved item alias",
			body: `
    foreach:
      items: [one]
      as: key
      steps:
        - run: echo ${foreach.key}`,
			wantParts: []string{"foreach.as", "key"},
		},
		{
			name: "rejects non string collect value",
			body: `
    foreach:
      items: [one]
      steps:
        - run: echo ${foreach.item}
      collect:
        value: 1`,
			wantParts: []string{"foreach.collect", "string"},
		},
		{
			name: "rejects item field without foreach namespace",
			body: `
    foreach:
      items: [{wallet: A}]
      steps:
        - action: js.run
          with:
            input:
              wallet: ${item.wallet}
            script: return input.wallet;`,
			wantParts: []string{
				"steps[0].foreach.steps[0].with.input.wallet",
				"${item.wallet} does not reference the foreach item; use ${foreach.item.wallet}",
			},
		},
		{
			name: "rejects aliased item field in item scope fields",
			body: `
    foreach:
      items: [{id: a}]
      as: row
      key: ${row.id}
      steps:
        - run: echo "${row.id}"
      collect:
        id: ${row.id}`,
			wantParts: []string{
				"steps[0].foreach.key",
				"steps[0].foreach.steps[0].run",
				"steps[0].foreach.collect.id",
				"use ${foreach.row.id}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(strings.TrimSpace(`
steps:
  - name: each
`+tt.body)), spec.SkipSchemaValidation(), spec.WithoutEval())
			require.Error(t, err)
			for _, part := range tt.wantParts {
				require.Contains(t, err.Error(), part)
			}
		})
	}
}
