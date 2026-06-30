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
