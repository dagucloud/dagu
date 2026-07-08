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

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/internal/service/history"
	"github.com/stretchr/testify/require"
)

func TestSubmitRunWritesPendingLifecycleState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-submit")
	dagRunStore := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	now := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	historySvc := history.New(history.Config{
		DAGRunStore: dagRunStore,
		LogBaseDir:  filepath.Join(tmp, "logs"),
		Now:         func() time.Time { return now },
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
	require.Equal(t, submitted.AttemptID, status.AttemptID)
	require.Empty(t, status.Conditions)
}

func TestDiscardSubmittedRunIgnoresContextCancellation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-submit-cancel-rollback")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		LogBaseDir:  filepath.Join(tmp, "logs"),
	})
	submitted, err := historySvc.SubmitRun(ctx, history.SubmitRunCommand{
		DAG:      dag,
		DAGRunID: "run-rollback",
	})
	require.NoError(t, err)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	err = historySvc.DiscardSubmittedRun(canceled, history.DiscardSubmittedRunCommand{
		RollbackToken: submitted.RollbackToken,
	})
	require.NoError(t, err)

	_, err = store.FindAttempt(ctx, submitted.DAGRun)
	require.Error(t, err)
}

func TestRetryRunRecordsPendingRetryState(t *testing.T) {
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
	now := time.Date(2026, 5, 19, 4, 5, 6, 0, time.UTC)
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		Now:         func() time.Time { return now },
	})

	retried, err := historySvc.RetryRun(ctx, history.RetryRunCommand{Status: status})
	require.NoError(t, err)
	require.Equal(t, exec.NewDAGRunRef(dag.Name, "run-1"), retried.DAGRun)

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-1"))
	require.NoError(t, err)
	queued, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, queued.Status)
	require.Equal(t, core.TriggerTypeRetry, queued.TriggerType)
	require.Equal(t, "prod", queued.ProfileName)
	require.Equal(t, 2, queued.AutoRetryCount)
	require.Equal(t, stringutil.FormatTime(now), queued.QueuedAt)
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
	historySvc := history.New(history.Config{DAGRunStore: store})

	_, err := historySvc.RetryRun(ctx, history.RetryRunCommand{
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

func TestRetryRunReturnsStaleWhenLatestAttemptChanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-stale-retry")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, staleStatus := writeHistoryAttemptStatus(t, ctx, store, dag, "run-stale", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now().Add(-time.Minute))
	writeHistoryAttemptStatus(t, ctx, store, dag, "run-stale", core.Running, exec.NewDAGRunAttemptOptions{Retry: true}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	_, err := historySvc.RetryRun(ctx, history.RetryRunCommand{Status: staleStatus})
	require.ErrorIs(t, err, history.ErrRetryStaleLatest)
}

func TestUndoRetryRunRestoresPreviousStatus(t *testing.T) {
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
	historySvc := history.New(history.Config{DAGRunStore: store})

	retried, err := historySvc.RetryRun(ctx, history.RetryRunCommand{
		Status: status,
		Options: history.RetryRunOptions{
			AutoRetry: true,
		},
	})
	require.NoError(t, err)

	err = historySvc.UndoRetryRun(ctx, history.UndoRetryRunCommand{
		RollbackToken: retried.RollbackToken,
	})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-rollback"))
	require.NoError(t, err)
	rolledBack, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, rolledBack.Status)
	require.Empty(t, rolledBack.QueuedAt)
	require.Equal(t, core.TriggerTypeUnknown, rolledBack.TriggerType)
	require.Equal(t, 1, rolledBack.AutoRetryCount)
}

func TestUndoRetryRunUsesRollbackTokenSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-retry-token")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, status := writeHistoryAttemptStatus(t, ctx, store, dag, "run-token", core.Failed, exec.NewDAGRunAttemptOptions{}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	retried, err := historySvc.RetryRun(ctx, history.RetryRunCommand{Status: status})
	require.NoError(t, err)
	retried.Status.AttemptID = "mutated-attempt"

	err = historySvc.UndoRetryRun(ctx, history.UndoRetryRunCommand{
		RollbackToken: retried.RollbackToken,
	})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-token"))
	require.NoError(t, err)
	rolledBack, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, rolledBack.Status)
}

