// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func outputsTestParallel(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "windows" || !raceEnabled() {
		t.Parallel()
	}
}

func TestLargeOutput_128KB(t *testing.T) {
	th := test.Setup(t)

	// Load DAG that reads a 128KB file
	textFilePath := test.TestdataPath(t, "integration/large-output-128kb.txt")
	dagSpec := `steps:
  - name: read-128kb-file
    run: ` + fmt.Sprintf("cat %s", test.PosixQuote(textFilePath)) + `
    output: OUTPUT_128KB
`
	if runtime.GOOS == "windows" {
		dagSpec = fmt.Sprintf(`steps:
  - name: read-128kb-file
    run: cmd /d /c type %s
    output: OUTPUT_128KB
`, `"`+textFilePath+`"`)
	}
	dag := th.DAG(t, dagSpec)
	agent := dag.Agent()

	// Run with timeout to detect hanging
	ctx, cancel := context.WithTimeout(agent.Context, intgTestTimeout(45*time.Second))
	defer cancel()

	err := agent.Run(ctx)
	require.NoError(t, err, "DAG should complete without hanging with large output")

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Get the latest status
	dagRunStatus, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.NotNil(t, dagRunStatus)

	// Verify the step completed successfully
	require.Len(t, dagRunStatus.Nodes, 1)
	assert.Equal(t, core.NodeSucceeded, dagRunStatus.Nodes[0].Status)
	assert.Equal(t, "read-128kb-file", dagRunStatus.Nodes[0].Step.Name)
}

type outputsCollectionCase struct {
	dagYAML         string
	runFunc         func(*testing.T, context.Context, *test.Agent)
	validateFunc    func(*testing.T, exec.DAGRunStatus)
	validateOutputs func(*testing.T, map[string]string)
}

type namedOutputsCollectionCase struct {
	name string
	outputsCollectionCase
}

var outputsCollectionCases = []namedOutputsCollectionCase{
	{
		name: "SimpleStringOutput",
		outputsCollectionCase: outputsCollectionCase{
			dagYAML: `
steps:
  - name: produce-output
    run: echo "RESULT=42"
    output: RESULT
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
				require.Len(t, status.Nodes, 1)
				require.Equal(t, core.NodeSucceeded, status.Nodes[0].Status)
			},
			validateOutputs: func(t *testing.T, outputs map[string]string) {
				require.NotNil(t, outputs)
				assert.Equal(t, "RESULT=42", outputs["result"])
			},
		},
	},
	{
		name: "StructuredObjectOutputNotCollected",
		outputsCollectionCase: outputsCollectionCase{
			dagYAML: `
steps:
  - id: publish
    output:
      version: v1.2.3
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
				require.Len(t, status.Nodes, 1)
			},
			validateOutputs: func(t *testing.T, outputs map[string]string) {
				assert.Nil(t, outputs)
			},
		},
	},
	{
		name: "MultipleStepsWithOutputs",
		outputsCollectionCase: outputsCollectionCase{
			dagYAML: `
steps:
  - name: step1
    run: echo "COUNT=10"
    output: COUNT

  - name: step2
    run: echo "TOTAL=100"
    output: TOTAL

  - name: step3
    run: echo "STATUS=completed"
    output: STATUS
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
				require.Len(t, status.Nodes, 3)
			},
			validateOutputs: func(t *testing.T, outputs map[string]string) {
				require.NotNil(t, outputs)
				assert.Len(t, outputs, 3)
				assert.Equal(t, "COUNT=10", outputs["count"])
				assert.Equal(t, "TOTAL=100", outputs["total"])
				assert.Equal(t, "STATUS=completed", outputs["status"])
			},
		},
	},
	{
		name: "LastOneWinsForDuplicateKeys",
		outputsCollectionCase: outputsCollectionCase{
			dagYAML: `
type: graph
steps:
  - name: step1
    run: echo "VALUE=first"
    output: VALUE

  - name: step2
    depends: [step1]
    run: echo "VALUE=second"
    output: VALUE
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
			},
			validateOutputs: func(t *testing.T, outputs map[string]string) {
				require.NotNil(t, outputs)
				assert.Equal(t, "VALUE=second", outputs["value"])
			},
		},
	},
	{
		name: "NoOutputsProduced",
		outputsCollectionCase: outputsCollectionCase{
			dagYAML: `
steps:
  - name: step1
    run: echo "hello"
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
			},
			validateOutputs: func(t *testing.T, outputs map[string]string) {
				assert.Nil(t, outputs)
			},
		},
	},
	{
		name: "OutputWithDollarPrefix",
		outputsCollectionCase: outputsCollectionCase{
			dagYAML: `
steps:
  - name: step1
    run: echo "MY_VAR=value123"
    output: $MY_VAR
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
			},
			validateOutputs: func(t *testing.T, outputs map[string]string) {
				require.NotNil(t, outputs)
				assert.Equal(t, "MY_VAR=value123", outputs["myVar"])
			},
		},
	},
	{
		name: "MixedStringAndStructuredOutputs",
		outputsCollectionCase: outputsCollectionCase{
			dagYAML: `
steps:
  - name: simple
    run: echo "SIMPLE_OUT=simple_value"
    output: SIMPLE_OUT

  - id: publish
    output:
      version: v1.2.3
`,
			runFunc: func(t *testing.T, _ context.Context, agent *test.Agent) {
				agent.RunSuccess(t)
			},
			validateFunc: func(t *testing.T, status exec.DAGRunStatus) {
				require.Equal(t, core.Succeeded, status.Status)
			},
			validateOutputs: func(t *testing.T, outputs map[string]string) {
				require.NotNil(t, outputs)
				assert.Len(t, outputs, 1)
				assert.Equal(t, "SIMPLE_OUT=simple_value", outputs["simpleOut"])
			},
		},
	},
}

