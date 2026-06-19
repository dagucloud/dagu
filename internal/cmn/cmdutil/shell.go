// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmdutil

import (
	"path/filepath"
	"slices"
	"strings"
)

// ShellQuote escapes a string for use in a shell command.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}

	// Use a conservative set of safe characters:
	// Alphanumeric, hyphen, underscore, dot, and slash.
	// We only consider ASCII alphanumeric as safe to avoid locale-dependent behavior.
	safe := true
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
			b == '-' || b == '_' || b == '.' || b == '/' {
			continue
		}
		safe = false
		break
	}
	if safe {
		return s
	}

	// Wrap in single quotes and escape any internal single quotes.
	// This is the most robust way to escape for POSIX-compliant shells.
	// 'user's file' -> 'user'\''s file'
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// ShellQuoteArgs escapes a slice of strings for use in a shell command.
func ShellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = ShellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

// shellBaseName extracts the base name from a shell path, handling both
// Unix and Windows path separators for cross-platform compatibility.
func shellBaseName(path string) string {
	// Use filepath.Base for OS-native paths
	name := filepath.Base(path)
	// Also check for the opposite separator (handles Windows paths on Unix)
	if idx := strings.LastIndex(name, "\\"); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(name)
}

// IsUnixLikeShell reports whether the shell supports -c and -e flags.
// Returns true for sh, bash, zsh, ksh, ash, dash (including .exe variants).
func IsUnixLikeShell(shell string) bool {
	if shell == "" {
		return false
	}
	name := shellBaseName(shell)
	// Handle both with and without .exe extension
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "sh", "bash", "zsh", "ksh", "ash", "dash":
		return true
	case "fish":
		// Fish shell doesn't support -e flag
		return false
	default:
		return false
	}
}

// IsPowerShell reports whether the shell is PowerShell or pwsh.
func IsPowerShell(shell string) bool {
	name := shellBaseName(shell)
	name = strings.TrimSuffix(name, ".exe")
	return name == "powershell" || name == "pwsh"
}

// IsCmdShell reports whether the shell is Windows cmd.exe.
func IsCmdShell(shell string) bool {
	name := shellBaseName(shell)
	name = strings.TrimSuffix(name, ".exe")
	return name == "cmd"
}

// IsNixShell reports whether the shell is nix-shell.
func IsNixShell(shell string) bool {
	name := shellBaseName(shell)
	name = strings.TrimSuffix(name, ".exe")
	return name == "nix-shell"
}

// ShellCommandFlag returns the flag used to pass a command string to the shell.
// Returns "-c" for Unix shells, "-Command" for PowerShell, "/c" for cmd.exe,
// "--run" for nix-shell, or "-c" for unknown shells (defaulting to Unix-style).
func ShellCommandFlag(shell string) string {
	switch {
	case IsUnixLikeShell(shell):
		return "-c"
	case IsPowerShell(shell):
		return "-Command"
	case IsCmdShell(shell):
		return "/c"
	case IsNixShell(shell):
		return "--run"
	default:
		return "-c" // Default to Unix-style
	}
}

// BuildShellCommandString constructs a complete shell command string suitable
// for remote execution (e.g., via SSH). The command is properly quoted.
//
// Example outputs:
//   - Unix: `/bin/bash -c 'echo hello'`
//   - PowerShell: `powershell -Command 'echo hello'`
//   - cmd.exe: `cmd.exe /c 'echo hello'`
func BuildShellCommandString(shell string, args []string, command string) string {
	if shell == "" {
		return command
	}

	parts := []string{ShellQuote(shell)}

	// Add user-provided args
	for _, arg := range args {
		parts = append(parts, ShellQuote(arg))
	}

	// Add command flag if not already present
	flag := ShellCommandFlag(shell)
	if flag != "" && !slices.Contains(args, flag) {
		parts = append(parts, flag)
	}

	// Quote the command for the remote shell
	parts = append(parts, ShellQuote(command))

	return strings.Join(parts, " ")
}
