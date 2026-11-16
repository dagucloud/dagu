// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package replaycache_test

import (
	"os"
	"path/filepath"
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

// Step keys that differ only in characters a file name cannot hold, such as
// a.b and a_b, keep separate records.
func TestReplayCacheKeepsSimilarStepKeysApart(t *testing.T) {
	t.Parallel()

	cache := replaycache.New(t.TempDir())
	seedReplayCache(t, cache, "billing", "a.b", "a_b")
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

// Earlier versions kept a device name such as con as written, which
// Windows 11 stores, so a run of the step claims the records under it.
func TestReplayCacheReadsRawDeviceName(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	raw := filepath.Join(dataDir, "cache", "con", "con.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(raw), 0o700))
	require.NoError(t, os.WriteFile(raw, []byte(`{"k":{}}`), 0o600))

	cache := replaycache.New(dataDir)
	require.NoError(t, cache.Claim("con", "con", []string{"con"}))
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

			cache := replaycache.New(t.TempDir())
			legacy := cache.Path("billing", "fetch_data")
			require.NoError(t, os.MkdirAll(filepath.Dir(legacy), 0o700))
			require.NoError(t, os.WriteFile(legacy, []byte(`{"k":{}}`), 0o600))

			require.NoError(t, cache.Claim("billing", "fetch data", tc.stepKeys))
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
