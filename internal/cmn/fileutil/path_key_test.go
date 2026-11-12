// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveExistingAncestor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	aliasDir := filepath.Join(dir, "alias")
	require.NoError(t, os.Mkdir(realDir, 0o750))
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("filesystem symlinks are unavailable: %v", err)
	}
	resolvedReal, err := filepath.EvalSymlinks(realDir)
	require.NoError(t, err)

	got, err := ResolveExistingAncestor(filepath.Join(aliasDir, "new", "book.xlsx"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolvedReal, "new", "book.xlsx"), got)
}

func TestIsCaseInsensitiveFS(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	upper := filepath.Join(dir, "Probe.txt")
	require.NoError(t, os.WriteFile(upper, []byte("probe"), 0o600))
	upperInfo, err := os.Lstat(upper)
	require.NoError(t, err)
	lowerInfo, lowerErr := os.Lstat(filepath.Join(dir, "probe.txt"))
	want := lowerErr == nil && os.SameFile(upperInfo, lowerInfo)

	assert.Equal(t, want, IsCaseInsensitiveFS(upper))
	assert.Equal(t, want, IsCaseInsensitiveFS(filepath.Join(dir, "missing", "Book.xlsx")))
}
