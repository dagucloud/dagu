// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package replaycache_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// writeLegacyRecords writes records where released versions kept them: in
// the DAG's directory, under the SafeName of a step key.
func writeLegacyRecords(t *testing.T, dataDir, dagName, safeName string) string {
	t.Helper()
	path := filepath.Join(dataDir, "cache", dagName, safeName+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"k":{}}`), 0o600))
	return path
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

// Step keys that differ only in characters a file name cannot hold, such as
// a.b and a_b, keep separate records.
func TestReplayCacheKeepsSimilarStepKeysApart(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	for _, key := range []string{"a.b", "a_b"} {
		records := replaycache.Open[string](cache.Path("billing", key))
		records.Stage("k", key)
		require.NoError(t, records.Commit(t.Context()))
	}
	assert.NotEqual(t, cache.Path("billing", "a.b"), cache.Path("billing", "a_b"))

	removed, err := cache.Clear("billing", "a.b")
	require.NoError(t, err)
	assert.Len(t, removed, 1)
	assert.FileExists(t, cache.Path("billing", "a_b"))
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

// A released version's file that two step keys shared, such as a_b.json for
// a.b and a_b, is read by neither of them, not even by a_b, whose own file
// has that name.
func TestReplayCacheHidesSharedLegacyRecords(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cache := replaycache.New(dataDir)
	writeLegacyRecords(t, dataDir, "billing", "a_b")

	stepKeys := []string{"a.b", "a_b"}
	for _, key := range stepKeys {
		require.NoError(t, cache.Claim(t.Context(), "billing", key, stepKeys))
	}
	for _, key := range stepKeys {
		entries, err := cache.Entries("billing", key)
		require.NoError(t, err)
		assert.Empty(t, entries, key)
	}
}

// Records that this version stores never mix with the file that released
// versions stored under the same name, which another step may have written.
func TestReplayCacheKeepsRecordsOfThisVersionApart(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cache := replaycache.New(dataDir)
	writeLegacyRecords(t, dataDir, "billing", "a_b")
	records := replaycache.Open[string](cache.Path("billing", "a_b"))
	records.Stage("new", "v")
	require.NoError(t, records.Commit(t.Context()))

	require.NoError(t, cache.Claim(t.Context(), "billing", "a.b", []string{"a.b", "a_b"}))
	entries, err := cache.Entries("billing", "a_b")
	require.NoError(t, err)
	assert.Equal(t, []string{"new"}, slices.Collect(maps.Keys(entries)))
}

// A step key too long for a file name once escaped, such as one of many
// non-ASCII characters, is listed so that its records read back, also after
// another step claims first.
func TestReplayCacheListsLongStepKeys(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cache := replaycache.New(dataDir)
	key := strings.Repeat("日", 12)
	writeLegacyRecords(t, dataDir, "billing", strings.Repeat("_", 12))
	stepKeys := []string{"other", key}
	require.NoError(t, cache.Claim(t.Context(), "billing", "other", stepKeys))
	steps, err := cache.Steps("billing")
	require.NoError(t, err)
	assert.Empty(t, steps, "nothing is listed in a form that does not read back")

	require.NoError(t, cache.Claim(t.Context(), "billing", key, stepKeys))
	steps, err = cache.Steps("billing")
	require.NoError(t, err)
	assert.Equal(t, []string{key}, steps)
	entries, err := cache.Entries("billing", steps[0])
	require.NoError(t, err)
	assert.Contains(t, entries, "k")
}

// Clearing a step before any run claims it also removes the records released
// versions stored under its SafeName, so a later run does not bring them back.
func TestReplayCacheClearRemovesLegacyRecords(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cache := replaycache.New(dataDir)
	writeLegacyRecords(t, dataDir, "billing", "fetch_data")
	removed, err := cache.Clear("billing", "fetch data")
	require.NoError(t, err)
	assert.Equal(t, []string{"fetch data"}, removed)

	require.NoError(t, cache.Claim(t.Context(), "billing", "fetch data", []string{"fetch data"}))
	entries, err := cache.Entries("billing", "fetch data")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// Earlier versions kept a device name such as con as written, which
// Windows 11 stores, so a run of the step claims the records under it.
func TestReplayCacheReadsRawDeviceName(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	writeLegacyRecords(t, dataDir, "con", "con")

	cache := replaycache.New(dataDir)
	require.NoError(t, cache.Claim(t.Context(), "con", "con", []string{"con"}))
	entries, err := cache.Entries("con", "con")
	require.NoError(t, err)
	assert.Contains(t, entries, "k")
	steps, err := cache.Steps("con")
	require.NoError(t, err)
	assert.Equal(t, []string{"con"}, steps)
}

// Released versions stored a step's records under the SafeName of its key,
// which other keys could share. A run claims them only when no other step of
// the DAG had that SafeName, so a step never reads another step's records.
func TestReplayCacheClaimsRecordsItOwns(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		stepKeys []string
		claimed  bool
	}{
		{name: "Owned", stepKeys: []string{"fetch data", "parse"}, claimed: true},
		{name: "SharedWithLiteral", stepKeys: []string{"fetch data", "fetch_data"}},
		{name: "SharedWithAnother", stepKeys: []string{"fetch data", "fetch.data"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dataDir := t.TempDir()
			cache := replaycache.New(dataDir)
			legacy := writeLegacyRecords(t, dataDir, "billing", "fetch_data")

			require.NoError(t, cache.Claim(t.Context(), "billing", "fetch data", tc.stepKeys))
			entries, err := cache.Entries("billing", "fetch data")
			require.NoError(t, err)
			if tc.claimed {
				assert.Contains(t, entries, "k")
				assert.NoFileExists(t, legacy)
			} else {
				assert.Empty(t, entries)
				assert.FileExists(t, legacy)
			}
		})
	}
}
