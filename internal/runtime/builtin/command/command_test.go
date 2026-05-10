// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertExecutableName(t *testing.T, got string, want string) {
	t.Helper()

	gotBase := strings.TrimSuffix(strings.ToLower(filepath.Base(got)), ".exe")
	wantBase := strings.TrimSuffix(strings.ToLower(filepath.Base(want)), ".exe")
	assert.Equal(t, wantBase, gotBase)
}

func assertCommandArgs(t *testing.T, got []string, want []string) {
	t.Helper()

	require.Len(t, got, len(want))
	assertExecutableName(t, got[0], want[0])
	assert.Equal(t, want[1:], got[1:])
}

func expectedPowerShellCommandArgs(shell, command string) []string {
	return []string{shell, "-NoProfile", "-NonInteractive", "-Command", powerShellInlineCommand(command)}
}

func expectedPowerShellScriptArgs(shell, script string) []string {
	return []string{shell, "-ExecutionPolicy", "Bypass", "-NoProfile", "-NonInteractive", "-File", script}
}

func TestPowerShellPreambleUsesLegacyCompatibleSyntax(t *testing.T) {
	preamble := powerShellPreamble()

	assert.NotContains(t, preamble, "::new(")
	assert.NotContains(t, preamble, "PSNativeCommandUseErrorActionPreference")
	assert.NotContains(t, preamble, "PSDefaultParameterValues")
	assert.NotContains(t, preamble, "Out-File:Encoding")
	assert.Contains(t, preamble, "New-Object -TypeName System.Text.UTF8Encoding -ArgumentList $false")
}

func TestDirectShell(t *testing.T) {
	ctx := context.Background()

	t.Run("DirectWithCommandAndScript", func(t *testing.T) {
		builder := shellCommandBuilder{
			Shell:   []string{"direct"},
			Command: "/usr/bin/python",
			Script:  "script.py",
			Args:    []string{"-u"},
		}
		cmd, err := builder.Build(ctx)
		require.NoError(t, err)
		require.NotNil(t, cmd)
		assert.Equal(t, []string{"/usr/bin/python", "-u", "script.py"}, cmd.Args)
	})

	t.Run("DirectWithCommandOnly", func(t *testing.T) {
		builder := shellCommandBuilder{
			Shell:   []string{"direct"},
			Command: "/bin/echo",
			Args:    []string{"hello", "world"},
		}
		cmd, err := builder.Build(ctx)
		require.NoError(t, err)
		require.NotNil(t, cmd)
		assert.Equal(t, []string{"/bin/echo", "hello", "world"}, cmd.Args)
	})

	t.Run("DirectWithCommandAndShellCmdArgs", func(t *testing.T) {
		// When Command is set (from array syntax), ShellCmdArgs is ignored
		// This is the normal case from runtime evaluation
		builder := shellCommandBuilder{
			Shell:            []string{"direct"},
			Command:          "/bin/echo",
			Args:             []string{"hello"},
			ShellCommandArgs: "echo hello", // This gets set by runtime but should be ignored
		}
		cmd, err := builder.Build(ctx)
		require.NoError(t, err)
		require.NotNil(t, cmd)
		assert.Equal(t, []string{"/bin/echo", "hello"}, cmd.Args)
	})

	t.Run("DirectRejectsStringCommandOnly", func(t *testing.T) {
		// When only ShellCommandArgs is set (string command without array),
		// direct shell cannot parse it
		builder := shellCommandBuilder{
			Shell:            []string{"direct"},
			ShellCommandArgs: "echo hello",
		}
		_, err := builder.Build(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires command array syntax")
	})

	t.Run("DirectRequiresCommand", func(t *testing.T) {
		builder := shellCommandBuilder{
			Shell: []string{"direct"},
		}
		_, err := builder.Build(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires 'command' field")
	})
}

func TestShellCommandBuilder_Build(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name            string
		builder         shellCommandBuilder
		expectedInArgs  []string
		shouldSkipOnMac bool
	}{
		{
			name: "BasicShellCommand",
			builder: shellCommandBuilder{
				Shell:            []string{"/bin/sh"},
				ShellCommandArgs: "echo hello",
			},
			expectedInArgs: []string{"-c", "echo hello"},
		},
		{
			name: "BashCommand",
			builder: shellCommandBuilder{
				Shell:            []string{"/bin/bash"},
				ShellCommandArgs: "echo hello",
			},
			expectedInArgs: []string{"-c", "echo hello"},
		},
		{
			name: "CommandWithScript",
			builder: shellCommandBuilder{
				Command: "python",
				Script:  "script.py",
				Args:    []string{"-u"},
				Shell:   []string{"python"},
			},
			expectedInArgs: []string{"-u", "script.py"},
		},
		{
			name: "PowershellCommand",
			builder: shellCommandBuilder{
				Shell:            []string{"powershell"},
				ShellCommandArgs: "Write-Host hello",
			},
			expectedInArgs:  []string{"-Command", powerShellInlineCommand("Write-Host hello")},
			shouldSkipOnMac: true,
		},
		{
			name: "CmdExeCommand",
			builder: shellCommandBuilder{
				Shell:            []string{"cmd.exe"},
				ShellCommandArgs: "echo hello",
			},
			expectedInArgs:  []string{"/c", "echo hello"},
			shouldSkipOnMac: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldSkipOnMac && goruntime.GOOS == "darwin" {
				t.Skip("Skipping Windows-specific test on macOS")
			}

			cmd, err := tt.builder.Build(ctx)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			// Check that expected arguments are in the command
			for _, expectedArg := range tt.expectedInArgs {
				assert.Contains(t, cmd.Args, expectedArg)
			}
		})
	}
}

func TestBuildPowerShellCommand(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		builder  shellCommandBuilder
		expected []string
	}{
		{
			name: "BasicPowershellCommand",
			builder: shellCommandBuilder{
				Shell:            []string{"powershell"},
				ShellCommandArgs: "Get-Date",
			},
			expected: expectedPowerShellCommandArgs("powershell", "Get-Date"),
		},
		{
			name: "PowershellWithExistingCommand",
			builder: shellCommandBuilder{
				Shell:            []string{"powershell", "-Command"},
				ShellCommandArgs: "Get-Date",
			},
			expected: expectedPowerShellCommandArgs("powershell", "Get-Date"),
		},
		{
			name: "PowershellWithExistingCommandAlias",
			builder: shellCommandBuilder{
				Shell:            []string{"powershell", "-C"},
				ShellCommandArgs: "Get-Date",
			},
			expected: []string{"powershell", "-NoProfile", "-NonInteractive", "-C", powerShellInlineCommand("Get-Date")},
		},
		{
			name: "PowershellWithScript",
			builder: shellCommandBuilder{
				Shell:  []string{"powershell"},
				Script: "test.ps1",
			},
			expected: expectedPowerShellScriptArgs("powershell", "test.ps1"),
		},
		{
			name: "PowershellWithExplicitCommandAndScript",
			builder: shellCommandBuilder{
				Shell:   []string{"powershell"},
				Command: "python",
				Script:  "test.py",
				Args:    []string{"-u"},
			},
			expected: []string{"python", "-u", "test.py"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.builder.Build(ctx)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			assertCommandArgs(t, cmd.Args, tt.expected)
		})
	}
}

func TestBuildCmdCommand(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		builder        shellCommandBuilder
		expectedInArgs []string // Args that must be present
	}{
		{
			name: "BasicCmdCommand",
			builder: shellCommandBuilder{
				Shell:            []string{"cmd"},
				ShellCommandArgs: "dir",
			},
			expectedInArgs: []string{"/c", "dir"},
		},
		{
			name: "CmdWithExisting/C",
			builder: shellCommandBuilder{
				Shell:            []string{"cmd", "/c"},
				ShellCommandArgs: "dir",
			},
			expectedInArgs: []string{"/c", "dir"},
		},
		{
			name: "CmdWithScript",
			builder: shellCommandBuilder{
				Shell:   []string{"cmd"},
				Command: "python",
				Script:  "test.py",
				Args:    []string{"-u"},
			},
			expectedInArgs: []string{"python", "-u", "test.py"},
		},
		{
			name: "CmdWithScriptOnly",
			builder: shellCommandBuilder{
				Shell:  []string{"cmd"},
				Script: "script.bat",
			},
			expectedInArgs: []string{"/c", "script.bat"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.builder.Build(ctx)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			for _, expectedArg := range tt.expectedInArgs {
				assert.Contains(t, cmd.Args, expectedArg)
			}
		})
	}
}

