// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSameFileSystem(t *testing.T) {
	t.Parallel()

	t.Run("Siblings", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
		require.NoError(t, os.Mkdir(a, 0o750))
		require.NoError(t, os.Mkdir(b, 0o750))

		same, err := SameFileSystem(a, b)
		require.NoError(t, err)
		assert.True(t, same)
	})

	// A path not created yet lands on the file system of its nearest
	// existing ancestor.
	t.Run("MissingPath", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		same, err := SameFileSystem(dir, filepath.Join(dir, "projects", "weather"))
		require.NoError(t, err)
		assert.True(t, same)
	})

	t.Run("DifferentFileSystems", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("no second volume to rely on")
		}

		// /dev is its own file system on Linux and macOS.
		same, err := SameFileSystem(t.TempDir(), "/dev")
		require.NoError(t, err)
		assert.False(t, same)
	})
}

func TestFreeSpace(t *testing.T) {
	t.Parallel()

	free, err := FreeSpace(filepath.Join(t.TempDir(), "not", "created"))
	require.NoError(t, err)
	assert.Positive(t, free, "judged by the nearest existing ancestor")
}
