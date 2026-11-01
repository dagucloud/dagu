// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// Background service definitions written by scripts/installer.sh and
// scripts/installer.ps1.
const (
	launchdServiceLabel  = "local.dagu.server"
	systemdServiceUnit   = "dagu.service"
	systemdSystemUnitDir = "/etc/systemd/system"
	winswServiceConfig   = "dagu-service.xml"
	winswServiceName     = "Dagu"
)

// RunningBinary identifies the executable file the current process started from.
type RunningBinary struct {
	path    string
	modTime time.Time
	size    int64
}

// CaptureRunningBinary records the executable file the current process started from.
func CaptureRunningBinary() (*RunningBinary, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}
	var arg0 string
	if len(os.Args) > 0 {
		arg0 = os.Args[0]
	}
	return newRunningBinary(launchPath(arg0, executable))
}

// launchPath returns the absolute path that arg0 resolves to when it reaches
// the running executable, and executable otherwise. os.Executable may resolve
// symlinks (Linux reads /proc/self/exe), while package managers upgrade by
// repointing the symlink the process was launched through.
func launchPath(arg0, executable string) string {
	if arg0 == "" {
		return executable
	}
	path, err := exec.LookPath(arg0)
	if err != nil {
		return executable
	}
	if path, err = filepath.Abs(path); err != nil {
		return executable
	}
	launched, err := os.Stat(path)
	if err != nil {
		return executable
	}
	running, err := os.Stat(executable)
	if err != nil || !os.SameFile(launched, running) {
		return executable
	}
	return path
}

func newRunningBinary(path string) (*RunningBinary, error) {
	info, err := os.Stat(path) //nolint:gosec // path is this process's own executable
	if err != nil {
		return nil, fmt.Errorf("failed to stat executable: %w", err)
	}
	return &RunningBinary{path: path, modTime: info.ModTime(), size: info.Size()}, nil
}

// Replaced reports whether a different file now exists where the process
// started from, so a restart is needed to run it.
func (b *RunningBinary) Replaced() bool {
	info, err := os.Stat(b.path)
	if err != nil {
		return false
	}
	return info.Size() != b.size || !info.ModTime().Equal(b.modTime)
}

// RestartCommand returns the shell command that restarts the background
// service the Dagu installer set up on this machine, or "" when none is found.
func RestartCommand() string {
	var execDir string
	if execPath, err := GetExecutablePath(); err == nil {
		execDir = filepath.Dir(execPath)
	}
	homeDir, _ := os.UserHomeDir()
	return restartCommand(runtime.GOOS, execDir, homeDir, systemdSystemUnitDir)
}

func restartCommand(goos, execDir, homeDir, systemUnitDir string) string {
	switch goos {
	case "darwin":
		if homeDir != "" && fileutil.IsFile(filepath.Join(homeDir, "Library", "LaunchAgents", launchdServiceLabel+".plist")) {
			return "launchctl kickstart -k gui/$(id -u)/" + launchdServiceLabel
		}
	case "linux":
		if fileutil.IsFile(filepath.Join(systemUnitDir, systemdServiceUnit)) {
			return "sudo systemctl restart dagu"
		}
		if homeDir != "" && fileutil.IsFile(filepath.Join(homeDir, ".config", "systemd", "user", systemdServiceUnit)) {
			return "systemctl --user restart dagu"
		}
	case "windows":
		// The installer keeps the service wrapper next to the binary it runs.
		if execDir != "" && fileutil.IsFile(filepath.Join(execDir, winswServiceConfig)) {
			return "Restart-Service " + winswServiceName
		}
	}
	return ""
}

// FormatRestartHint formats the guidance shown after a successful upgrade.
// restartCommand is the command from [RestartCommand], or "" when unknown.
func FormatRestartHint(restartCommand string) string {
	if restartCommand == "" {
		return "Restart any running Dagu processes to use the new version.\n"
	}
	return fmt.Sprintf("Restart Dagu to use the new version:\n  %s\n", restartCommand)
}
