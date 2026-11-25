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

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
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
	require.NoError(t, mr.move(context.Background(), move{From: from, To: to}, holding))

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

	require.NoError(t, newMover().move(context.Background(), move{From: from, To: to}, holding))

	assert.NoDirExists(t, from)
	assert.FileExists(t, filepath.Join(to, "item.json"))
}

// A move whose process lost the lock changes nothing, so the process that
// took the lock over never races it.
func TestMoveStopsWithoutLock(t *testing.T) {
	t.Parallel()
	lost := func() error { return dirlock.ErrLockNotHeld }

	for _, tc := range []struct {
		name           string
		sameFileSystem func(string, string) (bool, error)
	}{
		{name: "Rename", sameFileSystem: func(string, string) (bool, error) { return true, nil }},
		{name: "Copy", sameFileSystem: otherFileSystem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			from := v2Dir(t, root, "queue", "etl", "item.json")
			to := filepath.Join(root, "projects", "default", "state", "queue", "etl")
			mr := newMover()
			mr.sameFileSystem = tc.sameFileSystem

			err := mr.move(context.Background(), move{From: from, To: to}, lost)
			require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
			assert.FileExists(t, filepath.Join(from, "item.json"))
			assert.NoDirExists(t, to)
			assert.NoDirExists(t, to+copySuffix)
		})
	}
}

func holding() error { return nil }
