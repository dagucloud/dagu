// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestDryCommand(t *testing.T) {
	th := test.SetupCommand(t)

	dagBasic := th.DAG(t, `steps:
  - name: "1"
    run: "true"
`)

	dagWithParams := th.DAG(t, `params: "p1 p2"
steps:
  - name: "1"
    run: 'echo "params is $1 and $2"'
`)

	dagMissingShell := th.DAG(t, `steps:
  - name: check
    run: "true"
    with:
      shell: dagu-test-missing-shell-9f3c2b1a
`)

	dagMissingCommand := th.DAG(t, `steps:
  - name: check
    run: dagu-test-missing-command-9f3c2b1a --flag
    with:
      shell: direct
`)

	tests := []test.CmdTest{
		{
			Name:        "DryRunDAG",
			Args:        []string{"dry", dagBasic.Location},
			ExpectedOut: []string{"Dry-run completed"},
		},
		{
			Name:        "DryRunDAGWithParams",
			Args:        []string{"dry", dagWithParams.Location, "--params", "p3 p4"},
			ExpectedOut: []string{`[1=p3 2=p4]`},
		},
		{
			Name:        "DryRunDAGWithParamsAfterDash",
			Args:        []string{"dry", dagWithParams.Location, "--", "p5", "p6"},
			ExpectedOut: []string{`[1=p5 2=p6`},
		},
		{
			// A missing executable is reported but does not fail the dry run.
			Name:        "MissingShellWarns",
			Args:        []string{"dry", dagMissingShell.Location},
			ExpectedOut: []string{"Dry run: step may fail on this host", "dagu-test-missing-shell-9f3c2b1a"},
		},
		{
			Name:        "MissingCommandWarns",
			Args:        []string{"dry", dagMissingCommand.Location},
			ExpectedOut: []string{"Dry run: step may fail on this host", "dagu-test-missing-command-9f3c2b1a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			th.RunCommand(t, cmd.Dry(), tc)
		})
	}
}

func TestDryCommand_InvalidDependency(t *testing.T) {
	th := test.SetupCommand(t)

	dagFile := th.CreateDAGFile(t, "invalid.yaml", `
type: graph
steps:
  - run: echo A
  - name: "b"
    run: echo B
    depends: ["missing_step"]
`)

	err := th.RunCommandWithError(t, cmd.Dry(), test.CmdTest{
		Args: []string{"dry", dagFile},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "depends on non-existent step")
}

func TestDryCommand_RejectsFormat(t *testing.T) {
	th := test.SetupCommand(t)
	dag := th.DAG(t, `steps:
  - run: "true"
`)
	err := th.RunCommandWithError(t, cmd.Dry(), test.CmdTest{
		Args: []string{"dry", dag.Location, "--format", "yaml"},
	})
	require.ErrorContains(t, err, `--format "yaml": use text or json`)
}