func TestMarkDispatchCanceledPreservesPreviousVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-cancel-preserve")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	runRef := exec.NewDAGRunRef(dag.Name, "run-preserve")
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Succeeded, exec.NewDAGRunAttemptOptions{}, time.Now().Add(-time.Minute))
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Queued, exec.NewDAGRunAttemptOptions{Retry: true}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	err := historySvc.MarkDispatchCanceled(ctx, history.MarkDispatchCanceledCommand{DAGRun: runRef})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, runRef)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)
}

func TestMarkDispatchCanceledRemovesRunWhenPendingAttemptIsOnlyVisibleAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-cancel-remove")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	runRef := exec.NewDAGRunRef(dag.Name, "run-remove")
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Queued, exec.NewDAGRunAttemptOptions{}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	err := historySvc.MarkDispatchCanceled(ctx, history.MarkDispatchCanceledCommand{DAGRun: runRef})
	require.NoError(t, err)

	_, err = store.FindAttempt(ctx, runRef)
	require.Error(t, err)
	require.True(t, errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData))
}

func TestMarkDispatchCanceledRejectsNonPendingStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-cancel-reject")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	runRef := exec.NewDAGRunRef(dag.Name, "run-reject")
	writeHistoryAttemptStatus(t, ctx, store, dag, runRef.ID, core.Running, exec.NewDAGRunAttemptOptions{}, time.Now())
	historySvc := history.New(history.Config{DAGRunStore: store})

	err := historySvc.MarkDispatchCanceled(ctx, history.MarkDispatchCanceledCommand{DAGRun: runRef})
	require.Error(t, err)
	var notPendingErr *history.RunNotPendingError
	require.ErrorAs(t, err, &notPendingErr)
	require.Equal(t, core.Running, notPendingErr.Status)
}

func TestHistoryCommandsValidateRequiredInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))

	historySvc := history.New(history.Config{DAGRunStore: store})
	_, err := historySvc.RetryRun(ctx, history.RetryRunCommand{})
	require.ErrorContains(t, err, "status is required")

	err = historySvc.MarkDispatchCanceled(ctx, history.MarkDispatchCanceledCommand{})
	require.ErrorContains(t, err, "dag-run is required")

	err = historySvc.DiscardSubmittedRun(ctx, history.DiscardSubmittedRunCommand{})
	require.ErrorContains(t, err, "dag-run is required")

	err = historySvc.UndoRetryRun(ctx, history.UndoRetryRunCommand{})
	require.ErrorContains(t, err, "dag-run is required")

	missingStoreSvc := history.New(history.Config{})
	_, err = missingStoreSvc.RetryRun(ctx, history.RetryRunCommand{
		Status: &exec.DAGRunStatus{
			Name:      "dag",
			DAGRunID:  "run",
			AttemptID: "attempt",
		},
	})
	require.ErrorContains(t, err, "dag-run store is required")

	err = missingStoreSvc.UndoRetryRun(ctx, history.UndoRetryRunCommand{})
	require.ErrorContains(t, err, "dag-run store is required")

	err = missingStoreSvc.MarkDispatchCanceled(ctx, history.MarkDispatchCanceledCommand{
		DAGRun: exec.NewDAGRunRef("dag", "run"),
	})
	require.ErrorContains(t, err, "dag-run store is required")

	err = missingStoreSvc.DiscardSubmittedRun(ctx, history.DiscardSubmittedRunCommand{})
	require.ErrorContains(t, err, "dag-run store is required")
}

func TestDiscardSubmittedRunUsesSubmitRollbackToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-submit-rollback")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	historySvc := history.New(history.Config{
		DAGRunStore: store,
		LogBaseDir:  filepath.Join(tmp, "logs"),
	})

	first, err := historySvc.SubmitRun(ctx, history.SubmitRunCommand{
		DAG:      dag,
		DAGRunID: "run-first",
	})
	require.NoError(t, err)
	second, err := historySvc.SubmitRun(ctx, history.SubmitRunCommand{
		DAG:      dag,
		DAGRunID: "run-second",
	})
	require.NoError(t, err)

	err = historySvc.DiscardSubmittedRun(ctx, history.DiscardSubmittedRunCommand{
		RollbackToken: first.RollbackToken,
	})
	require.NoError(t, err)

	_, err = store.FindAttempt(ctx, first.DAGRun)
	require.Error(t, err)
	_, err = store.FindAttempt(ctx, second.DAGRun)
	require.NoError(t, err)
}

