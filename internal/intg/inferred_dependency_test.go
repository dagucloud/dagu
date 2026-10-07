// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// A step-output reference orders the consumer after the producer without an
// explicit depends, so the reference resolves instead of preserving.
func TestInferredDependencyResolvesStepOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		yaml            string
		expectedOutputs map[string]any
	}{
		{
			name: "ForeachItems",
			yaml: `
steps:
  - id: read
    run: |
      printf 'rows=[{"a":1},{"a":2}]\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: rows
  - id: each
    foreach:
      items: ${steps.read.outputs.rows}
      as: row
      steps:
        - id: show
          run: echo "item=${foreach.row.a}"
          output: ITEM
      collect:
        item: ${show.output}
    output: RESULTS
`,
			expectedOutputs: map[string]any{
				"RESULTS": []test.Contains{
					test.Contains(`"total":2`),
					test.Contains(`"item":"item=1"`),
					test.Contains(`"item":"item=2"`),
				},
			},
		},
		{
			name: "CommandEnv",
			yaml: `
steps:
  - id: build
    run: |
      printf 'image=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
  - id: deploy
    env:
      - IMAGE=${steps.build.outputs.image}
    run: echo "deploy $IMAGE"
    output: DEPLOYED
`,
			expectedOutputs: map[string]any{"DEPLOYED": "deploy v1.2.3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)
			testDAG := th.DAG(t, tc.yaml)
			agent := testDAG.Agent()
			agent.RunSuccess(t)

			testDAG.AssertLatestStatus(t, ir.Succeeded)
			testDAG.AssertOutputs(t, tc.expectedOutputs)
		})
	}
}
