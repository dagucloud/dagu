// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestAttemptCloseKeepsSingleStatusFile(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{})
	dag := &ir.DAG{Name: "single-status-close"}
	startedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	attempt, err := repository.CreateAttempt(ctx, dag, startedAt, "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: attempt.ID(),
		Status:    ir.Queued,
		QueuedAt:  stringutil.FormatTime(startedAt),
	}))

	statusFile := findOnlyStatusFile(t, baseDir)
	beforeClose, err := os.Stat(statusFile)
	require.NoError(t, err)

	require.NoError(t, attempt.Close(ctx))

	afterClose, err := os.Stat(statusFile)
	require.NoError(t, err)
	require.True(t, os.SameFile(beforeClose, afterClose), "single-entry close should not replace status file")

	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Queued, status.Status)
}

func TestAttempt_WriteClearsRuntimeConditionsWhenStatusLeavesQueued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{})
	dag := &ir.DAG{Name: "runtime-conditions"}
	startedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	attempt, err := repository.CreateAttempt(ctx, dag, startedAt, "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		_ = attempt.Close(ctx)
	}()

	condition := ir.NewDAGRunCondition(
		"Runnable",
		"False",
		"MaxConcurrencyReached",
		"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
		startedAt,
	)
	queued := ir.DAGRunStatus{
		Name:       dag.Name,
		DAGRunID:   "run-1",
		AttemptID:  attempt.ID(),
		Status:     ir.Queued,
		QueuedAt:   stringutil.FormatTime(startedAt),
		Conditions: []ir.DAGRunCondition{condition},
	}
	require.NoError(t, attempt.Write(ctx, queued))

	persistedQueued, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, []ir.DAGRunCondition{condition}, persistedQueued.Conditions)

	running := queued
	running.Status = ir.Running
	require.NoError(t, attempt.Write(ctx, running))

	persistedRunning, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Running, persistedRunning.Status)
	require.Empty(t, persistedRunning.Conditions)
}

func TestCompareAndSwapLatestAttemptStatusReturnsNormalizedConditions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{})
	dag := &ir.DAG{Name: "conditions-return"}
	startedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	attempt, err := repository.CreateAttempt(ctx, dag, startedAt, "run-conditions", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = attempt.Close(ctx)
		}
	})

	status := ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-conditions",
		AttemptID: attempt.ID(),
		Status:    ir.Queued,
		Conditions: []ir.DAGRunCondition{
			ir.NewDAGRunCondition(
				"Runnable",
				"False",
				"MaxConcurrencyReached",
				"The DAG-run cannot start because the queue active-run concurrency limit has been reached.",
				startedAt,
			),
		},
	}
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
	closed = true

	updated, swapped, err := repository.CompareAndSwapLatestAttemptStatus(
		ctx,
		ir.NewDAGRunRef(dag.Name, "run-conditions"),
		attempt.ID(),
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Failed
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	require.NoError(t, err)
	require.True(t, swapped)
	require.NotNil(t, updated)
	require.Equal(t, ir.Failed, updated.Status)
	require.Empty(t, updated.Conditions)
}

// A compare and swap must not change a status that other readers got from the
// store's cache: they would see a status that is not on disk yet, while the
// swap writes it.
func TestCompareAndSwapLatestAttemptStatusLeavesCachedStatusAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	repository := persis.NewDAGRunRepository(
		filedagrun.NewStore(baseDir, filedagrun.WithHistoryFileCache(
			fileutil.NewCache[*ir.DAGRunStatus]("dag-run-status", 10, time.Minute),
		)),
		filedagrun.NewWorkDirStore(filepath.Join(baseDir, ".dag-run-work"), baseDir),
		persis.DAGRunRepositoryOptions{},
	)
	dag := &ir.DAG{Name: "cas-cache"}

	attempt, err := repository.CreateAttempt(ctx, dag, time.Now(), "run-cas", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-cas",
		AttemptID: attempt.ID(),
		Status:    ir.Queued,
	}))
	require.NoError(t, attempt.Close(ctx))

	ref := ir.NewDAGRunRef(dag.Name, "run-cas")
	found, err := repository.FindAttempt(ctx, ref)
	require.NoError(t, err)
	cached, err := found.ReadStatus(ctx)
	require.NoError(t, err)

	_, swapped, err := repository.CompareAndSwapLatestAttemptStatus(
		ctx,
		ref,
		attempt.ID(),
		ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Aborted
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	require.NoError(t, err)
	require.True(t, swapped)
	require.Equal(t, ir.Queued, cached.Status, "the status read before the swap is unchanged")

	reread, err := found.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Aborted, reread.Status)
}

func newFileRepository(baseDir string, options persis.DAGRunRepositoryOptions) *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(
		filedagrun.NewStore(baseDir),
		filedagrun.NewWorkDirStore(filepath.Join(baseDir, ".dag-run-work"), baseDir),
		options,
	)
}

func findOnlyStatusFile(t *testing.T, root string) string {
	t.Helper()

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != filedagrun.JSONLStatusFile {
			return nil
		}
		matches = append(matches, path)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, matches, 1, fmt.Sprintf("status files under %s", root))
	return matches[0]
}
