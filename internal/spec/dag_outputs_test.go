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

const outputsSteps = `
steps:
  - id: diagnose
    run: echo ok
    outputs:
      - name: summary
      - name: findings
        type: json
  - id: loop
    foreach:
      items: [a]
      steps:
        - id: body
          run: echo ok
          outputs:
            - name: x
  - id: child
    call: other
handler_on:
  failure:
    id: onfail
    run: echo failed
    outputs:
      - name: x
`

func TestDAGOutputsBuild(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
name: test
outputs:
  summary: ${diagnose.outputs.summary}
  findings: ${steps.diagnose.outputs.findings}
  dyn: ${child.outputs.anything}
`+outputsSteps), spec.WithoutEval())
	require.NoError(t, err)
	require.Equal(t, map[string]ir.DAGOutput{
		"summary":  {Step: "diagnose", Output: "summary"},
		"findings": {Step: "diagnose", Output: "findings"},
		"dyn":      {Step: "child", Output: "anything"},
	}, dag.Outputs)
}

func TestDAGOutputsAbsent(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte("name: test\n"+outputsSteps), spec.WithoutEval())
	require.NoError(t, err)
	require.Nil(t, dag.Outputs)
}

func TestDAGOutputsNoInferredDepends(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
name: test
outputs:
  summary: ${diagnose.outputs.summary}
steps:
  - id: other
    run: echo ok
  - id: diagnose
    run: echo ok
    outputs:
      - name: summary
`), spec.WithoutEval())
	require.NoError(t, err)
	for _, step := range dag.Steps {
		require.Empty(t, step.Depends, step.ID)
		require.Empty(t, step.InferredDepends, step.ID)
	}
}

func TestDAGOutputsInvalid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		outputs string
		message string
	}{
		{"bad name", "  1bad: ${diagnose.outputs.summary}", "outputs.1bad"},
		{"dash name", "  bad-name: ${diagnose.outputs.summary}", "outputs.bad-name"},
		{"literal", "  summary: hello", "exactly one"},
		{"number", "  summary: 3", "must be a string"},
		{"combined", "  summary: 'x ${diagnose.outputs.summary}'", "exactly one"},
		{"two refs", "  summary: '${diagnose.outputs.summary}${diagnose.outputs.summary}'", "exactly one"},
		{"escaped", `  summary: '\${diagnose.outputs.summary}'`, "exactly one"},
		{"nested path", "  summary: ${steps.diagnose.outputs.findings.n}", "exactly one"},
		{"missing step", "  summary: ${nope.outputs.summary}", `step "nope" not found`},
		{"undeclared", "  summary: ${diagnose.outputs.nope}", `does not declare output "nope"`},
		{"foreach body", "  summary: ${body.outputs.x}", `step "body" not found`},
		{"handler", "  summary: ${onfail.outputs.x}", `step "onfail" not found`},
		{"not a mapping", "  - ${diagnose.outputs.summary}", "must be a mapping"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(context.Background(), []byte("name: test\noutputs:\n"+tc.outputs+"\n"+outputsSteps), spec.WithoutEval())
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.message)
		})
	}
}
