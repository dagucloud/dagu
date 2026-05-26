// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAbortQueuedDAGRun_PreservesPreviousVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := testQueueAbortDAG()
	runRef := exec.NewDAGRunRef(dag.Name, "run-1")

	writeAttemptStatus(t, ctx, store, dag, "run-1", core.Succeeded, exec.NewDAGRunAttemptOptions{}, time.Now().Add(-time.Minute))
	writeAttemptStatus(t, ctx, store, dag, "run-1", core.Queued, exec.NewDAGRunAttemptOptions{Retry: true}, time.Now())

	require.NoError(t, exec.AbortQueuedDAGRun(ctx, store, runRef))

	attempt, err := store.FindAttempt(ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, core.Succeeded, status.Status)
}

func TestAbortQueuedDAGRun_RemovesRunWhenQueuedAttemptIsOnlyVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := testQueueAbortDAG()
	runRef := exec.NewDAGRunRef(dag.Name, "run-2")

	writeAttemptStatus(t, ctx, store, dag, "run-2", core.Queued, exec.NewDAGRunAttemptOptions{}, time.Now())

	require.NoError(t, exec.AbortQueuedDAGRun(ctx, store, runRef))

	_, err := store.FindAttempt(ctx, runRef)
	require.Error(t, err)
	assert.True(t, errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData))
}

func TestAbortQueuedDAGRun_RejectsNonQueuedStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := testQueueAbortDAG()
	runRef := exec.NewDAGRunRef(dag.Name, "run-3")

	writeAttemptStatus(t, ctx, store, dag, "run-3", core.Running, exec.NewDAGRunAttemptOptions{}, time.Now())

	err := exec.AbortQueuedDAGRun(ctx, store, runRef)
	require.Error(t, err)

	var notQueuedErr *exec.DAGRunNotQueuedError
	require.ErrorAs(t, err, &notQueuedErr)
	assert.Equal(t, core.Running, notQueuedErr.Status)
}

func testQueueAbortDAG() *core.DAG {
	return &core.DAG{
		Name: "queue-abort-test",
		Steps: []core.Step{
			{Name: "step", Command: "echo hi"},
		},
	}
}

func writeAttemptStatus(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	dag *core.DAG,
	runID string,
	status core.Status,
	opts exec.NewDAGRunAttemptOptions,
	ts time.Time,
) {
	t.Helper()

	attempt, err := store.CreateAttempt(ctx, dag, ts, runID, opts)
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))

	runStatus := exec.InitialStatus(dag)
	runStatus.Status = status
	runStatus.DAGRunID = runID
	runStatus.AttemptID = attempt.ID()
	logPath := filepath.Join(t.TempDir(), runID+".log")
	require.NoError(t, os.WriteFile(logPath, []byte(""), 0o600))
	runStatus.Log = logPath
	if status != core.Queued {
		runStatus.StartedAt = ts.UTC().Format(time.RFC3339)
	}
	if status == core.Succeeded || status == core.Aborted || status == core.Failed {
		runStatus.FinishedAt = ts.Add(time.Second).UTC().Format(time.RFC3339)
	}

	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, attempt.Close(ctx))
}
