// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A copy keeps file modes and modification times, which stores such as
// suspend flags and process heartbeats read.
func TestMoveCopyKeepsModesAndTimes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	from := filepath.Join(root, "v2", "proc")
	nested := filepath.Join(from, "etl", "2026")
	require.NoError(t, os.MkdirAll(nested, 0o750))
	file := filepath.Join(nested, "heartbeat")
	require.NoError(t, os.WriteFile(file, []byte("beat"), 0o640))
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, os.Chtimes(file, old, old))
	require.NoError(t, os.Chtimes(nested, old, old))

	to := filepath.Join(root, "projects", "default", "state", "proc")
	mr := newMover()
	mr.sameFileSystem = otherFileSystem
	require.NoError(t, mr.move(context.Background(), move{From: from, To: to}))

	assert.NoDirExists(t, from)
	copied := filepath.Join(to, "etl", "2026", "heartbeat")
	info, err := os.Stat(copied)
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(old), "file time kept")
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(copied))
	require.NoError(t, err)
	assert.True(t, dirInfo.ModTime().Equal(old), "directory time kept")
}

// A destination beside its source is a copy an earlier attempt installed
// before it was killed; the move only removes the source.
func TestMoveFinishesInstalledCopy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	from := v2Dir(t, root, "queue", "etl", "item.json")
	to := v2Dir(t, filepath.Join(root, "projects"), "queue", "etl", "item.json")

	require.NoError(t, newMover().move(context.Background(), move{From: from, To: to}))

	assert.NoDirExists(t, from)
	assert.FileExists(t, filepath.Join(to, "item.json"))
}
