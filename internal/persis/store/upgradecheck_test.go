// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/upgrade"
)

func sampleUpgradeCache() *upgrade.UpgradeCheckCache {
	return &upgrade.UpgradeCheckCache{
		LastCheck:       time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		LatestVersion:   "v1.30.3",
		CurrentVersion:  "v1.30.0",
		UpdateAvailable: true,
	}
}

func newMemoryUpgradeCheckStore() *store.UpgradeCheckStore {
	col := testutil.NewMemoryBackend().Collection("upgrade")
	return store.NewUpgradeCheckStore(col)
}

// newFileUpgradeCheckStore constructs the store the same way production wiring does.
func newFileUpgradeCheckStore(t *testing.T, dir string) *store.UpgradeCheckStore {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	return store.NewUpgradeCheckStore(file.NewCollection(dir, file.WithIndentedJSON()))
}

// ─── Interface-level tests (memory-backed) ────────────────────────────────────

func TestUpgradeCheckStore_SaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemoryUpgradeCheckStore()

	original := sampleUpgradeCache()
	require.NoError(t, s.Save(original))

	loaded, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, original.LatestVersion, loaded.LatestVersion)
	assert.Equal(t, original.CurrentVersion, loaded.CurrentVersion)
	assert.Equal(t, original.UpdateAvailable, loaded.UpdateAvailable)
	assert.True(t, loaded.LastCheck.Equal(original.LastCheck))
}

func TestUpgradeCheckStore_Save_Overwrite(t *testing.T) {
	t.Parallel()
	s := newMemoryUpgradeCheckStore()

	require.NoError(t, s.Save(sampleUpgradeCache()))

	updated := &upgrade.UpgradeCheckCache{
		LastCheck:       time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		LatestVersion:   "v2.0.0",
		CurrentVersion:  "v1.30.0",
		UpdateAvailable: true,
	}
	require.NoError(t, s.Save(updated))

	loaded, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, updated.LatestVersion, loaded.LatestVersion)
	assert.True(t, loaded.LastCheck.Equal(updated.LastCheck))
}

func TestUpgradeCheckStore_Load_NoRecord(t *testing.T) {
	t.Parallel()
	s := newMemoryUpgradeCheckStore()

	got, err := s.Load()
	assert.NoError(t, err)
	assert.Nil(t, got)
}

// ─── File-layout compatibility tests ──────────────────────────────────────────

// On-disk bytes equal json.MarshalIndent(v, "", "  ") at {dir}/upgrade-check.json.
func TestUpgradeCheckStore_File_OnDiskFormatMatchesReleasedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileUpgradeCheckStore(t, dir)

	cache := sampleUpgradeCache()
	require.NoError(t, s.Save(cache))

	got, err := os.ReadFile(filepath.Join(dir, "upgrade-check.json"))
	require.NoError(t, err)

	expected, err := json.MarshalIndent(cache, "", "  ")
	require.NoError(t, err)

	assert.True(t, bytes.Equal(got, expected),
		"on-disk bytes must equal json.MarshalIndent output\n  got:  %q\n  want: %q",
		string(got), string(expected))
}

// Files written by older binaries (json.MarshalIndent, 0o600) decode through Load.
func TestUpgradeCheckStore_File_ReadsReleasedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o750))

	cache := sampleUpgradeCache()
	raw, err := json.MarshalIndent(cache, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "upgrade-check.json"), raw, 0o600))

	s := store.NewUpgradeCheckStore(file.NewCollection(dir, file.WithIndentedJSON()))
	loaded, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, cache.LatestVersion, loaded.LatestVersion)
	assert.True(t, loaded.LastCheck.Equal(cache.LastCheck))
}

func TestUpgradeCheckStore_File_FilePermissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileUpgradeCheckStore(t, dir)

	require.NoError(t, s.Save(sampleUpgradeCache()))

	info, err := os.Stat(filepath.Join(dir, "upgrade-check.json"))
	require.NoError(t, err)

	if testutil.SupportsPOSIXPermissionBits() {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
			"upgrade-check.json must be 0600")
	}
}

func TestUpgradeCheckStore_File_Save_PrettyPrinted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileUpgradeCheckStore(t, dir)

	require.NoError(t, s.Save(sampleUpgradeCache()))

	raw, err := os.ReadFile(filepath.Join(dir, "upgrade-check.json"))
	require.NoError(t, err)
	content := string(raw)
	assert.True(t, strings.Contains(content, "\n"), "on-disk JSON must be newline-separated")
	assert.True(t, strings.Contains(content, "  "), "on-disk JSON must be 2-space indented")
}

// Invalid on-disk JSON loads as (nil, nil) so callers fall back to a fresh check.
func TestUpgradeCheckStore_File_Load_InvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "upgrade-check.json"), []byte("invalid json{"), 0o600))

	s := store.NewUpgradeCheckStore(file.NewCollection(dir, file.WithIndentedJSON()))
	got, err := s.Load()
	assert.NoError(t, err, "Load() must not error on invalid JSON")
	assert.Nil(t, got, "Load() must return nil for invalid JSON")
}

func TestUpgradeCheckStore_File_Concurrent_SaveLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileUpgradeCheckStore(t, dir)

	require.NoError(t, s.Save(sampleUpgradeCache()))

	const numWorkers = 10
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*2)

	for range numWorkers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := s.Save(sampleUpgradeCache()); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := s.Load(); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	assert.Empty(t, errs)
}
