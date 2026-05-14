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

// I1: inline JSON Schema with defaults runs and produces correct env vars.
func TestIssue1182_InlineSchemaDefaultsRunSuccessfully(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1182-defaults.yaml", `
name: issue1182-defaults
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    start_date:
      type: string
    debug:
      type: boolean
      default: false
  required:
    - start_date
steps:
  - name: show-values
    run: echo "batch_size=$batch_size start_date=$start_date debug=$debug"
    output: VALUES
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", runID, "--params", "start_date=2026-01-15", dagFile},
		ExpectedOut: []string{"DAG run finished"},
	})

	status, outputs := readAttemptStatusAndOutputs(t, th, "issue1182-defaults", runID)
	require.Equal(t, core.Succeeded, status.Status)
	assert.Equal(t, "batch_size=10 start_date=2026-01-15 debug=false", outputs.Outputs["values"])
}

// I2: inline JSON Schema start fails when required param is missing.
func TestIssue1182_InlineSchemaMissingRequiredFails(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1182-required.yaml", `
name: issue1182-required
params:
  type: object
  properties:
    start_date:
      type: string
  required:
    - start_date
steps:
  - name: should-not-run
    run: echo "start_date=$start_date"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, dagFile},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start_date")

	_, lookupErr := th.DAGRunStore.FindAttempt(th.Context, exec1.NewDAGRunRef("issue1182-required", runID))
	require.ErrorIs(t, lookupErr, exec1.ErrDAGRunIDNotFound)
}

// I3: inline JSON Schema start fails when a value has the wrong type.
func TestIssue1182_InlineSchemaInvalidTypeFails(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1182-type-fail.yaml", `
name: issue1182-type-fail
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
steps:
  - name: should-not-run
    run: echo "batch_size=$batch_size"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, "--params", "batch_size=not-an-int", dagFile},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "batch_size")
}

// I4: inline JSON Schema with all defaults runs without any params provided.
func TestIssue1182_InlineSchemaAllDefaultsNoParamsRequired(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1182-all-defaults.yaml", `
name: issue1182-all-defaults
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
    debug:
      type: boolean
      default: false
steps:
  - name: show-values
    run: echo "batch_size=$batch_size debug=$debug"
    output: VALUES
  - name: params-json
    run: printenv DAGU_PARAMS_JSON
    output: PARAMS_JSON
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", runID, dagFile},
		ExpectedOut: []string{"DAG run finished"},
	})

	status, outputs := readAttemptStatusAndOutputs(t, th, "issue1182-all-defaults", runID)
	require.Equal(t, core.Succeeded, status.Status)
	assert.Equal(t, "batch_size=10 debug=false", outputs.Outputs["values"])
	assert.JSONEq(t, `{"batch_size":"10","debug":"false"}`, outputs.Outputs["paramsJson"])
}

// I5: inline JSON Schema positional parameter is rejected.
func TestIssue1182_InlineSchemaPositionalParamRejected(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1182-positional.yaml", `
name: issue1182-positional
params:
  type: object
  properties:
    batch_size:
      type: integer
      default: 10
steps:
  - name: show
    run: echo "batch_size=$batch_size"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, "--params", "50", dagFile},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positional")
}