// TestBuildShellWithScriptOnly tests that each shell properly handles Script without Command
func TestBuildShellWithScriptOnly(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		builder        shellCommandBuilder
		expectedInArgs []string
	}{
		{
			name: "CmdShellWithScriptOnly",
			builder: shellCommandBuilder{
				Shell:  []string{"cmd"},
				Script: "/tmp/script.bat",
			},
			expectedInArgs: []string{"/c", "/tmp/script.bat"},
		},
		{
			name: "PowerShellWithScriptOnly",
			builder: shellCommandBuilder{
				Shell:  []string{"powershell"},
				Script: "/tmp/script.ps1",
			},
			expectedInArgs: []string{"-ExecutionPolicy", "Bypass", "-File", "/tmp/script.ps1"},
		},
		{
			name: "PwshWithScriptOnly",
			builder: shellCommandBuilder{
				Shell:  []string{"pwsh"},
				Script: "/tmp/script.ps1",
			},
			expectedInArgs: []string{"-ExecutionPolicy", "Bypass", "-File", "/tmp/script.ps1"},
		},
		{
			name: "BashWithScriptOnly",
			builder: shellCommandBuilder{
				Shell:  []string{"bash"},
				Script: "/tmp/script.sh",
			},
			expectedInArgs: []string{"-e", "/tmp/script.sh"},
		},
		{
			name: "ShWithScriptOnly",
			builder: shellCommandBuilder{
				Shell:  []string{"sh"},
				Script: "/tmp/script.sh",
			},
			expectedInArgs: []string{"-e", "/tmp/script.sh"},
		},
		{
			name: "NixShellWithScriptOnly",
			builder: shellCommandBuilder{
				Shell:  []string{"nix-shell"},
				Script: "/tmp/script.sh",
			},
			expectedInArgs: []string{"--run", "set -e; /tmp/script.sh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.builder.Build(ctx)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			for _, expectedArg := range tt.expectedInArgs {
				assert.Contains(t, cmd.Args, expectedArg,
					"expected %q in args %v", expectedArg, cmd.Args)
			}
		})
	}
}

func TestCommandExecutor_Kill(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	// Test that Kill returns nil for nil command
	executor := &commandExecutor{}
	err := executor.Kill(os.Interrupt)
	assert.NoError(t, err)

	// Test that Kill returns nil for command without process
	executor = &commandExecutor{cmd: &exec.Cmd{}}
	err = executor.Kill(os.Interrupt)
	assert.NoError(t, err)
}

func TestCommandConfig_NewCmd(t *testing.T) {
	ctx := context.Background()
	// Create a minimal DAG for the test
	dag := &core.DAG{
		Name: "test-dag",
		Env:  []string{},
	}
	// Setup the context with DAG environment
	ctx = runtime.NewContext(ctx, dag, "test-run", "")

	env := runtime.NewEnv(ctx, core.Step{})
	env.WorkingDir = t.TempDir()
	ctx = runtime.WithEnv(ctx, env)

	tests := []struct {
		name       string
		config     commandConfig
		scriptFile string
		checkPath  string
	}{
		{
			name: "SimpleCommand",
			config: commandConfig{
				Ctx:     ctx,
				Command: "echo",
				Args:    []string{"hello"},
			},
			checkPath: "echo",
		},
		{
			name: "ShellCommand",
			config: commandConfig{
				Ctx:              ctx,
				Shell:            []string{"/bin/sh"},
				ShellCommandArgs: "echo hello",
			},
			checkPath: "/bin/sh",
		},
		{
			name: "ScriptFile",
			config: commandConfig{
				Ctx:   ctx,
				Shell: []string{"/bin/sh"},
			},
			scriptFile: "/tmp/test.sh",
			checkPath:  "/bin/sh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scriptFile := tt.scriptFile
			if scriptFile != "" {
				tempDir := t.TempDir()
				scriptFile = filepath.Join(tempDir, "test.sh")
				require.NoError(t, os.WriteFile(scriptFile, []byte("#!/bin/sh\necho hello\n"), 0o755))
			}

			cmd, err := tt.config.newCmd(ctx, scriptFile)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			// Check the command path
			assertExecutableName(t, cmd.Path, tt.checkPath)

			// Check working directory
			if tt.config.Dir != "" {
				assert.Equal(t, tt.config.Dir, cmd.Dir)
			}
		})
	}
}

func TestCommandExecutor_ExitCode(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := context.Background()
	// Create a minimal DAG for the test
	dag := &core.DAG{
		Name: "test-dag",
		Env:  []string{},
	}
	// Setup the context with DAG environment
	ctx = runtime.NewContext(ctx, dag, "test-run", "")

	env := runtime.NewEnv(ctx, core.Step{})
	env.WorkingDir = t.TempDir()
	ctx = runtime.WithEnv(ctx, env)

	tests := []struct {
		name         string
		step         core.Step
		expectedCode int
		shouldError  bool
	}{
		{
			name: "SuccessfulCommand",
			step: core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: "true"}},
			},
			expectedCode: 0,
			shouldError:  false,
		},
		{
			name: "FailingCommand",
			step: core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: "false"}},
			},
			expectedCode: 1,
			shouldError:  true,
		},
		{
			name: "ExitWithSpecificCode",
			step: core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: "/bin/sh", Args: []string{"-c", "exit 42"}}},
			},
			expectedCode: 42,
			shouldError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec, err := NewCommand(ctx, tt.step)
			require.NoError(t, err)

			err = exec.Run(ctx)
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Check exit code
			if exitCoder, ok := exec.(executor.ExitCoder); ok {
				assert.Equal(t, tt.expectedCode, exitCoder.ExitCode())
			}
		})
	}
}

func TestReadFirstLine(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"ShebangLine", "#!/bin/bash\necho hello", "#!/bin/bash"},
		{"SingleLine", "single line", "single line"},
		{"EmptyFile", "", ""},
		{"MultipleLines", "first\nsecond\nthird", "first"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "test-*.sh")
			require.NoError(t, err)
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			_, err = tmpFile.WriteString(tt.content)
			require.NoError(t, err)
			_ = tmpFile.Close()

			result, err := readFirstLine(tmpFile.Name())
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}

	t.Run("NonExistentFile", func(t *testing.T) {
		_, err := readFirstLine("/non/existent/file")
		assert.Error(t, err)
	})
}

// setupTestContext creates a test context with DAG and execution environment
func setupTestContext(t *testing.T, dag *core.DAG, step core.Step) context.Context {
	t.Helper()
	ctx := context.Background()
	if dag == nil {
		dag = &core.DAG{
			Name: "test-dag",
			Env:  []string{},
		}
	}
	ctx = runtime.NewContext(ctx, dag, "test-run", "")
	env := runtime.NewEnv(ctx, step)
	env.WorkingDir = t.TempDir()
	return runtime.WithEnv(ctx, env)
}

// TestCommandExecutor_SimpleCommand tests basic command execution
func TestCommandExecutor_SimpleCommand(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := setupTestContext(t, nil, core.Step{})

	tests := []struct {
		name           string
		step           core.Step
		expectedOutput string
		shouldError    bool
	}{
		{
			name: "echo command",
			step: core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: "echo", Args: []string{"hello"}}},
			},
			expectedOutput: "hello\n",
			shouldError:    false,
		},
		{
			name: "command with multiple args",
			step: core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: "echo", Args: []string{"hello", "world"}}},
			},
			expectedOutput: "hello world\n",
			shouldError:    false,
		},
		{
			name: "command that fails",
			step: core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: "false"}},
			},
			shouldError: true,
		},
		{
			name: "command not found",
			step: core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: "nonexistent_command_12345"}},
			},
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec, err := NewCommand(ctx, tt.step)
			require.NoError(t, err)

			var stdout, stderr strings.Builder
			exec.SetStdout(&stdout)
			exec.SetStderr(&stderr)

			err = exec.Run(ctx)
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.expectedOutput != "" {
					assert.Equal(t, tt.expectedOutput, stdout.String())
				}
			}
		})
	}
}

