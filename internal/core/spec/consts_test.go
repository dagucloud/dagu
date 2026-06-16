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
		},
		{
			name: "RejectsUnknownConst",
			yaml: `
consts:
  - service: api
steps:
  - name: print
    run: echo ${consts.missing}
`,
			want: "unknown consts binding",
		},
		{
			name: "KeepsEvalReference",
			yaml: `
steps:
  - name: print
    run: echo ${DATA.image}
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte(tt.yaml))
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
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
