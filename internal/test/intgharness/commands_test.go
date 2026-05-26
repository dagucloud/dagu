// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommandsSleepUsesShellSyntax(t *testing.T) {
	require.Equal(t, "sleep 1.5", commandsForShell(posixShell).Sleep(1500*time.Millisecond))
	require.Equal(t, "Start-Sleep -Milliseconds 1500", commandsForShell(powerShell).Sleep(1500*time.Millisecond))
}

func TestCommandsSleepClampsNonPositiveDurations(t *testing.T) {
	require.Equal(t, "sleep 0.001", commandsForShell(posixShell).Sleep(0))
	require.Equal(t, "Start-Sleep -Milliseconds 1", commandsForShell(powerShell).Sleep(-time.Second))
}

func TestCommandsWriteFileUsesShellSyntax(t *testing.T) {
	require.Equal(t, "printf '%s' 'started' > '/tmp/marker'", commandsForShell(posixShell).WriteFile("/tmp/marker", "started"))
	require.Equal(t, "Set-Content -Path 'C:/tmp/marker' -Value 'started' -NoNewline", commandsForShell(powerShell).WriteFile("C:/tmp/marker", "started"))
}

func TestCommandsWaitForFileUsesShellSyntax(t *testing.T) {
	require.Equal(t, "while [ ! -f '/tmp/marker' ]; do\n  sleep 0.05\ndone", commandsForShell(posixShell).WaitForFile("/tmp/marker"))
	require.Equal(t, "while (-not (Test-Path 'C:/tmp/marker')) {\n  Start-Sleep -Milliseconds 50\n}", commandsForShell(powerShell).WaitForFile("C:/tmp/marker"))
}