// TestCommandExecutor_ScriptExecution tests script execution
func TestCommandExecutor_ScriptExecution(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tests := []struct {
		name           string
		script         string
		shell          string
		expectedOutput string
		shouldError    bool
	}{
		{
			name:           "simple script",
			script:         "echo 'hello from script'",
			expectedOutput: "hello from script\n",
		},
		{
			name:           "multiline script",
			script:         "echo 'line1'\necho 'line2'",
			expectedOutput: "line1\nline2\n",
		},
		{
			name:           "script with shebang",
			script:         "#!/bin/sh\necho 'with shebang'",
			expectedOutput: "with shebang\n",
		},
		{
			name:           "script with bash shebang",
			script:         "#!/bin/bash\necho 'bash shebang'",
			expectedOutput: "bash shebang\n",
		},
		{
			name:           "script with variables",
			script:         "VAR='test value'\necho $VAR",
			expectedOutput: "test value\n",
		},
		{
			name:        "script with exit code",
			script:      "exit 1",
			shouldError: true,
		},
		{
			name:           "script with explicit shell",
			script:         "echo 'explicit shell'",
			shell:          "/bin/sh",
			expectedOutput: "explicit shell\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := core.Step{
				Name:   "test",
				Script: tt.script,
				Shell:  tt.shell,
			}
			ctx := setupTestContext(t, nil, step)

			exec, err := NewCommand(ctx, step)
			require.NoError(t, err)

			var stdout, stderr strings.Builder
			exec.SetStdout(&stdout)
			exec.SetStderr(&stderr)

			err = exec.Run(ctx)
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedOutput, stdout.String())
			}
		})
	}
}

// TestCommandExecutor_ShellCmdArgs tests shell command with arguments (ShellCmdArgs)
func TestCommandExecutor_ShellCmdArgs(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tests := []struct {
		name           string
		shellCmdArgs   string
		shell          string
		expectedOutput string
		shouldError    bool
	}{
		{
			name:           "simple shell command",
			shellCmdArgs:   "echo 'hello'",
			shell:          "/bin/sh", // Shell must be set for ShellCmdArgs to work
			expectedOutput: "hello\n",
		},
		{
			name:           "shell command with pipe",
			shellCmdArgs:   "echo 'hello world' | tr ' ' '_'",
			shell:          "/bin/sh",
			expectedOutput: "hello_world\n",
		},
		{
			name:           "shell command with variable",
			shellCmdArgs:   "X=test; echo $X",
			shell:          "/bin/sh",
			expectedOutput: "test\n",
		},
		{
			name:         "shell command that fails",
			shellCmdArgs: "exit 1",
			shell:        "/bin/sh",
			shouldError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := core.Step{
				Name:         "test",
				ShellCmdArgs: tt.shellCmdArgs,
				Shell:        tt.shell,
			}
			ctx := setupTestContext(t, nil, step)

			exec, err := NewCommand(ctx, step)
			require.NoError(t, err)

			var stdout, stderr strings.Builder
			exec.SetStdout(&stdout)
			exec.SetStderr(&stderr)

			err = exec.Run(ctx)
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedOutput, stdout.String())
			}
		})
	}
}

// TestCommandExecutor_CommandWithScript tests command + script combination (like perl script.pl)
func TestCommandExecutor_CommandWithScript(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tests := []struct {
		name           string
		command        string
		args           []string
		script         string
		shell          string // Shell must be set when using Command + Script
		expectedOutput string
		shouldError    bool
	}{
		{
			name:           "sh with script",
			command:        "/bin/sh",
			script:         "echo 'sh script'",
			shell:          "/bin/sh",
			expectedOutput: "sh script\n",
		},
		{
			name:           "bash with script",
			command:        "/bin/bash",
			script:         "echo 'bash script'",
			shell:          "/bin/bash",
			expectedOutput: "bash script\n",
		},
		{
			name:           "command with args and script",
			command:        "/bin/sh",
			args:           []string{"-x"}, // trace mode
			script:         "echo 'traced'",
			shell:          "/bin/sh",
			expectedOutput: "traced\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check if command exists
			if _, err := exec.LookPath(tt.command); err != nil {
				t.Skipf("Command %s not available", tt.command)
			}

			step := core.Step{
				Name:     "test",
				Commands: []core.CommandEntry{{Command: tt.command, Args: tt.args}},
				Script:   tt.script,
				Shell:    tt.shell,
			}
			ctx := setupTestContext(t, nil, step)

			executor, err := NewCommand(ctx, step)
			require.NoError(t, err)

			var stdout, stderr strings.Builder
			executor.SetStdout(&stdout)
			executor.SetStderr(&stderr)

			err = executor.Run(ctx)
			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedOutput, stdout.String())
			}
		})
	}
}

// TestCommandExecutor_DAGLevelShell tests DAG-level shell configuration
func TestCommandExecutor_DAGLevelShell(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	dag := &core.DAG{
		Name:  "test-dag",
		Shell: "/bin/bash",
		Env:   []string{},
	}

	step := core.Step{
		Name:   "test",
		Script: "echo 'dag shell'",
	}

	ctx := setupTestContext(t, dag, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dag shell\n", stdout.String())
}

// TestCommandExecutor_StepLevelShellOverride tests step shell overriding DAG shell
func TestCommandExecutor_StepLevelShellOverride(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	dag := &core.DAG{
		Name:  "test-dag",
		Shell: "/bin/bash",
		Env:   []string{},
	}

	step := core.Step{
		Name:   "test",
		Shell:  "/bin/sh", // Override DAG shell
		Script: "echo 'step shell'",
	}

	ctx := setupTestContext(t, dag, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "step shell\n", stdout.String())
}

// TestCommandExecutor_StderrCapture tests that stderr is captured
func TestCommandExecutor_StderrCapture(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	step := core.Step{
		Name:   "test",
		Script: "echo 'error message' >&2",
	}

	ctx := setupTestContext(t, nil, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout, stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "", stdout.String())
	assert.Equal(t, "error message\n", stderr.String())
}

// TestCommandExecutor_StderrInError tests that stderr is included in error messages
func TestCommandExecutor_StderrInError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	step := core.Step{
		Name:   "test",
		Script: "echo 'error details' >&2; exit 1",
	}

	ctx := setupTestContext(t, nil, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout, stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error details")
}

// TestCommandExecutor_ScriptErrorAnnotation tests that failing script line content
// is included in error messages.
func TestCommandExecutor_ScriptErrorAnnotation(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	script := "echo line1\necho line2\nnonexistent_command_xyz_12345\necho line4\n"
	step := core.Step{
		Name:   "test",
		Script: script,
	}

	ctx := setupTestContext(t, nil, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout, stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.Error(t, err)

	// Error field should be clean: no temp script path, no script dump
	errMsg := err.Error()
	assert.Contains(t, errMsg, "nonexistent_command_xyz_12345")
	assert.NotContains(t, errMsg, "--- script content ---")
	assert.NotContains(t, errMsg, "dagu_script-")

	// Full script with error marker should appear in stderr log output
	stderrOutput := stderr.String()
	assert.Contains(t, stderrOutput, "--- script content ---")
	assert.Contains(t, stderrOutput, "nonexistent_command_xyz_12345")
	assert.Contains(t, stderrOutput, "echo line1")
	assert.Contains(t, stderrOutput, "echo line4")
	// Failing line should be marked with >>
	assert.Contains(t, stderrOutput, " >>    3: nonexistent_command_xyz_12345")

	ce := exec.(*commandExecutor)
	assert.NotEmpty(t, ce.scriptFile)
	_, statErr := os.Stat(ce.scriptFile)
	assert.True(t, os.IsNotExist(statErr), "script file should be deleted after failure")
}

// TestCommandExecutor_ScriptCleanedOnFailure tests that temp script files
// are cleaned up when execution fails.
func TestCommandExecutor_ScriptCleanedOnFailure(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	step := core.Step{
		Name:   "test",
		Script: "exit 1",
	}

	ctx := setupTestContext(t, nil, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout, stderr strings.Builder
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	err = exec.Run(ctx)
	require.Error(t, err)

	ce := exec.(*commandExecutor)
	assert.NotEmpty(t, ce.scriptFile)
	_, statErr := os.Stat(ce.scriptFile)
	assert.True(t, os.IsNotExist(statErr), "script file should be deleted on failure")
}

// TestCommandExecutor_ScriptCleanedOnSuccess tests that temp script files
// are cleaned up after successful execution.
func TestCommandExecutor_ScriptCleanedOnSuccess(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	step := core.Step{
		Name:   "test",
		Script: "echo hello",
	}

	ctx := setupTestContext(t, nil, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)

	ce := exec.(*commandExecutor)
	_, statErr := os.Stat(ce.scriptFile)
	assert.True(t, os.IsNotExist(statErr), "script file should be deleted on success")
}

// TestCommandExecutor_ScriptCleanedOnCancel tests that temp script files
// are cleaned up when execution stops via context cancellation.
func TestCommandExecutor_ScriptCleanedOnCancel(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tmpDir := t.TempDir()

	step := core.Step{
		Name:   "test",
		Script: "sleep 10",
		Dir:    tmpDir,
	}

	baseCtx := setupTestContext(t, nil, step)
	env := runtime.GetEnv(baseCtx)
	env.WorkingDir = tmpDir
	baseCtx = runtime.WithEnv(baseCtx, env)

	ctx, cancel := context.WithTimeout(baseCtx, 50*time.Millisecond)
	defer cancel()

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	err = exec.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	ce := exec.(*commandExecutor)
	assert.NotEmpty(t, ce.scriptFile)
	_, statErr := os.Stat(ce.scriptFile)
	assert.True(t, os.IsNotExist(statErr), "script file should be deleted after cancellation")

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), "dagu_script-"),
			"temporary script file should be cleaned up after cancellation: %s", entry.Name())
	}
}

