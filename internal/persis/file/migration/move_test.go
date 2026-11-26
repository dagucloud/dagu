// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"fmt"
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
	require.NoError(t, mr.move(context.Background(), move{From: from, To: to}, fresh, holding))

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

// A destination beside its source that the journal records is the copy of
// an earlier attempt killed before it removed the source; the move only
// removes the source.
func TestMoveFinishesRecordedCopy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	from := v2Dir(t, root, "queue", "etl", "item.json")
	to := v2Dir(t, filepath.Join(root, "projects"), "queue", "etl", "item.json")
	install := installRecord{recorded: true, record: holding}

	require.NoError(t, newMover().move(context.Background(), move{From: from, To: to}, install, holding))

	assert.NoDirExists(t, from)
	assert.FileExists(t, filepath.Join(to, "item.json"))
}

// A destination beside its source that the journal does not record is state
// the migration did not make, so the move refuses and both stay.
func TestMoveRefusesForeignDestination(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	from := v2Dir(t, root, "queue", "etl", "item.json")
	to := v2Dir(t, filepath.Join(root, "projects"), "queue", "etl", "item.json")

	err := newMover().move(context.Background(), move{From: from, To: to}, fresh, holding)

	require.ErrorIs(t, err, errForeignDestination)
	assert.FileExists(t, filepath.Join(from, "item.json"))
	assert.FileExists(t, filepath.Join(to, "item.json"))
}

// A copy is recorded once it is complete and before it takes the
// destination's name, so no installed copy lacks its record.
func TestMoveRecordsCopyBeforeInstall(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	from := v2Dir(t, root, "queue", "etl", "item.json")
	to := filepath.Join(root, "projects", "default", "state", "queue", "etl")
	mr := newMover()
	mr.sameFileSystem = otherFileSystem
	recorded := false
	install := installRecord{record: func() error {
		recorded = true
		assert.NoDirExists(t, to)
		assert.FileExists(t, filepath.Join(to+copySuffix, "item.json"))
		return nil
	}}

	require.NoError(t, mr.move(context.Background(), move{From: from, To: to}, install, holding))

	assert.True(t, recorded)
	assert.FileExists(t, filepath.Join(to, "item.json"))
	assert.NoDirExists(t, from)
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

			err := mr.move(context.Background(), move{From: from, To: to}, fresh, lost)
			require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
			assert.FileExists(t, filepath.Join(from, "item.json"))
			assert.NoDirExists(t, to)
			assert.NoDirExists(t, to+copySuffix)
		})
	}
}

// Run stores are mostly small files, which take whole blocks on disk.
func TestCopySizeCountsAllocationUnits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for i := range 100 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("status-%d.json", i)), make([]byte, 100), 0o600))
	}

	size, err := copySize(dir)
	require.NoError(t, err)
	units := uint64(100 + 1) // the files and their directory
	assert.Equal(t, units*allocationUnit+units*allocationUnit/16, size)
}

// Copies to one file system need room for all of them together, and copies
// to another file system do not count against it.
func TestShortageSumsCopiesPerFileSystem(t *testing.T) {
	t.Parallel()
	// Each parent directory stands for a file system.
	mr := mover{sameFileSystem: func(a, b string) (bool, error) {
		return filepath.Dir(a) == filepath.Dir(b), nil
	}}
	copies := []pendingCopy{
		{move: move{To: filepath.Join("a", "x")}, size: 3},
		{move: move{To: filepath.Join("b", "z")}, size: 4},
		{move: move{To: filepath.Join("a", "y")}, size: 3},
	}

	for _, tc := range []struct {
		name string
		free map[string]uint64
		want *SpaceShortage
	}{
		{name: "Fits", free: map[string]uint64{"a": 6, "b": 4}},
		{name: "Short", free: map[string]uint64{"a": 5, "b": 100}, want: &SpaceShortage{Path: "a", Needed: 6, Free: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mr := mr
			mr.freeSpace = func(path string) (uint64, error) { return tc.free[filepath.Dir(path)], nil }

			got, err := mr.shortage(copies)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// An irregular entry is a link only when the system reads it as one. A
// directory that reports as irregular, such as a cloud-sync placeholder, is
// never a link, and an irregular file that is not one moves as a file.
func TestReadLinkTellsIrregularEntriesApart(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "placeholder")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	for _, mode := range []os.FileMode{os.ModeIrregular, os.ModeDir | os.ModeIrregular} {
		target, link, err := readLink(file, mode)
		require.NoError(t, err, mode)
		assert.False(t, link, mode)
		assert.Empty(t, target, mode)
	}
}

func holding() error { return nil }

// fresh is the install record of a move that no earlier attempt started.
var fresh = installRecord{record: holding}
