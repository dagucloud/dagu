// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
)

// shellCommandBuilder holds the configuration for building shell commands.
type shellCommandBuilder struct {
	Dir                string
	Command            string
	Args               []string
	Shell              []string // Shell command, e.g., ["/bin/sh"]
	ShellCommandArgs   string
	ShellPackages      []string
	Script             string
	UserSpecifiedShell bool // If true, don't auto-add -e flag
}

// Build constructs an exec.Cmd based on the shell type.
// It delegates to the appropriate Shell implementation from the registry.
func (b *shellCommandBuilder) Build(ctx context.Context) (*exec.Cmd, error) {
	if len(b.Shell) == 0 {
		return nil, fmt.Errorf("shell command is required")
	}

	builder := *b
	builder.Shell = cloneArgs(b.Shell)
	builder.Shell[0] = cmdutil.ResolveExecutable(builder.Shell[0])
	if builder.Script == "" && builder.ShellCommandArgs != "" &&
		len(builder.ShellPackages) > 0 && !cmdutil.IsNixShell(builder.Shell[0]) {
		return nil, fmt.Errorf("shell_packages can only be used with nix-shell for command-form run")
	}

	shell := findShell(builder.Shell[0])
	return shell.Build(ctx, &builder)
}
