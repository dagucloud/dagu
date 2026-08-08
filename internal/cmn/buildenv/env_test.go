// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package buildenv

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareAndLoad(t *testing.T) {
	extraEnv, cleanup, err := Prepare(NewSnapshot([]string{
		"SECOND=value-2",
		"FIRST=value-1",
		"SECOND=latest",
	}, true))
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	require.Len(t, extraEnv, 1)

	key, path, ok := strings.Cut(extraEnv[0], "=")
	require.True(t, ok)
	require.Equal(t, PresolvedEnvFileKey, key)

	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	t.Setenv(PresolvedEnvFileKey, path)

	loaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, Snapshot{
		Env: map[string]string{
			"FIRST":  "value-1",
			"SECOND": "latest",
		},
		RuntimeResolved: true,
	}, loaded)
}

func TestPrepare_EmptyEnv(t *testing.T) {
	t.Run("Unresolved", func(t *testing.T) {
		extraEnv, cleanup, err := Prepare(Snapshot{})
		require.NoError(t, err)
		assert.Nil(t, extraEnv)
		assert.Nil(t, cleanup)
	})

	t.Run("Resolved", func(t *testing.T) {
		extraEnv, cleanup, err := Prepare(Snapshot{RuntimeResolved: true})
		require.NoError(t, err)
		require.NotNil(t, cleanup)
		t.Cleanup(func() { require.NoError(t, cleanup()) })
		require.Len(t, extraEnv, 1)

		key, path, ok := strings.Cut(extraEnv[0], "=")
		require.True(t, ok)
		t.Setenv(key, path)
		loaded, err := Load()
		require.NoError(t, err)
		assert.Equal(t, Snapshot{RuntimeResolved: true}, loaded)
	})
}

func TestLoadLegacyMap(t *testing.T) {
	path := t.TempDir() + "/env.json"
	require.NoError(t, os.WriteFile(path, []byte(`{"KEY":"value"}`), 0o600))
	t.Setenv(PresolvedEnvFileKey, path)

	loaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, Snapshot{Env: map[string]string{"KEY": "value"}}, loaded)
}
