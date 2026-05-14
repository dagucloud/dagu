// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/test"
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

func TestEnqueueCommand_RequiresDAGDefinition(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)

	err := th.RunCommandWithError(t, cmd.Enqueue(), test.CmdTest{
		Args: []string{"enqueue"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires at least 1 arg")
}
