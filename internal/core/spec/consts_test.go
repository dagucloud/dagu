// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstsListFormResolvesEarlierConsts(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
consts:
  - service: api
  - enabled: true
  - replicas: 3
  - endpoint: https://example.test/${consts.service}/${consts.enabled}/${consts.replicas}
steps:
  - name: print
    run: echo ${consts.endpoint}
`))
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"service":  "api",
		"enabled":  true,
		"replicas": uint64(3),
		"endpoint": "https://example.test/api/true/3",
	}, dag.Consts)
}

func TestConstsPreservesUnavailableReferences(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
consts:
  - self: ${consts.self}
  - later: ${consts.after}
  - after: ready
  - unknown: ${consts.missing}
  - from_env: ${env.VALUE_RESOLUTION_CONST_LOAD_ENV}
  - from_params: ${params.name}
  - from_step: ${steps.build.outputs.image}
steps:
  - name: build
    run: echo ok
`))
	require.NoError(t, err)

	assert.Equal(t, "${consts.self}", dag.Consts["self"])
	assert.Equal(t, "${consts.after}", dag.Consts["later"])
	assert.Equal(t, "ready", dag.Consts["after"])
	assert.Equal(t, "${consts.missing}", dag.Consts["unknown"])
	assert.Equal(t, "${env.VALUE_RESOLUTION_CONST_LOAD_ENV}", dag.Consts["from_env"])
	assert.Equal(t, "${params.name}", dag.Consts["from_params"])
	assert.Equal(t, "${steps.build.outputs.image}", dag.Consts["from_step"])
}

func TestConstsTreatUnsupportedSyntaxAsOrdinaryContent(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
consts:
  - shorthand: $consts.service
  - malformed: ${params...}
  - dotted: ${consts.service.name}
steps:
  - name: print
    run: echo ok
`))
	require.NoError(t, err)

	assert.Equal(t, "$consts.service", dag.Consts["shorthand"])
	assert.Equal(t, "${params...}", dag.Consts["malformed"])
	assert.Equal(t, "${consts.service.name}", dag.Consts["dotted"])
}

func TestConstsRejectsMappingForm(t *testing.T) {
	t.Parallel()

	_, err := spec.LoadYAML(context.Background(), []byte(`
consts:
  service: api
steps:
  - name: print
    run: echo ok
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consts")
	assert.Contains(t, err.Error(), "list")
}

func TestConstsRejectsNullValue(t *testing.T) {
	t.Parallel()

	_, err := spec.LoadYAML(context.Background(), []byte(`
consts:
  - service:
steps:
  - name: print
    run: echo ok
`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consts.service")
	assert.Contains(t, err.Error(), "literal string, number, or boolean")
}

func TestReservedBindingsValidateRunFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "KeepsReservedShorthand",
			yaml: `
consts:
  - service: api
steps:
  - name: print
    run: echo $consts.service
`,
			want: "echo $consts.service",
		},
		{
			name: "PreservesUnknownConst",
			yaml: `
consts:
  - service: api
steps:
  - name: print
    run: echo ${consts.missing}
`,
			want: "echo ${consts.missing}",
		},
		{
			name: "KeepsEvalReference",
			yaml: `
steps:
  - name: print
    run: echo ${DATA.image}
`,
			want: "echo ${DATA.image}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			require.NoError(t, err)
			require.Len(t, dag.Steps, 1)
			assert.Equal(t, tt.want, dag.Steps[0].Script)
		})
	}
}

func TestConstsInheritFromBaseConfig(t *testing.T) {
	t.Parallel()

	t.Run("BaseConstResolvesEnv", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(
			context.Background(),
			[]byte(`
env:
  - "SERVICE=${consts.service}"
steps:
  - name: print
    run: echo ${consts.service}
`),
			spec.WithBaseConfigContent([]byte(`
consts:
  - service: base-api
`)),
		)
		require.NoError(t, err)
		assert.Equal(t, "SERVICE=base-api", dag.Env[0])
		assert.Equal(t, "base-api", dag.Consts["service"])
	})

	t.Run("LocalConstOverridesBase", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(
			context.Background(),
			[]byte(`
consts:
  - service: local-api
env:
  - "SERVICE=${consts.service}"
steps:
  - name: print
    run: echo ${consts.service}
`),
			spec.WithBaseConfigContent([]byte(`
consts:
  - service: base-api
  - region: us-east-1
`)),
		)
		require.NoError(t, err)
		assert.Equal(t, "SERVICE=local-api", dag.Env[0])
		assert.Equal(t, "local-api", dag.Consts["service"])
		assert.Equal(t, "us-east-1", dag.Consts["region"])
	})

	t.Run("LocalConstReferencesBaseConst", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAML(
			context.Background(),
			[]byte(`
consts:
  - endpoint: https://${consts.host}/v1
env:
  - "ENDPOINT=${consts.endpoint}"
steps:
  - name: print
    run: echo ${consts.endpoint}
`),
			spec.WithBaseConfigContent([]byte(`
consts:
  - host: api.example.test
`)),
		)
		require.NoError(t, err)
		assert.Equal(t, "ENDPOINT=https://api.example.test/v1", dag.Env[0])
		assert.Equal(t, "https://api.example.test/v1", dag.Consts["endpoint"])
	})
}
