// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis"
	filecalendar "github.com/dagucloud/dagu/v2/internal/persis/file/calendar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T, dir string) (*filecalendar.Store, *int) {
	t.Helper()
	changes := 0
	store, err := filecalendar.New(dir, filecalendar.WithOnChange(func(context.Context) { changes++ }))
	require.NoError(t, err)
	return store, &changes
}

func TestStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "calendars")
	store, changes := newStore(t, dir)

	_, err := store.Get(ctx, "ops")
	require.ErrorIs(t, err, persis.ErrCalendarNotFound)
	require.ErrorIs(t, store.Update(ctx, "ops", []byte("holidays: []\n")), persis.ErrCalendarNotFound)

	require.NoError(t, store.Create(ctx, "ops", []byte("holidays: []\n")), "creates the directory")
	require.ErrorIs(t, store.Create(ctx, "ops", []byte("x")), persis.ErrCalendarAlreadyExists)
	data, err := os.ReadFile(filepath.Join(dir, "ops.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "holidays: []\n", string(data))

	require.NoError(t, store.Update(ctx, "ops", []byte("holidays: [2027-01-01]\n")))
	data, err = store.Get(ctx, "ops")
	require.NoError(t, err)
	assert.Equal(t, "holidays: [2027-01-01]\n", string(data))

	require.NoError(t, store.Delete(ctx, "ops"))
	require.ErrorIs(t, store.Delete(ctx, "ops"), persis.ErrCalendarNotFound)
	assert.Equal(t, 3, *changes, "one change per successful write")
}

func TestStoreRejectsNamesOutsideItsDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	store, changes := newStore(t, filepath.Join(root, "calendars"))

	require.Error(t, store.Create(ctx, "../escape", []byte("holidays: []\n")))
	_, err := store.Get(ctx, "../escape")
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(root, "escape.yaml"))
	assert.Zero(t, *changes)
}

// A calendar file linked from a shared location stays linked.
func TestStoreUpdatesThroughSymlinks(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}

	ctx := context.Background()
	root := t.TempDir()
	shared := filepath.Join(root, "shared.yaml")
	require.NoError(t, os.WriteFile(shared, []byte("holidays: []\n"), 0o600))
	dir := filepath.Join(root, "calendars")
	require.NoError(t, os.Mkdir(dir, 0o750))
	link := filepath.Join(dir, "ops.yaml")
	require.NoError(t, os.Symlink(shared, link))
	store, _ := newStore(t, dir)

	require.NoError(t, store.Update(ctx, "ops", []byte("holidays: [2027-01-01]\n")))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	data, err := os.ReadFile(shared)
	require.NoError(t, err)
	assert.Equal(t, "holidays: [2027-01-01]\n", string(data))
}
