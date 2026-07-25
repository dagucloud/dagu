// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestHumanTask_RootRunResumesOnDistributedWorker(t *testing.T) {
	f := newTestFixture(t, `
worker_selector:
  type: test-worker
steps:
  - id: review
    action: human.task
    with:
      prompt: Select the deployment environment
      form:
        type: object
        properties:
          environment:
            type: string
        required: [environment]
  - id: deploy
    depends: review
`+test.ForOS("    shell: /bin/sh\n", "    shell: powershell\n")+`    command: `+test.Output("environment=${steps.review.outputs.environment}")+`
`, withLabels(map[string]string{"type": "test-worker"}), withLogPersistence())
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	waiting := f.waitForStatus(core.Waiting, 20*time.Second)
	require.NotEmpty(t, waiting.FinishedAt)
	require.Equal(t, "worker-1", waiting.WorkerID)
	require.Equal(t, core.NodeWaiting, waiting.Nodes[0].Status)

	root := &cobra.Command{Use: "root"}
	root.AddCommand(cmd.HumanTask())
	root.SetArgs(test.WithConfigFlag([]string{
		"human-task",
		"complete",
		"--run-id=" + waiting.DAGRunID,
		"--step=review",
		"--input=environment=production",
		f.dagWrapper.Name,
	}, f.coord.Config))
	require.NoError(t, root.ExecuteContext(f.coord.Context))

	completed := f.waitForStatus(core.Succeeded, 20*time.Second)
	require.Len(t, completed.Nodes, 2)
	require.Equal(t, core.NodeSucceeded, completed.Nodes[0].Status)
	require.JSONEq(t, `{"environment":"production"}`, string(completed.Nodes[0].HumanTaskInput))
	require.Equal(t, core.NodeSucceeded, completed.Nodes[1].Status)
	assertLogContains(t, f.logDir(), f.dagWrapper.Name, completed.DAGRunID, "deploy", "environment=production")
}
