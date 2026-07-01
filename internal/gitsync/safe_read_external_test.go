// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/gitsync"
)

func TestSafeReadFileWithinBaseForTestReadsRegularFile(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	filePath := filepath.Join(baseDir, "dag.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte("steps: []\n"), 0600))

	content, err := gitsync.SafeReadFileWithinBaseForTest(baseDir, filePath)

	require.NoError(t, err)
	assert.Equal(t, "steps: []\n", string(content))
}

func TestSafeReadFileWithinBaseForTestRejectsSymlink(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.yaml")
	require.NoError(t, os.WriteFile(outsidePath, []byte("secret\n"), 0600))
	linkPath := filepath.Join(baseDir, "dag.yaml")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	_, err := gitsync.SafeReadFileWithinBaseForTest(baseDir, linkPath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to read through symlink")
}

func TestSafeReadFileWithinBaseForTestRejectsIntermediateSymlink(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	realDir := filepath.Join(baseDir, "real")
	require.NoError(t, os.Mkdir(realDir, 0750))
	realPath := filepath.Join(realDir, "dag.yaml")
	require.NoError(t, os.WriteFile(realPath, []byte("steps: []\n"), 0600))
	linkDir := filepath.Join(baseDir, "alias")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	_, err := gitsync.SafeReadFileWithinBaseForTest(baseDir, filepath.Join(linkDir, "dag.yaml"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to read through symlink")
}

func TestSafeWriteFileWithinBaseForTestWritesRegularFile(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	filePath := filepath.Join(baseDir, "dag.yaml")

	err := gitsync.SafeWriteFileWithinBaseForTest(baseDir, filePath, []byte("steps:\n  - echo ok\n"))

	require.NoError(t, err)
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "steps:\n  - echo ok\n", string(content))
}

func TestSafeWriteFileWithinBaseForTestCreatesMissingBaseDir(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "dags")
	filePath := filepath.Join(baseDir, "nested", "dag.yaml")

	err := gitsync.SafeWriteFileWithinBaseForTest(baseDir, filePath, []byte("steps:\n  - echo ok\n"))

	require.NoError(t, err)
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "steps:\n  - echo ok\n", string(content))
}

func TestSafeWriteFileWithinBaseForTestRejectsPathEscapesMissingBaseDir(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "dags")
	outsidePath := filepath.Join(tempDir, "outside.yaml")

	err := gitsync.SafeWriteFileWithinBaseForTest(baseDir, outsidePath, []byte("changed\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path escapes allowed base directory")
	_, statErr := os.Stat(outsidePath)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestSafeWriteFileWithinBaseForTestRejectsSymlink(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.yaml")
	require.NoError(t, os.WriteFile(outsidePath, []byte("secret\n"), 0600))
	linkPath := filepath.Join(baseDir, "dag.yaml")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	err := gitsync.SafeWriteFileWithinBaseForTest(baseDir, linkPath, []byte("changed\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to write through symlink")
	content, readErr := os.ReadFile(outsidePath)
	require.NoError(t, readErr)
	assert.Equal(t, "secret\n", string(content))
}

func TestSafeWriteFileWithinBaseForTestRejectsIntermediateSymlink(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	realDir := filepath.Join(baseDir, "real")
	require.NoError(t, os.Mkdir(realDir, 0750))
	realPath := filepath.Join(realDir, "dag.yaml")
	require.NoError(t, os.WriteFile(realPath, []byte("steps: []\n"), 0600))
	linkDir := filepath.Join(baseDir, "alias")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	err := gitsync.SafeWriteFileWithinBaseForTest(baseDir, filepath.Join(linkDir, "dag.yaml"), []byte("changed\n"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to write through symlink")
	content, readErr := os.ReadFile(realPath)
	require.NoError(t, readErr)
	assert.Equal(t, "steps: []\n", string(content))
}
