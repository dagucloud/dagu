// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// cmdShell handles Windows cmd.exe.
type cmdShell struct{}

var _ Shell = (*cmdShell)(nil)

func (s *cmdShell) Match(name string) bool {
	switch name {
	case "cmd.exe", "cmd":
		return true
	default:
		return false
	}
}

// defaultCmdExePath is the standard Windows cmd.exe location.
const defaultCmdExePath = `C:\Windows\System32\cmd.exe`

// resolveCmdPath returns the full path to cmd.exe if the shell is specified
// as just "cmd" or "cmd.exe". It checks paths in order of preference:
// 1. COMSPEC environment variable (honors user/OS configuration)
// 2. Default path C:\Windows\System32\cmd.exe
// 3. SystemRoot-based path (for non-standard Windows installations)
// This ensures command execution avoids Go's security restrictions on relative paths.
func resolveCmdPath(shell string) string {
	if shell != "cmd" && shell != "cmd.exe" {
		return shell
	}

	// Check COMSPEC first to honor user/OS configuration
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		if _, err := os.Stat(comspec); err == nil {
			return comspec
		}
	}

	// Try the default path (most common case)
	if _, err := os.Stat(defaultCmdExePath); err == nil {
		return defaultCmdExePath
	}

	// Fallback to SystemRoot if Windows is installed on a different drive
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		systemRootCmd := filepath.Join(systemRoot, `System32\cmd.exe`)
		if _, err := os.Stat(systemRootCmd); err == nil {
			return systemRootCmd
		}
	}

	return shell
}

func (s *cmdShell) Build(ctx context.Context, b *shellCommandBuilder) (*exec.Cmd, error) {
	cmd := resolveCmdPath(b.Shell[0])

	// When running a command directly with a script, don't include cmd.exe arguments
	// e.g., python script.py
	if b.Command != "" && b.Script != "" {
		args := cloneArgs(b.Args)
		args = append(args, b.Script)
		return exec.CommandContext(ctx, b.Command, args...), nil // nolint: gosec
	}

	// When running just a script file with shell (no explicit command)
	// e.g., cmd.exe /c script.bat
	if b.Script != "" {
		args := cloneArgs(b.Shell[1:])
		args = append(args, b.Args...)
		carrierIdx := indexOfCmdCarrier(args)
		if carrierIdx >= 0 && carrierIdx != len(args)-1 {
			return nil, fmt.Errorf("script form cannot be used with cmd carrier %q followed by authored command text", args[carrierIdx])
		}
		if carrierIdx < 0 {
			args = append(args, "/c")
		}
		args = append(args, b.Script)
		return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
	}

	// Running a command string via shell
	args := cloneArgs(b.Shell[1:])

	// cmd.exe uses /c instead of -c
	if !slices.Contains(args, "/c") && !slices.Contains(args, "/C") {
		args = append(args, "/c")
	}

	if scriptPath := b.normalizeScriptPath(); scriptPath != "" {
		args = append(args, scriptPath)
	}

	return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
}

func indexOfCmdCarrier(args []string) int {
	for i, arg := range args {
		if arg == "/c" || arg == "/C" {
			return i
		}
	}
	return -1
}