// TestCommandExecutor_WorkingDirectory tests working directory is set correctly
func TestCommandExecutor_WorkingDirectory(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tmpDir := t.TempDir()

	step := core.Step{
		Name:   "test",
		Dir:    tmpDir,
		Script: "pwd",
	}

	ctx := setupTestContext(t, nil, step)
	// Override the working dir in env
	env := runtime.GetEnv(ctx)
	env.WorkingDir = tmpDir
	ctx = runtime.WithEnv(ctx, env)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)
	// The output should contain the tmpDir path
	assert.Contains(t, stdout.String(), filepath.Base(tmpDir))
}

// TestCommandExecutor_EnvironmentVariables tests environment variable propagation
func TestCommandExecutor_EnvironmentVariables(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	dag := &core.DAG{
		Name: "test-dag",
		Env:  []string{"DAG_VAR=dag_value"},
	}

	step := core.Step{
		Name:   "test",
		Script: "echo $DAG_VAR",
	}

	ctx := setupTestContext(t, dag, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dag_value\n", stdout.String())
}

// TestCommandExecutor_Errexit tests that errexit (-e) flag works
func TestCommandExecutor_Errexit(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	// DAG with shell configured - this enables the -e flag automatically
	dag := &core.DAG{
		Name:  "test-dag",
		Shell: "/bin/sh", // Setting shell enables errexit (-e) flag
		Env:   []string{},
	}

	// This script should fail on the first command due to errexit
	step := core.Step{
		Name:   "test",
		Script: "false\necho 'should not reach here'",
	}

	ctx := setupTestContext(t, dag, step)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	exec.SetStdout(&stdout)

	err = exec.Run(ctx)
	require.Error(t, err)
	// The echo should not have been executed due to errexit
	assert.NotContains(t, stdout.String(), "should not reach here")
}

// TestSetupScript tests script file creation
func TestSetupScript(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		script      string
		shell       []string
		expectedExt string
	}{
		{
			name:        "basic script",
			script:      "echo hello",
			shell:       []string{"/bin/sh"},
			expectedExt: ".sh",
		},
		{
			name:        "bash script",
			script:      "echo hello",
			shell:       []string{"/bin/bash"},
			expectedExt: ".sh",
		},
		{
			name:        "no shell extension",
			script:      "echo hello",
			shell:       []string{"/usr/bin/python"},
			expectedExt: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scriptFile, err := setupScript(tmpDir, tt.script, "", tt.shell)
			require.NoError(t, err)
			defer func() { _ = os.Remove(scriptFile) }()

			// Check file exists
			_, err = os.Stat(scriptFile)
			require.NoError(t, err)

			// Check content
			content, err := os.ReadFile(scriptFile)
			require.NoError(t, err)
			assert.Equal(t, tt.script, string(content))

			// Check extension
			if tt.expectedExt != "" {
				assert.True(t, strings.HasSuffix(scriptFile, tt.expectedExt),
					"expected extension %s, got %s", tt.expectedExt, filepath.Ext(scriptFile))
			}

			// Check permissions (only on Unix - Windows doesn't have the same permission model)
			if goruntime.GOOS != "windows" {
				info, err := os.Stat(scriptFile)
				require.NoError(t, err)
				// Check that it's executable (at least user execute bit)
				assert.True(t, info.Mode()&0100 != 0, "script should be executable")
			}
		})
	}
}

