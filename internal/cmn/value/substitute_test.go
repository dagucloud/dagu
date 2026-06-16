// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package value

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubstituteCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping tests on Windows")
	}

	tests := []struct {
		name     string
		input    string
		want     string
		wantErr  bool
		setupEnv map[string]string
	}{
		{
			name:  "NoCommandSubstitutionNeeded",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "SimpleEchoCommand",
			input: "prefix `echo hello` suffix",
			want:  "prefix hello suffix",
		},
		{
			name:  "MultipleCommands",
			input: "`echo foo` and `echo bar`",
			want:  "foo and bar",
		},
		{
			name:  "NestedQuotes",
			input: "`echo \"hello world\"`",
			want:  "hello world",
		},
		{
			name:  "CommandWithEnvironmentVariable",
			input: "`echo $TEST_VAR`",
			want:  "test_value",
			setupEnv: map[string]string{
				"TEST_VAR": "test_value",
			},
		},
		{
			name:  "CommandWithSpaces",
			input: "`echo 'hello   world'`",
			want:  "hello   world",
		},
		{
			name:    "InvalidCommand",
			input:   "`nonexistentcommand123`",
			wantErr: true,
		},
		{
			name:  "EmptyBackticks",
			input: "``",
			want:  "``",
		},
		{
			name:    "CommandThatReturnsError",
			input:   "`exit 1`",
			wantErr: true,
		},
		{
			name:  "CommandWithPipeline",
			input: "`echo hello | tr 'a-z' 'A-Z'`",
			want:  "HELLO",
		},
		{
			name:  "MultipleLinesInOutput",
			input: "`printf 'line1\\nline2'`",
			want:  "line1\nline2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.setupEnv {
				t.Setenv(k, v)
			}

			got, err := substituteCommandsWithContext(context.Background(), tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSubstituteCommandsEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "EmptyInput",
			input: "",
			want:  "",
		},
		{
			name:  "OnlySpaces",
			input: "     ",
			want:  "     ",
		},
		{
			name:  "UnmatchedBackticks",
			input: "hello `world",
			want:  "hello `world",
		},
		{
			name:  "EscapedBackticks",
			input: "hello \\`world\\`",
			want:  "hello \\`world\\`",
		},
		{
			name:  "MultipleBackticksWithoutCommand",
			input: "``````",
			want:  "``````",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := substituteCommandsWithContext(context.Background(), tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSubstituteCommands_Extended(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping tests on Windows")
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "SimpleCommandSubstitution",
			input: "`echo hello`",
			want:  "hello",
		},
		{
			name:  "CommandInMiddleOfString",
			input: "prefix `echo test` suffix",
			want:  "prefix test suffix",
		},
		{
			name:  "MultipleCommands",
			input: "`echo one` and `echo two`",
			want:  "one and two",
		},
		{
			name:  "NestedBackticksNotSupported",
			input: "`echo `echo nested``",
			want:  "echo nested``",
		},
		{
			name:  "CommandWithArgs",
			input: "`echo hello world`",
			want:  "hello world",
		},
		{
			name:  "EmptyCommand",
			input: "``",
			want:  "``",
		},
		{
			name:    "CommandFailure",
			input:   "`false`",
			wantErr: true,
		},
		{
			name:    "InvalidCommand",
			input:   "`command_that_does_not_exist`",
			wantErr: true,
		},
		{
			name:  "NoCommandSubstitution",
			input: "plain text without backticks",
			want:  "plain text without backticks",
		},
		{
			name:  "EscapedBackticks",
			input: "text with \\`escaped\\` backticks",
			want:  "text with \\`escaped\\` backticks",
		},
		{
			name:  "CommandWithNewlineOutput",
			input: "`printf 'line1\nline2'`",
			want:  "line1\nline2",
		},
		{
			name:  "CommandWithTrailingNewlineRemoved",
			input: "`echo -n hello`",
			want:  "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := substituteCommandsWithContext(context.Background(), tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildShellCommand_Variants(t *testing.T) {
	tests := []struct {
		name        string
		shell       string
		cmdStr      string
		wantArgFlag string
	}{
		{
			name:        "PowerShell",
			shell:       "powershell",
			cmdStr:      "Get-Date",
			wantArgFlag: "-Command",
		},
		{
			name:        "CmdExe",
			shell:       "cmd.exe",
			cmdStr:      "dir",
			wantArgFlag: "/c",
		},
		{
			name:        "EmptyShellFallback",
			shell:       "",
			cmdStr:      "echo hi",
			wantArgFlag: "-c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildShellCommand(tt.shell, tt.cmdStr)
			assert.Contains(t, cmd.Args, tt.wantArgFlag)
		})
	}
}

func TestRunCommandWithContext_WithScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping tests on Windows")
	}

	scope := NewEnvScope(nil, false)
	scope = scope.WithEntry("CMD_TEST_VAR", "from_scope", EnvSourceDAGEnv)
	scope = scope.WithEntry("PATH", os.Getenv("PATH"), EnvSourceOS)
	ctx := WithEnvScope(context.Background(), scope)

	result, err := runCommandWithContext(ctx, "echo hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", result)
}

func TestShellCommandFromContext(t *testing.T) {
	t.Run("PrefersConfigDefaultShell", func(t *testing.T) {
		scope := NewEnvScope(nil, false).WithEntry("SHELL", "/usr/bin/bash", EnvSourceOS)
		ctx := WithEnvScope(context.Background(), scope)
		ctx = config.WithConfig(ctx, &config.Config{
			Core: config.Core{
				DefaultShell: "powershell",
			},
		})

		assert.Equal(t, "powershell", shellCommandFromContext(ctx))
	})

	t.Run("FallsBackToScopeShell", func(t *testing.T) {
		scope := NewEnvScope(nil, false).WithEntry("SHELL", "/usr/bin/bash", EnvSourceOS)
		ctx := WithEnvScope(context.Background(), scope)

		assert.Equal(t, "/usr/bin/bash", shellCommandFromContext(ctx))
	})
}
