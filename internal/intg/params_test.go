// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInlineParams_DefaultsAndJSONSerialization(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "inline-defaults.yaml", `
name: inline-defaults
params:
  - name: region
    default: us-east-1
    type: string
  - name: count
    default: 3
    type: integer
  - name: debug
    default: false
    type: boolean
steps:
  - name: shell-values
    run: echo "region=$region count=$count debug=$debug"
    output: SHELL_VALUES
  - name: params-json
    run: printenv DAGU_PARAMS_JSON
    output: PARAMS_JSON
  - name: params-json-compat
    run: printenv DAG_PARAMS_JSON
    output: PARAMS_JSON_COMPAT
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", runID, dagFile},
		ExpectedOut: []string{"DAG run finished"},
	})

	status, outputs := readAttemptStatusAndOutputs(t, th, "inline-defaults", runID)
	require.Equal(t, core.Succeeded, status.Status)
	assert.Equal(t, []string{"region=us-east-1", "count=3", "debug=false"}, status.ParamsList)

	require.Contains(t, outputs.Outputs, "shellValues")
	require.Contains(t, outputs.Outputs, "paramsJson")
	require.Contains(t, outputs.Outputs, "paramsJsonCompat")
	assert.Equal(t, "region=us-east-1 count=3 debug=false", outputs.Outputs["shellValues"])
	assert.JSONEq(t, `{"region":"us-east-1","count":"3","debug":"false"}`, outputs.Outputs["paramsJson"])
	assert.JSONEq(t, `{"region":"us-east-1","count":"3","debug":"false"}`, outputs.Outputs["paramsJsonCompat"])
	assert.JSONEq(t, `["region=us-east-1","count=3","debug=false"]`, outputs.Metadata.Params)
}

func TestInlineParams_StartFailsWhenRequiredMissing(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "inline-required.yaml", `
name: inline-required
params:
  - name: region
    type: string
    required: true
steps:
  - name: should-not-run
    run: echo "region=$region"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, dagFile},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "region")

	_, err = th.DAGRunStore.FindAttempt(th.Context, exec1.NewDAGRunRef("inline-required", runID))
	require.ErrorIs(t, err, exec1.ErrDAGRunIDNotFound)
}

func TestInlineParams_StartFailsOnInvalidTypedValue(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "inline-invalid.yaml", `
name: inline-invalid
params:
  - name: region
    type: string
    required: true
  - name: count
    type: integer
    required: true
steps:
  - name: should-not-run
    command: echo "region=$region count=$count"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			"--params", "region=us-west-2 count=abc",
			dagFile,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "count")
	require.Contains(t, err.Error(), "integer")

	_, err = th.DAGRunStore.FindAttempt(th.Context, exec1.NewDAGRunRef("inline-invalid", runID))
	require.ErrorIs(t, err, exec1.ErrDAGRunIDNotFound)
}

func TestInlineParams_LocalSubDAGRuntimeCoercion(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "inline-subdag.yaml", `
name: inline-subdag-parent
steps:
  - name: invoke-child
    action: dag.run
    with:
      dag: inline-subdag-child
      params: "region=us-west-2 count=5 debug=true"

---
name: inline-subdag-child
params:
  - name: region
    type: string
    enum: [us-east-1, us-west-2]
    required: true
  - name: count
    type: integer
    minimum: 1
    maximum: 10
    required: true
  - name: debug
    type: boolean
    required: true
steps:
  - name: shell-values
    command: echo "region=$region count=$count debug=$debug"
    output: SHELL_VALUES
  - name: params-json
    command: printenv DAGU_PARAMS_JSON
    output: PARAMS_JSON
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", runID, dagFile},
		ExpectedOut: []string{"DAG run finished"},
	})

	rootRef := exec1.NewDAGRunRef("inline-subdag-parent", runID)
	parentAttempt, err := th.DAGRunStore.FindAttempt(th.Context, rootRef)
	require.NoError(t, err)

	parentStatus, err := parentAttempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, parentStatus.Status)
	require.Len(t, parentStatus.Nodes, 1)
	require.Len(t, parentStatus.Nodes[0].SubRuns, 1)

	subRunID := parentStatus.Nodes[0].SubRuns[0].DAGRunID
	subStatus, subOutputs := readSubAttemptStatusAndOutputs(t, th, rootRef, subRunID)
	require.Equal(t, core.Succeeded, subStatus.Status)
	assert.Equal(t, []string{"region=us-west-2", "count=5", "debug=true"}, subStatus.ParamsList)

	require.Contains(t, subOutputs.Outputs, "shellValues")
	require.Contains(t, subOutputs.Outputs, "paramsJson")
	assert.Equal(t, "region=us-west-2 count=5 debug=true", subOutputs.Outputs["shellValues"])
	assert.JSONEq(t, `{"region":"us-west-2","count":"5","debug":"true"}`, subOutputs.Outputs["paramsJson"])
	assert.JSONEq(t, `["region=us-west-2","count=5","debug=true"]`, subOutputs.Metadata.Params)
}

func readAttemptStatusAndOutputs(t *testing.T, th test.Command, dagName, runID string) (*exec1.DAGRunStatus, *exec1.DAGRunOutputs) {
	t.Helper()

	attempt, err := th.DAGRunStore.FindAttempt(th.Context, exec1.NewDAGRunRef(dagName, runID))
	require.NoError(t, err)

	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)

	outputs, err := attempt.ReadOutputs(th.Context)
	require.NoError(t, err)

	return status, outputs
}

func readSubAttemptStatusAndOutputs(t *testing.T, th test.Command, rootRef exec1.DAGRunRef, subRunID string) (*exec1.DAGRunStatus, *exec1.DAGRunOutputs) {
	t.Helper()

	attempt, err := th.DAGRunStore.FindSubAttempt(th.Context, rootRef, subRunID)
	require.NoError(t, err)

	status, err := attempt.ReadStatus(th.Context)
	require.NoError(t, err)

	outputs, err := attempt.ReadOutputs(th.Context)
	require.NoError(t, err)

	return status, outputs
}
