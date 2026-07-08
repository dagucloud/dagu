// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package history_test

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
	"github.com/stretchr/testify/require"
)

func TestSubmitRunWritesQueuedLifecycleState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := &core.DAG{Name: "history-submit"}
	core.InitializeDefaults(dag)
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	queueStore := store.NewQueueStore(file.NewCollection(filepath.Join(tmp, "queue")))
	now := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	historySvc := history.New(history.Config{
		DAGRunStore: dagRunStore,
		LogBaseDir:  filepath.Join(tmp, "logs"),
		Now:         func() time.Time { return now },
		Scheduler: history.ScheduleFunc(func(ctx context.Context, req history.ScheduleRequest) error {
			return queueStore.Enqueue(ctx, req.QueueName, req.Priority, req.DAGRun)
		}),
	})
	submitted, err := historySvc.SubmitRun(ctx, history.SubmitRunCommand{
		DAG:      dag,
		DAGRunID: "run-1",
	})
	require.NoError(t, err)
	require.Equal(t, exec.NewDAGRunRef(dag.Name, "run-1"), submitted.DAGRun)

	attempt, err := dagRunStore.FindAttempt(ctx, submitted.DAGRun)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, status.Status)
	require.Equal(t, submitted.Attempt.ID(), status.AttemptID)
	require.Empty(t, status.Conditions)
}

func TestRetryRunQueuesRunThroughScheduler(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-retry")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, status := writeHistoryAttemptStatus(t, ctx, store, dag, "run-1", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now(),
		func(st *exec.DAGRunStatus) {
			st.ProfileName = "prod"
			st.AutoRetryCount = 2
			st.Conditions = []exec.DAGRunCondition{{Type: "ready", Status: "true"}}
		},
	)
	scheduler := &recordingHistoryScheduler{}
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		Scheduler:   scheduler,
	})

	err := historySvc.RetryRun(ctx, history.RetryRunCommand{DAG: dag, Status: status})
	require.NoError(t, err)
	require.Len(t, scheduler.requests, 1)
	require.Equal(t, history.ScheduleRequest{
		QueueName: dag.ProcGroup(),
		Priority:  exec.QueuePriorityLow,
		DAGRun:    exec.NewDAGRunRef(dag.Name, "run-1"),
	}, scheduler.requests[0])

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	queued, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, queued.Status)
	require.Equal(t, core.TriggerTypeRetry, queued.TriggerType)
	require.Equal(t, "prod", queued.ProfileName)
	require.Equal(t, 2, queued.AutoRetryCount)
	require.NotEmpty(t, queued.QueuedAt)
	require.Empty(t, queued.Conditions)
}

func TestRetryRunAutoRetryIncrementsCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-auto-retry")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, status := writeHistoryAttemptStatus(t, ctx, store, dag, "run-auto", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now(),
		func(st *exec.DAGRunStatus) {
			st.AutoRetryCount = 2
		},
	)
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		Scheduler:   &recordingHistoryScheduler{},
	})

	err := historySvc.RetryRun(ctx, history.RetryRunCommand{
		DAG:    dag,
		Status: status,
		Options: history.RetryRunOptions{
			AutoRetry: true,
		},
	})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-auto"))
	require.NoError(t, err)
	queued, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, queued.AutoRetryCount)
}

func TestRetryRunUsesPersistedProcGroupWhenDAGIsNil(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-proc-group")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, status := writeHistoryAttemptStatus(t, ctx, store, dag, "run-proc-group", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now(),
		func(st *exec.DAGRunStatus) {
			st.ProcGroup = "persisted-queue"
		},
	)
	scheduler := &recordingHistoryScheduler{}
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		Scheduler:   scheduler,
	})

	err := historySvc.RetryRun(ctx, history.RetryRunCommand{Status: status})
	require.NoError(t, err)
	require.Len(t, scheduler.requests, 1)
	require.Equal(t, "persisted-queue", scheduler.requests[0].QueueName)
}

func TestRetryRunReturnsStaleWhenLatestAttemptChanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-stale-retry")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, staleStatus := writeHistoryAttemptStatus(t, ctx, store, dag, "run-stale", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now().Add(-time.Minute))
	writeHistoryAttemptStatus(t, ctx, store, dag, "run-stale", core.Running, exec.NewDAGRunAttemptOptions{Retry: true}, time.Now())
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		Scheduler:   &recordingHistoryScheduler{},
	})

	err := historySvc.RetryRun(ctx, history.RetryRunCommand{DAG: dag, Status: staleStatus})
	require.ErrorIs(t, err, history.ErrRetryStaleLatest)
}

func TestRetryRunRollsBackStatusWhenSchedulerFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-retry-rollback")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, status := writeHistoryAttemptStatus(t, ctx, store, dag, "run-rollback", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now(),
		func(st *exec.DAGRunStatus) {
			st.AutoRetryCount = 1
		},
	)
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		Scheduler:   &recordingHistoryScheduler{err: errors.New("scheduler failed")},
	})

	err := historySvc.RetryRun(ctx, history.RetryRunCommand{
		DAG:    dag,
		Status: status,
		Options: history.RetryRunOptions{
			AutoRetry: true,
		},
	})
	require.ErrorContains(t, err, "enqueue retry")

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-rollback"))
	require.NoError(t, err)
	rolledBack, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, rolledBack.Status)
	require.Empty(t, rolledBack.QueuedAt)
	require.Equal(t, core.TriggerTypeUnknown, rolledBack.TriggerType)
	require.Equal(t, 1, rolledBack.AutoRetryCount)
}

func TestCancelQueuedRunPreservesPreviousVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-cancel-preserve")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	runRef := exec.NewDAGRunRef(dag.Name, "run-preserve")
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Succeeded, exec.NewDAGRunAttemptOptions{}, time.Now().Add(-time.Minute))
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Queued, exec.NewDAGRunAttemptOptions{Retry: true}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	err := historySvc.CancelQueuedRun(ctx, history.CancelQueuedRunCommand{DAGRun: runRef})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)
}

func TestCancelQueuedRunRemovesRunWhenQueuedAttemptIsOnlyVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-cancel-remove")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	runRef := exec.NewDAGRunRef(dag.Name, "run-remove")
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Queued, exec.NewDAGRunAttemptOptions{}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	err := historySvc.CancelQueuedRun(ctx, history.CancelQueuedRunCommand{DAGRun: runRef})
	require.NoError(t, err)

	_, err = store.FindAttempt(ctx, runRef)
	require.Error(t, err)
	require.True(t, errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData))
}

func TestCancelQueuedRunRejectsNonQueuedStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-cancel-reject")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	runRef := exec.NewDAGRunRef(dag.Name, "run-reject")
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Running, exec.NewDAGRunAttemptOptions{}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	err := historySvc.CancelQueuedRun(ctx, history.CancelQueuedRunCommand{DAGRun: runRef})
	require.Error(t, err)
	var notQueuedErr *history.DAGRunNotQueuedError
	require.ErrorAs(t, err, &notQueuedErr)
	require.Equal(t, core.Running, notQueuedErr.Status)
}

type recordingHistoryScheduler struct {
	requests []history.ScheduleRequest
	err      error
}

func (s *recordingHistoryScheduler) ScheduleRun(_ context.Context, req history.ScheduleRequest) error {
	s.requests = append(s.requests, req)
	return s.err
}

func testHistoryDAG(name string) *core.DAG {
	dag := &core.DAG{
		Name: name,
		Steps: []core.Step{
			{Name: "step", Command: "echo hi"},
		},
	}
	core.InitializeDefaults(dag)
	return dag
}

func writeHistoryAttemptStatus(
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
