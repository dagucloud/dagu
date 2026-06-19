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

// nixShell handles nix-shell with package management support.
type nixShell struct{}

var _ Shell = (*nixShell)(nil)

func (s *nixShell) Match(name string) bool {
	return name == "nix-shell"
}

func (s *nixShell) Build(ctx context.Context, b *shellCommandBuilder) (*exec.Cmd, error) {
	cmd := b.Shell[0]
	args := cloneArgs(b.Shell[1:])
	scriptForm := b.Script != ""
	if scriptForm {
		if idx := indexOfNixRun(args); idx >= 0 && idx != len(args)-1 {
			return nil, fmt.Errorf("script form cannot be used with nix-shell --run followed by authored command text")
		}
	} else if b.ShellCommandArgs != "" {
		if err := validateNixCommandCarrier(args); err != nil {
			return nil, err
		}
	}

	args = insertNixGeneratedArgs(args, nixPackageArgs(b.ShellPackages)...)

	// Add pure mode if not already specified
	if !slices.Contains(args, "--pure") && !slices.Contains(args, "--impure") {
		args = insertNixGeneratedArgs(args, "--pure")
	}

	if !slices.Contains(args, "--run") {
		args = append(args, "--run")
	}

	// When using nix-shell with a direct command and script,
	// run the command inside nix-shell
	// e.g., nix-shell -p python --run "python script.py"
	if b.Command != "" && b.Script != "" {
		cmdParts := []string{b.Command}
		cmdParts = append(cmdParts, b.Args...)
		cmdParts = append(cmdParts, b.Script)
		cmdStr := nixShellCommandString(cmdParts, !b.UserSpecifiedShell)

		return exec.CommandContext(ctx, cmd, append(args, cmdStr)...), nil // nolint: gosec
	}

	// When running just a script file with nix-shell (no explicit command)
	// e.g., nix-shell --run "set -e; ./script.sh"
	if b.Script != "" {
		scriptCmd := nixShellCommandString([]string{b.Script}, !b.UserSpecifiedShell)
		return exec.CommandContext(ctx, cmd, append(args, scriptCmd)...), nil // nolint: gosec
	}

	// For shell command args, prepend "set -e;" for errexit
	shellCmdArgs := b.ShellCommandArgs
	if !b.UserSpecifiedShell && shellCmdArgs != "" && !strings.HasPrefix(shellCmdArgs, "set -e") {
		shellCmdArgs = "set -e; " + shellCmdArgs
	}

	if shellCmdArgs != "" {
		args = append(args, shellCmdArgs)
	}

	return exec.CommandContext(ctx, cmd, args...), nil // nolint: gosec
}

func indexOfNixRun(args []string) int {
	for i, arg := range args {
		if arg == "--run" {
			return i
		}
	}
	return -1
}

func validateNixCommandCarrier(args []string) error {
	carrierIdx := -1
	for i, arg := range args {
		if arg == "--run" {
			if carrierIdx >= 0 {
				return fmt.Errorf("command-form run accepts only one nix-shell --run argument")
			}
			carrierIdx = i
			continue
		}
		if strings.HasPrefix(arg, "--run=") {
			return fmt.Errorf("command-form run requires nix-shell command carrier --run as a separate shell argument; got %q", arg)
		}
	}
	if carrierIdx >= 0 && carrierIdx != len(args)-1 {
		return fmt.Errorf("command-form run requires nix-shell command carrier --run to be final before the command payload")
	}
	return nil
}

func nixPackageArgs(packages []string) []string {
	args := make([]string, 0, len(packages)*2)
	for _, pkg := range packages {
		args = append(args, "-p", pkg)
	}
	return args
}

func insertNixGeneratedArgs(args []string, additions ...string) []string {
	if len(additions) == 0 {
		return args
	}
	insertAt := len(args)
	if idx := indexOfNixRun(args); idx >= 0 {
		insertAt = idx
	}
	result := make([]string, 0, len(args)+len(additions))
	result = append(result, args[:insertAt]...)
	result = append(result, additions...)
	result = append(result, args[insertAt:]...)
	return result
}

func nixShellCommandString(parts []string, failFast bool) string {
	cmdStr := cmdutil.ShellQuoteArgs(parts)
	if failFast && !strings.HasPrefix(cmdStr, "set -e") {
		cmdStr = "set -e; " + cmdStr
	}
	return cmdStr
}
