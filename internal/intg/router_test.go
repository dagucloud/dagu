// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestRouterExecutor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		dagYAML         string
		expectedStatus  core.Status
		expectedOutputs map[string]any
	}{
		{
			name: "ExactMatch",
			dagYAML: `
type: graph
env:
  - INPUT: exact_value
steps:
  - name: router
    action: router.route
    with:
      value: ${INPUT}
      routes:
        "exact_value": [route_a]
        "other": [route_b]

  - name: route_a
    run: echo "Route A executed"
    output: RESULT_A

  - name: route_b
    run: echo "Route B executed"
    output: RESULT_B
`,
			expectedStatus: core.Succeeded,
			expectedOutputs: map[string]any{
				"RESULT_A": "Route A executed",
			},
		},
		{
			name: "RegexMatch",
			dagYAML: `
type: graph
env:
  - INPUT: apple_pie
steps:
  - name: router
    action: router.route
    with:
      value: ${INPUT}
      routes:
        "re:^apple.*": [route_a]
        "re:^banana.*": [route_b]

  - name: route_a
    run: echo "Apple route"
    output: RESULT_A

  - name: route_b
    run: echo "Banana route"
    output: RESULT_B
`,
			expectedStatus: core.Succeeded,
			expectedOutputs: map[string]any{
				"RESULT_A": "Apple route",
			},
		},
		{
			name: "CatchAllRoute",
			dagYAML: `
type: graph
env:
  - INPUT: unknown_value
steps:
  - name: router
    action: router.route
    with:
      value: ${INPUT}
      routes:
        "specific": [route_a]
        "re:.*": [default_route]

  - name: route_a
    run: echo "Specific route"
    output: RESULT_A

  - name: default_route
    run: echo "Default route"
    output: RESULT_DEFAULT
`,
			expectedStatus: core.Succeeded,
			expectedOutputs: map[string]any{
				"RESULT_DEFAULT": "Default route",
			},
		},
		{
			name: "MultipleTargetsPerRoute",
			dagYAML: `
type: graph
env:
  - INPUT: trigger
steps:
  - name: router
    action: router.route
    with:
      value: ${INPUT}
      routes:
        "trigger": [step_a, step_b]

  - name: step_a
    run: echo "Step A"
    output: RESULT_A

  - name: step_b
    run: echo "Step B"
    output: RESULT_B

  - name: step_c
    run: echo "Step C"
    output: RESULT_C
    depends:
      - step_a
`,
			expectedStatus: core.Succeeded,
			expectedOutputs: map[string]any{
				"RESULT_A": "Step A",
				"RESULT_B": "Step B",
				"RESULT_C": "Step C",
			},
		},
		{
			name: "MultipleMatchingRoutes",
			dagYAML: `
type: graph
env:
  - INPUT: success_code
steps:
  - name: router
    action: router.route
    with:
      value: ${INPUT}
      routes:
        "re:^success.*": [handle_success]
        "re:.*_code$": [handle_code]
        "re:.*": [catch_all]

  - name: handle_success
    run: echo "Success handler"
    output: SUCCESS

  - name: handle_code
    run: echo "Code handler"
    output: CODE

  - name: catch_all
    run: echo "Catch all"
    output: CATCH_ALL
`,
			expectedStatus: core.Succeeded,
			expectedOutputs: map[string]any{
				"SUCCESS":   "Success handler",
				"CODE":      "Code handler",
				"CATCH_ALL": "Catch all",
			},
		},
		{
			name: "NoMatchingRoute",
			dagYAML: `
type: graph
env:
  - INPUT: no_match
steps:
  - name: router
    action: router.route
    with:
      value: ${INPUT}
      routes:
        "specific_value": [route_a]

  - name: route_a
    run: echo "Route A"
    output: RESULT_A

  - name: always_runs
    run: echo "Always runs"
    output: ALWAYS
    depends:
      - router
`,
			expectedStatus: core.Succeeded,
			expectedOutputs: map[string]any{
				"ALWAYS": "Always runs",
			},
		},
		{
			name: "RouterWithEnvVarExpansion",
			dagYAML: `
type: graph
env:
  - STATUS: production
steps:
  - name: router
    action: router.route
    with:
      value: ${STATUS}
      routes:
        "production": [prod_handler]
        "staging": [staging_handler]

  - name: prod_handler
    run: echo "Production"
    output: ENV

  - name: staging_handler
    run: echo "Staging"
    output: ENV
`,
			expectedStatus: core.Succeeded,
			expectedOutputs: map[string]any{
				"ENV": "Production",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)
			dag := th.DAG(t, tc.dagYAML)
			agent := dag.Agent()
			agent.RunSuccess(t)

			dag.AssertLatestStatus(t, tc.expectedStatus)
			if tc.expectedOutputs != nil {
				dag.AssertOutputs(t, tc.expectedOutputs)
			}
		})
	}
}

