// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestStepIDPropertyAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		yaml           string
		expectedStatus core.Status
		expectedOutput map[string]any
	}{
		{
			name: "BasicStdout/StderrFileAccess",
			yaml: fmt.Sprintf(`
type: graph
steps:
  - id: gen
    run: |
%s
    output: GEN_OUTPUT

  - depends:
      - gen
    run: |
%s
    output: FILE_PATHS
`, indentScript(test.JoinLines(
				test.Output("Test output data"),
				test.Stderr("Error message"),
			), 6), indentScript(test.JoinLines(
				test.Output("stdout_file=${gen.stdout}"),
				test.Output("stderr_file=${gen.stderr}"),
			), 6)),
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"GEN_OUTPUT": "Test output data",
				// FILE_PATHS will contain file paths which include .out and .err
				"FILE_PATHS": []test.Contains{
					test.Contains(".out"),
					test.Contains(".err"),
				},
			},
		},
		{
			name: "ExitCodeAccess",
			yaml: `
type: graph
steps:
  - id: success
    run: exit 0

  - id: failure
    run: exit 42
    continue_on:
      failure: true

  - depends:
      - success
      - failure
    run: |
      echo "success_code=${success.exitCode}"
      echo "failure_code=${failure.exitCode}"
    output: EXIT_CODES
`,
			expectedStatus: core.PartiallySucceeded,
			expectedOutput: map[string]any{
				"EXIT_CODES": "success_code=0\nfailure_code=42",
			},
		},
		{
			name: "UnknownStepIDRemainsUnchanged",
			yaml: fmt.Sprintf(`
type: graph
steps:
  - id: first_step
    run: echo "Hello"
    output: FIRST_OUT

  - depends:
      - first_step
    run: |
%s
    output: RESULT
`, indentScript(test.JoinLines(
				test.Output("known=${first_step.stdout}"),
				test.Output("unknown=${unknown_step.stdout}"),
				test.Output("invalid=${first_step.unknown_property}"),
			), 6)),
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"FIRST_OUT": "Hello",
				"RESULT": []test.Contains{
					test.Contains("known="),
					test.Contains(".out"),
					test.Contains("${unknown_step.stdout}"),
					test.Contains("${first_step.unknown_property}"),
				},
			},
		},
		{
			name: "RegularVariableTakesPrecedenceOverStepID",
			yaml: `
type: graph
steps:
  - id: check
    run: echo '{"status":"from-step"}'
    output: check

  - depends:
      - check
    run: |
      echo "variable=${check.status}"
      echo "stdout=${check.stdout}"
    output: PRECEDENCE_TEST
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"check": `{"status":"from-step"}`,
				"PRECEDENCE_TEST": []test.Contains{
					test.Contains("variable=from-step"),
					test.Contains("stdout="), // When variable exists, step properties are still accessible
					test.Contains(".out"),    // Should contain the stdout file path
				},
			},
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

func TestStepIDComplexScenarios(t *testing.T) {
	t.Parallel()

	t.Run("MultipleStepsWithSameOutput", func(t *testing.T) {
		th := test.Setup(t)

		yaml := `
type: graph
steps:
  - id: gen1
    run: echo "data from gen1"
    output: DATA

  - id: gen2
    run: echo "data from gen2"
    output: DATA

  - depends:
      - gen1
      - gen2
    run: |
      echo "gen1_output=data from gen1"
      echo "gen2_output=data from gen2"
      echo "current_DATA=${DATA}"
    output: RESULT
`
		testDAG := th.DAG(t, yaml)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		// Note: When multiple steps output to the same variable name,
		// the final value depends on the order of execution which may not be deterministic
		testDAG.AssertOutputs(t, map[string]any{
			"DATA": test.NotEmpty{}, // Either gen1 or gen2's value
			"RESULT": []test.Contains{
				test.Contains("gen1_output=data from gen1"),
				test.Contains("gen2_output=data from gen2"),
			},
		})
	})

	t.Run("ChainedStepReferences", func(t *testing.T) {
		th := test.Setup(t)

		yaml := `
steps:
  - id: s1
    run: echo "10"
    output: NUM

  - id: s2
    run: echo "15"
    output: NUM2

  - id: s3
    run: echo "20"
    output: NUM3

  - run: |
      echo "s1=10"
      echo "s2=15"
      echo "s3=20"
      echo "total=45"
    output: FINAL_RESULT
`
		testDAG := th.DAG(t, yaml)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)
		testDAG.AssertOutputs(t, map[string]any{
			"NUM":          "10",
			"NUM2":         "15",
			"NUM3":         "20",
			"FINAL_RESULT": "s1=10\ns2=15\ns3=20\ntotal=45",
		})
	})

	t.Run("StepIDInScript", func(t *testing.T) {
		th := test.Setup(t)

		yaml := `
type: graph
steps:
  - id: setup_step
    run: echo '{"env":"test","timeout":30}'
    output: CONFIG

  - depends:
      - setup_step
    run: |
      #!/bin/bash
      set -e

      # Access file paths
      echo "Setup logs available at: ${setup_step.stdout}"
    output: SCRIPT_OUTPUT
`
		testDAG := th.DAG(t, yaml)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		testDAG.AssertLatestStatus(t, core.Succeeded)

		testDAG.AssertOutputs(t, map[string]any{
			"CONFIG": `{"env":"test","timeout":30}`,
			"SCRIPT_OUTPUT": []test.Contains{
				test.Contains("Setup logs available at:"),
				test.Contains(".out"),
			},
		})
	})
}

func TestStepScopedOutputAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		yaml               string
		expectedStatus     core.Status
		expectedOutput     map[string]any
		expectedStepOutput map[string]string
	}{
		{
			name: "BasicOutputAccess",
			yaml: `
type: graph
steps:
  - id: extract_title
    output: RESULT
    run: |
      printf 'Quarterly Revenue'

  - id: extract_summary
    output: RESULT
    run: |
      printf 'Revenue grew 18 percent year over year.'

  - id: report
    depends: [extract_title, extract_summary]
    run: |
      printf 'Title: %s\nSummary: %s' "${extract_title.output}" "${extract_summary.output}"
    output: REPORT
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"REPORT": "Title: Quarterly Revenue\nSummary: Revenue grew 18 percent year over year.",
			},
		},
		{
			name: "EmptyOutputResolvesToEmptyString",
			yaml: `
type: graph
steps:
  - id: empty_step
    output: RESULT
    run: |
      printf ''

  - id: consumer
    depends: [empty_step]
    run: |
      printf 'got:[%s]' "${empty_step.output}"
    output: CONSUMED
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"CONSUMED": "got:[]",
			},
		},
		{
			name: "OutputVsStdout",
			yaml: `
type: graph
steps:
  - id: producer
    output: CAPTURED
    run: |
      printf 'captured value'

  - id: consumer
    depends: [producer]
    run: |
      printf 'output=%s\nstdout=%s' "${producer.output}" "${producer.stdout}"
    output: RESULT
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"CAPTURED": "captured value",
				"RESULT": []test.Contains{
					test.Contains("output=captured value"),
					test.Contains("stdout="),
					test.Contains(".out"), // stdout is a file path
				},
			},
		},
		{
			name: "OutputWithoutCapture",
			yaml: fmt.Sprintf(`
type: graph
steps:
  - id: no_output
    run: |
      printf 'hello'

  - id: consumer
    depends: [no_output]
    run: %q
    output: RESULT
`, test.Output("ref=${no_output.output}")),
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"RESULT": "ref=${no_output.output}",
			},
		},
		{
			name: "OutputPrecedenceOverJSONPath",
			yaml: fmt.Sprintf(`
type: graph
steps:
  - id: check
    output: check
    run: %q

  - id: consumer
    depends: [check]
    run: %q
    output: RESULT
`, test.Output(`{"output":"from-json"}`), test.Output("value=${check.output}")),
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"check":  `{"output":"from-json"}`,
				"RESULT": `value={"output":"from-json"}`,
			},
		},
		{
			name: "OutputSlicing",
			yaml: `
type: graph
steps:
  - id: producer
    output: DATA
    run: |
      printf 'hello world'

  - id: consumer
    depends: [producer]
    run: |
      printf 'sliced=%s' "${producer.output:0:5}"
    output: RESULT
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"DATA":   "hello world",
				"RESULT": "sliced=hello",
			},
		},
		{
			name: "NestedJSONOutputAccess",
			yaml: `
type: graph
steps:
  - id: build
    output: BUILD_JSON
    run: |
      printf '{"version":"v1.2.3","artifact":{"url":"https://example.test/release.tgz"}}'

  - id: consumer
    depends: [build]
    run: |
      printf 'version=%s\nartifact=%s' "${build.output.version}" "${build.output.artifact.url}"
    output: RESULT
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"BUILD_JSON": `{"version":"v1.2.3","artifact":{"url":"https://example.test/release.tgz"}}`,
				"RESULT":     "version=v1.2.3\nartifact=https://example.test/release.tgz",
			},
		},
		{
			name: "StructuredOutputFromStdout",
			yaml: `
type: graph
steps:
  - id: analyze
    run: |
      printf '{"version":"v1.2.3","artifact":{"url":"https://example.test/release.tgz"}}'
    output:
      version:
        from: stdout
        decode: json
        select: .version
      artifact:
        from: stdout
        decode: json
        select: .artifact

  - id: consumer
    depends: [analyze]
    run: |
      printf 'version=%s\nartifact=%s' "${analyze.output.version}" "${analyze.output.artifact.url}"
    output: RESULT
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"RESULT": "version=v1.2.3\nartifact=https://example.test/release.tgz",
			},
			expectedStepOutput: map[string]string{
				"analyze": `{"artifact":{"url":"https://example.test/release.tgz"},"version":"v1.2.3"}`,
			},
		},
		{
			name: "StructuredOutputPublishOnlyNoop",
			yaml: `
type: graph
steps:
  - id: build
    run: |
      printf '{"version":"v1.2.3","artifact":{"url":"https://example.test/release.tgz"}}'
    output: BUILD_JSON

  - id: publish
    depends: [build]
    output:
      version: "${build.output.version}"
      versionLabel: "ver - ${build.output.version}"
      artifact:
        url: "${build.output.artifact.url}"

  - id: consumer
    depends: [publish]
    run: |
      printf 'version=%s\nlabel=%s\nartifact=%s' "${publish.output.version}" "${publish.output.versionLabel}" "${publish.output.artifact.url}"
    output: RESULT
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"BUILD_JSON": `{"version":"v1.2.3","artifact":{"url":"https://example.test/release.tgz"}}`,
				"RESULT":     "version=v1.2.3\nlabel=ver - v1.2.3\nartifact=https://example.test/release.tgz",
			},
			expectedStepOutput: map[string]string{
				"publish": `{"artifact":{"url":"https://example.test/release.tgz"},"version":"v1.2.3","versionLabel":"ver - v1.2.3"}`,
			},
		},
		{
			name: "StructuredOutputFromFileAndStderr",
			yaml: `
type: graph
steps:
  - id: producer
    run: |
      #!/bin/sh
      printf '{"artifact":{"path":"build/report.md"}}' > meta.json
      printf '{"warning":"retry required"}' >&2
    output:
      artifactPath:
        from: file
        path: meta.json
        decode: json
        select: .artifact.path
      warning:
        from: stderr
        decode: json
        select: .warning

  - id: consumer
    depends: [producer]
    run: |
      printf 'artifact=%s\nwarning=%s' "${producer.output.artifactPath}" "${producer.output.warning}"
    output: RESULT
`,
			expectedStatus: core.Succeeded,
			expectedOutput: map[string]any{
				"RESULT": "artifact=build/report.md\nwarning=retry required",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)
			testDAG := th.DAG(t, tc.yaml)

			agent := testDAG.Agent()
			err := agent.Run(agent.Context)

			if tc.expectedStatus == core.Succeeded {
				require.NoError(t, err)
			}

			testDAG.AssertLatestStatus(t, tc.expectedStatus)

			if tc.expectedOutput != nil {
				testDAG.AssertOutputs(t, tc.expectedOutput)
			}

			if tc.expectedStepOutput != nil {
				status, statusErr := th.DAGRunMgr.GetLatestStatus(th.Context, testDAG.DAG)
				require.NoError(t, statusErr)

				for stepID, expected := range tc.expectedStepOutput {
					node := findNodeByStepID(t, status.Nodes, stepID)
					require.NotNil(t, node.OutputValue, "step %s should expose step-scoped output", stepID)
					require.JSONEq(t, expected, *node.OutputValue)
				}
			}
		})
	}
}

func findNodeByStepID(t *testing.T, nodes []*exec1.Node, stepID string) *exec1.Node {
	t.Helper()

	for _, node := range nodes {
		if node.Step.ID == stepID {
			return node
		}
	}

	t.Fatalf("step %s not found", stepID)
	return nil
}

func TestStepIDErrorCases(t *testing.T) {
	t.Parallel()

	t.Run("InvalidJSONPath", func(t *testing.T) {
		th := test.Setup(t)

		yaml := `
type: graph
steps:
  - id: gen
    run: echo "not json"
    output: DATA

  - depends:
      - gen
    run: |
      echo "data=not json"
    output: RESULT
`
		testDAG := th.DAG(t, yaml)

		agent := testDAG.Agent()
		require.NoError(t, agent.Run(agent.Context))

		// Should succeed but the invalid path should remain as-is
		testDAG.AssertLatestStatus(t, core.Succeeded)
		testDAG.AssertOutputs(t, map[string]any{
			"DATA":   "not json",
			"RESULT": "data=not json",
		})
	})

}
