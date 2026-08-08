// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAbortQueuedDAGRun_PreservesPreviousVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := testQueueAbortDAG()
	runRef := ir.NewDAGRunRef(dag.Name, "run-1")

	writeAttemptStatus(t, ctx, store, dag, "run-1", ir.Succeeded, dagrun.NewDAGRunAttemptOptions{}, time.Now().Add(-time.Minute))
	writeAttemptStatus(t, ctx, store, dag, "run-1", ir.Queued, dagrun.NewDAGRunAttemptOptions{Retry: true}, time.Now())

	require.NoError(t, queue.AbortQueuedDAGRun(ctx, store, runRef))

	attempt, err := store.FindAttempt(ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, ir.Succeeded, status.Status)
}

func TestAbortQueuedDAGRun_RemovesRunWhenQueuedAttemptIsOnlyVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := testQueueAbortDAG()
	runRef := ir.NewDAGRunRef(dag.Name, "run-2")

	writeAttemptStatus(t, ctx, store, dag, "run-2", ir.Queued, dagrun.NewDAGRunAttemptOptions{}, time.Now())

	require.NoError(t, queue.AbortQueuedDAGRun(ctx, store, runRef))

	_, err := store.FindAttempt(ctx, runRef)
	require.Error(t, err)
	assert.True(t, errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData))
}

func TestAbortQueuedDAGRun_RejectsNonQueuedStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := testQueueAbortDAG()
	runRef := ir.NewDAGRunRef(dag.Name, "run-3")

	writeAttemptStatus(t, ctx, store, dag, "run-3", ir.Running, dagrun.NewDAGRunAttemptOptions{}, time.Now())

	err := queue.AbortQueuedDAGRun(ctx, store, runRef)
	require.Error(t, err)

	var notQueuedErr *queue.DAGRunNotQueuedError
	require.ErrorAs(t, err, &notQueuedErr)
	assert.Equal(t, ir.Running, notQueuedErr.Status)
}

func testQueueAbortDAG() *ir.DAG {
	return &ir.DAG{
		Name: "queue-abort-test",
		Steps: []ir.Step{
			{Name: "step", Command: "echo hi"},
		},
	}
}

func writeAttemptStatus(
	t *testing.T,
	ctx context.Context,
	store dagrun.DAGRunStore,
	dag *ir.DAG,
	runID string,
	status ir.Status,
	opts dagrun.NewDAGRunAttemptOptions,
	ts time.Time,
) {
	t.Helper()

	attempt, err := store.CreateAttempt(ctx, dag, ts, runID, opts)
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))

	runStatus := ir.InitialStatus(dag)
	runStatus.Status = status
	runStatus.DAGRunID = runID
	runStatus.AttemptID = attempt.ID()
	logPath := filepath.Join(t.TempDir(), runID+".log")
	require.NoError(t, os.WriteFile(logPath, []byte(""), 0o600))
	runStatus.Log = logPath
	if status != ir.Queued {
		runStatus.StartedAt = ts.UTC().Format(time.RFC3339)
	}
	if status == ir.Succeeded || status == ir.Aborted || status == ir.Failed {
		runStatus.FinishedAt = ts.Add(time.Second).UTC().Format(time.RFC3339)
	}

	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, attempt.Close(ctx))
}
