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

func TestReservedBindingsValidateRunFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "RejectsReservedShorthand",
			yaml: `
consts:
  - service: api
steps:
  - name: print
    run: echo $consts.service
`,
			want: "invalid binding shorthand",
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
			name: "KeepsLegacyReferenceCompatible",
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
