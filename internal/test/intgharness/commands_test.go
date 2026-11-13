// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommandsSleepUsesShellSyntax(t *testing.T) {
	require.Equal(t, "sleep 1.5", commandsForShell(posixShell).Sleep(1500*time.Millisecond))
	require.Equal(t, "Start-Sleep -Milliseconds 1500", commandsForShell(powerShell).Sleep(1500*time.Millisecond))
}

func TestPortableCommandsReturnsPlatformCommands(t *testing.T) {
	require.NotEmpty(t, PortableCommands().Sleep(time.Millisecond))
}

func TestCommandsSleepClampsNonPositiveDurations(t *testing.T) {
	require.Equal(t, "sleep 0.001", commandsForShell(posixShell).Sleep(0))
	require.Equal(t, "Start-Sleep -Milliseconds 1", commandsForShell(powerShell).Sleep(-time.Second))
}

func TestCommandsWriteFileUsesShellSyntax(t *testing.T) {
	require.Equal(t, "printf '%s' 'started' > '/tmp/marker'", commandsForShell(posixShell).WriteFile("/tmp/marker", "started"))
	require.Equal(t, "Set-Content -Path 'C:/tmp/marker' -Value 'started' -NoNewline", commandsForShell(powerShell).WriteFile("C:/tmp/marker", "started"))
}

func TestCommandsEnvOutputWithSeparatorUsesShellSyntax(t *testing.T) {
	require.Equal(t, "printf '%s:%s' ${TASK_ID:-} ${TASK_NAME:-}", commandsForShell(posixShell).EnvOutputWithSeparator(":", "TASK_ID", "TASK_NAME"))
	require.Equal(t, "Write-Output ((@($env:TASK_ID, $env:TASK_NAME) | ForEach-Object { if ($null -eq $_) { '' } else { [string]$_ } }) -join ':')", commandsForShell(powerShell).EnvOutputWithSeparator(":", "TASK_ID", "TASK_NAME"))
}

func TestCommandsWaitForFileUsesShellSyntax(t *testing.T) {
	require.Equal(t, "while [ ! -f '/tmp/marker' ]; do\n  [ -d '/tmp' ] || exit 1\n  sleep 0.05\ndone", commandsForShell(posixShell).WaitForFile("/tmp/marker"))
	require.Equal(t, "while (-not (Test-Path 'C:/tmp/marker')) {\n  if (-not (Test-Path (Split-Path -Parent 'C:/tmp/marker'))) { exit 1 }\n  Start-Sleep -Milliseconds 50\n}", commandsForShell(powerShell).WaitForFile("C:/tmp/marker"))
}

// A wait whose directory is removed, as when its test ends, fails rather than
// leaving the step running forever.
func TestCommandsWaitForFileEndsWhenDirectoryIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs the POSIX snippet")
	}
	dir := filepath.Join(t.TempDir(), "run")
	require.NoError(t, os.Mkdir(dir, 0o750))
	wait := exec.Command("/bin/sh", "-c", commandsForShell(posixShell).WaitForFile(filepath.Join(dir, "release"))) //nolint:gosec // Test runs its own snippet.
	require.NoError(t, wait.Start())
	done := make(chan error, 1)
	go func() { done <- wait.Wait() }()

	require.NoError(t, os.RemoveAll(dir))
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		_ = wait.Process.Kill()
		t.Fatal("the wait outlived its directory")
	}
}