func TestRouterComplexScenarios(t *testing.T) {
	t.Parallel()

	t.Run("ChainedRouters", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
env:
  - CATEGORY: electronics
  - SUBCATEGORY: phone
steps:
  - name: category_router
    action: router.route
    with:
      value: ${CATEGORY}
      routes:
        "electronics": [electronics_router]
        "clothing": [clothing_handler]

  - name: electronics_router
    action: router.route
    with:
      value: ${SUBCATEGORY}
      routes:
        "phone": [phone_handler]
        "laptop": [laptop_handler]

  - name: phone_handler
    run: echo "Phone"
    output: RESULT

  - name: laptop_handler
    run: echo "Laptop"
    output: RESULT

  - name: clothing_handler
    run: echo "Clothing"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "Phone",
		})

		// Verify correct routing path: electronics -> phone
		status := agent.Status(agent.Context)
		for _, node := range status.Nodes {
			switch node.Step.Name {
			case "category_router", "electronics_router", "phone_handler":
				require.Equal(t, core.NodeSucceeded, node.Status, "%s should succeed", node.Step.Name)
			case "laptop_handler", "clothing_handler":
				require.Equal(t, core.NodeSkipped, node.Status, "%s should be skipped", node.Step.Name)
			}
		}
	})

	t.Run("BranchesWithMultipleSteps", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
env:
  - MODE: premium
steps:
  - name: router
    action: router.route
    with:
      value: ${MODE}
      routes:
        "premium": [premium_step1]
        "standard": [standard_step1]

  # Premium branch: 3 steps
  - name: premium_step1
    run: echo "Premium-1"
    output: P1

  - name: premium_step2
    run: echo "Premium-2 with ${P1}"
    output: P2
    depends: [premium_step1]

  - name: premium_step3
    run: echo "Premium-3 with ${P2}"
    output: FINAL
    depends: [premium_step2]

  # Standard branch: 2 steps
  - name: standard_step1
    run: echo "Standard-1"
    output: S1

  - name: standard_step2
    run: echo "Standard-2 with ${S1}"
    output: FINAL
    depends: [standard_step1]
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		// Premium branch executed
		dag.AssertOutputs(t, map[string]any{
			"P1": "Premium-1",
			"P2": "Premium-2 with Premium-1",
		})

		// Verify standard branch was skipped
		status := agent.Status(agent.Context)
		for _, node := range status.Nodes {
			switch node.Step.Name {
			case "router", "premium_step1", "premium_step2", "premium_step3":
				require.Equal(t, core.NodeSucceeded, node.Status, "%s should succeed", node.Step.Name)
			case "standard_step1":
				require.Equal(t, core.NodeSkipped, node.Status, "%s should be skipped", node.Step.Name)
			}
		}
	})

	t.Run("ComplexDAGTopology", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
env:
  - TRIGGER: all
steps:
  - name: setup
    run: echo "Setup complete"
    output: SETUP

  - name: router
    action: router.route
    with:
      value: ${TRIGGER}
      routes:
        "all": [branch_a, branch_b, branch_c]
    depends: [setup]

  # Three parallel branches
  - name: branch_a
    run: echo "A:${SETUP}"
    output: OUT_A

  - name: branch_b
    run: echo "B:${SETUP}"
    output: OUT_B

  - name: branch_c
    run: echo "C:${SETUP}"
    output: OUT_C

  # Fan-in: aggregator waits for all branches
  - name: aggregator
    run: echo "Aggregated"
    output: AGGREGATED
    depends: [branch_a, branch_b, branch_c]

  - name: final
    run: echo "Final"
    output: FINAL
    depends: [aggregator]
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		// Verify all steps succeeded
		status := agent.Status(agent.Context)
		for _, node := range status.Nodes {
			switch node.Step.Name {
			case "setup", "router", "branch_a", "branch_b", "branch_c", "aggregator", "final":
				require.Equal(t, core.NodeSucceeded, node.Status, "%s should succeed", node.Step.Name)
			}
		}

		// All branches and fan-in executed since TRIGGER=all
		dag.AssertOutputs(t, map[string]any{
			"SETUP":      "Setup complete",
			"OUT_A":      "A:Setup complete",
			"OUT_B":      "B:Setup complete",
			"OUT_C":      "C:Setup complete",
			"AGGREGATED": "Aggregated",
			"FINAL":      "Final",
		})
	})

	t.Run("RouterWithStepOutput", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
steps:
  - name: check_status
    run: echo "success"
    output: STATUS

  - name: router
    action: router.route
    with:
      value: ${STATUS}
      routes:
        "success": [success_handler]
        "failure": [failure_handler]
    depends: [check_status]

  - name: success_handler
    run: echo "Handling success"
    output: RESULT

  - name: failure_handler
    run: echo "Handling failure"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"STATUS": "success",
			"RESULT": "Handling success",
		})

		// Verify correct routing based on step output
		status := agent.Status(agent.Context)
		for _, node := range status.Nodes {
			switch node.Step.Name {
			case "check_status", "router", "success_handler":
				require.Equal(t, core.NodeSucceeded, node.Status, "%s should succeed", node.Step.Name)
			case "failure_handler":
				require.Equal(t, core.NodeSkipped, node.Status, "%s should be skipped", node.Step.Name)
			}
		}
	})
}

