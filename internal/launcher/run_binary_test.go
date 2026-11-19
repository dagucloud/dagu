// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Runs execute a copy of the binary that is made once for each build and
// reused after that. Rebuilding the binary gives runs a new copy and removes
// the copy of the earlier build, which no run executes.
func TestCopyRunBinary(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "dagu.exe")
	writeBuild := func(content string) {
		require.NoError(t, os.WriteFile(executable, []byte(content), 0o755)) //nolint:gosec // stands in for an executable
	}
	dir := filepath.Join(t.TempDir(), "run-binaries")

	writeBuild("first build")
	first, err := copyRunBinary(executable, dir)
	require.NoError(t, err)
	assertCopyOf(t, executable, first)
	again, err := copyRunBinary(executable, dir)
	require.NoError(t, err)
	assert.Equal(t, first, again, "the same build reuses its copy")

	writeBuild("second, longer build")
	second, err := copyRunBinary(executable, dir)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assertCopyOf(t, executable, second)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the copy of the current build is left")
	assert.Equal(t, filepath.Base(second), entries[0].Name())
}

func assertCopyOf(t *testing.T, source, copied string) {
	t.Helper()
	want, err := os.ReadFile(source) //nolint:gosec // test-owned file
	require.NoError(t, err)
	got, err := os.ReadFile(copied) //nolint:gosec // test-owned file
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
	assert.Equal(t, filepath.Ext(source), filepath.Ext(copied), "the copy runs as the same kind of file")
}
