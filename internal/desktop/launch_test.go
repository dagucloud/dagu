// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An application starts in the given working directory and is not waited
// for.
func TestLaunchUsesWorkingDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	command, args := "/bin/sh", []string{"-c", "pwd > where.txt"}
	if runtime.GOOS == "windows" {
		command, args = "cmd", []string{"/c", "cd > where.txt"}
	}
	require.NoError(t, desktop.Launch(dir, command, args))

	marker := filepath.Join(dir, "where.txt")
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(marker)
		return err == nil && strings.TrimSpace(string(data)) != ""
	}, 10*time.Second, 50*time.Millisecond)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
