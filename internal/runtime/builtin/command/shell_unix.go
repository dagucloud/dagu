// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
)

// unixShell handles standard Unix shells (sh, bash, zsh, etc.).
// This is the default fallback for any shell not explicitly handled.
type unixShell struct{}

var _ Shell = (*unixShell)(nil)

func (s *unixShell) Match(_ string) bool {
	// Matches everything as the default fallback
	return true
}

func (s *unixShell) Build(ctx context.Context, b *shellCommandBuilder) (*exec.Cmd, error) {
	if len(b.Shell) == 0 {
		return nil, fmt.Errorf("shell command is required")
	}

	cmd := b.Shell[0]

	// When running a command directly with a script (e.g., perl script.pl),
	// don't include shell arguments like -e
	if b.Command != "" && b.Script != "" {
		args := cloneArgs(b.Args)
		args = append(args, b.Script)
		return exec.CommandContext(ctx, b.Command, args...), nil // nolint: gosec
	}

	// When running just a script file with shell (no explicit command)
	// e.g., sh -e script.sh
	if b.Script != "" {
		args := cloneArgs(b.Shell[1:])
		args = append(args, b.Args...)
		if cmdutil.IsUnixLikeShell(cmd) {
			if arg, ok := unixScriptCarrierConflict(args); ok {
				return nil, fmt.Errorf("script form cannot be used with shell argument %q because it consumes command text or stdin", arg)
			}
		}
		// Add errexit flag for Unix-like shells (unless user specified shell)
		if !b.UserSpecifiedShell && cmdutil.IsUnixLikeShell(cmd) && !slices.Contains(args, "-e") {
			args = append(args, "-e")
		}
		args = append(args, b.Script)
		return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
	}

	// Running a command string via shell
	args := cloneArgs(b.Shell[1:])
	carrierIdx, err := unixCommandCarrierIndex(args, cmdutil.IsUnixLikeShell(cmd))
	if err != nil {
		return nil, err
	}

	// Add errexit flag for Unix-like shells (unless user specified shell)
	if !b.UserSpecifiedShell && cmdutil.IsUnixLikeShell(cmd) && !slices.Contains(args, "-e") {
		args = insertShellArgsBeforeCarrier(args, carrierIdx, "-e")
	}

	if carrierIdx < 0 {
		args = append(args, "-c")
	}
	args = append(args, b.ShellCommandArgs)

	return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
}

func unixScriptCarrierConflict(args []string) (string, bool) {
	for _, arg := range args {
		if arg == "-c" || arg == "-s" {
			return arg, true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			flags := strings.TrimLeft(arg, "-")
			if strings.ContainsAny(flags, "cs") {
				return arg, true
			}
		}
	}
	return "", false
}

func unixCommandCarrierIndex(args []string, unixLike bool) (int, error) {
	carrierIdx := -1
	for i, arg := range args {
		if arg == "-c" {
			if carrierIdx >= 0 {
				return -1, fmt.Errorf("command-form run accepts only one -c shell argument")
			}
			carrierIdx = i
			continue
		}
		if unixCommandCarrierConflict(arg, unixLike) {
			return -1, fmt.Errorf("command-form run requires command-string carrier -c as a separate shell argument; got %q", arg)
		}
	}
	if carrierIdx >= 0 && carrierIdx != len(args)-1 {
		return -1, fmt.Errorf("command-form run requires shell argument -c to be final before the command payload")
	}
	return carrierIdx, nil
}

func unixCommandCarrierConflict(arg string, unixLike bool) bool {
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	if arg == "-c" {
		return false
	}
	flags := strings.TrimLeft(arg, "-")
	if unixLike {
		return strings.Contains(flags, "c")
	}
	return strings.HasPrefix(flags, "c")
}

// cloneArgs returns a shallow copy of the provided args slice so callers can modify the result without mutating the original.
func cloneArgs(args []string) []string {
	result := make([]string, len(args))
	copy(result, args)
	return result
}
