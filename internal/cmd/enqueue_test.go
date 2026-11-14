// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestEnqueueCommand(t *testing.T) {
	th := test.SetupCommand(t)

	dagEnqueue := th.DAG(t, `steps:
  - name: "1"
    run: "true"
`)

	dagEnqueueWithParams := th.DAG(t, `params: "p1 p2"
steps:
  - name: "1"
    run: "echo \"params is $1 and $2\""
`)

	tests := []test.CmdTest{
		{
			Name:        "Enqueue",
			Args:        []string{"enqueue", dagEnqueue.Location},
			ExpectedOut: []string{"Enqueued"},
		},
		{
			Name:        "EnqueueWithParams",
			Args:        []string{"enqueue", `--params="p3 p4"`, dagEnqueueWithParams.Location},
			ExpectedOut: []string{`params="[1=p3 2=p4]"`},
		},
		{
			Name:        "StartDAGWithParamsAfterDash",
			Args:        []string{"enqueue", dagEnqueueWithParams.Location, "--", "p5", "p6"},
			ExpectedOut: []string{`params="[1=p5 2=p6`},
		},
		{
			Name:        "EnqueueWithDAGRunID",
			Args:        []string{"enqueue", `--run-id="test-dag-run"`, dagEnqueue.Location},
			ExpectedOut: []string{"test-dag-run"},
		},
		{
			Name:        "EnqueueWithQueueOverride",
			Args:        []string{"enqueue", `--queue="custom-queue"`, dagEnqueue.Location},
			ExpectedOut: []string{"Enqueued"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			th.RunCommand(t, cmd.Enqueue(), tc)
		})
	}
}

// TestEnqueueCommand_StdinParams replaces the process-global os.Stdin, so it
// must stay sequential to avoid feeding other commands.
func TestEnqueueCommand_StdinParams(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "Positional", input: "s1 s2\n", want: "1=s1 2=s2"},
		{name: "Quoted", input: `"hello world"`, want: "1=hello world 2=p2"},
		{name: "EmptyValue", input: `""`, want: "1= 2=p2"},
		{name: "QuotedSpaces", input: `" hello world "`, want: "1= hello world  2=p2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			th := test.SetupCommand(t)
			dag := th.DAG(t, `params: "p1 p2"
steps:
  - name: "1"
    run: "echo \"params is $1 and $2\""
`)

			pipeCommandStdin(t, tt.input)

			th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
				Args: []string{"enqueue", "--params-stdin", dag.Location},
			})
			status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
			require.NoError(t, err)
			require.Equal(t, ir.Queued, status.Status)
			require.Equal(t, tt.want, status.Params)
		})
	}
}

func TestEnqueueCommand_RequiresDAGDefinition(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	err := th.RunCommandWithError(t, cmd.Enqueue(), test.CmdTest{
		Args: []string{"enqueue"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires at least 1 arg")
}
