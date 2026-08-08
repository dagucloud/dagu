// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

func TestStepOutputsDeclarationBuilds(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
name: test
steps:
  - id: build
    run: echo ok
    outputs:
      - name: image_tag
      - name: metadata
        type: json
`), spec.WithoutEval())
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	require.Equal(t, []ir.StepOutputDeclaration{
		{Name: "image_tag", Type: ir.StepDeclaredOutputTypeString},
		{Name: "metadata", Type: ir.StepDeclaredOutputTypeJSON},
	}, dag.Steps[0].Outputs)
}

func TestStepOutputsDeclarationValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		yaml    string
		message string
	}{
		{
			name: "null",
			yaml: `
name: test
steps:
  - id: build
    run: echo ok
    outputs: null
`,
			message: "outputs must be a non-empty sequence",
		},
		{
			name: "empty",
			yaml: `
name: test
steps:
  - id: build
    run: echo ok
    outputs: []
`,
			message: "outputs must be a non-empty sequence",
		},
		{
			name: "missing id",
			yaml: `
name: test
steps:
  - name: build
    run: echo ok
    outputs:
      - name: image_tag
`,
			message: "a step with outputs must define id",
		},
		{
			name: "missing name",
			yaml: `
name: test
steps:
  - id: build
    run: echo ok
    outputs:
      - type: string
`,
			message: "name is required",
		},
		{
			name: "invalid name",
			yaml: `
name: test
steps:
  - id: build
    run: echo ok
    outputs:
      - name: 1invalid
`,
			message: "name must match",
		},
		{
			name: "duplicate name",
			yaml: `
name: test
steps:
  - id: build
    run: echo ok
    outputs:
      - name: image_tag
      - name: image_tag
`,
			message: "duplicate output name",
		},
		{
			name: "unknown field",
			yaml: `
name: test
steps:
  - id: build
    run: echo ok
    outputs:
      - name: image_tag
        value: latest
`,
			message: `unknown field "value"`,
		},
		{
			name: "invalid type",
			yaml: `
name: test
steps:
  - id: build
    run: echo ok
    outputs:
      - name: image_tag
        type: yaml
`,
			message: `type must be "string" or "json"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(
				context.Background(),
				[]byte(tc.yaml),
				spec.WithoutEval(),
			)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.message)
		})
	}
}