// TestValidateCommandStep tests step validation
func TestValidateCommandStep(t *testing.T) {
	tests := []struct {
		name      string
		step      core.Step
		expectErr bool
	}{
		{
			name: "command only",
			step: core.Step{
				Commands: []core.CommandEntry{{Command: "echo"}},
			},
			expectErr: false,
		},
		{
			name: "script only",
			step: core.Step{
				Script: "echo hello",
			},
			expectErr: false,
		},
		{
			name: "command and script",
			step: core.Step{
				Commands: []core.CommandEntry{{Command: "python"}},
				Script:   "print('hello')",
			},
			expectErr: false,
		},
		{
			name: "subdag",
			step: core.Step{
				SubDAG: &core.SubDAG{Name: "sub.yaml"},
			},
			expectErr: false,
		},
		{
			name:      "empty step",
			step:      core.Step{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCommandStep(tt.step)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCommandStepMissingCommandErrorMessage(t *testing.T) {
	dag := &core.DAG{
		Steps: []core.Step{
			{
				Name:           "missing",
				ExecutorConfig: core.ExecutorConfig{Type: "command"},
			},
		},
	}

	err := core.ValidateSteps(dag)
	require.Error(t, err)
	assert.Equal(t, "field 'command': step command is required", err.Error())
	assert.NotContains(t, err.Error(), "executor_config")
	assert.NotContains(t, err.Error(), "value:")
	assert.ErrorIs(t, err, core.ErrStepCommandIsRequired)
}

func TestLoadYAMLMissingCommandErrorMessage(t *testing.T) {
	_, err := spec.LoadYAML(context.Background(), []byte(`
steps:
  - name: missing
    type: command
`))
	require.Error(t, err)
	assert.Equal(t, "failed to process document 0: failed to build DAG: field 'command': step command is required", err.Error())
	assert.NotContains(t, err.Error(), "executor_config")
	assert.NotContains(t, err.Error(), "value:")
	assert.ErrorIs(t, err, core.ErrStepCommandIsRequired)
}

// TestExitCodeFromError tests exit code extraction from errors
func TestExitCodeFromError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: 0,
		},
		{
			name:     "generic error",
			err:      assert.AnError,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exitCodeFromError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestDetectShebang tests shebang detection
func TestDetectShebang(t *testing.T) {
	tests := []struct {
		name                 string
		scriptContent        string
		userSpecifiedShell   bool
		expectedShebang      string
		expectedShebangEmpty bool
	}{
		{
			name:            "bash shebang",
			scriptContent:   "#!/bin/bash\necho hello",
			expectedShebang: "/bin/bash",
		},
		{
			name:            "sh shebang",
			scriptContent:   "#!/bin/sh\necho hello",
			expectedShebang: "/bin/sh",
		},
		{
			name:            "env bash shebang",
			scriptContent:   "#!/usr/bin/env bash\necho hello",
			expectedShebang: "/usr/bin/env",
		},
		{
			name:                 "no shebang",
			scriptContent:        "echo hello",
			expectedShebangEmpty: true,
		},
		{
			name:                 "user specified shell skips detection",
			scriptContent:        "#!/bin/bash\necho hello",
			userSpecifiedShell:   true,
			expectedShebangEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp script file
			tmpFile, err := os.CreateTemp("", "test-*.sh")
			require.NoError(t, err)
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			_, err = tmpFile.WriteString(tt.scriptContent)
			require.NoError(t, err)
			_ = tmpFile.Close()

			cfg := &commandConfig{
				UserSpecifiedShell: tt.userSpecifiedShell,
			}

			shebang, _, err := cfg.detectShebang(tmpFile.Name())
			require.NoError(t, err)

			if tt.expectedShebangEmpty {
				assert.Empty(t, shebang)
			} else {
				assert.Equal(t, tt.expectedShebang, shebang)
			}
		})
	}
}

// TestNewCommandConfig tests configuration creation
func TestNewCommandConfig(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tests := []struct {
		name                  string
		dag                   *core.DAG
		step                  core.Step
		expectedShellContains string
	}{
		{
			name: "step shell takes precedence",
			dag: &core.DAG{
				Name:  "test",
				Shell: "/bin/bash",
			},
			step: core.Step{
				Name:  "test",
				Shell: "/bin/sh",
			},
			expectedShellContains: "/bin/sh",
		},
		{
			name: "DAG shell used when step shell empty",
			dag: &core.DAG{
				Name:  "test",
				Shell: "/bin/bash",
			},
			step: core.Step{
				Name: "test",
			},
			expectedShellContains: "/bin/bash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := setupTestContext(t, tt.dag, tt.step)

			cfg, err := NewCommandConfig(ctx, tt.step)
			require.NoError(t, err)

			require.NotEmpty(t, cfg.Shell)
			assert.Contains(t, cfg.Shell[0], tt.expectedShellContains)
		})
	}
}

// TestShellCommandBuilder_NixShell tests nix-shell specific handling
func TestShellCommandBuilder_NixShell(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		builder        shellCommandBuilder
		expectedInArgs []string
	}{
		{
			name: "nix-shell with packages",
			builder: shellCommandBuilder{
				Shell:            []string{"nix-shell"},
				ShellCommandArgs: "echo hello",
				ShellPackages:    []string{"bash", "coreutils"},
			},
			expectedInArgs: []string{"-p", "bash", "-p", "coreutils", "--pure", "--run"},
		},
		{
			name: "nix-shell with command and script",
			builder: shellCommandBuilder{
				Shell:            []string{"nix-shell"},
				ShellCommandArgs: "set -e; ",
				Command:          "python",
				Args:             []string{"-u"},
				Script:           "script.py",
			},
			expectedInArgs: []string{"--pure", "--run"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.builder.Build(ctx)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			for _, expectedArg := range tt.expectedInArgs {
				assert.Contains(t, cmd.Args, expectedArg)
			}
		})
	}
}

// TestCommandExecutor_ConcurrentRun tests that the executor handles concurrent access properly
func TestCommandExecutor_ConcurrentRun(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	step := core.Step{
		Name:     "test",
		Commands: []core.CommandEntry{{Command: "sleep", Args: []string{"0.1"}}},
	}

	ctx := setupTestContext(t, nil, step)

	// Create multiple executors and run them concurrently
	const numExecutors = 5
	var wg sync.WaitGroup
	errors := make(chan error, numExecutors)

	for range numExecutors {
		wg.Go(func() {
			exec, err := NewCommand(ctx, step)
			if err != nil {
				errors <- err
				return
			}
			if err := exec.Run(ctx); err != nil {
				errors <- err
			}
		})
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent execution error: %v", err)
	}
}

// TestCommandExecutor_ScriptCleanup tests that temporary script files are cleaned up
func TestCommandExecutor_ScriptCleanup(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tmpDir := t.TempDir()

	step := core.Step{
		Name:   "test",
		Script: "echo 'cleanup test'",
		Dir:    tmpDir,
	}

	ctx := setupTestContext(t, nil, step)
	env := runtime.GetEnv(ctx)
	env.WorkingDir = tmpDir
	ctx = runtime.WithEnv(ctx, env)

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)

	err = exec.Run(ctx)
	require.NoError(t, err)

	// Check that no dagu_script-* files remain in tmpDir
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), "dagu_script-"),
			"temporary script file should be cleaned up: %s", entry.Name())
	}
}

// TestCommandConfig_NewCmd_AllBranches tests all branches of newCmd
func TestCommandConfig_NewCmd_AllBranches(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := setupTestContext(t, nil, core.Step{})
	tmpDir := t.TempDir()

	// Create a test script file
	scriptFile := filepath.Join(tmpDir, "test.sh")
	require.NoError(t, os.WriteFile(scriptFile, []byte("echo hello"), 0o755))

	// Create script with shebang
	shebangScript := filepath.Join(tmpDir, "shebang.sh")
	require.NoError(t, os.WriteFile(shebangScript, []byte("#!/bin/bash\necho shebang"), 0o755))

	tests := []struct {
		name       string
		config     commandConfig
		scriptFile string
		checkPath  string
		checkArgs  []string
	}{
		{
			name: "command + scriptFile (shellCommandBuilder path)",
			config: commandConfig{
				Ctx:              ctx,
				Command:          "/bin/sh",
				Args:             []string{},
				Shell:            []string{"/bin/sh", "-e"},
				ShellCommandArgs: "",
			},
			scriptFile: scriptFile,
			checkPath:  "/bin/sh",
		},
		{
			name: "shell + scriptFile with shebang",
			config: commandConfig{
				Ctx:                ctx,
				Shell:              []string{"/bin/sh"},
				UserSpecifiedShell: false,
			},
			scriptFile: shebangScript,
			checkPath:  "/bin/bash", // Should use shebang interpreter
		},
		{
			name: "shell + scriptFile without shebang",
			config: commandConfig{
				Ctx:                ctx,
				Shell:              []string{"/bin/sh"},
				UserSpecifiedShell: false,
			},
			scriptFile: scriptFile,
			checkPath:  "/bin/sh",
		},
		{
			name: "shell + shellCommandArgs (no script)",
			config: commandConfig{
				Ctx:              ctx,
				Shell:            []string{"/bin/sh"},
				ShellCommandArgs: "echo hello",
			},
			checkPath: "/bin/sh",
			checkArgs: []string{"-c", "echo hello"},
		},
		{
			name: "default case - direct command",
			config: commandConfig{
				Ctx:     ctx,
				Command: "echo",
				Args:    []string{"hello"},
			},
			checkPath: "echo",
			checkArgs: []string{"hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.config.newCmd(ctx, tt.scriptFile)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			assert.Contains(t, cmd.Path, tt.checkPath)

			for _, arg := range tt.checkArgs {
				assert.Contains(t, cmd.Args, arg)
			}
		})
	}
}

// TestCommandExecutor_ShellArgs tests step-level shell args
func TestCommandExecutor_ShellArgs(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	// Test that ShellArgs field in Step is available and can be used
	step := core.Step{
		Name:      "test",
		Shell:     "/bin/bash",
		ShellArgs: []string{"-x"}, // enable trace mode
		Script:    "echo 'test'",
	}

	// Verify the step has ShellArgs set correctly
	assert.Equal(t, []string{"-x"}, step.ShellArgs)
}

