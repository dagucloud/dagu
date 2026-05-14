// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestParams_RejectsUnknownNamedParam_InlineTyped(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "reject-unknown-inline.yaml", `
name: reject-unknown-inline
params:
  - name: region
    type: string
    required: true
steps:
  - name: echo
    run: echo "region=$region"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			"--params", "region=us-west-2 regoin=typo",
			dagFile,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "regoin")
}

func TestParams_AcceptsValidParams_InlineTyped(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "accept-valid-inline.yaml", `
name: accept-valid-inline
params:
  - name: region
    type: string
    required: true
steps:
  - name: echo
    run: echo "region=$region"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			"--params", "region=us-west-2",
			dagFile,
		},
		ExpectedOut: []string{"DAG run finished"},
	})
}

func TestParams_AcceptsAnythingWithNoParamsSection(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "no-params.yaml", `
name: no-params
steps:
  - name: echo
    run: echo hello
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			dagFile,
			"--",
			"foo=bar",
		},
		ExpectedOut: []string{"DAG run finished"},
	})
}

func TestParams_RejectsUnknownNamedParam_LegacyNamed(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "reject-unknown-legacy.yaml", `
name: reject-unknown-legacy
params:
  - region: us-east-1
steps:
  - name: echo
    run: echo "region=$region"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			"--params", "region=us-west-2 regoin=typo",
			dagFile,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "regoin")
}

func TestParams_RejectsUnknownViaJSON_InlineTyped(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "reject-json-unknown.yaml", `
name: reject-json-unknown
params:
  - name: region
    type: string
    required: true
steps:
  - name: echo
    run: echo "region=$region"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			"--params", `{"region":"us-west-2","regoin":"typo"}`,
			dagFile,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "regoin")
}

func TestParams_ExternalSchemaRejectsUnknown(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	schemaContent := `{
  "type": "object",
  "properties": {
    "region": { "type": "string" }
  },
  "required": ["region"],
  "additionalProperties": false
}`

	dagDir := filepath.Dir(th.CreateDAGFile(t, "dummy.yaml", ""))
	schemaPath := filepath.Join(dagDir, "params.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(schemaContent), 0o600))

	dagFile := th.CreateDAGFile(t, "external-reject.yaml", `
name: external-reject
params:
  schema: params.schema.json
steps:
  - name: echo
    run: echo "region=$region"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	err := th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			"--params", "region=us-west-2 regoin=typo",
			dagFile,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "regoin")
}

func TestParams_ExternalSchemaAllowsExtraWhenPermitted(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	schemaContent := `{
  "type": "object",
  "properties": {
    "region": { "type": "string" }
  },
  "required": ["region"],
  "additionalProperties": true
}`

	dagDir := filepath.Dir(th.CreateDAGFile(t, "dummy2.yaml", ""))
	schemaPath := filepath.Join(dagDir, "params-allow.schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(schemaContent), 0o600))

	dagFile := th.CreateDAGFile(t, "external-allow.yaml", `
name: external-allow
params:
  schema: params-allow.schema.json
steps:
  - name: echo
    run: echo "region=$region"
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args: []string{
			"start",
			"--run-id", runID,
			"--params", "region=us-west-2 extra=ok",
			dagFile,
		},
		ExpectedOut: []string{"DAG run finished"},
	})
}
