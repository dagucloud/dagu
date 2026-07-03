// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package dagrun_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestAttemptWorkDirShortensLongWindowsPath(t *testing.T) {
	t.Parallel()

	dagRunDir := filepath.Join(t.TempDir(), "dag-run_20260703_111258Z_run")
	for len(filepath.Join(dagRunDir, "work")) < 300 {
		dagRunDir = filepath.Join(dagRunDir, "nested-directory")
	}
	attemptDir := filepath.Join(dagRunDir, "attempt_20260703_111258_000Z_attempt")
	statusFile := filepath.Join(attemptDir, dagrun.JSONLStatusFile)

	att, err := dagrun.NewAttempt(statusFile, nil)
	require.NoError(t, err)

	defaultWorkDir := filepath.Join(dagRunDir, "work")
	workDir := att.WorkDir()
	require.NotEqual(t, defaultWorkDir, workDir)
	require.Less(t, len(workDir), 248)
	require.NoError(t, os.MkdirAll(workDir, 0750))

	attAgain, err := dagrun.NewAttempt(statusFile, nil)
	require.NoError(t, err)
	require.Equal(t, workDir, attAgain.WorkDir())
}
