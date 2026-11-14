// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunningBinaryReplaced(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "dagu")
	require.NoError(t, os.WriteFile(execPath, []byte("old binary"), 0o755))

	binary, err := newRunningBinary(execPath)
	require.NoError(t, err)
	assert.False(t, binary.Replaced())

	// Mirror how the upgrade installs a release: rename a new file over the old one.
	newPath := filepath.Join(dir, "dagu-new")
	require.NoError(t, os.WriteFile(newPath, []byte("new release binary"), 0o755))
	require.NoError(t, os.Rename(newPath, execPath))

	assert.True(t, binary.Replaced())
}

// Package managers such as Homebrew upgrade by repointing the symlink the
// process was launched through, which os.Executable may already resolve.
func TestRunningBinaryReplacedThroughLaunchSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs extra privileges on Windows")
	}
	dir := t.TempDir()
	oldTarget := filepath.Join(dir, "dagu-1.0.0")
	require.NoError(t, os.WriteFile(oldTarget, []byte("old binary"), 0o755))
	link := filepath.Join(dir, "dagu")
	require.NoError(t, os.Symlink(oldTarget, link))

	binary, err := newRunningBinary(launchPath(link, oldTarget))
	require.NoError(t, err)
	assert.False(t, binary.Replaced())

	newTarget := filepath.Join(dir, "dagu-1.1.0")
	require.NoError(t, os.WriteFile(newTarget, []byte("new release binary"), 0o755))
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(newTarget, link))

	assert.True(t, binary.Replaced())
}

func TestLaunchPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup needs an executable extension on Windows")
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "dagu")
	require.NoError(t, os.WriteFile(executable, []byte("binary"), 0o755))
	other := filepath.Join(dir, "other")
	require.NoError(t, os.WriteFile(other, []byte("other binary"), 0o755))
	t.Setenv("PATH", dir)

	assert.Equal(t, executable, launchPath("dagu", executable), "bare name found on PATH")
	assert.Equal(t, executable, launchPath(other, executable), "argument naming a different file")
	assert.Equal(t, executable, launchPath("missing", executable), "argument not found")
	assert.Equal(t, executable, launchPath("", executable), "empty argument")
}

func TestRunningBinaryReplacedMissingFile(t *testing.T) {
	execPath := filepath.Join(t.TempDir(), "dagu")
	require.NoError(t, os.WriteFile(execPath, []byte("binary"), 0o755))

	binary, err := newRunningBinary(execPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(execPath))

	assert.False(t, binary.Replaced())
}

func TestFormatRestartHint(t *testing.T) {
	tests := []struct {
		name    string
		service *Service
		want    string
	}{
		{
			name:    "no service",
			service: nil,
			want:    "Restart any running Dagu processes to use the new version.\n",
		},
		{
			name:    "restart keeps runs",
			service: &Service{RestartCommand: "sudo systemctl restart dagu"},
			want:    "Restart Dagu to use the new version:\n  sudo systemctl restart dagu\n",
		},
		{
			name: "restart ends runs and can be reconfigured",
			service: &Service{
				RestartCommand:  "sudo systemctl restart dagu",
				StopsRuns:       true,
				KeepRunsCommand: "keep-runs",
			},
			want: "Restart Dagu to use the new version:\n  sudo systemctl restart dagu\n" +
				"\nThe restart also ends runs in progress. To keep them running, first run:\n  keep-runs\n",
		},
		{
			name:    "restart ends runs",
			service: &Service{RestartCommand: "Restart-Service Dagu", StopsRuns: true},
			want: "Restart Dagu to use the new version:\n  Restart-Service Dagu\n" +
				"\nThe restart also ends runs in progress.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatRestartHint(tt.service))
		})
	}
}
