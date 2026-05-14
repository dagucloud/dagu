// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/test"
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
