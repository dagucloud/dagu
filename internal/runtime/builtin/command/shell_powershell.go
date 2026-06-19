// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// powerShell handles both Windows PowerShell and PowerShell Core (pwsh).
type powerShell struct{}

var _ Shell = (*powerShell)(nil)

func appendPowerShellStartupArgs(args []string) []string {
	startupArgs := make([]string, 0, 2)
	if !containsPowerShellArg(args, "-NoProfile") {
		startupArgs = append(startupArgs, "-NoProfile")
	}
	if !containsPowerShellArg(args, "-NonInteractive") {
		startupArgs = append(startupArgs, "-NonInteractive")
	}
	if len(startupArgs) == 0 {
		return args
	}

	insertAt := len(args)
	if idx := indexOfPowerShellArg(args, "-Command", "-C", "-File", "-F"); idx >= 0 {
		insertAt = idx
	}

	result := make([]string, 0, len(args)+len(startupArgs))
	result = append(result, args[:insertAt]...)
	result = append(result, startupArgs...)
	result = append(result, args[insertAt:]...)
	return result
}

func containsPowerShellArg(args []string, target string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, target) {
			return true
		}
	}
	return false
}

func indexOfPowerShellArg(args []string, targets ...string) int {
	for i, arg := range args {
		for _, target := range targets {
			if strings.EqualFold(arg, target) {
				return i
			}
		}
	}
	return -1
}

func (s *powerShell) Match(name string) bool {
	switch name {
	case "powershell.exe", "powershell", "pwsh.exe", "pwsh":
		return true
	default:
		return false
	}
}

func (s *powerShell) Build(ctx context.Context, b *shellCommandBuilder) (*exec.Cmd, error) {
	cmd := b.Shell[0]

	// When running a command directly with a script, don't include PowerShell arguments
	// e.g., python script.py
	if b.Command != "" && b.Script != "" {
		args := cloneArgs(b.Args)
		args = append(args, b.Script)
		return exec.CommandContext(ctx, b.Command, args...), nil // nolint: gosec
	}

	// When running just a script file with PowerShell (no explicit command)
	// e.g., powershell -ExecutionPolicy Bypass -File script.ps1
	if b.Script != "" {
		configured := cloneArgs(b.Shell[1:])
		configured = append(configured, b.Args...)
		args, err := powerShellScriptArgs(configured, b.Script)
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
	}

	// Running a command string via PowerShell
	args := appendPowerShellStartupArgs(cloneArgs(b.Shell[1:]))

	// PowerShell uses -Command instead of -c
	if !slices.Contains(args, "-Command") && !slices.Contains(args, "-C") {
		args = append(args, "-Command")
	}

	if scriptPath := b.normalizeScriptPath(); scriptPath != "" {
		args = append(args, powerShellInlineCommand(scriptPath))
	} else {
		args = append(args, powerShellInlineCommand(""))
	}

	return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
}

func powerShellScriptArgs(configured []string, script string) ([]string, error) {
	for _, arg := range configured {
		if containsPowerShellArg([]string{arg}, "-Command") ||
			containsPowerShellArg([]string{arg}, "-C") ||
			containsPowerShellArg([]string{arg}, "-EncodedCommand") {
			return nil, fmt.Errorf("script form cannot be used with PowerShell command carrier %q", arg)
		}
	}

	fileIdx := indexOfPowerShellArg(configured, "-File", "-F")
	if fileIdx >= 0 && fileIdx != len(configured)-1 {
		return nil, fmt.Errorf("script form cannot be used with PowerShell file carrier %q followed by authored arguments", configured[fileIdx])
	}

	args := cloneArgs(configured)
	if !containsPowerShellArg(args, "-ExecutionPolicy") {
		args = insertBeforePowerShellFileCarrier(args, "-ExecutionPolicy", "Bypass")
	}
	args = appendPowerShellStartupArgs(args)
	if indexOfPowerShellArg(args, "-File", "-F") < 0 {
		args = append(args, "-File")
	}
	args = append(args, script)
	return args, nil
}

func insertBeforePowerShellFileCarrier(args []string, additions ...string) []string {
	insertAt := len(args)
	if idx := indexOfPowerShellArg(args, "-File", "-F"); idx >= 0 {
		insertAt = idx
	}

	result := make([]string, 0, len(args)+len(additions))
	result = append(result, args[:insertAt]...)
	result = append(result, additions...)
	result = append(result, args[insertAt:]...)
	return result
}
