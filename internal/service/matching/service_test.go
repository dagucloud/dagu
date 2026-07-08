// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package matching_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/service/history"
	"github.com/dagucloud/dagu/internal/service/matching"
	"github.com/stretchr/testify/require"
)

func TestSubmitRunRecordsHistoryAndEnqueuesDispatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testMatchingDAG("matching-submit")
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	svc := newMatchingService(dagRunStore, queueStore, tmp)

	submitted, err := svc.SubmitRun(ctx, matching.SubmitRunCommand{
		DAG:      dag,
		DAGRunID: "run-1",
	})
	require.NoError(t, err)
	require.Equal(t, dag.ProcGroup(), submitted.QueueName)

	items, err := queueStore.List(ctx, dag.ProcGroup())
	require.NoError(t, err)
	require.Len(t, items, 1)

	attempt, err := dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, status.Status)
}

func TestRetryRunUsesPersistedProcGroupWhenDAGIsNil(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testMatchingDAG("matching-retry")
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	_, status := writeMatchingAttemptStatus(t, ctx, dagRunStore, dag, "run-retry", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now(),
		func(st *exec.DAGRunStatus) {
			st.ProcGroup = "persisted-queue"
		},
	)
	svc := newMatchingService(dagRunStore, queueStore, tmp)

	_, err := svc.RetryRun(ctx, matching.RetryRunCommand{Status: status})
	require.NoError(t, err)

	items, err := queueStore.List(ctx, "persisted-queue")
	require.NoError(t, err)
	require.Len(t, items, 1)
}

func TestRetryRunRollsBackHistoryWhenEnqueueFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testMatchingDAG("matching-retry-rollback")
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, status := writeMatchingAttemptStatus(t, ctx, dagRunStore, dag, "run-rollback", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now(),
		func(st *exec.DAGRunStatus) {
			st.AutoRetryCount = 1
		},
	)
	historySvc := history.New(history.Config{DAGRunStore: dagRunStore})
	svc := matching.New(matching.Config{
		QueueStore: failingQueueStore{err: errors.New("enqueue failed")},
		History:    historySvc,
	})

	_, err := svc.RetryRun(ctx, matching.RetryRunCommand{
		DAG:    dag,
		Status: status,
		Options: history.RetryRunOptions{
			AutoRetry: true,
		},
	})
	require.ErrorContains(t, err, "enqueue retry")

	attempt, err := dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-rollback"))
	require.NoError(t, err)
	rolledBack, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, rolledBack.Status)
	require.Empty(t, rolledBack.QueuedAt)
	require.Equal(t, core.TriggerTypeUnknown, rolledBack.TriggerType)
	require.Equal(t, 1, rolledBack.AutoRetryCount)
}

func TestCancelPendingRunRemovesQueueItemAndRecordsCancellation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testMatchingDAG("matching-cancel")
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	runRef := exec.NewDAGRunRef(dag.Name, "run-cancel")
	writeMatchingAttemptStatus(t, ctx, dagRunStore, dag, runRef.ID, core.Queued, exec.NewDAGRunAttemptOptions{}, time.Now())
	require.NoError(t, queueStore.Enqueue(ctx, dag.ProcGroup(), exec.QueuePriorityLow, runRef))
	svc := newMatchingService(dagRunStore, queueStore, tmp)

	err := svc.CancelPendingRun(ctx, matching.CancelPendingRunCommand{
		DAGRun:    runRef,
		QueueName: dag.ProcGroup(),
	})
	require.NoError(t, err)

	items, err := queueStore.List(ctx, dag.ProcGroup())
	require.NoError(t, err)
	require.Empty(t, items)
	_, err = dagRunStore.FindAttempt(ctx, runRef)
	require.Error(t, err)
	require.True(t, errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData))
}

type failingQueueStore struct {
	exec.QueueStore
	err error
}

func (s failingQueueStore) Enqueue(context.Context, string, exec.QueuePriority, exec.DAGRunRef) error {
	return s.err
}

func newMatchingService(dagRunStore exec.DAGRunStore, queueStore exec.QueueStore, tmp string) *matching.Service {
	return matching.New(matching.Config{
		QueueStore: queueStore,
		History: history.New(history.Config{
			DAGRunStore:     dagRunStore,
			LogBaseDir:      filepath.Join(tmp, "logs"),
			ArtifactBaseDir: filepath.Join(tmp, "artifacts"),
		}),
	})
}

func testMatchingDAG(name string) *core.DAG {
	dag := &core.DAG{
		Name: name,
		Steps: []core.Step{
			{Name: "step", Command: "echo hi"},
		},
	}
	core.InitializeDefaults(dag)
	return dag
}

func writeMatchingAttemptStatus(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	dag *core.DAG,
	runID string,
	status core.Status,
	opts exec.NewDAGRunAttemptOptions,
	ts time.Time,
	mutate ...func(*exec.DAGRunStatus),
) (exec.DAGRunAttempt, *exec.DAGRunStatus) {
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
	for _, fn := range mutate {
		fn(&runStatus)
	}

	require.NoError(t, attempt.Write(ctx, runStatus))
	require.NoError(t, attempt.Close(ctx))
	return attempt, &runStatus
}
