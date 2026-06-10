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
