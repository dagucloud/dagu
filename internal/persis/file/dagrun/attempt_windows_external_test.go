// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

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

func TestDAGRunRemoveDeletesShortenedWindowsWorkDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunDir := filepath.Join(t.TempDir(), "dag-run_20260703_111258Z_run")
	for len(filepath.Join(dagRunDir, "work")) < 300 {
		dagRunDir = filepath.Join(dagRunDir, "nested-directory")
	}
	require.NoError(t, os.MkdirAll(dagRunDir, 0750))

	run, err := dagrun.NewDAGRun(dagRunDir)
	require.NoError(t, err)

	now := time.Date(2026, 7, 3, 11, 12, 58, 0, time.UTC)
	att, err := run.CreateAttempt(ctx, exec.NewUTC(now), nil, "")
	require.NoError(t, err)
	require.NoError(t, att.Open(ctx))

	status := exec.InitialStatus(&core.DAG{Name: "long-work-dir-cleanup"})
	status.DAGRunID = "run"
	status.Status = core.Succeeded
	require.NoError(t, att.Write(ctx, status))
	require.NoError(t, att.Close(ctx))

	workDir := att.WorkDir()
	require.NotEqual(t, filepath.Join(dagRunDir, "work"), workDir)
	require.DirExists(t, workDir)

	require.NoError(t, run.Remove(ctx))
	require.NoDirExists(t, workDir)
}
