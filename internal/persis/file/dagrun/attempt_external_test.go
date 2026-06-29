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

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestAttemptCloseKeepsSingleStatusFile(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir, dagrun.WithLatestStatusToday(false))
	dag := &core.DAG{Name: "single-status-close"}
	startedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	attempt, err := store.CreateAttempt(ctx, dag, startedAt, "run-1", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: attempt.ID(),
		Status:    core.Queued,
		QueuedAt:  exec.FormatTime(startedAt),
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
	require.Equal(t, core.Queued, status.Status)
}

func TestAttempt_WriteClearsRuntimeConditionsWhenStatusLeavesQueued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir, dagrun.WithLatestStatusToday(false))
	dag := &core.DAG{Name: "runtime-conditions"}
	startedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	attempt, err := store.CreateAttempt(ctx, dag, startedAt, "run-1", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	defer func() {
		_ = attempt.Close(ctx)
	}()

	condition := exec.NewQueuedDAGRunCondition(
		"QueueAccepted",
		"DAG-run is waiting in the queue.",
		startedAt,
	)
	queued := exec.DAGRunStatus{
		Name:       dag.Name,
		DAGRunID:   "run-1",
		AttemptID:  attempt.ID(),
		Status:     core.Queued,
		QueuedAt:   exec.FormatTime(startedAt),
		Conditions: []exec.DAGRunCondition{condition},
	}
	require.NoError(t, attempt.Write(ctx, queued))

	persistedQueued, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, []exec.DAGRunCondition{condition}, persistedQueued.Conditions)

	running := queued
	running.Status = core.Running
	require.NoError(t, attempt.Write(ctx, running))

	persistedRunning, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Running, persistedRunning.Status)
	require.Empty(t, persistedRunning.Conditions)
}

func TestCompareAndSwapLatestAttemptStatusReturnsNormalizedConditions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := dagrun.New(baseDir, dagrun.WithLatestStatusToday(false))
	dag := &core.DAG{Name: "conditions-return"}
	startedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)

	attempt, err := store.CreateAttempt(ctx, dag, startedAt, "run-conditions", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))

	status := exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-conditions",
		AttemptID: attempt.ID(),
		Status:    core.Queued,
		Conditions: []exec.DAGRunCondition{
			exec.NewQueuedDAGRunCondition(
				"QueueAccepted",
				"DAG-run is waiting in the queue.",
				startedAt,
			),
		},
	}
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	updated, swapped, err := store.CompareAndSwapLatestAttemptStatus(
		ctx,
		exec.NewDAGRunRef(dag.Name, "run-conditions"),
		attempt.ID(),
		core.Queued,
		func(latest *exec.DAGRunStatus) error {
			latest.Status = core.Failed
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, swapped)
	require.NotNil(t, updated)
	require.Equal(t, core.Failed, updated.Status)
	require.Empty(t, updated.Conditions)
}

func findOnlyStatusFile(t *testing.T, root string) string {
	t.Helper()

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != dagrun.JSONLStatusFile {
			return nil
		}
		matches = append(matches, path)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, matches, 1, fmt.Sprintf("status files under %s", root))
	return matches[0]
}