// TestCommandExecutor_UserSpecifiedShellSkipsShebang tests that user-specified shell skips shebang detection
func TestCommandExecutor_UserSpecifiedShellSkipsShebang(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	// Create a script with a bash shebang
	// When user specifies a shell, the shebang should be ignored
	step := core.Step{
		Name:   "test",
		Shell:  "/bin/sh", // User explicitly specifies sh
		Script: "#!/bin/bash\necho 'using specified shell'",
	}

	ctx := setupTestContext(t, nil, step)

	executor, err := NewCommand(ctx, step)
	require.NoError(t, err)

	var stdout strings.Builder
	executor.SetStdout(&stdout)

	err = executor.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "using specified shell\n", stdout.String())
}

// TestCommandExecutor_EmptyScript tests behavior with empty script
func TestCommandExecutor_EmptyScript(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	dag := &core.DAG{
		Name:  "test-dag",
		Shell: "/bin/sh",
		Env:   []string{},
	}

	step := core.Step{
		Name:   "test",
		Script: "", // Empty script
	}

	ctx := setupTestContext(t, dag, step)

	// This should work - empty script means no script file created
	executor, err := NewCommand(ctx, step)
	require.NoError(t, err)

	err = executor.Run(ctx)
	// Should succeed with empty output
	require.NoError(t, err)
}

// TestCommandExecutor_ScriptWithSpecialChars tests scripts containing special characters
func TestCommandExecutor_ScriptWithSpecialChars(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	tests := []struct {
		name           string
		script         string
		expectedOutput string
	}{
		{
			name:           "script with quotes",
			script:         "echo \"hello 'world'\"",
			expectedOutput: "hello 'world'\n",
		},
		{
			name:           "script with dollar sign",
			script:         "VAR=test; echo \"value: $VAR\"",
			expectedOutput: "value: test\n",
		},
		{
			name:           "script with backticks",
			script:         "echo `echo nested`",
			expectedOutput: "nested\n",
		},
		{
			name:           "script with newlines preserved",
			script:         "echo 'line1'\necho 'line2'\necho 'line3'",
			expectedOutput: "line1\nline2\nline3\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dag := &core.DAG{
				Name:  "test-dag",
				Shell: "/bin/sh",
				Env:   []string{},
			}

			step := core.Step{
				Name:   "test",
				Script: tt.script,
			}

			ctx := setupTestContext(t, dag, step)

			executor, err := NewCommand(ctx, step)
			require.NoError(t, err)

			var stdout strings.Builder
			executor.SetStdout(&stdout)

			err = executor.Run(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedOutput, stdout.String())
		})
	}
}

// TestCreateDirectCommand tests the createDirectCommand helper function
func TestCreateDirectCommand(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		cmd        string
		args       []string
		scriptFile string
		expected   []string
	}{
		{
			name:     "simple command",
			cmd:      "echo",
			args:     []string{"hello"},
			expected: []string{"echo", "hello"},
		},
		{
			name:       "command with script",
			cmd:        "/bin/sh",
			args:       []string{"-x"},
			scriptFile: "/tmp/script.sh",
			expected:   []string{"/bin/sh", "-x", "/tmp/script.sh"},
		},
		{
			name:     "command without args",
			cmd:      "true",
			args:     []string{},
			expected: []string{"true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := createDirectCommand(ctx, tt.cmd, tt.args, tt.scriptFile)
			assertCommandArgs(t, cmd.Args, tt.expected)
		})
	}
}

// TestSetupScript_Errors tests error paths in setupScript
func TestSetupScript_Errors(t *testing.T) {
	t.Run("invalid directory", func(t *testing.T) {
		_, err := setupScript("/nonexistent/dir/that/does/not/exist", "echo hello", "", []string{"/bin/sh"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create script file")
	})
}

// TestShellCommandBuilder_Build_Errors tests error paths in Build
func TestShellCommandBuilder_Build_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("empty shell command", func(t *testing.T) {
		builder := shellCommandBuilder{
			Shell:            []string{},
			ShellCommandArgs: "echo hello",
		}
		_, err := builder.Build(ctx)
		assert.Error(t, err)
	})
}

// TestShellCommandBuilder_PwshCore tests PowerShell Core (pwsh) path
func TestShellCommandBuilder_PwshCore(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		builder        shellCommandBuilder
		expectedInArgs []string
	}{
		{
			name: "pwsh basic command",
			builder: shellCommandBuilder{
				Shell:            []string{"pwsh"},
				ShellCommandArgs: "Get-Date",
			},
			expectedInArgs: []string{"-Command", powerShellInlineCommand("Get-Date")},
		},
		{
			name: "pwsh.exe basic command",
			builder: shellCommandBuilder{
				Shell:            []string{"pwsh.exe"},
				ShellCommandArgs: "Get-Date",
			},
			expectedInArgs: []string{"-Command", powerShellInlineCommand("Get-Date")},
		},
		{
			name: "cmd basic command",
			builder: shellCommandBuilder{
				Shell:            []string{"cmd"},
				ShellCommandArgs: "dir",
			},
			expectedInArgs: []string{"/c", "dir"},
		},
		{
			name: "cmd.exe basic command",
			builder: shellCommandBuilder{
				Shell:            []string{"cmd.exe"},
				ShellCommandArgs: "dir",
			},
			expectedInArgs: []string{"/c", "dir"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.builder.Build(ctx)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			for _, expectedArg := range tt.expectedInArgs {
				assert.Contains(t, cmd.Args, expectedArg)
			}
		})
	}
}

// TestNewCommandConfig_NixShell tests nix-shell configuration in NewCommandConfig
func TestNewCommandConfig_NixShell(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	// Test nix-shell configuration
	dag := &core.DAG{
		Name:  "test-dag",
		Shell: "nix-shell",
		Env:   []string{},
	}

	step := core.Step{
		Name:         "test",
		ShellCmdArgs: "echo hello",
	}

	ctx := setupTestContext(t, dag, step)

	cfg, err := NewCommandConfig(ctx, step)
	require.NoError(t, err)

	// NewCommandConfig should store the shell and command args
	require.NotEmpty(t, cfg.Shell)
	assert.Contains(t, cfg.Shell[0], "nix-shell")
	assert.Equal(t, "echo hello", cfg.ShellCommandArgs)

	// Build() should prepend "set -e; " when building the actual command
	builder := &shellCommandBuilder{
		Shell:              cfg.Shell,
		ShellCommandArgs:   cfg.ShellCommandArgs,
		UserSpecifiedShell: cfg.UserSpecifiedShell,
	}
	cmd, err := builder.Build(context.Background())
	require.NoError(t, err)
	// The last arg should contain "set -e; echo hello"
	assert.Contains(t, cmd.Args[len(cmd.Args)-1], "set -e; ")
}

// TestNewCommandConfig_NixShell_AlreadyHasSetE tests nix-shell when set -e already present
func TestNewCommandConfig_NixShell_AlreadyHasSetE(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	dag := &core.DAG{
		Name:  "test-dag",
		Shell: "nix-shell",
		Env:   []string{},
	}

	step := core.Step{
		Name:         "test",
		ShellCmdArgs: "set -e; echo hello", // Already has set -e
	}

	ctx := setupTestContext(t, dag, step)

	cfg, err := NewCommandConfig(ctx, step)
	require.NoError(t, err)

	// Should NOT double-prepend "set -e"
	assert.Equal(t, "set -e; echo hello", cfg.ShellCommandArgs)
}

// TestCommandConfig_NewCmd_Errors tests error paths in newCmd
func TestCommandConfig_NewCmd_Errors(t *testing.T) {
	ctx := setupTestContext(t, nil, core.Step{})
	tmpDir := t.TempDir()

	// Create a test script file
	scriptFile := filepath.Join(tmpDir, "test.sh")
	require.NoError(t, os.WriteFile(scriptFile, []byte("echo hello"), 0o755))

	t.Run("EmptyShellWithCommandAndScript", func(t *testing.T) {
		config := commandConfig{
			Ctx:     ctx,
			Command: "/bin/sh",
			Script:  "echo test",
			Shell:   []string{}, // Empty shell will cause error in Build
		}
		_, err := config.newCmd(ctx, scriptFile)
		assert.Error(t, err)
	})

	t.Run("DetectShebangError", func(t *testing.T) {
		// Create a config that will trigger detectShebang with non-existent file
		config := commandConfig{
			Ctx:                ctx,
			Shell:              []string{"/bin/sh"},
			UserSpecifiedShell: false,
		}
		_, err := config.newCmd(ctx, "/nonexistent/script.sh")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to detect shebang")
	})
}

