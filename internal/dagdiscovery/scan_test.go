// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagdiscovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanRecursive(t *testing.T) {
	baseDir := t.TempDir()
	root := filepath.Join(baseDir, "dags")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "team", "nested"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hidden"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "team", ".private"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "workspaces", "ops"), 0750))

	for _, path := range []string{
		filepath.Join(root, "root.yaml"),
		filepath.Join(root, "team", "nested.yml"),
		filepath.Join(root, "team", "nested", "deep.yaml"),
		filepath.Join(root, ".hidden", "hidden.yaml"),
		filepath.Join(root, "team", ".private", "private.yaml"),
		filepath.Join(root, "workspaces", "ops", "base.yaml"),
	} {
		require.NoError(t, os.WriteFile(path, []byte("steps: []\n"), 0600))
	}

	externalDir := filepath.Join(baseDir, "external")
	require.NoError(t, os.MkdirAll(externalDir, 0750))
	externalFile := filepath.Join(externalDir, "linked.yaml")
	require.NoError(t, os.WriteFile(externalFile, []byte("steps: []\n"), 0600))
	_ = os.Symlink(externalDir, filepath.Join(root, "linked-dir"))
	_ = os.Symlink(externalFile, filepath.Join(root, "linked-file.yaml"))

	result, err := Scan(root, true)
	require.NoError(t, err)
	require.Empty(t, result.Errors)

	var files []string
	for _, file := range result.Files {
		files = append(files, file.RelPath)
	}
	assert.Equal(t, []string{
		"root.yaml",
		"team/nested.yml",
		"team/nested/deep.yaml",
	}, files)
	assert.Equal(t, []string{
		root,
		filepath.Join(root, "team"),
		filepath.Join(root, "team", "nested"),
	}, result.Dirs)
}

func TestScanNonRecursiveOnlyReadsRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.yaml"), []byte("steps: []\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "child.yaml"), []byte("steps: []\n"), 0600))

	result, err := Scan(root, false)
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "root.yaml", result.Files[0].RelPath)
	assert.Equal(t, []string{root}, result.Dirs)
}

func TestScanRecursiveAllowsConfiguredSymlinkRoot(t *testing.T) {
	baseDir := t.TempDir()
	realRoot := filepath.Join(baseDir, "real")
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(realRoot, "nested", "child.yaml"), []byte("steps: []\n"), 0600))

	linkRoot := filepath.Join(baseDir, "dags")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	result, err := Scan(linkRoot, true)
	require.NoError(t, err)
	require.Empty(t, result.Errors)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "nested/child.yaml", result.Files[0].RelPath)
	assert.Equal(t, []string{linkRoot, filepath.Join(linkRoot, "nested")}, result.Dirs)
}