func TestRecordEarlyFailureRecordsHistoryState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-early-failure")
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	now := time.Date(2026, 5, 19, 7, 8, 9, 0, time.UTC)
	historySvc := history.New(history.Config{
		DAGRunStore:     store,
		LogBaseDir:      filepath.Join(tmp, "logs"),
		ArtifactBaseDir: filepath.Join(tmp, "artifacts"),
		Now:             func() time.Time { return now },
	})

	err := historySvc.RecordEarlyFailure(ctx, history.RecordEarlyFailureCommand{
		DAG:      dag,
		DAGRunID: "run-early",
		Err:      errors.New("process acquisition failed"),
	})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-early"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, status.Status)
	require.Equal(t, "process acquisition failed", status.Error)
	require.Equal(t, stringutil.FormatTime(now), status.FinishedAt)
	require.NotEmpty(t, status.Log)

	storedDAG, err := attempt.ReadDAG(ctx)
	require.NoError(t, err)
	require.Equal(t, dag.Name, storedDAG.Name)
}

func TestRepairQueuedCatchupRunPersistsLocalMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-catchup-repair")
	dag.Artifacts = &core.ArtifactsConfig{Enabled: true, Dir: "dag-artifacts"}
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	_, status := writeHistoryAttemptStatus(t, ctx, store, dag, "run-catchup", core.Queued, exec.NewDAGRunAttemptOptions{}, time.Now(),
		func(st *exec.DAGRunStatus) {
			st.TriggerType = core.TriggerTypeCatchUp
			st.Log = ""
			st.ArchiveDir = ""
		},
	)
	historySvc := history.New(history.Config{
		DAGRunStore:     store,
		LogBaseDir:      filepath.Join(tmp, "logs"),
		ArtifactBaseDir: filepath.Join(tmp, "artifacts"),
	})

	err := historySvc.RepairQueuedCatchupRun(ctx, history.RepairQueuedCatchupRunCommand{
		DAG:    dag,
		Status: status,
	})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, "run-catchup"))
	require.NoError(t, err)
	repaired, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Queued, repaired.Status)
	require.Equal(t, core.TriggerTypeCatchUp, repaired.TriggerType)
	require.NotEmpty(t, repaired.Log)
	require.NotEmpty(t, repaired.ArchiveDir)
	require.Contains(t, filepath.Clean(repaired.Log), filepath.Clean(filepath.Join(tmp, "logs")))
	require.Contains(t, filepath.Clean(repaired.ArchiveDir), "dag-artifacts")
}

func TestSeedEditRetryRunRecordsQueuedStateAndFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmp := t.TempDir()
	dag := testHistoryDAG("history-edit-retry")
	dag.Params = []string{"P1=old"}
	store := dagrun.New(filepath.Join(tmp, "dag-runs"), dagrun.WithLatestStatusToday(false))
	now := time.Date(2026, 5, 19, 8, 9, 10, 0, time.UTC)
	historySvc := history.New(history.Config{
		DAGRunStore:     store,
		LogBaseDir:      filepath.Join(tmp, "logs"),
		ArtifactBaseDir: filepath.Join(tmp, "artifacts"),
		Now:             func() time.Time { return now },
	})

	seeded, err := historySvc.SeedEditRetryRun(ctx, history.SeedEditRetryRunCommand{
		DAG:          dag,
		DAGRunID:     "run-edit-retry",
		Params:       "P1=new",
		ProfileName:  "prod",
		SourceStatus: &exec.DAGRunStatus{},
	})
	require.NoError(t, err)
	require.Equal(t, exec.NewDAGRunRef(dag.Name, "run-edit-retry"), seeded.DAGRun)
	require.Equal(t, core.Queued, seeded.Status.Status)
	require.Equal(t, core.TriggerTypeRetry, seeded.Status.TriggerType)
	require.Equal(t, "P1=new", seeded.Status.Params)
	require.Equal(t, []string{"P1=old"}, seeded.Status.ParamsList)
	require.Equal(t, "prod", seeded.Status.ProfileName)
	require.Equal(t, stringutil.FormatTime(now), seeded.Status.QueuedAt)
	require.NotEmpty(t, seeded.Status.Log)

	now = now.Add(time.Minute)
	err = historySvc.MarkEditRetrySeedFailed(ctx, history.MarkEditRetrySeedFailedCommand{
		Status: seeded.Status,
		Cause:  errors.New("launcher failed"),
	})
	require.NoError(t, err)

	attempt, err := store.FindAttempt(ctx, seeded.DAGRun)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, status.Status)
	require.Equal(t, "launcher failed", status.Error)
	require.Equal(t, stringutil.FormatTime(now), status.FinishedAt)
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