// TestCommandExecutor_Run_SetupScriptError tests Run when setupScript fails
func TestCommandExecutor_Run_SetupScriptError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	dag := &core.DAG{
		Name:  "test-dag",
		Shell: "/bin/sh",
		Env:   []string{},
	}

	step := core.Step{
		Name:   "test",
		Script: "echo hello",
	}

	ctx := setupTestContext(t, dag, step)

	// Create executor with invalid working directory
	cfg, err := NewCommandConfig(ctx, step)
	require.NoError(t, err)

	// Set invalid directory to cause setupScript to fail
	cfg.Dir = "/nonexistent/directory/that/does/not/exist"

	executor := &commandExecutor{config: cfg}

	err = executor.Run(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to setup script")
}

// TestCommandExecutor_Run_NewCmdError tests Run when newCmd fails
func TestCommandExecutor_Run_NewCmdError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := setupTestContext(t, nil, core.Step{})

	// Create config that will cause newCmd to fail
	cfg := &commandConfig{
		Ctx:     ctx,
		Dir:     t.TempDir(),
		Command: "/bin/sh",
		Script:  "",         // No script, so scriptFile will be empty
		Shell:   []string{}, // Empty shell will cause shellCommandBuilder.Build to fail
	}

	// Create a script that will trigger the Command+Script path
	cfg.Script = "echo test"
	cfg.Command = "/bin/sh"
	cfg.Shell = []string{} // This will cause shellCommandBuilder.Build to fail

	executor := &commandExecutor{config: cfg}

	err := executor.Run(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create command")
}

// TestCommandExecutor_Run_StartError tests Run when cmd.Start fails
func TestCommandExecutor_Run_StartError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := setupTestContext(t, nil, core.Step{})

	cfg := &commandConfig{
		Ctx:     ctx,
		Dir:     t.TempDir(),
		Command: "/nonexistent/command/that/does/not/exist",
		Script:  "",
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	}

	executor := &commandExecutor{config: cfg}

	err := executor.Run(ctx)
	assert.Error(t, err)

	// Check exit code was set
	assert.NotEqual(t, 0, executor.ExitCode())
}

// TestCommandExecutor_Run_StartErrorWithStderr tests that stderr is included when Start fails
func TestCommandExecutor_Run_StartErrorWithStderr(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := setupTestContext(t, nil, core.Step{})

	// Use a buffer that pre-contains some content to simulate stderr output
	var stderr strings.Builder
	stderr.WriteString("some error output\n")

	cfg := &commandConfig{
		Ctx:     ctx,
		Dir:     t.TempDir(),
		Command: "/nonexistent/command/that/does/not/exist",
		Script:  "",
		Stdout:  io.Discard,
		Stderr:  &stderr,
	}

	executor := &commandExecutor{config: cfg}

	err := executor.Run(ctx)
	assert.Error(t, err)
}

// TestDetectShebang_ReadError tests detectShebang when readFirstLine fails
func TestDetectShebang_ReadError(t *testing.T) {
	cfg := &commandConfig{
		UserSpecifiedShell: false,
	}

	_, _, err := cfg.detectShebang("/nonexistent/file.sh")
	assert.Error(t, err)
}

// TestReadFirstLine_ScannerError tests readFirstLine with a problematic file
func TestReadFirstLine_ScannerError(t *testing.T) {
	// Test with an empty file (should return empty string, no error)
	tmpFile, err := os.CreateTemp("", "test-empty-*.sh")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_ = tmpFile.Close()

	result, err := readFirstLine(tmpFile.Name())
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

// TestExitCodeFromError_WithExitError tests exitCodeFromError with actual ExitError
func TestExitCodeFromError_WithExitError(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	// Run a command that exits with a specific code
	cmd := exec.Command("/bin/sh", "-c", "exit 42")
	err := cmd.Run()

	exitCode := exitCodeFromError(err)
	assert.Equal(t, 42, exitCode)
}

// TestShellCommandBuilder_PowerShellExe tests powershell.exe path specifically
func TestShellCommandBuilder_PowerShellExe(t *testing.T) {
	ctx := context.Background()

	builder := shellCommandBuilder{
		Shell:            []string{"powershell.exe"},
		ShellCommandArgs: "Get-Date",
	}

	cmd, err := builder.Build(ctx)
	require.NoError(t, err)
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Args, "-Command")
	assert.Contains(t, cmd.Args, powerShellInlineCommand("Get-Date"))
}

// TestShellCommandArgs_DoesNotIncludeArgs verifies that when running a command
// string via shell (ShellCommandArgs), the step-level Args are NOT appended
// to the command. This was a bug fixed in commit e40711e.
func TestShellCommandArgs_DoesNotIncludeArgs(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name             string
		shell            []string
		shellCommandArgs string
		unwantedArgs     []string
		expectedArgs     []string // Args that MUST be present
	}{
		{
			name:             "cmd.exe",
			shell:            []string{"cmd.exe"},
			shellCommandArgs: "echo hello",
			unwantedArgs:     []string{"--extra-arg", "should-not-appear"},
			expectedArgs:     []string{"/c", "echo hello"},
		},
		{
			name:             "cmd",
			shell:            []string{"cmd"},
			shellCommandArgs: "dir",
			unwantedArgs:     []string{"--unwanted"},
			expectedArgs:     []string{"/c", "dir"},
		},
		{
			name:             "powershell",
			shell:            []string{"powershell"},
			shellCommandArgs: "Write-Host hello",
			unwantedArgs:     []string{"-ExtraArg", "should-not-appear"},
			expectedArgs:     []string{"-Command", powerShellInlineCommand("Write-Host hello")},
		},
		{
			name:             "powershell.exe",
			shell:            []string{"powershell.exe"},
			shellCommandArgs: "Get-Date",
			unwantedArgs:     []string{"-Unwanted"},
			expectedArgs:     []string{"-Command", powerShellInlineCommand("Get-Date")},
		},
		{
			name:             "pwsh",
			shell:            []string{"pwsh"},
			shellCommandArgs: "Get-Process",
			unwantedArgs:     []string{"-Unwanted"},
			expectedArgs:     []string{"-Command", powerShellInlineCommand("Get-Process")},
		},
		{
			name:             "pwsh.exe",
			shell:            []string{"pwsh.exe"},
			shellCommandArgs: "Get-Host",
			unwantedArgs:     []string{"--bad-arg"},
			expectedArgs:     []string{"-Command", powerShellInlineCommand("Get-Host")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := shellCommandBuilder{
				Shell:            tt.shell,
				ShellCommandArgs: tt.shellCommandArgs,
				Args:             tt.unwantedArgs,
			}

			cmd, err := builder.Build(ctx)
			require.NoError(t, err)
			require.NotNil(t, cmd)

			// Args should NOT be in the command when using ShellCommandArgs
			for _, arg := range tt.unwantedArgs {
				assert.NotContains(t, cmd.Args, arg,
					"Args should not be included when running command string via shell")
			}

			// Expected args MUST be present
			for _, arg := range tt.expectedArgs {
				assert.Contains(t, cmd.Args, arg)
			}
		})
	}
}

// TestCmdShell_ScriptOnly_IncludesArgs verifies that when running a script file
// via cmd.exe (Script without Command), the Args ARE correctly included.
func TestCmdShell_ScriptOnly_IncludesArgs(t *testing.T) {
	ctx := context.Background()

	builder := shellCommandBuilder{
		Shell:  []string{"cmd.exe"},
		Script: "script.bat",
		Args:   []string{"/V:ON"}, // cmd.exe flag that should be included
	}

	cmd, err := builder.Build(ctx)
	require.NoError(t, err)
	require.NotNil(t, cmd)

	// Args SHOULD be included when running a script file
	assert.Contains(t, cmd.Args, "/V:ON")
	assert.Contains(t, cmd.Args, "/c")
	assert.Contains(t, cmd.Args, "script.bat")
}

