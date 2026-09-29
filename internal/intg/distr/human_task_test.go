// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/test"
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

	waiting := f.waitForStatus(ir.Waiting, 20*time.Second)
	require.NotEmpty(t, waiting.FinishedAt)
	require.Equal(t, "worker-1", waiting.WorkerID)
	require.Equal(t, ir.NodeWaiting, waiting.Nodes[0].Status)

	f.runHumanTaskCommand(
		"complete",
		"--run-id="+waiting.DAGRunID,
		"--step=review",
		"--input=environment=production",
	)

	completed := f.waitForStatus(ir.Succeeded, 20*time.Second)
	require.Len(t, completed.Nodes, 2)
	require.Equal(t, ir.NodeSucceeded, completed.Nodes[0].Status)
	require.JSONEq(t, `{"environment":"production"}`, string(completed.Nodes[0].HumanTaskInput))
	require.Equal(t, ir.NodeSucceeded, completed.Nodes[1].Status)
	assertLogContains(t, f.logDir(), f.dagWrapper.Name, completed.DAGRunID, "deploy", "environment=production")
}

// Every push-back resumes the same attempt on a worker. The owning worker stops
// before each push-back so the queued resume is checked for stale ownership,
// as a UI read of the run does, before any worker claims it.
func TestHumanTask_RepeatedPushBackResumesOnDistributedWorker(t *testing.T) {
	labels := map[string]string{"type": "test-worker"}
	f := newTestFixture(t, `
worker_selector:
  type: test-worker
steps:
  - id: implement
`+test.ForOS("    shell: /bin/sh\n", "    shell: powershell\n")+`    command: `+test.Output("implement")+`
  - id: review
    depends: implement
    action: human.task
    with:
      prompt: Review the change
      push_back:
        rewind_to: implement
        form:
          type: object
          properties:
            feedback:
              type: string
          required: [feedback]
`, withLabels(labels))
	defer f.cleanup()

	require.NoError(t, f.enqueue())
	f.waitForQueued()
	f.startScheduler(30 * time.Second)

	waiting := f.waitForStatus(ir.Waiting, 20*time.Second)
	attemptID := waiting.AttemptID
	owner := f.workers[0]

	for iteration, feedback := range []string{"first", "second"} {
		stopCtx, cancel := context.WithTimeout(f.coord.Context, distrTestTimeout(5*time.Second))
		require.NoError(t, owner.Stop(stopCtx))
		cancel()

		f.runHumanTaskCommand(
			"push-back",
			"--run-id="+waiting.DAGRunID,
			"--step=review",
			"--input=feedback="+feedback,
			fmt.Sprintf("--expected-iteration=%d", iteration),
		)

		queued, err := f.latestStoredStatus()
		require.NoError(t, err)
		require.Equal(t, ir.Queued, queued.Status)
		checked, _, err := runtime.RepairStaleRemoteRun(f.coord.Context, runtime.StaleRunRepairConfig{
			DAGRunRepository:     f.coord.DAGRunRepository,
			DAGRunLeaseStore:     f.coord.DAGRunLeaseStore,
			WorkerHeartbeatStore: f.coord.WorkerHeartbeatStore,
		}, &queued, queued.AttemptID, queued.WorkerID)
		require.NoError(t, err)
		require.Equal(t, ir.Queued, checked.Status)

		workerID := fmt.Sprintf("worker-%d", iteration+2)
		owner = f.setupWorker(workerID, labels, "")

		waiting = f.waitForStatus(ir.Waiting, 20*time.Second)
		require.Equal(t, attemptID, waiting.AttemptID)
		require.Equal(t, workerID, waiting.WorkerID)
		require.Equal(t, ir.NodeSucceeded, waiting.Nodes[0].Status)
		require.Equal(t, iteration+1, waiting.Nodes[0].ApprovalIteration)
		require.Equal(t, map[string]string{"feedback": feedback}, waiting.Nodes[0].PushBackInputs)
		require.Equal(t, ir.NodeWaiting, waiting.Nodes[1].Status)
	}
}

func (f *testFixture) runHumanTaskCommand(args ...string) {
	f.t.Helper()

	args = append([]string{"human-task"}, args...)
	args = append(args, f.dagWrapper.Name)

	root := &cobra.Command{Use: "root"}
	root.AddCommand(cmd.HumanTask())
	root.SetArgs(test.WithConfigFlag(args, f.coord.Config))
	require.NoError(f.t, root.ExecuteContext(f.coord.Context))
}
