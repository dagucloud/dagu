// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadYAMLWithResultReturnsValueReferenceNotices(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
name: notices
consts:
  - image: ${consts.missing}
steps:
  - run: echo ok
`))

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	require.Len(t, result.ValueReferenceNotices, 1)

	got := result.ValueReferenceNotices[0]
	assert.Equal(t, "consts.image", got.FieldPath)
	assert.Equal(t, "${consts.missing}", got.Token)
	assert.Contains(t, got.Message, "was left unchanged")
}

func TestLoadYAMLWithResultInspectsWorkflowFieldsForValueReferenceNotices(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
name: notices
params:
  - name: environment
    required: true
steps:
  - run: echo ${params.environment}
`))

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	require.Len(t, result.ValueReferenceNotices, 1)

	got := result.ValueReferenceNotices[0]
	assert.Equal(t, "steps[0].run", got.FieldPath)
	assert.Equal(t, "${params.environment}", got.Token)
	assert.Contains(t, got.Message, "was left unchanged")
}

func TestLoadYAMLWithResultPreservesMapEnvOrder(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
working_dir: .
env:
  SERVICE: api
  HOST: ${env.SERVICE}.internal
steps:
  - run: echo ok
`))

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	assert.Equal(t, []string{"SERVICE=api", "HOST=api.internal"}, result.DAG.Env)
	assert.Empty(t, result.ValueReferenceNotices)
}

func TestLoadYAMLWithResultReportsEnvNoticesWithEarlierEnvScope(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
working_dir: .
env:
  - SERVICE=api
  - HOST=${env.SERVICE}.internal
  - SELF=${env.SELF}
  - LATER=$AFTER
  - AFTER=done
steps:
  - run: echo ok
`), spec.WithoutEval())

	require.NoError(t, err)
	require.NotNil(t, result.DAG)

	tokens := make([]string, 0, len(result.ValueReferenceNotices))
	for _, notice := range result.ValueReferenceNotices {
		tokens = append(tokens, notice.Token)
	}
	assert.NotContains(t, tokens, "${env.SERVICE}")
	assert.Contains(t, tokens, "${env.SELF}")
	assert.Contains(t, tokens, "$AFTER")
}

func TestLoadYAMLWithResultUsesDAGEnvScopeForWorkflowNoticePass(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
working_dir: .
env:
  - SERVICE=api
steps:
  - run: |
      printf '%s %s\n' "$SERVICE" "${env.SERVICE}"
`), spec.WithoutEval())

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	assert.Empty(t, result.ValueReferenceNotices)
}

func TestLoadYAMLWithResultReportsStepOutputReferenceReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		yaml       string
		wantField  string
		wantToken  string
		wantReason cmnvalue.ValueReferenceNoticeReason
	}{
		{
			name: "MissingDependency",
			yaml: `
steps:
  - id: build
    run: printf 'image=v1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
  - id: deploy
    run: echo ${steps.build.outputs.image}
`,
			wantField:  "steps[1].run",
			wantToken:  "${steps.build.outputs.image}",
			wantReason: cmnvalue.ValueReferenceReasonMissingDependency,
		},
		{
			name: "UnknownStepID",
			yaml: `
steps:
  - id: deploy
    run: echo ${steps.build.outputs.image}
`,
			wantField:  "steps[0].run",
			wantToken:  "${steps.build.outputs.image}",
			wantReason: cmnvalue.ValueReferenceReasonUnknownStepID,
		},
		{
			name: "SelfReference",
			yaml: `
steps:
  - id: build
    run: echo ${steps.build.outputs.image}
    outputs:
      - name: image
`,
			wantField:  "steps[0].run",
			wantToken:  "${steps.build.outputs.image}",
			wantReason: cmnvalue.ValueReferenceReasonSelfReference,
		},
		{
			name: "UnknownOutputName",
			yaml: `
steps:
  - id: build
    run: printf 'image=v1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
  - id: deploy
    depends: build
    run: echo ${steps.build.outputs.tag}
`,
			wantField:  "steps[1].run",
			wantToken:  "${steps.build.outputs.tag}",
			wantReason: cmnvalue.ValueReferenceReasonUnknownOutputName,
		},
		{
			name: "NoTopLevelOutputsContract",
			yaml: `
steps:
  - id: build
    run: echo legacy
  - id: deploy
    depends: build
    run: echo ${steps.build.outputs.image}
`,
			wantField:  "steps[1].run",
			wantToken:  "${steps.build.outputs.image}",
			wantReason: cmnvalue.ValueReferenceReasonUnknownOutputName,
		},
		{
			name: "NamespaceUnavailableRootEnv",
			yaml: `
env:
  IMAGE: ${steps.build.outputs.image}
steps:
  - id: build
    run: printf 'image=v1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
`,
			wantField:  "env[0]",
			wantToken:  "${steps.build.outputs.image}",
			wantReason: cmnvalue.ValueReferenceReasonNamespaceUnavailable,
		},
		{
			name: "NamespaceUnavailableHandler",
			yaml: `
steps:
  - id: build
    run: printf 'image=v1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
handler_on:
  success:
    run: echo ${steps.build.outputs.image}
`,
			wantField:  "handler_on.success.run",
			wantToken:  "${steps.build.outputs.image}",
			wantReason: cmnvalue.ValueReferenceReasonNamespaceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := spec.LoadYAMLWithResult(context.Background(), []byte(tt.yaml), spec.WithoutEval())
			require.NoError(t, err)
			require.NotNil(t, result.DAG)
			require.Len(t, result.ValueReferenceNotices, 1)

			got := result.ValueReferenceNotices[0]
			assert.Equal(t, tt.wantField, got.FieldPath)
			assert.Equal(t, tt.wantToken, got.Token)
			assert.Equal(t, tt.wantReason, got.Reason)
		})
	}
}

func TestLoadYAMLWithResultLeavesValidEscapedAndUnsupportedStepOutputTextQuiet(t *testing.T) {
	t.Parallel()

	result, err := spec.LoadYAMLWithResult(context.Background(), []byte(`
steps:
  - id: build
    run: printf 'image=v1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
  - id: deploy
    depends: build
    run: |
      echo ${steps.build.outputs.image}
      echo \${steps.build.outputs.image}
      echo ${steps.build.outputs.meta.tag}
      echo ${steps.build-step.outputs.image}
      echo ${build.output.image}
      echo ${step.xxx.foo}
`), spec.WithoutEval())

	require.NoError(t, err)
	require.NotNil(t, result.DAG)
	assert.Empty(t, result.ValueReferenceNotices)
}