func runOutputsCollectionCase(t *testing.T, tc outputsCollectionCase) {
	t.Helper()
	outputsTestParallel(t)

	th := test.Setup(t)
	dag := th.DAG(t, tc.dagYAML)
	agent := dag.Agent()

	tc.runFunc(t, agent.Context, agent)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	tc.validateFunc(t, status)

	outputs := readOutputsFile(t, th, dag.DAG)
	tc.validateOutputs(t, outputs)
}

func TestOutputsCollection(t *testing.T) {
	for _, tc := range outputsCollectionCases {
		t.Run(tc.name, func(t *testing.T) {
			runOutputsCollectionCase(t, tc.outputsCollectionCase)
		})
	}
}

func TestOutputsCollection_FailedDAG(t *testing.T) {
	outputsTestParallel(t)

	th := test.Setup(t)
	dag := th.DAG(t, `
type: graph
steps:
  - name: step1
    run: echo "BEFORE_FAIL=collected"
    output: BEFORE_FAIL

  - name: step2
    depends: [step1]
    run: exit 1

  - name: step3
    depends: [step2]
    run: echo "AFTER_FAIL=not_collected"
    output: AFTER_FAIL
`)
	agent := dag.Agent()

	err := agent.Run(agent.Context)
	require.Error(t, err)

	status := agent.Status(agent.Context)
	require.Equal(t, core.Failed, status.Status)

	// Outputs from successful steps should still be collected
	outputs := readOutputsFile(t, th, dag.DAG)
	require.NotNil(t, outputs)
	assert.Equal(t, "BEFORE_FAIL=collected", outputs["beforeFail"])
	_, hasAfterFail := outputs["afterFail"]
	assert.False(t, hasAfterFail, "output from step after failure should not be collected")
}

func runOutputsCollectionCamelCaseConversion(t *testing.T, envVarName, expectedKey, expectedValue string) {
	outputsTestParallel(t)

	th := test.Setup(t)
	dag := th.DAG(t, `
steps:
  - name: step1
    run: echo "`+envVarName+`=test_value"
    output: `+envVarName+`
`)
	agent := dag.Agent()
	agent.RunSuccess(t)

	outputs := readOutputsFile(t, th, dag.DAG)
	require.NotNil(t, outputs)
	assert.Equal(t, expectedValue, outputs[expectedKey])
}

func TestOutputsCollection_CamelCaseConversion_Simple(t *testing.T) {
	runOutputsCollectionCamelCaseConversion(t, "SIMPLE", "simple", "SIMPLE=test_value")
}

