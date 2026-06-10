// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
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

type retryCandidateLister interface {
	ListRetryCandidates(ctx context.Context, from exec.TimeInUTC) ([]*exec.DAGRunStatus, error)
}

func TestStoreListRetryCandidatesTracksFailedRunWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := dagrun.New(t.TempDir())
	lister, ok := store.(retryCandidateLister)
	require.True(t, ok)

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()

	successAttempt, _ := writeRetryCandidateStatus(t, ctx, store, dag, now, "success-run", core.Succeeded)
	defer func() { require.NoError(t, successAttempt.Close(ctx)) }()
	failedAttempt, failedStatus := writeRetryCandidateStatus(t, ctx, store, dag, now.Add(time.Second), "failed-run", core.Failed)
	defer func() { require.NoError(t, failedAttempt.Close(ctx)) }()

	candidates, err := lister.ListRetryCandidates(ctx, exec.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
	assert.Equal(t, core.Failed, candidates[0].Status)
	assert.Equal(t, 2, candidates[0].AutoRetryLimit)
	assert.NotEmpty(t, candidates[0].ProcGroup)

	failedStatus.Status = core.Queued
	failedStatus.QueuedAt = now.Add(2 * time.Second).Format(time.RFC3339)
	require.NoError(t, failedAttempt.Write(ctx, *failedStatus))

	candidates, err = lister.ListRetryCandidates(ctx, exec.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	assert.Empty(t, candidates)
}

func TestStoreListRetryCandidatesRebuildsMissingCandidateDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir)
	lister, ok := store.(retryCandidateLister)
	require.True(t, ok)

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()

	attempt, _ := writeRetryCandidateStatus(t, ctx, store, dag, now, "failed-run", core.Failed)
	defer func() { require.NoError(t, attempt.Close(ctx)) }()

	candidateDir := filepath.Join(baseDir, dag.Name, "dag-runs", "2026", "06", "08", ".dagrun.retry-candidates")
	require.NoError(t, os.RemoveAll(candidateDir))

	candidates, err := lister.ListRetryCandidates(ctx, exec.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
	require.DirExists(t, candidateDir)
}

func TestStoreListRetryCandidatesRebuildsDirtyCandidateDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir)
	lister, ok := store.(retryCandidateLister)
	require.True(t, ok)

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()
	candidateDir := filepath.Join(baseDir, dag.Name, "dag-runs", "2026", "06", "08", ".dagrun.retry-candidates")
	require.NoError(t, os.MkdirAll(filepath.Dir(candidateDir), 0750))
	require.NoError(t, os.WriteFile(candidateDir, []byte("not a directory"), 0600))

	attempt, _ := writeRetryCandidateStatus(t, ctx, store, dag, now, "failed-run", core.Failed)
	defer func() { require.NoError(t, attempt.Close(ctx)) }()

	candidates, err := lister.ListRetryCandidates(ctx, exec.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
	require.DirExists(t, candidateDir)
}

func TestStoreListRetryCandidatesRebuildsCorruptedCandidateFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir)
	lister, ok := store.(retryCandidateLister)
	require.True(t, ok)

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()
	candidateDir := filepath.Join(baseDir, dag.Name, "dag-runs", "2026", "06", "08", ".dagrun.retry-candidates")

	attempt, _ := writeRetryCandidateStatus(t, ctx, store, dag, now, "failed-run", core.Failed)
	defer func() { require.NoError(t, attempt.Close(ctx)) }()

	entries, err := os.ReadDir(candidateDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoError(t, os.WriteFile(filepath.Join(candidateDir, entries[0].Name()), []byte("{"), 0600))

	candidates, err := lister.ListRetryCandidates(ctx, exec.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
}

func TestStoreListRetryCandidatesRemovesCandidateWhenRunIsGone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir)
	lister, ok := store.(retryCandidateLister)
	require.True(t, ok)

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()
	attempt, _ := writeRetryCandidateStatus(t, ctx, store, dag, now, "failed-run", core.Failed)
	require.NoError(t, attempt.Close(ctx))

	require.NoError(t, store.RemoveDAGRun(ctx, exec.NewDAGRunRef(dag.Name, "failed-run")))

	candidates, err := lister.ListRetryCandidates(ctx, exec.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	assert.Empty(t, candidates)
}

func TestStoreListRetryCandidatesIgnoresChildAttemptStatusFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir)
	lister, ok := store.(retryCandidateLister)
	require.True(t, ok)

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	parentDAG := retryCandidateDAG()
	parentAttempt, _ := writeRetryCandidateStatus(t, ctx, store, parentDAG, now, "parent-run", core.Running)
	defer func() { require.NoError(t, parentAttempt.Close(ctx)) }()

	rootRef := exec.NewDAGRunRef(parentDAG.Name, "parent-run")
	childDAG := retryCandidateDAG()
	childDAG.Name = "child-retry-dag"
	childAttempt, err := store.CreateAttempt(ctx, childDAG, now.Add(time.Second), "child-run", exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	require.NoError(t, childAttempt.Open(ctx))
	defer func() { require.NoError(t, childAttempt.Close(ctx)) }()

	childStatus := exec.InitialStatus(childDAG)
	childStatus.DAGRunID = "child-run"
	childStatus.AttemptID = childAttempt.ID()
	childStatus.Status = core.Failed
	childStatus.StartedAt = now.Add(time.Second).Format(time.RFC3339)
	childStatus.FinishedAt = now.Add(2 * time.Second).Format(time.RFC3339)
	require.NoError(t, childAttempt.Write(ctx, childStatus))

	candidates, err := lister.ListRetryCandidates(ctx, exec.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	assert.Empty(t, candidates)

	childSidecarDir := filepath.Join(
		baseDir,
		parentDAG.Name,
		"dag-runs",
		"2026",
		"06",
		"08",
		"dag-run_20260608_120000Z_parent-run",
		"children",
		".dagrun.retry-candidates",
	)
	require.NoDirExists(t, childSidecarDir)
}

func retryCandidateDAG() *core.DAG {
	return &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       2,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
}

func writeRetryCandidateStatus(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	dag *core.DAG,
	ts time.Time,
	runID string,
	status core.Status,
) (exec.DAGRunAttempt, *exec.DAGRunStatus) {
	t.Helper()

	attempt, err := store.CreateAttempt(ctx, dag, ts, runID, exec.NewDAGRunAttemptOptions{
		AttemptID: runID + "-attempt",
	})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))

	runStatus := exec.InitialStatus(dag)
	runStatus.DAGRunID = runID
	runStatus.AttemptID = attempt.ID()
	runStatus.Status = status
	runStatus.StartedAt = ts.Format(time.RFC3339)
	runStatus.FinishedAt = ts.Add(time.Minute).Format(time.RFC3339)
	require.NoError(t, attempt.Write(ctx, runStatus))
	return attempt, &runStatus
}
