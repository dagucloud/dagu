// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreListRetryCandidatesTracksFailedRunWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repository := newFileRepository(t.TempDir(), persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()

	successAttempt, _ := writeRetryCandidateStatus(t, ctx, repository, dag, now, "success-run", ir.Succeeded)
	defer func() { require.NoError(t, successAttempt.Close(ctx)) }()
	failedAttempt, failedStatus := writeRetryCandidateStatus(t, ctx, repository, dag, now.Add(time.Second), "failed-run", ir.Failed)
	defer func() { require.NoError(t, failedAttempt.Close(ctx)) }()

	candidates, err := repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
	assert.Equal(t, ir.Failed, candidates[0].Status)
	assert.Equal(t, 2, candidates[0].AutoRetryLimit)
	assert.NotEmpty(t, candidates[0].ProcGroup)

	failedStatus.AutoRetryCount = 1
	require.NoError(t, failedAttempt.Write(ctx, *failedStatus))

	candidates, err = repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, 1, candidates[0].AutoRetryCount)

	failedStatus.Status = ir.Queued
	failedStatus.QueuedAt = now.Add(2 * time.Second).Format(time.RFC3339)
	require.NoError(t, failedAttempt.Write(ctx, *failedStatus))

	candidates, err = repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	assert.Empty(t, candidates)
}

func TestStoreListRetryCandidatesRebuildsMissingCandidateDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()

	attempt, _ := writeRetryCandidateStatus(t, ctx, repository, dag, now, "failed-run", ir.Failed)
	defer func() { require.NoError(t, attempt.Close(ctx)) }()

	candidateDir := filepath.Join(baseDir, dag.Name, "dag-runs", "2026", "06", "08", ".dagrun.retry-candidates")
	require.NoError(t, os.RemoveAll(candidateDir))

	candidates, err := repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
	require.DirExists(t, candidateDir)
}

func TestStoreListRetryCandidatesRebuildsDirtyCandidateDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()
	candidateDir := filepath.Join(baseDir, dag.Name, "dag-runs", "2026", "06", "08", ".dagrun.retry-candidates")
	require.NoError(t, os.MkdirAll(filepath.Dir(candidateDir), 0750))
	require.NoError(t, os.WriteFile(candidateDir, []byte("not a directory"), 0600))

	attempt, _ := writeRetryCandidateStatus(t, ctx, repository, dag, now, "failed-run", ir.Failed)
	defer func() { require.NoError(t, attempt.Close(ctx)) }()

	candidates, err := repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
	require.DirExists(t, candidateDir)
}

func TestStoreListRetryCandidatesRebuildsCorruptedCandidateFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()
	candidateDir := filepath.Join(baseDir, dag.Name, "dag-runs", "2026", "06", "08", ".dagrun.retry-candidates")

	attempt, _ := writeRetryCandidateStatus(t, ctx, repository, dag, now, "failed-run", ir.Failed)
	defer func() { require.NoError(t, attempt.Close(ctx)) }()

	candidates, err := repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)

	entries, err := os.ReadDir(candidateDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoError(t, os.WriteFile(filepath.Join(candidateDir, entries[0].Name()), []byte("{"), 0600))

	candidates, err = repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, "failed-run", candidates[0].DAGRunID)
}

func TestStoreListRetryCandidatesRemovesCandidateWhenRunIsGone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()
	attempt, _ := writeRetryCandidateStatus(t, ctx, repository, dag, now, "failed-run", ir.Failed)
	require.NoError(t, attempt.Close(ctx))

	candidates, err := repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)

	require.NoError(t, repository.RemoveDAGRun(ctx, ir.NewDAGRunRef(dag.Name, "failed-run"), persis.DAGRunRemoveOptions{}))

	candidates, err = repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	assert.Empty(t, candidates)
}

func TestStoreListRetryCandidatesCachesUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	dag := retryCandidateDAG()
	for i := range 8 {
		attempt, _ := writeRetryCandidateStatus(t, ctx, repository, dag, now.Add(time.Duration(i)*time.Second), fmt.Sprintf("failed-run-%d", i), ir.Failed)
		require.NoError(t, attempt.Close(ctx))
	}

	candidateDir := filepath.Join(baseDir, dag.Name, "dag-runs", "2026", "06", "08", ".dagrun.retry-candidates")
	entries, err := os.ReadDir(candidateDir)
	require.NoError(t, err)
	require.Len(t, entries, 8)

	var candidatePaths []string
	for _, entry := range entries {
		candidatePaths = append(candidatePaths, filepath.Join(candidateDir, entry.Name()))
	}

	from := persis.NewUTC(now.Add(-time.Hour))
	var scanErr error
	mtime := now
	coldAllocs := testing.AllocsPerRun(5, func() {
		mtime = mtime.Add(time.Second)
		for _, path := range candidatePaths {
			if err := os.Chtimes(path, mtime, mtime); err != nil {
				scanErr = err
				return
			}
		}
		_, scanErr = repository.ListRetryCandidates(ctx, from)
	})
	require.NoError(t, scanErr)

	warmAllocs := testing.AllocsPerRun(5, func() {
		_, scanErr = repository.ListRetryCandidates(ctx, from)
	})
	require.NoError(t, scanErr)
	assert.LessOrEqual(t, warmAllocs, coldAllocs*0.75)
}

func TestStoreListRetryCandidatesIgnoresChildAttemptStatusFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{LatestStatusToday: true})

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	parentDAG := retryCandidateDAG()
	parentAttempt, _ := writeRetryCandidateStatus(t, ctx, repository, parentDAG, now, "parent-run", ir.Running)
	defer func() { require.NoError(t, parentAttempt.Close(ctx)) }()

	rootRef := ir.NewDAGRunRef(parentDAG.Name, "parent-run")
	childDAG := retryCandidateDAG()
	childDAG.Name = "child-retry-dag"
	childAttempt, err := repository.CreateAttempt(ctx, childDAG, now.Add(time.Second), "child-run", persis.DAGRunCreateAttemptOptions{
		RootDAGRun: rootRef,
		AttemptID:  "child-attempt",
	})
	require.NoError(t, err)
	require.NoError(t, childAttempt.Open(ctx))
	defer func() { require.NoError(t, childAttempt.Close(ctx)) }()

	childStatus := ir.InitialStatus(childDAG)
	childStatus.DAGRunID = "child-run"
	childStatus.AttemptID = childAttempt.ID()
	childStatus.Status = ir.Failed
	childStatus.StartedAt = now.Add(time.Second).Format(time.RFC3339)
	childStatus.FinishedAt = now.Add(2 * time.Second).Format(time.RFC3339)
	require.NoError(t, childAttempt.Write(ctx, childStatus))

	candidates, err := repository.ListRetryCandidates(ctx, persis.NewUTC(now.Add(-time.Hour)))
	require.NoError(t, err)
	assert.Empty(t, candidates)

	runDir := filepath.Join(
		baseDir,
		parentDAG.Name,
		"dag-runs",
		"2026",
		"06",
		"08",
		"dag-run_20260608_120000Z_parent-run",
	)
	require.NoDirExists(t, filepath.Join(runDir, filedagrun.SubDAGRunsDir, ".dagrun.retry-candidates"))
	require.NoDirExists(t, filepath.Join(runDir, filedagrun.LegacySubDAGRunsDir, ".dagrun.retry-candidates"))
}

func retryCandidateDAG() *ir.DAG {
	return &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
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
	repository *persis.DAGRunRepository,
	dag *ir.DAG,
	ts time.Time,
	runID string,
	status ir.Status,
) (dagrun.Attempt, *ir.DAGRunStatus) {
	t.Helper()

	attempt, err := repository.CreateAttempt(ctx, dag, ts, runID, persis.DAGRunCreateAttemptOptions{
		AttemptID: runID + "-attempt",
	})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))

	runStatus := ir.InitialStatus(dag)
	runStatus.DAGRunID = runID
	runStatus.AttemptID = attempt.ID()
	runStatus.Status = status
	runStatus.StartedAt = ts.Format(time.RFC3339)
	runStatus.FinishedAt = ts.Add(time.Minute).Format(time.RFC3339)
	require.NoError(t, attempt.Write(ctx, runStatus))
	return attempt, &runStatus
}
