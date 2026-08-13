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

func newFileRepository(baseDir string, options persis.DAGRunRepositoryOptions) *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(
		filedagrun.NewStore(baseDir),
		filedagrun.NewWorkspaceStore(baseDir),
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
