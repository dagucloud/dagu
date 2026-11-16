// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package replaycache_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedReplayCache(t *testing.T, cache *replaycache.Store, dagName string, steps ...string) {
	t.Helper()
	for _, step := range steps {
		path := cache.Path(dagName, step)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

func TestReplayCacheClearDAG(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "login", "download")
	seedReplayCache(t, cache, "other", "login")

	removed, err := cache.Clear("billing", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"download", "login"}, removed)

	steps, err := cache.Steps("billing")
	require.NoError(t, err)
	assert.Empty(t, steps)

	steps, err = cache.Steps("other")
	require.NoError(t, err)
	assert.Equal(t, []string{"login"}, steps, "other DAGs keep their records")
}

func TestReplayCacheClearStep(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "login", "download")

	removed, err := cache.Clear("billing", "login")
	require.NoError(t, err)
	assert.Equal(t, []string{"login"}, removed)

	steps, err := cache.Steps("billing")
	require.NoError(t, err)
	assert.Equal(t, []string{"download"}, steps)
}

// A step without an ID is keyed by its name, which may hold characters a file
// name cannot.
func TestReplayCacheClearStepByName(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "Log in")

	removed, err := cache.Clear("billing", "Log in")
	require.NoError(t, err)
	assert.Equal(t, []string{"Log in"}, removed)
}

// Names that map to the same file-safe form still keep separate records. The
// second pair also shares the first 16 bits of its SHA-256.
func TestReplayCacheKeepsDAGsApart(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]string{
		{"etl.daily", "etl_daily"},
		{"x..._...__...x", "x..._._._..._x"},
	} {
		cache := replaycache.New(t.TempDir())
		seedReplayCache(t, cache, pair[0], "login")
		seedReplayCache(t, cache, pair[1], "login")

		removed, err := cache.Clear(pair[0], "")
		require.NoError(t, err)
		assert.Equal(t, []string{"login"}, removed, pair[0])

		steps, err := cache.Steps(pair[1])
		require.NoError(t, err)
		assert.Equal(t, []string{"login"}, steps, "%s keeps its records", pair[1])
	}
}

func TestReplayCacheClearMissing(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "login")

	removed, err := cache.Clear("unknown", "")
	require.NoError(t, err)
	assert.Empty(t, removed)

	removed, err = cache.Clear("billing", "unknown")
	require.NoError(t, err)
	assert.Empty(t, removed)
}

// An empty DAG name would address every DAG's records.
func TestReplayCacheClearRequiresDAGName(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "login")

	_, err := cache.Clear("", "")
	require.Error(t, err)

	steps, err := cache.Steps("billing")
	require.NoError(t, err)
	assert.Equal(t, []string{"login"}, steps)
}

// Step keys con and con_ keep separate records on every system, although
// Windows does not store con as written.
func TestReplayCacheKeepsDeviceNamedStepApart(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "con", "con_")
	assert.NotEqual(t, cache.Path("billing", "con"), cache.Path("billing", "con_"))

	removed, err := cache.Clear("billing", "con")
	require.NoError(t, err)
	assert.Equal(t, []string{"con"}, removed)
	assert.FileExists(t, cache.Path("billing", "con_"))
}

// A step key whose raw name cleans to another key, such as ./b for b, keeps
// its own records.
func TestReplayCacheKeepsCleanedKeysApart(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "b")
	for _, key := range []string{"./b", "x/../b", "b/."} {
		assert.NotEqual(t, cache.Path("billing", "b"), cache.Path("billing", key), key)
	}
}

// Step keys that differ only in characters a file name cannot hold, such as
// a.b and a_b, keep separate records.
func TestReplayCacheKeepsSimilarStepKeysApart(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	for _, key := range []string{"a.b", "a_b"} {
		records := replaycache.OpenStep[string](cache, "billing", key)
		records.Stage("k", key)
		require.NoError(t, records.Commit(t.Context()))
	}
	assert.NotEqual(t, cache.Path("billing", "a.b"), cache.Path("billing", "a_b"))

	removed, err := cache.Clear("billing", "a.b")
	require.NoError(t, err)
	assert.Len(t, removed, 1)
	assert.FileExists(t, cache.Path("billing", "a_b"))
}

// Released versions stored records under the SafeName of a step key, which
// other keys could share, so no step reads them, not even a_b, whose name
// SafeName kept.
func TestReplayCacheIgnoresLegacyRecords(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	legacy := filepath.Join(dataDir, "cache", "billing", "a_b.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o700))
	require.NoError(t, os.WriteFile(legacy, []byte(`{"k":{}}`), 0o600))

	cache := replaycache.New(dataDir)
	entries, err := cache.Entries("billing", "a_b")
	require.NoError(t, err)
	assert.Empty(t, entries)
	steps, err := cache.Steps("billing")
	require.NoError(t, err)
	assert.Empty(t, steps)
}

// A step key too long for a file name once escaped, such as one of many
// non-ASCII characters, is listed in full, so its records read back.
func TestReplayCacheListsLongStepKeys(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	key := strings.Repeat("日", 12)
	records := replaycache.OpenStep[string](cache, "billing", key)
	records.Stage("k", "v")
	require.NoError(t, records.Commit(t.Context()))

	steps, err := cache.Steps("billing")
	require.NoError(t, err)
	assert.Equal(t, []string{key}, steps)
	entries, err := cache.Entries("billing", steps[0])
	require.NoError(t, err)
	assert.Contains(t, entries, "k")
}