func TestRouterStepStatus(t *testing.T) {
	t.Parallel()

	t.Run("SkippedStepsHaveCorrectStatus", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
env:
  - INPUT: route_a
steps:
  - name: router
    action: router.route
    with:
      value: ${INPUT}
      routes:
        "route_a": [step_a]
        "route_b": [step_b]

  - name: step_a
    run: echo "A"

  - name: step_b
    run: echo "B"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		// Verify the status
		status := agent.Status(agent.Context)
		require.Equal(t, core.Succeeded, status.Status)

		// Check individual node statuses
		for _, node := range status.Nodes {
			switch node.Step.Name {
			case "router":
				require.Equal(t, core.NodeSucceeded, node.Status, "router should succeed")
			case "step_a":
				require.Equal(t, core.NodeSucceeded, node.Status, "step_a should succeed")
			case "step_b":
				require.Equal(t, core.NodeSkipped, node.Status, "step_b should be skipped")
			}
		}
	})
}

func TestRouterValidation(t *testing.T) {
	t.Parallel()

	t.Run("DuplicateTargetValidation", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)

		// Write DAG file manually to test validation error
		dagContent := `
type: graph
env:
  - MODE: full
steps:
  - name: router
    action: router.route
    with:
      value: ${MODE}
      routes:
        "full": [process_a, process_b]
        "minimal": [process_a]

  - name: process_a
    run: echo "A"

  - name: process_b
    run: echo "B"
`
		dagFile := th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "duplicate_target_test.yaml", []byte(dagContent))

		_, err := spec.Load(th.Context, dagFile)
		require.Error(t, err)
		require.Contains(t, err.Error(), "targeted by multiple routes")
	})
}
