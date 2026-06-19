// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// Shell defines the interface for shell-specific command building.
// Each shell type (bash, PowerShell, cmd, nix-shell) implements this interface
// to handle its unique argument syntax and behaviors.
type Shell interface {
	// Match returns true if this shell handles the given executable name.
	// The name is lowercase and without path (e.g., "bash", "powershell.exe").
	Match(name string) bool

	// Build constructs an exec.Cmd for executing the command in this shell.
	Build(ctx context.Context, b *shellCommandBuilder) (*exec.Cmd, error)
}

// shellRegistry holds all registered shell implementations.
// Order matters: first match wins. The default Unix shell should be last.
var shellRegistry = []Shell{
	&directShell{}, // explicit no-shell execution
	&nixShell{},
	&powerShell{},
	&cmdShell{},
	&unixShell{}, // default fallback - must be last
}

// findShell selects the first registered Shell implementation that matches the provided command.
// It normalizes the executable by taking its base name and converting it to lowercase before matching.
// If no registered shell matches (should not occur), it falls back to the unixShell implementation.
func findShell(cmd string) Shell {
	name := normalizedShellName(cmd)
	for _, shell := range shellRegistry {
		if shell.Match(name) {
			return shell
		}
	}
	// Should never reach here since unixShell matches everything
	return &unixShell{}
}

func normalizedShellName(cmd string) string {
	name := filepath.Base(cmd)
	if idx := strings.LastIndex(name, "\\"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.ToLower(name)
	return strings.TrimSuffix(name, ".exe")
}

func insertShellArgsBeforeCarrier(args []string, carrierIdx int, additions ...string) []string {
	if len(additions) == 0 {
		return args
	}
	insertAt := len(args)
	if carrierIdx >= 0 {
		insertAt = carrierIdx
	}
	result := make([]string, 0, len(args)+len(additions))
	result = append(result, args[:insertAt]...)
	result = append(result, additions...)
	result = append(result, args[insertAt:]...)
	return result
}
