// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package buscal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/core"
)

func writeCalendar(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestStoreLoad(t *testing.T) {
	t.Parallel()

	t.Run("LoadAndCache", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeCalendar(t, dir, "jp-banking.yaml", "holidays: [2026-01-01]")

		store := NewStore(dir)
		calendar, err := store.Load("jp-banking")
		require.NoError(t, err)
		assert.Equal(t, "jp-banking", calendar.Name)

		again, err := store.Load("jp-banking")
		require.NoError(t, err)
		assert.Same(t, calendar, again)
	})

	t.Run("ReloadOnChange", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeCalendar(t, dir, "cal.yaml", "holidays: [2026-01-01]")

		store := NewStore(dir)
		calendar, err := store.Load("cal")
		require.NoError(t, err)
		assert.True(t, calendar.IsHoliday(time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local)))

		require.NoError(t, os.WriteFile(path, []byte("holidays: [2026-01-02]"), 0o600))
		// Force a distinct mtime on filesystems with coarse timestamps.
		require.NoError(t, os.Chtimes(path, time.Now(), time.Now().Add(2*time.Second)))

		reloaded, err := store.Load("cal")
		require.NoError(t, err)
		assert.False(t, reloaded.IsHoliday(time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local)))
		assert.True(t, reloaded.IsHoliday(time.Date(2026, 1, 2, 9, 0, 0, 0, time.Local)))
	})

	t.Run("YmlExtension", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeCalendar(t, dir, "alt.yml", "")

		_, err := NewStore(dir).Load("alt")
		require.NoError(t, err)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		_, err := NewStore(t.TempDir()).Load("missing")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("MissingDir", func(t *testing.T) {
		t.Parallel()
		_, err := NewStore(filepath.Join(t.TempDir(), "nope")).Load("missing")
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("RejectsTraversal", func(t *testing.T) {
		t.Parallel()
		_, err := NewStore(t.TempDir()).Load("../etc/passwd")
		assert.ErrorContains(t, err, "invalid calendar name")
	})
}

func TestServiceDecide(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeCalendar(t, dir, "jp-banking.yaml", "holidays: [2026-01-01]")
	store := NewStore(dir)

	holiday := time.Date(2026, 1, 1, 1, 0, 0, 0, time.Local)
	businessDay := time.Date(2026, 1, 5, 1, 0, 0, 0, time.Local)
	cfg := &core.CalendarConfig{Name: "jp-banking"}

	t.Run("NilConfig", func(t *testing.T) {
		t.Parallel()
		decision := NewService(store, func() bool { return true }).Decide(nil, holiday)
		assert.Equal(t, Decision{}, decision)
	})

	t.Run("Unlicensed", func(t *testing.T) {
		t.Parallel()
		decision := NewService(store, func() bool { return false }).Decide(cfg, holiday)
		assert.Equal(t, DecisionUnlicensed, decision.Kind)

		decision = NewService(store, nil).Decide(cfg, holiday)
		assert.Equal(t, DecisionUnlicensed, decision.Kind)
	})

	t.Run("LicensedSkipsHoliday", func(t *testing.T) {
		t.Parallel()
		service := NewService(store, func() bool { return true })
		decision := service.Decide(cfg, holiday)
		assert.Equal(t, DecisionSkip, decision.Kind)
		assert.Contains(t, decision.Reason, "holiday")

		decision = service.Decide(cfg, businessDay)
		assert.Equal(t, DecisionAllow, decision.Kind)
	})

	t.Run("MissingCalendarErrors", func(t *testing.T) {
		t.Parallel()
		service := NewService(store, func() bool { return true })
		decision := service.Decide(&core.CalendarConfig{Name: "missing"}, businessDay)
		assert.Equal(t, DecisionError, decision.Kind)
		assert.ErrorIs(t, decision.Err, ErrNotFound)
	})
}
