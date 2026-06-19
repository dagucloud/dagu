// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command_test

import (
	"context"
	"strings"
	"testing"

	command "github.com/dagucloud/dagu/internal/runtime/builtin/command"
	"github.com/stretchr/testify/require"
)

func TestSpec014ShellBuilderCommandCarrierValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		builder command.ShellCommandBuilderForTest
		want    []string
		wantErr string
	}{
		{
			name: "unix default inserts errexit before configured carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"sh", "-c"},
				ShellCommandArgs: "echo ok",
			},
			want: []string{"sh", "-e", "-c", "echo ok"},
		},
		{
			name: "unix explicit shell allows separate options before carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:              []string{"bash", "-e", "-c"},
				ShellCommandArgs:   "echo ok",
				UserSpecifiedShell: true,
			},
			want: []string{"bash", "-e", "-c", "echo ok"},
		},
		{
			name: "unix rejects combined carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"bash", "-lc"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "-lc",
		},
		{
			name: "unix rejects inline carrier body",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"sh", "-cecho ok"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "-cecho ok",
		},
		{
			name: "unix rejects authored argument after carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"sh", "-c", "echo old"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "-c",
		},
		{
			name: "powershell accepts command alias as final carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"pwsh", "-C"},
				ShellCommandArgs: "Write-Output ok",
			},
			want: []string{"pwsh", "-NoProfile", "-NonInteractive", "-C", spec014PowerShellInline("Write-Output ok")},
		},
		{
			name: "powershell preserves bare local script command text",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"pwsh"},
				ShellCommandArgs: "test.ps1 arg1",
			},
			want: []string{"pwsh", "-NoProfile", "-NonInteractive", "-Command", spec014PowerShellInline("test.ps1 arg1")},
		},
		{
			name: "powershell rejects inline command carrier body",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"pwsh", "-Command:Write-Output ok"},
				ShellCommandArgs: "Write-Output ok",
			},
			wantErr: "-Command:Write-Output ok",
		},
		{
			name: "powershell rejects encoded command",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"pwsh", "-EncodedCommand"},
				ShellCommandArgs: "Write-Output ok",
			},
			wantErr: "-EncodedCommand",
		},
		{
			name: "powershell rejects file carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"pwsh", "-File"},
				ShellCommandArgs: "Write-Output ok",
			},
			wantErr: "-File",
		},
		{
			name: "powershell rejects authored argument after carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"pwsh", "-Command", "Write-Output old"},
				ShellCommandArgs: "Write-Output ok",
			},
			wantErr: "-Command",
		},
		{
			name: "cmd accepts final carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"cmd", "/C"},
				ShellCommandArgs: "echo ok",
			},
			want: []string{"cmd", "/C", "echo ok"},
		},
		{
			name: "cmd preserves bare local script command text",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"cmd"},
				ShellCommandArgs: "test.cmd arg1",
			},
			want: []string{"cmd", "/c", "test.cmd arg1"},
		},
		{
			name: "cmd rejects inline carrier body",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"cmd", "/cecho ok"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "/cecho ok",
		},
		{
			name: "cmd rejects authored argument after carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"cmd", "/c", "echo old"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "/c",
		},
		{
			name: "nix inserts packages and purity before configured carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"nix-shell", "--run"},
				ShellCommandArgs: "echo ok",
				ShellPackages:    []string{"bash"},
			},
			want: []string{"nix-shell", "-p", "bash", "--pure", "--run", "set -e; echo ok"},
		},
		{
			name: "nix rejects inline run carrier body",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"nix-shell", "--run=echo old"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "--run=echo old",
		},
		{
			name: "nix rejects authored argument after carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"nix-shell", "--run", "echo old"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "--run",
		},
		{
			name: "other shell uses exact c fallback",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"custom-shell", "-c"},
				ShellCommandArgs: "echo ok",
			},
			want: []string{"custom-shell", "-c", "echo ok"},
		},
		{
			name: "other shell rejects inline c carrier body",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"custom-shell", "-cecho old"},
				ShellCommandArgs: "echo ok",
			},
			wantErr: "-cecho old",
		},
		{
			name: "shell packages require nix shell",
			builder: command.ShellCommandBuilderForTest{
				Shell:            []string{"sh"},
				ShellCommandArgs: "echo ok",
				ShellPackages:    []string{"bash"},
			},
			wantErr: "shell_packages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := command.BuildShellCommandForTest(ctx, tt.builder)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			requireShellCommand(t, tt.want, got)
		})
	}
}

func spec014PowerShellInline(command string) string {
	statements := []string{
		"$ErrorActionPreference = 'Stop'",
		"$utf8NoBom = New-Object -TypeName System.Text.UTF8Encoding -ArgumentList $false",
		"[Console]::InputEncoding = $utf8NoBom",
		"[Console]::OutputEncoding = $utf8NoBom",
		"$OutputEncoding = $utf8NoBom",
	}
	if command != "" {
		statements = append(statements, command)
	}
	return strings.Join(statements, "; ")
}
