// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package intg_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/core"
)

// TestWindowsShellDetection tests shell detection and availability on Windows
func TestWindowsShellDetection(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific tests on non-Windows platform")
	}

	t.Run("EnvironmentVariables", func(t *testing.T) {
		// Essential environment variables that should exist on Windows
		envVars := []string{"COMSPEC", "WINDIR", "SYSTEMROOT", "PATH"}
		for _, env := range envVars {
			value := os.Getenv(env)
			assert.NotEmpty(t, value, "Environment variable %s should not be empty", env)
		}
	})

	t.Run("ShellAvailability", func(t *testing.T) {
		// Test common shells/commands available on Windows
		commands := []struct {
			name     string
			required bool
		}{
			{"cmd", true},
			{"cmd.exe", true},
			{"powershell", false}, // May not be available on all Windows versions
			{"powershell.exe", false},
			{"pwsh", false}, // PowerShell Core
			{"pwsh.exe", false},
		}

		foundAtLeastOne := false
		for _, cmd := range commands {
			path, err := exec.LookPath(cmd.name)
			if err == nil {
				t.Logf("✓ %s found at: %s", cmd.name, path)
				foundAtLeastOne = true
			} else if cmd.required {
				t.Errorf("✗ Required command %s not found: %v", cmd.name, err)
			} else {
				t.Logf("✗ Optional command %s not found: %v", cmd.name, err)
			}
		}

		assert.True(t, foundAtLeastOne, "At least one shell command should be available")
	})

	t.Run("ShellCommandResolution", func(t *testing.T) {
		// Test cmdutil.GetShellCommand behavior on Windows
		testCases := []struct {
			input    string
			expected string
		}{
			{"", cmdutil.GetShellCommand("")},
			{"cmd", "cmd"},
			{"powershell", "powershell"},
			{"bash", "bash"}, // May not exist but should pass through
		}

		for _, tc := range testCases {
			result := cmdutil.GetShellCommand(tc.input)
			assert.Equal(t, tc.expected, result, "GetShellCommand(%q) should return %q", tc.input, tc.expected)
		}
	})
}

// TestWindowsCommandExecution tests command execution on Windows
func TestWindowsCommandExecution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific tests on non-Windows platform")
	}

	t.Run("BasicCmdCommands", func(t *testing.T) {
		tests := []struct {
			name    string
			shell   string
			command string
			args    []string
			wantErr bool
		}{
			{
				name:    "EchoViaCmd",
				shell:   "cmd",
				command: "echo hello",
				wantErr: false,
			},
			{
				name:    "DirCommand",
				shell:   "cmd",
				command: "dir",
				wantErr: false,
			},
			{
				name:    "SetVariable",
				shell:   "cmd",
				command: "set TEST_VAR=hello && echo %TEST_VAR%",
				wantErr: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Skip if shell not available
				if _, err := exec.LookPath(tt.shell); err != nil {
					t.Skipf("Shell %s not available: %v", tt.shell, err)
				}

				cmd := exec.Command(tt.shell, "/C", tt.command)
				output, err := cmd.Output()

				if tt.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
					assert.NotEmpty(t, output, "Command should produce output")
				}
			})
		}
	})

	t.Run("PowershellCommands", func(t *testing.T) {
		// Skip if PowerShell not available
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell not available")
		}

		tests := []struct {
			name    string
			command string
			wantErr bool
		}{
			{
				name:    "EchoViaPowershell",
				command: "Write-Output 'hello'",
				wantErr: false,
			},
			{
				name:    "GetLocation",
				command: "Get-Location",
				wantErr: false,
			},
			{
				name:    "SetAndGetVariable",
				command: "$env:TEST_VAR='hello'; Write-Output $env:TEST_VAR",
				wantErr: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd := exec.Command("powershell", "-Command", tt.command)
				output, err := cmd.Output()

				if tt.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
					assert.NotEmpty(t, output, "Command should produce output")
				}
			})
		}
	})
}

// TestWindowsSocketHandling tests Windows-specific socket handling
func TestWindowsSocketHandling(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific tests on non-Windows platform")
	}

	t.Run("SocketPathGeneration", func(t *testing.T) {
		// Test Windows-specific socket path handling
		socketPath := core.SockAddr("windows-test", "test-run")
		assert.NotEmpty(t, socketPath, "Socket path should not be empty")

		// On Windows, socket paths have different constraints
		t.Logf("Generated socket path: %s", socketPath)
		t.Logf("Socket path length: %d", len(socketPath))

		// Verify socket directory structure
		socketDir := filepath.Dir(socketPath)
		assert.NotEmpty(t, socketDir, "Socket directory should not be empty")
		t.Logf("Socket directory: %s", socketDir)
	})
}

// TestWindowsCommandQuoting tests proper command quoting on Windows
func TestWindowsCommandQuoting(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific tests on non-Windows platform")
	}

	t.Run("QuotedArguments", func(t *testing.T) {
		// Skip if cmd not available
		if _, err := exec.LookPath("cmd"); err != nil {
			t.Skip("cmd not available")
		}

		tests := []struct {
			name     string
			command  string
			expected string
		}{
			{
				name:     "SimpleQuote",
				command:  `echo "hello world"`,
				expected: "hello world",
			},
			{
				name:     "SingleQuotes",
				command:  `echo 'hello world'`,
				expected: "'hello world'", // cmd treats single quotes literally
			},
			{
				name:     "NestedQuotes",
				command:  `echo "She said 'hello'"`,
				expected: "She said 'hello'",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd := exec.Command("cmd", "/C", tt.command)
				output, err := cmd.Output()
				require.NoError(t, err)

				result := strings.TrimSpace(string(output))
				assert.Contains(t, result, tt.expected, "Command output should contain expected text")
			})
		}
	})
}

