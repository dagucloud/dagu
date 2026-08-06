// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package materialization

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestAcquirePathsAllowsReadersAndExcludesWriter(t *testing.T) {
	t.Parallel()

	store := New(t.TempDir())
	request := []exec.PathLockRequest{{Key: "/data/input.txt", Mode: exec.PathLockShared}}
	first, err := store.AcquirePaths(context.Background(), request)
	require.NoError(t, err)
	second, err := store.AcquirePaths(context.Background(), request)
	require.NoError(t, err)

	blockedCtx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	_, err = store.AcquirePaths(blockedCtx, []exec.PathLockRequest{{
		Key:  "/data/input.txt",
		Mode: exec.PathLockExclusive,
	}})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, second.Release())
	require.NoError(t, first.Release())
	writer, err := store.AcquirePaths(context.Background(), []exec.PathLockRequest{{
		Key:  "/data/input.txt",
		Mode: exec.PathLockExclusive,
	}})
	require.NoError(t, err)
	require.NoError(t, writer.Release())
}

func TestRestorePreviousBeforeBackupExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	finalPath := filepath.Join(dir, "output.txt")
	manifestPath := filepath.Join(dir, "manifest.json")
	require.NoError(t, os.WriteFile(finalPath, []byte("known-good"), 0o600))
	previous, err := snapshotFile(finalPath)
	require.NoError(t, err)

	err = restorePrevious(commitJournal{
		FinalPath:        finalPath,
		BackupPath:       filepath.Join(dir, "missing-backup"),
		ManifestPath:     manifestPath,
		PreviousFinal:    &previous,
		PreviousManifest: []byte(`{"commitId":"previous"}`),
	})
	require.NoError(t, err)
	content, err := os.ReadFile(finalPath)
	require.NoError(t, err)
	require.Equal(t, "known-good", string(content))
	manifest, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.JSONEq(t, `{"commitId":"previous"}`, string(manifest))
}

func TestRestorePreviousPreservesUnknownFinal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	finalPath := filepath.Join(dir, "output.txt")
	require.NoError(t, os.WriteFile(finalPath, []byte("user-data"), 0o600))
	proposedPath := filepath.Join(dir, "proposed.txt")
	require.NoError(t, os.WriteFile(proposedPath, []byte("proposed"), 0o600))
	proposed, err := snapshotFile(proposedPath)
	require.NoError(t, err)
	proposed.Path = finalPath

	err = restorePrevious(commitJournal{
		FinalPath: finalPath,
		Proposed:  exec.Materialization{Output: proposed},
	})
	require.Error(t, err)
	content, readErr := os.ReadFile(finalPath)
	require.NoError(t, readErr)
	require.Equal(t, "user-data", string(content))
}
