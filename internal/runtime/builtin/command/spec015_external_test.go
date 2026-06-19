// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command_test

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	daguruntime "github.com/dagucloud/dagu/internal/runtime"
	command "github.com/dagucloud/dagu/internal/runtime/builtin/command"
	"github.com/stretchr/testify/require"
)

func TestSpec015ParseScriptShebang(t *testing.T) {
	tests := []struct {
		name      string
		script    string
		wantCmd   string
		wantArgs  []string
		wantEmpty bool
		wantErr   string
	}{
		{
			name:     "bare interpreter and argument",
			script:   "#!/usr/bin/env bash\necho ok\n",
			wantCmd:  "/usr/bin/env",
			wantArgs: []string{"bash"},
		},
		{
			name:     "spaces and tabs after marker",
			script:   "#! \t/usr/bin/env\tbash\n",
			wantCmd:  "/usr/bin/env",
			wantArgs: []string{"bash"},
		},
		{
			name:    "quoted spans and backslash grammar",
			script:  "#!interp 'arg one' \"two \\\"quoted\\\"\" '' a\\ b $PATH *.go a|b\n",
			wantCmd: "interp",
			wantArgs: []string{
				"arg one",
				`two "quoted"`,
				"",
				"a b",
				"$PATH",
				"*.go",
				"a|b",
			},
		},
		{
			name:      "leading blank line suppresses shebang",
			script:    "\n#!/bin/sh\necho ordinary text\n",
			wantEmpty: true,
		},
		{
			name:      "leading BOM suppresses shebang",
			script:    "\ufeff#!/bin/sh\necho ordinary text\n",
			wantEmpty: true,
		},
		{
			name:     "CRLF first line",
			script:   "#!/bin/sh\r\necho ok\r\n",
			wantCmd:  "/bin/sh",
			wantArgs: []string{},
		},
		{
			name:    "empty interpreter",
			script:  "#!   \n",
			wantErr: "no interpreter",
		},
		{
			name:    "unterminated single quote",
			script:  "#!interp 'unterminated\n",
			wantErr: "unterminated single quote",
		},
		{
			name:    "unterminated double quote",
			script:  "#!interp \"unterminated\n",
			wantErr: "unterminated double quote",
		},
		{
			name:    "trailing backslash",
			script:  "#!interp arg\\\n",
			wantErr: "trailing unpaired backslash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotArgs, err := command.ParseScriptShebangForTest(tt.script)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantEmpty {
				require.Empty(t, gotCmd)
				require.Empty(t, gotArgs)
				return
			}
			require.Equal(t, tt.wantCmd, gotCmd)
			require.Equal(t, tt.wantArgs, gotArgs)
		})
	}
}

func TestSpec015BareShebangInterpreterResolvesFromStepPATH(t *testing.T) {
	dir := t.TempDir()
	commandName := "spec015-interp"
	fileName := commandName
	if goruntime.GOOS == "windows" {
		fileName += ".exe"
	}
	interpreter := filepath.Join(dir, fileName)
	require.NoError(t, os.WriteFile(interpreter, []byte(""), 0o755))

	dag := &core.DAG{
		Name:       "spec015-test",
		WorkingDir: t.TempDir(),
	}
	ctx := coreexec.NewContext(context.Background(), dag, "", "")
	env := daguruntime.NewEnv(ctx, core.Step{Name: "script"})
	env.Scope = env.Scope.WithEntries(map[string]string{"PATH": dir}, cmnvalue.EnvSourceStepEnv)
	ctx = daguruntime.WithEnv(ctx, env)

	got, err := command.ResolveShebangExecutableForTest(ctx, commandName)
	require.NoError(t, err)
	require.Equal(t, interpreter, got)
}

func TestSpec015ShellBuilderScriptCarrierValidation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		builder command.ShellCommandBuilderForTest
		want    []string
		wantErr string
	}{
		{
			name: "unix rejects c carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"sh", "-c"},
				Script: "/tmp/script.sh",
			},
			wantErr: "-c",
		},
		{
			name: "unix rejects combined c carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"bash", "-lc"},
				Script: "/tmp/script.sh",
			},
			wantErr: "-lc",
		},
		{
			name: "unix rejects s carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"sh", "-s"},
				Script: "/tmp/script.sh",
			},
			wantErr: "-s",
		},
		{
			name: "explicit unix shell does not add errexit",
			builder: command.ShellCommandBuilderForTest{
				Shell:              []string{"bash"},
				Script:             "/tmp/script.sh",
				UserSpecifiedShell: true,
			},
			want: []string{"bash", "/tmp/script.sh"},
		},
		{
			name: "powershell includes defaults",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"pwsh"},
				Script: "script.ps1",
			},
			want: []string{"pwsh", "-ExecutionPolicy", "Bypass", "-NoProfile", "-NonInteractive", "-File", "script.ps1"},
		},
		{
			name: "powershell honors final file carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"pwsh", "-File"},
				Script: "script.ps1",
			},
			want: []string{"pwsh", "-ExecutionPolicy", "Bypass", "-NoProfile", "-NonInteractive", "-File", "script.ps1"},
		},
		{
			name: "powershell rejects args after file carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"pwsh", "-File", "author.ps1"},
				Script: "script.ps1",
			},
			wantErr: "-File",
		},
		{
			name: "powershell rejects command carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"pwsh", "-Command"},
				Script: "script.ps1",
			},
			wantErr: "-Command",
		},
		{
			name: "cmd rejects authored body after c carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"cmd", "/c", "echo body"},
				Script: "script.bat",
			},
			wantErr: "/c",
		},
		{
			name: "cmd accepts final c carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"cmd", "/c"},
				Script: "script.bat",
			},
			want: []string{"cmd", "/c", "script.bat"},
		},
		{
			name: "nix rejects authored run body",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"nix-shell", "--run", "echo body"},
				Script: "/tmp/script.sh",
			},
			wantErr: "--run",
		},
		{
			name: "nix quotes prepared script path",
			builder: command.ShellCommandBuilderForTest{
				Shell:  []string{"nix-shell"},
				Script: "/tmp/dagu script;touch unexpected.sh",
			},
			want: []string{"nix-shell", "--pure", "--run", "set -e; '/tmp/dagu script;touch unexpected.sh'"},
		},
		{
			name: "nix inserts packages before configured run carrier",
			builder: command.ShellCommandBuilderForTest{
				Shell:         []string{"nix-shell", "--run"},
				ShellPackages: []string{"bash"},
				Script:        "/tmp/script.sh",
			},
			want: []string{"nix-shell", "-p", "bash", "--pure", "--run", "set -e; /tmp/script.sh"},
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
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSpec015ScriptPreparationUsesSystemTempAndExplicitShellExtension(t *testing.T) {
	workDir := t.TempDir()
	blockingFile := filepath.Join(workDir, "not-a-directory")
	require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0o600))

	scriptFile, err := command.SetupScriptForExecutionForTest(
		filepath.Join(blockingFile, "child"),
		"#!/usr/bin/env pwsh\nWrite-Output ok\n",
		"",
		[]string{"pwsh"},
		true,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(scriptFile) })

	require.False(t, strings.HasPrefix(scriptFile, workDir+string(os.PathSeparator)))
	require.Equal(t, ".ps1", filepath.Ext(scriptFile))
}
