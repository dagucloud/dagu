// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestAttemptWorkDirUsesRootRunAnchorForSubDAG(t *testing.T) {
	t.Parallel()

	rootRunDir := filepath.Join(t.TempDir(), "dag-run_20260705_120000Z_root-run")
	rootAttemptFile := filepath.Join(
		rootRunDir,
		"attempt_20260705_120000_000Z_root-attempt",
		dagrun.JSONLStatusFile,
	)
	rootAttempt, err := dagrun.NewAttempt(rootAttemptFile, nil)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(rootRunDir, "work"), rootAttempt.WorkDir())

	childRunID := "child-run"
	childRunDir := filepath.Join(rootRunDir, dagrun.SubDAGRunsDir, dagrun.SubDAGRunDirPrefix+childRunID)
	childAttemptFile := filepath.Join(
		childRunDir,
		"attempt_20260705_120001_000Z_child-attempt",
		dagrun.JSONLStatusFile,
	)
	childAttempt, err := dagrun.NewAttempt(childAttemptFile, nil)
	require.NoError(t, err)

	require.Equal(t, filepath.Join(rootRunDir, dagrun.SubDAGWorkDirNameForTest(childRunID)), childAttempt.WorkDir())
	require.NotEqual(t, filepath.Join(childRunDir, "work"), childAttempt.WorkDir())
}