func TestOutputsCollection_CamelCaseConversion_TwoWords(t *testing.T) {
	runOutputsCollectionCamelCaseConversion(t, "TWO_WORDS", "twoWords", "TWO_WORDS=test_value")
}

func TestOutputsCollection_CamelCaseConversion_MultipleWordName(t *testing.T) {
	runOutputsCollectionCamelCaseConversion(t, "MULTIPLE_WORD_NAME", "multipleWordName", "MULTIPLE_WORD_NAME=test_value")
}

func TestOutputsCollection_CamelCaseConversion_AlreadyCamelCase(t *testing.T) {
	runOutputsCollectionCamelCaseConversion(t, "ALREADY_CAMEL_Case", "alreadyCamelCase", "ALREADY_CAMEL_Case=test_value")
}

func TestOutputsCollection_SecretsMasked(t *testing.T) {
	outputsTestParallel(t)

	th := test.Setup(t)

	// Create a temporary secret file
	secretValue := "super-secret-api-token-xyz123"
	secretFile := th.TempFile(t, "secret.txt", []byte(secretValue))

	dag := th.DAG(t, `
secrets:
  - name: API_TOKEN
    provider: file
    key: `+secretFile+`

steps:
  - name: output-secret
    run: echo "TOKEN=${API_TOKEN}"
    output: TOKEN
`)
	agent := dag.Agent()
	agent.RunSuccess(t)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)

	// Read outputs.json
	outputs := readOutputsFile(t, th, dag.DAG)
	require.NotNil(t, outputs)

	// The output value should contain the masked secret, not the actual value
	tokenOutput := outputs["token"]
	require.NotEmpty(t, tokenOutput)
	assert.NotContains(t, tokenOutput, secretValue, "secret value should be masked in outputs")
	assert.Contains(t, tokenOutput, "*******", "masked placeholder should appear in outputs")
}

func TestOutputsCollection_MetadataIncluded(t *testing.T) {
	outputsTestParallel(t)

	th := test.Setup(t)
	dag := th.DAG(t, `
steps:
  - name: step1
    run: echo "RESULT=42"
    output: RESULT
`)
	agent := dag.Agent()
	agent.RunSuccess(t)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)

	// Read full outputs including metadata
	fullOutputs := readFullOutputsFile(t, th, dag.DAG)
	require.NotNil(t, fullOutputs)

	// Validate metadata
	assert.Equal(t, dag.Name, fullOutputs.Metadata.DAGName)
	assert.Equal(t, status.DAGRunID, fullOutputs.Metadata.DAGRunID)
	assert.NotEmpty(t, fullOutputs.Metadata.AttemptID)
	assert.Equal(t, "succeeded", fullOutputs.Metadata.Status)
	assert.NotEmpty(t, fullOutputs.Metadata.CompletedAt)

	// Validate outputs are still present
	assert.Equal(t, "RESULT=42", fullOutputs.Outputs["result"])
}

// readOutputsFile reads the outputs.json file for a given DAG run
// Returns just the outputs map for backward compatibility with existing tests
func readOutputsFile(t *testing.T, th test.Helper, dag *core.DAG) map[string]string {
	t.Helper()

	fullOutputs := readFullOutputsFile(t, th, dag)
	if fullOutputs == nil {
		return nil
	}
	return fullOutputs.Outputs
}

// readFullOutputsFile reads the full outputs.json file including metadata
func readFullOutputsFile(t *testing.T, th test.Helper, dag *core.DAG) *exec.DAGRunOutputs {
	t.Helper()

	// Find the attempt directory
	dagRunsDir := th.Config.Paths.DAGRunsDir
	dagRunDir := filepath.Join(dagRunsDir, dag.Name, "dag-runs")

	// Walk to find the outputs.json file
	var outputsPath string
	_ = filepath.Walk(dagRunDir, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.Name() == filedagrun.OutputsFile {
			outputsPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if outputsPath == "" {
		return nil
	}

	data, err := os.ReadFile(outputsPath)
	require.NoError(t, err)

	var outputs exec.DAGRunOutputs
	require.NoError(t, json.Unmarshal(data, &outputs))

	// Return nil if old format (no metadata)
	require.NotEmpty(t, outputs.Metadata.DAGRunID)
	return &outputs
}
