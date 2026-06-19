// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"os/exec"
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
		if strings.EqualFold(powerShellArgName(arg), target) {
			return true
		}
	}
	return false
}

func indexOfPowerShellArg(args []string, targets ...string) int {
	for i, arg := range args {
		for _, target := range targets {
			if strings.EqualFold(powerShellArgName(arg), target) {
				return i
			}
		}
	}
	return -1
}

func powerShellArgName(arg string) string {
	if !strings.HasPrefix(arg, "-") {
		return arg
	}
	name, _, _ := strings.Cut(arg, ":")
	return name
}

func powerShellArgHasInlineValue(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	_, _, ok := strings.Cut(arg, ":")
	return ok
}

func (s *powerShell) Match(name string) bool {
	switch name {
	case "powershell", "pwsh":
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
	configured := cloneArgs(b.Shell[1:])
	if err := validatePowerShellCommandCarrier(configured); err != nil {
		return nil, err
	}
	args := appendPowerShellStartupArgs(configured)

	// PowerShell uses -Command instead of -c
	if !containsPowerShellArg(args, "-Command") && !containsPowerShellArg(args, "-C") {
		args = append(args, "-Command")
	}

	args = append(args, powerShellInlineCommand(b.ShellCommandArgs))

	return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
}

func validatePowerShellCommandCarrier(args []string) error {
	carrierIdx := -1
	for i, arg := range args {
		switch {
		case containsPowerShellArg([]string{arg}, "-EncodedCommand"):
			return fmt.Errorf("command-form run cannot use PowerShell command carrier %q", arg)
		case containsPowerShellArg([]string{arg}, "-File"), containsPowerShellArg([]string{arg}, "-F"):
			return fmt.Errorf("command-form run cannot use PowerShell file carrier %q", arg)
		case containsPowerShellArg([]string{arg}, "-Command"), containsPowerShellArg([]string{arg}, "-C"):
			if powerShellArgHasInlineValue(arg) {
				return fmt.Errorf("command-form run requires PowerShell command carrier %s as a separate shell argument; got %q", powerShellArgName(arg), arg)
			}
			if carrierIdx >= 0 {
				return fmt.Errorf("command-form run accepts only one PowerShell command carrier")
			}
			carrierIdx = i
		}
	}
	if carrierIdx >= 0 && carrierIdx != len(args)-1 {
		return fmt.Errorf("command-form run requires PowerShell command carrier %q to be final before the command payload", args[carrierIdx])
	}
	return nil
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
	if fileIdx >= 0 {
		if powerShellArgHasInlineValue(configured[fileIdx]) {
			return nil, fmt.Errorf("script form cannot be used with PowerShell file carrier %q because it already supplies a script path", configured[fileIdx])
		}
		if fileIdx != len(configured)-1 {
			return nil, fmt.Errorf("script form cannot be used with PowerShell file carrier %q followed by authored arguments", configured[fileIdx])
		}
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
