// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestPartialSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		yaml           string
		expectedStatus core.Status
		expectedOutput map[string]any
	}{
		{
			name: "BasicPartialSuccess",
			yaml: `
steps:
  - name: fail-step
    run: exit 1
    continue_on:
      failure: true

  - name: success-step
    run: echo "This step should run even if the previous one fails"
`,
			expectedStatus: core.PartiallySucceeded,
		},
		{
			name: "SuccessByMarkingStepAsSuccessful",
			yaml: `
steps:
  - name: fail-step
    run: exit 1
    continue_on:
      failure: true
      mark_success: true

  - name: success-step
    run: echo "This step should run even if the previous one fails"
`,
			expectedStatus: core.Succeeded,
		},
		{
			name: "SingleStepWithContinueOnFailure",
			yaml: `
steps:
  - name: fail-step
    run: exit 1
    continue_on:
      failure: true
`,
			expectedStatus: core.Failed,
		},
		{
			name: "SingleStepWithContinueOnMarkingAsSuccess",
			yaml: `
steps:
  - name: fail-step
    run: exit 1
    continue_on:
      failure: true
      mark_success: true
`,
			expectedStatus: core.Succeeded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)

			// Create DAG from YAML
			testDAG := th.DAG(t, tc.yaml)

			// Run the DAG
			agent := testDAG.Agent()
			err := agent.Run(agent.Context)

			if tc.expectedStatus == core.Succeeded {
				require.NoError(t, err)
			}

			// Check status
			testDAG.AssertLatestStatus(t, tc.expectedStatus)

			// Check outputs
			if tc.expectedOutput != nil {
				testDAG.AssertOutputs(t, tc.expectedOutput)
			}
		})
	}
}