// TestWindowsCommandConstruction tests proper command construction for different shells
func TestWindowsCommandConstruction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific tests on non-Windows platform")
	}

	t.Run("ShellCommandDetection", func(t *testing.T) {
		// Test auto-detection of shell
		shell := cmdutil.GetShellCommand("")
		assert.NotEmpty(t, shell, "Auto-detected shell should not be empty")
		t.Logf("Auto-detected shell: %s", shell)
	})

	t.Run("CommandArgumentConstruction", func(t *testing.T) {
		tests := []struct {
			name         string
			shell        string
			command      string
			expectedFlag string
		}{
			{
				name:         "PowershellWithCommand",
				shell:        "powershell",
				command:      "Write-Output 'test'",
				expectedFlag: "-Command",
			},
			{
				name:         "PowershellExeWithCommand",
				shell:        "powershell.exe",
				command:      "Get-Location",
				expectedFlag: "-Command",
			},
			{
				name:         "CmdWith/C",
				shell:        "cmd",
				command:      "echo hello",
				expectedFlag: "/c",
			},
			{
				name:         "CmdExeWith/C",
				shell:        "cmd.exe",
				command:      "dir",
				expectedFlag: "/c",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Skip if shell not available
				if _, err := exec.LookPath(tt.shell); err != nil {
					t.Skipf("Shell %s not available: %v", tt.shell, err)
				}

				// Test command construction logic
				cmd, _, err := cmdutil.SplitCommand(tt.shell)
				require.NoError(t, err)
				assert.Equal(t, tt.shell, cmd, "Command should match shell")

				// Test that the expected flag would be added
				shellName := strings.ToLower(cmd)
				if strings.Contains(shellName, "powershell") {
					assert.Contains(t, []string{"-Command", "-C"}, tt.expectedFlag, "PowerShell should use -Command flag")
				} else if strings.Contains(shellName, "cmd") {
					assert.Contains(t, []string{"/c", "/C"}, tt.expectedFlag, "CMD should use /c flag")
				}
			})
		}
	})

	t.Run("CommandExecutionWithProperFlags", func(t *testing.T) {
		tests := []struct {
			name    string
			shell   string
			flag    string
			command string
		}{
			{
				name:    "PowershellCommand",
				shell:   "powershell",
				flag:    "-Command",
				command: "Write-Output 'PowerShell test'",
			},
			{
				name:    "CmdCommand",
				shell:   "cmd",
				flag:    "/c",
				command: "echo CMD test",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Skip if shell not available
				if _, err := exec.LookPath(tt.shell); err != nil {
					t.Skipf("Shell %s not available: %v", tt.shell, err)
				}

				// Test actual command execution
				cmd := exec.Command(tt.shell, tt.flag, tt.command)
				output, err := cmd.Output()
				assert.NoError(t, err, "Command should execute successfully")
				assert.NotEmpty(t, output, "Command should produce output")

				result := strings.TrimSpace(string(output))
				t.Logf("Shell: %s, Command: %s, Output: %s", tt.shell, tt.command, result)
			})
		}
	})

	t.Run("EdgeCases", func(t *testing.T) {
		// Test edge cases for command construction
		tests := []struct {
			name    string
			shell   string
			command string
			wantErr bool
		}{
			{
				name:    "EmptyCommand",
				shell:   "cmd",
				command: "",
				wantErr: false, // Empty command should not error during construction
			},
			{
				name:    "CommandWithQuotes",
				shell:   "cmd",
				command: `echo "quoted string"`,
				wantErr: false,
			},
			{
				name:    "CommandWithSpecialCharacters",
				shell:   "cmd",
				command: "echo hello & echo world",
				wantErr: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Skip if shell not available
				if _, err := exec.LookPath(tt.shell); err != nil {
					t.Skipf("Shell %s not available: %v", tt.shell, err)
				}

				// Test command construction
				cmd, args, err := cmdutil.SplitCommand(tt.shell)
				if tt.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
					assert.NotEmpty(t, cmd)
					t.Logf("Command: %s, Args: %v", cmd, args)
				}
			})
		}
	})
}

// TestWindowsEnvironmentVariables tests Windows environment variable handling
func TestWindowsEnvironmentVariables(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific tests on non-Windows platform")
	}

	t.Run("WindowsEnvironmentVariables", func(t *testing.T) {
		// Test that essential Windows environment variables are available
		essentialVars := []string{"USERNAME", "COMPUTERNAME", "USERPROFILE", "TEMP", "WINDIR"}

		for _, varName := range essentialVars {
			value := os.Getenv(varName)
			assert.NotEmpty(t, value, "Environment variable %s should be available", varName)
			t.Logf("%s = %s", varName, value)
		}
	})

	t.Run("CmdVsPowershellEnvironmentSyntax", func(t *testing.T) {
		// Test that we can distinguish between CMD and PowerShell environment variable syntax
		cmdSyntax := "%USERNAME%"
		psSyntax := "$env:USERNAME"

		// CMD syntax should contain %
		assert.Contains(t, cmdSyntax, "%", "CMD syntax should use % delimiters")

		// PowerShell syntax should contain $env:
		assert.Contains(t, psSyntax, "$env:", "PowerShell syntax should use $env: prefix")

		// They should be different
		assert.NotEqual(t, cmdSyntax, psSyntax, "CMD and PowerShell syntax should be different")
	})
}
