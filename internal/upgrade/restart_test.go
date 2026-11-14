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

func TestRestartCommand(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		definition func(execDir, homeDir, systemUnitDir string) string
		want       string
	}{
		{
			name: "macOS launch agent",
			goos: "darwin",
			definition: func(_, homeDir, _ string) string {
				return filepath.Join(homeDir, "Library", "LaunchAgents", "local.dagu.server.plist")
			},
			want: "launchctl kickstart -k gui/$(id -u)/local.dagu.server",
		},
		{
			name: "Linux system unit",
			goos: "linux",
			definition: func(_, _, systemUnitDir string) string {
				return filepath.Join(systemUnitDir, "dagu.service")
			},
			want: "sudo systemctl restart dagu",
		},
		{
			name: "Linux user unit",
			goos: "linux",
			definition: func(_, homeDir, _ string) string {
				return filepath.Join(homeDir, ".config", "systemd", "user", "dagu.service")
			},
			want: "systemctl --user restart dagu",
		},
		{
			name: "Windows service wrapper",
			goos: "windows",
			definition: func(execDir, _, _ string) string {
				return filepath.Join(execDir, "dagu-service.xml")
			},
			want: "Restart-Service Dagu",
		},
		{
			name: "definition from another platform",
			goos: "linux",
			definition: func(_, homeDir, _ string) string {
				return filepath.Join(homeDir, "Library", "LaunchAgents", "local.dagu.server.plist")
			},
			want: "",
		},
		{
			name:       "no service installed",
			goos:       "darwin",
			definition: nil,
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execDir, homeDir, systemUnitDir := t.TempDir(), t.TempDir(), t.TempDir()
			if tt.definition != nil {
				path := tt.definition(execDir, homeDir, systemUnitDir)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
				require.NoError(t, os.WriteFile(path, nil, 0o600))
			}

			assert.Equal(t, tt.want, restartCommand(tt.goos, execDir, homeDir, systemUnitDir))
		})
	}
}

func TestFormatRestartHint(t *testing.T) {
	assert.Equal(t,
		"Restart Dagu to use the new version:\n  sudo systemctl restart dagu\n",
		FormatRestartHint("sudo systemctl restart dagu"),
	)
	assert.Equal(t,
		"Restart any running Dagu processes to use the new version.\n",
		FormatRestartHint(""),
	)
}
