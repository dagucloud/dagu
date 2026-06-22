// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
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
	assert.Equal(t, core.ExecutorTypeForeach, step.ExecutorConfig.Type)
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
          run: echo ${foreach.episode.url}
      collect:
        slug: ${foreach.episode.slug}
`)))

	require.NoError(t, err)
	require.Empty(t, result.ValueReferenceNotices)
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
