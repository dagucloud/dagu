// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

// FormatRestartHint formats the guidance shown after a successful upgrade.
// service is the result of [DetectService], or nil when none is found.
func FormatRestartHint(service *Service) string {
	if service == nil {
		return "Restart any running Dagu processes to use the new version.\n"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Restart Dagu to use the new version:\n  %s\n", service.RestartCommand)
	switch {
	case service.KeepRunsCommand != "":
		fmt.Fprintf(&sb, "\nThe restart also ends runs in progress. To keep them running, first run:\n  %s\n", service.KeepRunsCommand)
	case service.StopsRuns:
		sb.WriteString("\nThe restart also ends runs in progress.\n")
	}
	return sb.String()
}
