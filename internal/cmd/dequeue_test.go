// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDequeueCommand(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `steps:
  - name: "1"
    run: "true"
`)

	// Enqueue the DAG first
	th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
		Name: "Enqueue",
		Args: []string{"enqueue", "--run-id", "test-DAG", dag.Location},
	})

	// Now test the dequeue command
	th.RunCommand(t, cmd.Dequeue(), test.CmdTest{
		Name:        "Dequeue",
		Args:        []string{"dequeue", dag.ProcGroup(), "--dag-run", dag.Name + ":test-DAG"},
		ExpectedOut: []string{"Dequeued dag-run"},
	})
}

func TestDequeueCommand_PreservesState(t *testing.T) {
	th := test.SetupCommand(t)
	ctx := context.Background()

	// Create a DAG
	dag := th.DAG(t, `steps:
  - name: step1
    run: echo "success"
`)

	// First run the DAG successfully
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Name: "RunDAG",
		Args: []string{"start", "--run-id", "success-run", dag.Location},
	})

	// Wait for it to complete
	attempt, err := th.DAGRunStore.FindAttempt(ctx, exec.DAGRunRef{
		Name: dag.Name,
		ID:   "success-run",
	})
	require.NoError(t, err)

	dagStatus, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, core.Succeeded, dagStatus.Status)

	// Now enqueue a new run
	th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
		Name: "Enqueue",
		Args: []string{"enqueue", "--run-id", "queued-run", dag.Location},
	})

	// Dequeue it
	th.RunCommand(t, cmd.Dequeue(), test.CmdTest{
		Name:        "Dequeue",
		Args:        []string{"dequeue", dag.ProcGroup(), "--dag-run", dag.Name + ":queued-run"},
		ExpectedOut: []string{"Dequeued dag-run"},
	})

	// Verify the previous successful run remains intact after the queued run is hidden.
	successAttempt, err := th.DAGRunStore.FindAttempt(ctx, exec.DAGRunRef{
		Name: dag.Name,
		ID:   "success-run",
	})
	require.NoError(t, err)

	successStatus, err := successAttempt.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, core.Succeeded, successStatus.Status, "Dequeuing should not alter the prior successful run")

	queuedAttempt, err := th.DAGRunStore.FindAttempt(ctx, exec.DAGRunRef{
		Name: dag.Name,
		ID:   "queued-run",
	})
	if err == nil {
		assert.True(t, queuedAttempt.Hidden(), "Dequeued run should be hidden from normal lookups")

		queuedStatus, readErr := queuedAttempt.ReadStatus(ctx)
		require.NoError(t, readErr)
		assert.Equal(t, core.Aborted, queuedStatus.Status, "Dequeued run should be marked aborted before it is hidden")
	} else {
		assert.ErrorIs(t, err, exec.ErrDAGRunIDNotFound, "Dequeued run should not remain visible after it is hidden")
	}
}

func TestDequeueCommand_DefaultsToFirstItem(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `steps:
  - name: "1"
    run: "true"
`)

	// Enqueue the DAG first
	th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
		Name: "Enqueue",
		Args: []string{"enqueue", "--run-id", "test-DAG", dag.Location},
	})

	// Now test the dequeue command without --dag-run to pop the first item
	th.RunCommand(t, cmd.Dequeue(), test.CmdTest{
		Name:        "DequeueFirst",
		Args:        []string{"dequeue", dag.ProcGroup()},
		ExpectedOut: []string{"Dequeued dag-run"},
	})

	// Verify queue is empty
	length, err := th.QueueStore.Len(th.Context, dag.ProcGroup())
	require.NoError(t, err)
	assert.Equal(t, 0, length)
}

func TestDequeueCommand_TargetedDequeuesUseActualQueue(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `queue: actual-queue
steps:
  - name: "1"
    run: "true"
`)

	th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
		Name: "Enqueue",
		Args: []string{"enqueue", "--run-id", "actual-queue-run", dag.Location},
	})

	th.RunCommand(t, cmd.Dequeue(), test.CmdTest{
		Name:        "DequeueResolvedQueue",
		Args:        []string{"dequeue", "wrong-queue", "--dag-run", dag.Name + ":actual-queue-run"},
		ExpectedOut: []string{"Dequeued dag-run"},
	})

	length, err := th.QueueStore.Len(th.Context, dag.ProcGroup())
	require.NoError(t, err)
	assert.Equal(t, 0, length)
}

func TestDequeueCommand_DefaultsToFirstItemSkipsStaleHead(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `queue: shared-queue
steps:
  - name: "1"
    run: "true"
`)

	require.NoError(t, th.QueueStore.Enqueue(
		th.Context,
		dag.ProcGroup(),
		exec.QueuePriorityLow,
		exec.NewDAGRunRef(dag.Name, "stale-run"),
	))

	th.RunCommand(t, cmd.Enqueue(), test.CmdTest{
		Name: "Enqueue",
		Args: []string{"enqueue", "--run-id", "valid-run", dag.Location},
	})

	th.RunCommand(t, cmd.Dequeue(), test.CmdTest{
		Name:        "DequeueFirstSkipsStaleHead",
		Args:        []string{"dequeue", dag.ProcGroup()},
		ExpectedOut: []string{"Dequeued dag-run"},
	})

	length, err := th.QueueStore.Len(th.Context, dag.ProcGroup())
	require.NoError(t, err)
	assert.Equal(t, 0, length)

	_, err = th.DAGRunStore.FindAttempt(th.Context, exec.NewDAGRunRef(dag.Name, "valid-run"))
	assert.ErrorIs(t, err, exec.ErrDAGRunIDNotFound)
}

func TestDequeueCommand_TargetedDequeueFallsBackToRequestedQueueForOrphanedItem(t *testing.T) {
	th := test.SetupCommand(t)

	dag := th.DAG(t, `queue: fallback-queue
steps:
  - name: "1"
    run: "true"
`)

	runRef := exec.NewDAGRunRef(dag.Name, "orphaned-run")
	require.NoError(t, th.QueueStore.Enqueue(
		th.Context,
		dag.ProcGroup(),
		exec.QueuePriorityLow,
		runRef,
	))

	th.RunCommand(t, cmd.Dequeue(), test.CmdTest{
		Name:        "DequeueOrphanedRun",
		Args:        []string{"dequeue", dag.ProcGroup(), "--dag-run", runRef.String()},
		ExpectedOut: []string{"Removed orphaned queued dag-run"},
	})

	length, err := th.QueueStore.Len(th.Context, dag.ProcGroup())
	require.NoError(t, err)
	assert.Equal(t, 0, length)
}
