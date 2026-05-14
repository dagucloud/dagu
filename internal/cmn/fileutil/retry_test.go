// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReplaceFileWithRetry verifies replacing existing and missing target files.
func TestReplaceFileWithRetry(t *testing.T) {
	t.Parallel()

	t.Run("overwrites existing target", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		source := filepath.Join(dir, "source.txt")
		target := filepath.Join(dir, "target.txt")
		require.NoError(t, os.WriteFile(source, []byte("new"), 0o600))
		require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))

		require.NoError(t, ReplaceFileWithRetry(source, target))

		data, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, []byte("new"), data)
		require.NoFileExists(t, source)
	})

	t.Run("creates missing target", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		source := filepath.Join(dir, "source.txt")
		target := filepath.Join(dir, "target.txt")
		require.NoError(t, os.WriteFile(source, []byte("new"), 0o600))

		require.NoError(t, ReplaceFileWithRetry(source, target))

		data, err := os.ReadFile(target)
		require.NoError(t, err)
		require.Equal(t, []byte("new"), data)
		require.NoFileExists(t, source)
	})
}

// TestRemoveAllWithRetry verifies recursive removal and symlink handling.
func TestRemoveAllWithRetry(t *testing.T) {
	t.Parallel()

	t.Run("removes nested directory tree", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		nested := filepath.Join(target, "a", "b")
		require.NoError(t, os.MkdirAll(nested, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(target, "root.txt"), []byte("root"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(nested, "leaf.txt"), []byte("leaf"), 0o600))

		require.NoError(t, RemoveAllWithRetry(target))

		require.NoDirExists(t, target)
	})

	t.Run("missing path is success", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, RemoveAllWithRetry(filepath.Join(t.TempDir(), "missing")))
	})

	t.Run("removes symlink without following target", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		external := filepath.Join(dir, "external")
		require.NoError(t, os.MkdirAll(external, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(external, "keep.txt"), []byte("keep"), 0o600))

		target := filepath.Join(dir, "target")
		require.NoError(t, os.MkdirAll(target, 0o750))
		link := filepath.Join(target, "external-link")
		if err := os.Symlink(external, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("creating symlinks requires privilege on Windows: %v", err)
			}
			require.NoError(t, err)
		}

		require.NoError(t, RemoveAllWithRetry(target))

		require.NoDirExists(t, target)
		require.FileExists(t, filepath.Join(external, "keep.txt"))
	})
}
