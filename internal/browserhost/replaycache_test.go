// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedReplayCache(t *testing.T, cache *browserhost.ReplayCache, dagName string, steps ...string) {
	t.Helper()
	for _, step := range steps {
		path := cache.Path(dagName, step)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

func TestReplayCacheClearDAG(t *testing.T) {
	t.Parallel()

	cache := browserhost.NewReplayCache(t.TempDir())
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

	cache := browserhost.NewReplayCache(t.TempDir())
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

	cache := browserhost.NewReplayCache(t.TempDir())
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
		cache := browserhost.NewReplayCache(t.TempDir())
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

	cache := browserhost.NewReplayCache(t.TempDir())
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

	cache := browserhost.NewReplayCache(t.TempDir())
	seedReplayCache(t, cache, "billing", "login")

	_, err := cache.Clear("", "")
	require.Error(t, err)

	steps, err := cache.Steps("billing")
	require.NoError(t, err)
	assert.Equal(t, []string{"login"}, steps)
}
