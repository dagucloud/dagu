// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestStartAllCommand(t *testing.T) {
	t.Run("StartAll", func(t *testing.T) {
		th := test.SetupCommand(t, test.WithCoordinatorEnabled())
		cancelWhenLogContains(t, th, "Scheduler initialization", "Coordinator initialization")
		th.RunCommand(t, cmd.StartAll(), test.CmdTest{
			Args: []string{
				"start-all",
				fmt.Sprintf("--port=%s", findPort(t)),
				"--coordinator.host=0.0.0.0",
				fmt.Sprintf("--coordinator.port=%s", findPort(t)),
			},
			ExpectedOut: []string{"Server initialization", "Scheduler initialization", "Coordinator initialization", "Scheduler stopped"},
		})

	})
	t.Run("StartAllWithConfig", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Coordinator initialization")
		th.RunCommand(t, cmd.StartAll(), test.CmdTest{
			Args: []string{
				"start-all",
				"--config", test.TestdataPath(t, "cli/config_startall.yaml"),
				fmt.Sprintf("--coordinator.port=%s", findPort(t)),
			},
			ExpectedOut: []string{"54322", "dagu_test", "Coordinator initialization"},
		})
	})
}

func TestStartAllSecondInterruptTerminatesBlockedCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes are not available on Windows")
	}

	th := test.SetupCommand(t, test.WithBuiltExecutable())
	cleanupDir := filepath.Join(th.Config.Paths.DataDir, "agent-session-cleanups")
	require.NoError(t, os.MkdirAll(cleanupDir, 0o750))
	blockedRecord := filepath.Join(cleanupDir, "blocked.json")
	require.NoError(t, exec.Command("mkfifo", blockedRecord).Run())

	args := test.WithConfigFlag([]string{
		"start-all",
		fmt.Sprintf("--port=%s", findPort(t)),
	}, th.Config)
	command := exec.Command(th.Config.Paths.Executable, args...) //nolint:gosec // Test executes the binary built from this repository.
	command.Env = th.ChildEnv
	output := th.LoggingOutput
	command.Stdout = output
	command.Stderr = output
	require.NoError(t, command.Start())

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- command.Wait()
	}()
	exited := false
	defer func() {
		if exited {
			return
		}
		unblockCleanupRecord(t, blockedRecord)
		select {
		case <-waitCh:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-waitCh
		}
	}()

	require.Eventually(t, func() bool {
		return strings.Contains(output.String(), "Server is starting")
	}, commandLogWaitTimeout(), 50*time.Millisecond, "output: %s", output.String())
	require.NoError(t, command.Process.Signal(os.Interrupt))
	require.Eventually(t, func() bool {
		return strings.Contains(output.String(), "All services stopped gracefully")
	}, commandLogWaitTimeout(), 50*time.Millisecond, "output: %s", output.String())
	require.NoError(t, command.Process.Signal(os.Interrupt))

	select {
	case <-waitCh:
		exited = true
	case <-time.After(2 * time.Second):
		unblockCleanupRecord(t, blockedRecord)
		select {
		case <-waitCh:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-waitCh
		}
		exited = true
		t.Fatalf("second interrupt did not terminate blocked cleanup; output: %s", output.String())
	}
}

func unblockCleanupRecord(t *testing.T, path string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = file.WriteString("{}")
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