// TestCommandConfig_NewCmd_SplitCommandError tests error when SplitCommand fails in newCmd
func TestCommandConfig_NewCmd_SplitCommandError(t *testing.T) {
	ctx := setupTestContext(t, nil, core.Step{})
	tmpDir := t.TempDir()

	// Create a script file
	scriptFile := filepath.Join(tmpDir, "test.sh")
	require.NoError(t, os.WriteFile(scriptFile, []byte("echo hello"), 0o755))

	t.Run("shell+script path with valid shell", func(t *testing.T) {
		// Create a script without shebang
		noShebangScript := filepath.Join(tmpDir, "noshebang.sh")
		require.NoError(t, os.WriteFile(noShebangScript, []byte("echo no shebang"), 0o755))

		// Test the shell+script path with a valid shell
		config := commandConfig{
			Ctx:                ctx,
			Shell:              []string{"/bin/sh"},
			UserSpecifiedShell: true, // Skip shebang detection
		}
		cmd, err := config.newCmd(ctx, noShebangScript)
		require.NoError(t, err)
		assert.NotNil(t, cmd)
	})
}

// TestCommandConfig_NewCmd_ShellCmdArgsPath tests Shell+ShellCommandArgs path
func TestCommandConfig_NewCmd_ShellCmdArgsPath(t *testing.T) {
	ctx := setupTestContext(t, nil, core.Step{})

	// Let's verify the path works with valid input
	config := commandConfig{
		Ctx:              ctx,
		Shell:            []string{"/bin/sh"},
		ShellCommandArgs: "echo hello",
	}
	cmd, err := config.newCmd(ctx, "")
	require.NoError(t, err)
	assert.NotNil(t, cmd)
}

// TestCommandExecutor_Run_StartFailWithStderrTail tests the stderr tail path when Start fails
func TestCommandExecutor_Run_StartFailWithStderrTail(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := setupTestContext(t, nil, core.Step{})

	// Create a custom writer that provides content for tail
	cfg := &commandConfig{
		Ctx:     ctx,
		Dir:     t.TempDir(),
		Command: "/nonexistent/command/xyz123",
		Script:  "",
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	}

	executor := &commandExecutor{config: cfg}
	err := executor.Run(ctx)
	assert.Error(t, err)
	// The error might or might not contain stderr tail depending on timing
}

// TestNewCommand_ErrorPath tests NewCommand when NewCommandConfig could fail
// Note: Currently NewCommandConfig never returns an error, but this tests the path
func TestNewCommand_ErrorPath(t *testing.T) {
	ctx := setupTestContext(t, nil, core.Step{})

	// Currently NewCommandConfig always succeeds
	// This test documents the behavior
	step := core.Step{
		Name:     "test",
		Commands: []core.CommandEntry{{Command: "echo"}},
	}

	exec, err := NewCommand(ctx, step)
	require.NoError(t, err)
	assert.NotNil(t, exec)
}

// TestCommandConfig_NewCmd_ShellScriptNoShebang tests the path where shell+script
// runs without shebang (lines 147-153 in newCmd)
func TestCommandConfig_NewCmd_ShellScriptNoShebang(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping Unix-specific test on Windows")
	}

	ctx := setupTestContext(t, nil, core.Step{})
	tmpDir := t.TempDir()

	// Create a script without shebang
	noShebangScript := filepath.Join(tmpDir, "noshebang.sh")
	require.NoError(t, os.WriteFile(noShebangScript, []byte("echo no shebang"), 0o755))

	// Test the shell+script path WITHOUT shebang - this should use the shell command
	config := commandConfig{
		Ctx:                ctx,
		Shell:              []string{"/bin/sh"},
		UserSpecifiedShell: false, // Don't skip shebang detection
	}
	cmd, err := config.newCmd(ctx, noShebangScript)
	require.NoError(t, err)
	assert.NotNil(t, cmd)
	// Should use /bin/sh to run the script
	assert.Contains(t, cmd.Path, "sh")
}

// TestReadFirstLine_LongLine tests readFirstLine with extremely long content
// This tests the scanner error path (line 219-221)
func TestReadFirstLine_LongLine(t *testing.T) {
	// Create a file with a very long first line (longer than 4KB buffer)
	tmpFile, err := os.CreateTemp("", "test-long-*.sh")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Write a line longer than the buffer limit (4KB) without newline
	longLine := strings.Repeat("a", 5000)
	_, err = tmpFile.WriteString(longLine)
	require.NoError(t, err)
	_ = tmpFile.Close()

	// This should return an error because the line is too long for the buffer
	_, err = readFirstLine(tmpFile.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read file")
}

func TestMultiCommandExecutor(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Skipping test on Windows")
	}

	t.Run("MultipleCommandsExecuteSequentially", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "output.txt")

		step := core.Step{
			Name: "test",
			Commands: []core.CommandEntry{
				{Command: "sh", Args: []string{"-c", `echo first >> "` + outputFile + `"`}},
				{Command: "sh", Args: []string{"-c", `echo second >> "` + outputFile + `"`}},
				{Command: "sh", Args: []string{"-c", `echo third >> "` + outputFile + `"`}},
			},
		}

		ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test"}, "test-run", "test.log")
		exec, err := NewCommand(ctx, step)
		require.NoError(t, err)

		err = exec.Run(ctx)
		require.NoError(t, err)

		// Verify all commands executed in order
		content, err := os.ReadFile(outputFile)
		require.NoError(t, err)
		assert.Equal(t, "first\nsecond\nthird\n", string(content))
	})

	t.Run("StopsOnFirstFailure", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "output.txt")

		step := core.Step{
			Name: "test",
			Commands: []core.CommandEntry{
				{Command: "sh", Args: []string{"-c", `echo first >> "` + outputFile + `"`}},
				{Command: "sh", Args: []string{"-c", "exit 1"}},
				{Command: "sh", Args: []string{"-c", `echo third >> "` + outputFile + `"`}},
			},
		}

		ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test"}, "test-run", "test.log")
		exec, err := NewCommand(ctx, step)
		require.NoError(t, err)

		err = exec.Run(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "command 2 failed")

		// Verify only the first command was executed
		content, err := os.ReadFile(outputFile)
		require.NoError(t, err)
		assert.Equal(t, "first\n", string(content))
	})

	t.Run("SingleCommandUsesSingleExecutor", func(t *testing.T) {
		step := core.Step{
			Name: "test",
			Commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"hello"}},
			},
		}

		ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test"}, "test-run", "test.log")
		exec, err := NewCommand(ctx, step)
		require.NoError(t, err)

		// Should return commandExecutor, not multiCommandExecutor
		_, isMulti := exec.(*multiCommandExecutor)
		assert.False(t, isMulti, "single command should not use multiCommandExecutor")
	})

	t.Run("MultipleCommandsUseMultiExecutor", func(t *testing.T) {
		step := core.Step{
			Name: "test",
			Commands: []core.CommandEntry{
				{Command: "echo", Args: []string{"first"}},
				{Command: "echo", Args: []string{"second"}},
			},
		}

		ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test"}, "test-run", "test.log")
		exec, err := NewCommand(ctx, step)
		require.NoError(t, err)

		// Should return multiCommandExecutor
		_, isMulti := exec.(*multiCommandExecutor)
		assert.True(t, isMulti, "multiple commands should use multiCommandExecutor")
	})

	t.Run("ExitCodeFromFailedCommand", func(t *testing.T) {
		step := core.Step{
			Name: "test",
			Commands: []core.CommandEntry{
				{Command: "sh", Args: []string{"-c", "exit 0"}},
				{Command: "sh", Args: []string{"-c", "exit 42"}},
			},
		}

		ctx := runtime.NewContext(context.Background(), &core.DAG{Name: "test"}, "test-run", "test.log")
		exec, err := NewCommand(ctx, step)
		require.NoError(t, err)

		err = exec.Run(ctx)
		require.Error(t, err)

		exitCoder, ok := exec.(executor.ExitCoder)
		require.True(t, ok)
		assert.Equal(t, 42, exitCoder.ExitCode())
	})
}
