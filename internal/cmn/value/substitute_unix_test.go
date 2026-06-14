// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package value

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildShellCommand(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		cmdStr   string
		wantArgs []string
	}{
		{
			name:     "BashShell",
			shell:    "/bin/bash",
			cmdStr:   "echo hello",
			wantArgs: []string{"-c", "echo hello"},
		},
		{
			name:     "ShShell",
			shell:    "/bin/sh",
			cmdStr:   "echo hello",
			wantArgs: []string{"-c", "echo hello"},
		},
		{
			name:     "ZshShell",
			shell:    "/bin/zsh",
			cmdStr:   "echo hello",
			wantArgs: []string{"-c", "echo hello"},
		},
		{
			name:     "EmptyShellFallback",
			shell:    "",
			cmdStr:   "echo hello",
			wantArgs: []string{"-c", "echo hello"},
		},
		{
			name:     "PowershellDetectionEvenOnUnix",
			shell:    "/usr/local/bin/powershell",
			cmdStr:   "echo hello",
			wantArgs: []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"},
		},
		{
			name:     "PwshDetection",
			shell:    "/usr/local/bin/pwsh",
			cmdStr:   "echo hello",
			wantArgs: []string{"-NoProfile", "-NonInteractive", "-Command", "echo hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildShellCommand(tt.shell, tt.cmdStr)
			require.NotNil(t, cmd)

			if tt.shell != "" {
				assert.Equal(t, tt.shell, cmd.Path)
			} else {
				assert.NotEmpty(t, cmd.Path)
			}

			// Args[0] is the command path; the rest are the flags and arguments.
			assert.Equal(t, tt.wantArgs, cmd.Args[1:])
		})
	}
}

func TestRunCommandWithContext_WithBuildShellCommand(t *testing.T) {
	output, err := runCommandWithContext(context.Background(), "echo test123")
	require.NoError(t, err)
	assert.Equal(t, "test123", output)
}

func TestRunCommandWithContext_DefaultTimeout(t *testing.T) {
	start := time.Now()
	_, err := runCommandWithContext(context.Background(), "sleep 3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after")
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestRunCommandWithContext_UsesParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := runCommandWithContext(ctx, "sleep 3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out after")
	assert.Less(t, time.Since(start), time.Second)
}

func TestBuildShellCommand_ComplexCommands(t *testing.T) {
	tests := []struct {
		name   string
		shell  string
		cmdStr string
	}{
		{
			name:   "CommandWithPipes",
			shell:  "/bin/bash",
			cmdStr: "echo hello | tr a-z A-Z",
		},
		{
			name:   "CommandWithQuotes",
			shell:  "/bin/bash",
			cmdStr: `echo "hello world"`,
		},
		{
			name:   "CommandWithVariables",
			shell:  "/bin/bash",
			cmdStr: "VAR=test; echo $VAR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildShellCommand(tt.shell, tt.cmdStr)
			require.NotNil(t, cmd)

			if _, err := exec.LookPath(cmd.Path); err != nil {
				t.Skipf("shell %q not available: %v", cmd.Path, err)
				return
			}
			assert.Contains(t, cmd.Args, "-c")
			assert.Contains(t, cmd.Args, tt.cmdStr)
		})
	}
}
