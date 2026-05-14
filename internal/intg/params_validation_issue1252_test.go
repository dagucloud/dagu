// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIssue1252_InlineEnumValidation verifies the exact YAML from issue #1252:
// inline param definitions with type, enum, required, default, and description.
func TestIssue1252_InlineEnumValidation(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1252-enum.yaml", `
name: issue1252-enum
params:
  - name: ENVIRONMENT
    type: string
    enum: [dev, staging, prod]
    default: dev
    description: Deployment environment
    required: true
steps:
  - name: show-env
    run: echo "env=$ENVIRONMENT"
    output: ENV_VALUE
`)

	t.Run("valid enum value accepted", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        []string{"start", "--run-id", runID, "--params", "ENVIRONMENT=staging", dagFile},
			ExpectedOut: []string{"DAG run finished"},
		})
		status, outputs := readAttemptStatusAndOutputs(t, th, "issue1252-enum", runID)
		require.Equal(t, core.Succeeded, status.Status)
		assert.Equal(t, "env=staging", outputs.Outputs["envValue"])
	})

	t.Run("invalid enum value rejected", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id", runID, "--params", "ENVIRONMENT=production", dagFile},
		})
		require.Error(t, err)

		_, lookupErr := th.DAGRunStore.FindAttempt(th.Context, exec1.NewDAGRunRef("issue1252-enum", runID))
		require.ErrorIs(t, lookupErr, exec1.ErrDAGRunIDNotFound)
	})

	t.Run("default value used when param not explicitly provided", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        []string{"start", "--run-id", runID, dagFile},
			ExpectedOut: []string{"DAG run finished"},
		})
		status, outputs := readAttemptStatusAndOutputs(t, th, "issue1252-enum", runID)
		require.Equal(t, core.Succeeded, status.Status)
		assert.Equal(t, "env=dev", outputs.Outputs["envValue"])
	})
}

// TestIssue1252_MissingRequiredParamNoDefault verifies that a required param
// without a default value causes a validation failure.
func TestIssue1252_MissingRequiredParamNoDefault(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1252-required.yaml", `
name: issue1252-required
params:
  - name: REGION
    type: string
    required: true
steps:
  - name: show-region
    run: echo "$REGION"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, dagFile},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REGION")
}

// TestIssue1252_MultipleParamsMixedTypes verifies mixed-type params with various
// constraints are all validated correctly in a single DAG run.
func TestIssue1252_MultipleParamsMixedTypes(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1252-mixed.yaml", `
name: issue1252-mixed
params:
  - name: ENVIRONMENT
    type: string
    enum: [dev, staging, prod]
    required: true
  - name: REPLICAS
    type: integer
    minimum: 1
    maximum: 10
    default: 3
  - name: VERBOSE
    type: boolean
    default: false
steps:
  - name: show-all
    run: echo "env=$ENVIRONMENT replicas=$REPLICAS verbose=$VERBOSE"
    output: ALL_VALUES
`)

	t.Run("all valid params accepted", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        []string{"start", "--run-id", runID, "--params", "ENVIRONMENT=prod REPLICAS=5 VERBOSE=true", dagFile},
			ExpectedOut: []string{"DAG run finished"},
		})
		status, outputs := readAttemptStatusAndOutputs(t, th, "issue1252-mixed", runID)
		require.Equal(t, core.Succeeded, status.Status)
		assert.Equal(t, "env=prod replicas=5 verbose=true", outputs.Outputs["allValues"])
	})

	t.Run("integer out of range rejected", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id", runID, "--params", "ENVIRONMENT=dev REPLICAS=99", dagFile},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "REPLICAS")
	})

	t.Run("defaults used for optional params", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        []string{"start", "--run-id", runID, "--params", "ENVIRONMENT=dev", dagFile},
			ExpectedOut: []string{"DAG run finished"},
		})
		status, outputs := readAttemptStatusAndOutputs(t, th, "issue1252-mixed", runID)
		require.Equal(t, core.Succeeded, status.Status)
		assert.Equal(t, "env=dev replicas=3 verbose=false", outputs.Outputs["allValues"])
	})
}

// TestIssue1252_ExternalSchemaReference verifies params validation against an
// external JSON Schema file.
func TestIssue1252_ExternalSchemaReference(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	// Write schema file to a temp location accessible by the DAG
	schemaDir := t.TempDir()
	schemaPath := filepath.Join(schemaDir, "test-schema.json")
	schemaPathForYAML := filepath.ToSlash(schemaPath)
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{
  "type": "object",
  "properties": {
    "ENVIRONMENT": {
      "type": "string",
      "enum": ["dev", "staging", "prod"]
    },
    "REPLICAS": {
      "type": "integer",
      "minimum": 1,
      "maximum": 10,
      "default": 3
    }
  },
  "required": ["ENVIRONMENT"]
}`), 0o600))

	dagFile := th.CreateDAGFile(t, "issue1252-external.yaml", `
name: issue1252-external
params:
  schema: "`+schemaPathForYAML+`"
  values:
    ENVIRONMENT: staging
steps:
  - name: show-env
    run: echo "env=$ENVIRONMENT replicas=$REPLICAS"
    output: VALUES
`)

	t.Run("schema values accepted", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        []string{"start", "--run-id", runID, dagFile},
			ExpectedOut: []string{"DAG run finished"},
		})
		status, outputs := readAttemptStatusAndOutputs(t, th, "issue1252-external", runID)
		require.Equal(t, core.Succeeded, status.Status)
		assert.Equal(t, "env=staging replicas=3", outputs.Outputs["values"])
	})

	t.Run("override with invalid value rejected", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id", runID, "--params", "ENVIRONMENT=production", dagFile},
		})
		require.Error(t, err)
	})
}

func TestIssue1252_InlineSchemaReferenceMode(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "issue1252-inline-schema.yaml", `
name: issue1252-inline-schema
params:
  schema:
    type: object
    properties:
      ENVIRONMENT:
        type: string
        enum: [dev, staging, prod]
      REPLICAS:
        type: integer
        minimum: 1
        maximum: 10
        default: 3
    required: [ENVIRONMENT]
  values:
    ENVIRONMENT: staging
steps:
  - name: show-env
    run: echo "env=$ENVIRONMENT replicas=$REPLICAS"
    output: VALUES
`)

	t.Run("schema defaults applied", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args:        []string{"start", "--run-id", runID, dagFile},
			ExpectedOut: []string{"DAG run finished"},
		})
		status, outputs := readAttemptStatusAndOutputs(t, th, "issue1252-inline-schema", runID)
		require.Equal(t, core.Succeeded, status.Status)
		assert.Equal(t, "env=staging replicas=3", outputs.Outputs["values"])
	})

	t.Run("override with invalid value rejected", func(t *testing.T) {
		runID := uuid.Must(uuid.NewV7()).String()
		err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
			Args: []string{"start", "--run-id", runID, "--params", "ENVIRONMENT=production", dagFile},
		})
		require.Error(t, err)
	})
}
