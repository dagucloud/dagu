// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/core"
)

// setupScript creates a temporary executable script file containing the provided
// script after applying shell-specific preprocessing (e.g., PowerShell error-handling
// directives). If workDir is non-empty, the file is created there; otherwise it falls
// back to the system temp directory. The file extension is chosen based on the shell.
// Returns the created file path or an error if file creation, writing, syncing, or
// permission setting fails.
func setupScript(workDir, script, command string, shell []string) (string, error) {
	// Determine file extension based on the actual execution path. Scripts that
	// are passed to an explicit command or start with a shebang should preserve
	// their original first line so the intended interpreter can handle them.
	shellCmd := ""
	if command == "" && !hasShebang(script) && len(shell) > 0 {
		shellCmd = shell[0]
	}
	ext := cmdutil.GetScriptExtension(shellCmd)
	pattern := "dagu_script-*" + ext

	file, err := os.CreateTemp(workDir, pattern)
	if err != nil {
		return "", fmt.Errorf("failed to create script file: %w", err)
	}

	// cleanup removes the temp file on error
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}

	// Apply shell-specific preprocessing
	script = preprocessScript(script, ext)

	if _, err = file.WriteString(script); err != nil {
		cleanup()
		return "", fmt.Errorf("failed to write script to file: %w", err)
	}

	if err = file.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("failed to sync script file: %w", err)
	}

	// Add execute permissions to the script file
	if err = os.Chmod(file.Name(), 0750); err != nil { // nolint: gosec
		cleanup()
		return "", fmt.Errorf("failed to set execute permissions on script file: %w", err)
	}

	_ = file.Close()
	return file.Name(), nil
}

func hasShebang(script string) bool {
	return strings.HasPrefix(script, "#!")
}

var powerShellPreambleStatements = []string{
	"$ErrorActionPreference = 'Stop'",
	"$utf8NoBom = New-Object -TypeName System.Text.UTF8Encoding -ArgumentList $false",
	"[Console]::InputEncoding = $utf8NoBom",
	"[Console]::OutputEncoding = $utf8NoBom",
	"$OutputEncoding = $utf8NoBom",
}

func powerShellPreamble() string {
	return strings.Join(powerShellPreambleStatements, "\n")
}

func powerShellInlineCommand(command string) string {
	parts := append([]string{}, powerShellPreambleStatements...)
	command = strings.TrimSpace(command)
	if command != "" {
		parts = append(parts, command)
	}
	return strings.Join(parts, "; ")
}

// scriptLineOffset returns the number of lines prepended by preprocessScript
// for the given shell. This is needed to map error line numbers back to the
// user's original script content.
func scriptLineOffset(scriptFile string) int {
	if scriptFile == "" {
		return 0
	}
	if cmdutil.GetScriptExtension(scriptFile) == ".ps1" {
		return len(powerShellPreambleStatements)
	}
	return 0
}

// preprocessScript returns the script content adjusted for the shell indicated by ext.
// For ".ps1" it prepends PowerShell directives that make cmdlet errors stop
// execution and normalize UTF-8 console/pipeline encoding; for other extensions
// it returns the original script.
func preprocessScript(script, ext string) string {
	switch ext {
	case ".ps1":
		return powerShellPreamble() + "\n" + script
	default:
		return script
	}
}

// createDirectCommand returns an *exec.Cmd that invokes cmd with the provided args.
// If scriptFile is non-empty it is appended to the argument list. The returned command
// is bound to ctx.
func createDirectCommand(ctx context.Context, cmd string, args []string, scriptFile string) *exec.Cmd {
	clonedArgs := cloneArgs(args)
	if scriptFile != "" {
		clonedArgs = append(clonedArgs, scriptFile)
	}
	command := exec.CommandContext(ctx, cmdutil.ResolveExecutable(cmd), clonedArgs...) // nolint: gosec
	cmdutil.SetupCommand(command)
	return command
}

// validateCommandStep checks that a Step has a valid command configuration.
// It considers a step valid when it provides Commands, a Script, both, or a non-nil SubDAG.
// Returns core.ErrStepCommandIsRequired when none of those are present.
func validateCommandStep(step core.Step) error {
	hasCommands := len(step.Commands) > 0

	switch {
	case hasCommands && step.Script != "":
		// Both commands and script provided - valid
	case hasCommands && step.Script == "":
		// Commands only - valid
	case !hasCommands && step.Script != "":
		// Script only - valid
	case step.SubDAG != nil:
		// Sub DAG - valid
	default:
		return core.NewValidationError("command", nil, core.ErrStepCommandIsRequired)
	}

	return nil
}
