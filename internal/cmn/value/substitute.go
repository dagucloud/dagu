// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
)

func substituteCommandTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 10 * time.Second
	}
	return 2 * time.Second
}

// buildShellCommand creates an exec.Cmd with appropriate arguments for the shell type.
func buildShellCommand(shell, cmdStr string) *exec.Cmd {
	return buildShellCommandContext(context.Background(), shell, cmdStr)
}

func buildShellCommandContext(ctx context.Context, shell, cmdStr string) *exec.Cmd {
	if shell == "" {
		return exec.CommandContext(ctx, "sh", "-c", cmdStr) //nolint:gosec
	}

	shell, shellArgs := splitShellCommand(shell)
	switch strings.ToLower(filepath.Base(shell)) {
	case "powershell.exe", "powershell", "pwsh.exe", "pwsh":
		return exec.CommandContext(ctx, shell, appendPowerShellCommandArgs(shellArgs, cmdStr)...) //nolint:gosec
	case "cmd.exe", "cmd":
		return exec.CommandContext(ctx, shell, appendShellCommandArgs(shellArgs, "/c", cmdStr)...) //nolint:gosec
	default:
		return exec.CommandContext(ctx, shell, appendShellCommandArgs(shellArgs, "-c", cmdStr)...) //nolint:gosec
	}
}

func splitShellCommand(shell string) (string, []string) {
	if _, err := os.Stat(shell); err == nil {
		return shell, nil
	}

	command, args, err := cmdutil.SplitCommand(shell)
	if err != nil || command == "" {
		return shell, nil
	}
	return command, args
}

func appendPowerShellCommandArgs(args []string, cmdStr string) []string {
	result := append([]string(nil), args...)
	commandFlagIndex := shellArgIndex(result, "-Command")
	if commandFlagIndex < 0 {
		commandFlagIndex = len(result)
	}

	prefix := append([]string(nil), result[:commandFlagIndex]...)
	suffix := append([]string(nil), result[commandFlagIndex:]...)
	if !hasShellArg(result, "-NoProfile") {
		prefix = append(prefix, "-NoProfile")
	}
	if !hasShellArg(result, "-NonInteractive") {
		prefix = append(prefix, "-NonInteractive")
	}
	result = append(prefix, suffix...)
	return appendShellCommandArgs(result, "-Command", cmdStr)
}

func appendShellCommandArgs(args []string, flag, cmdStr string) []string {
	result := append([]string(nil), args...)
	if !hasShellArg(result, flag) {
		result = append(result, flag)
	}
	return append(result, cmdStr)
}

func shellArgIndex(args []string, flag string) int {
	for i, arg := range args {
		if strings.EqualFold(arg, flag) {
			return i
		}
	}
	return -1
}

func hasShellArg(args []string, flag string) bool {
	return shellArgIndex(args, flag) >= 0
}

// runCommandWithContext executes cmdStr in a shell using the EnvScope from context,
// falling back to os.Environ() when no scope is present.
func runCommandWithContext(ctx context.Context, cmdStr string) (string, error) {
	commandCtx, cancel, timeout := withCommandTimeout(ctx, substituteCommandTimeout())
	defer cancel()

	cmd := buildShellCommandContext(commandCtx, shellCommandFromContext(ctx), cmdStr)

	if scope := GetEnvScope(ctx); scope != nil {
		cmd.Env = scope.ToSlice()
	} else {
		cmd.Env = os.Environ()
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf(
				"failed to execute command %q: timed out after %v\nstderr=%s",
				cmdStr, timeout, stderr.String(),
			)
		}
		return "", fmt.Errorf(
			"failed to execute command %q: %w\nstderr=%s",
			cmdStr, err, stderr.String(),
		)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func shellCommandFromContext(ctx context.Context) string {
	if cfg := config.GetConfig(ctx); cfg != nil && cfg.Core.DefaultShell != "" {
		return cmdutil.GetShellCommand(cfg.Core.DefaultShell)
	}

	if scope := GetEnvScope(ctx); scope != nil {
		if shell, ok := scope.Get("SHELL"); ok && strings.TrimSpace(shell) != "" {
			return shell
		}
	}

	return cmdutil.GetShellCommand("")
}

func withCommandTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, time.Duration) {
	if ctx == nil {
		ctx = context.Background()
	}

	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= timeout {
			return ctx, func() {}, remaining
		}
	}

	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	return commandCtx, cancel, timeout
}

// substituteCommandsWithContext replaces backtick-delimited commands in input
// with their execution output, using the EnvScope from context if available.
func substituteCommandsWithContext(ctx context.Context, input string) (string, error) {
	var result, cmdBuilder strings.Builder
	inCommand := false

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// Escaped backtick: preserve literally.
		if r == '\\' && i+1 < len(runes) && runes[i+1] == '`' {
			result.WriteString("\\`")
			i++
			continue
		}

		if r == '`' {
			if inCommand {
				if cmdBuilder.Len() == 0 {
					result.WriteString("``")
				} else {
					cmdStr := unescapeDollars(ctx, cmdBuilder.String())
					output, err := runCommandWithContext(ctx, cmdStr)
					if err != nil {
						return "", err
					}
					result.WriteString(output)
				}
				cmdBuilder.Reset()
				inCommand = false
			} else {
				inCommand = true
			}
		} else if inCommand {
			cmdBuilder.WriteRune(r)
		} else {
			result.WriteRune(r)
		}
	}

	// Unclosed backtick: append the partial command as-is.
	if inCommand {
		result.WriteRune('`')
		result.WriteString(cmdBuilder.String())
	}

	return result.String(), nil
}

func substituteShellCommandsWithContext(ctx context.Context, input string) (string, error) {
	var result strings.Builder
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' || i+1 >= len(runes) || runes[i+1] != '(' {
			result.WriteRune(runes[i])
			continue
		}

		cmd, end, ok := readShellCommandSubstitution(runes, i+2)
		if !ok {
			result.WriteString("$(")
			i++
			continue
		}
		output, err := runCommandWithContext(ctx, unescapeDollars(ctx, cmd))
		if err != nil {
			return "", err
		}
		result.WriteString(output)
		i = end
	}
	return result.String(), nil
}

func readShellCommandSubstitution(runes []rune, start int) (string, int, bool) {
	var cmd strings.Builder
	depth := 1
	var singleQuoted bool
	var doubleQuoted bool
	var escaped bool

	for i := start; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			cmd.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			cmd.WriteRune(r)
			escaped = true
			continue
		}
		if r == '\'' && !doubleQuoted {
			singleQuoted = !singleQuoted
			cmd.WriteRune(r)
			continue
		}
		if r == '"' && !singleQuoted {
			doubleQuoted = !doubleQuoted
			cmd.WriteRune(r)
			continue
		}
		if singleQuoted {
			cmd.WriteRune(r)
			continue
		}
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return cmd.String(), i, true
			}
		}
		cmd.WriteRune(r)
	}
	return "", 0, false
}
