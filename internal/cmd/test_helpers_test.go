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
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func cancelWhenLogContains(t *testing.T, th test.Command, want ...string) {
	t.Helper()

	done := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(commandLogWaitTimeout())
		for time.Now().Before(deadline) {
			out := th.LoggingOutput.String()
			matched := true
			for _, token := range want {
				if !strings.Contains(out, token) {
					matched = false
					break
				}
			}
			if matched {
				th.Cancel()
				done <- true
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		th.Cancel()
		done <- false
	}()

	t.Cleanup(func() {
		require.True(t, <-done, "startup log never appeared: %v", want)
	})
}

func commandLogWaitTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 30 * time.Second
	}
	return 10 * time.Second
}

func assertSecondInterruptTerminatesBlockedCleanup(t *testing.T, commandName, shutdownLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("named pipes are not available on Windows")
	}

	th := test.SetupCommand(t, test.WithBuiltExecutable())
	cleanupDir := filepath.Join(th.Config.Paths.DataDir, "agent-session-cleanups")
	require.NoError(t, os.MkdirAll(cleanupDir, 0o750))
	require.NoError(t, exec.Command("mkfifo", filepath.Join(cleanupDir, "blocked.json")).Run())

	args := test.WithConfigFlag([]string{
		commandName,
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
		if !exited {
			terminateTestCommand(command, waitCh)
		}
	}()

	require.Eventually(t, func() bool {
		return strings.Contains(output.String(), "Server is starting")
	}, commandLogWaitTimeout(), 50*time.Millisecond, "output: %s", output.String())
	require.NoError(t, command.Process.Signal(os.Interrupt))
	require.Eventually(t, func() bool {
		return strings.Contains(output.String(), shutdownLog)
	}, commandLogWaitTimeout(), 50*time.Millisecond, "output: %s", output.String())
	require.NoError(t, command.Process.Signal(os.Interrupt))

	select {
	case err := <-waitCh:
		exited = true
		exitErr, ok := err.(*exec.ExitError)
		require.True(t, ok, "expected signal exit, got %v", err)
		waitStatus, ok := exitErr.Sys().(syscall.WaitStatus)
		require.True(t, ok, "expected Unix wait status, got %T", exitErr.Sys())
		require.True(t, waitStatus.Signaled(), "expected signal exit, got %v", waitStatus)
		require.Equal(t, syscall.SIGINT, waitStatus.Signal())
	case <-time.After(2 * time.Second):
		terminateTestCommand(command, waitCh)
		exited = true
		t.Fatalf("second interrupt did not terminate blocked cleanup; output: %s", output.String())
	}
}

func terminateTestCommand(command *exec.Cmd, waitCh <-chan error) {
	_ = command.Process.Kill()
	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
	}
}

func newHoldFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() {
		_ = os.WriteFile(path, []byte("release"), 0o600)
	})
	return path
}

func releaseHoldFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("release"), 0o600))
}

func holdUntilFileExistsCommand(path string) string {
	return holdUntilFileExistsCommandWithin(path, commandLogWaitTimeout())
}

func holdUntilFileExistsCommandWithin(path string, timeout time.Duration) string {
	iterations := int(timeout / (50 * time.Millisecond))
	return test.ForOS(
		fmt.Sprintf("for i in $(seq 1 %d); do [ -f %s ] && exit 0; sleep 0.05; done; exit 124", iterations, test.PosixQuote(path)),
		fmt.Sprintf("for ($i = 0; $i -lt %d; $i++) { if (Test-Path %s) { exit 0 }; Start-Sleep -Milliseconds 50 }; exit 124", iterations, test.PowerShellQuote(path)),
	)
}

func releaseHoldFileWhenRecentStatusCountAtLeast(
	t *testing.T,
	th test.Command,
	dagName string,
	count int,
	path string,
) <-chan error {
	return releaseHoldFileWhenRecentStatusCountAtLeastWithin(t, th, dagName, count, path, commandLogWaitTimeout())
}

func releaseHoldFileWhenRecentStatusCountAtLeastWithin(
	t *testing.T,
	th test.Command,
	dagName string,
	count int,
	path string,
	timeout time.Duration,
) <-chan error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			statuses, err := th.DAGRunRepository.RecentStatuses(th.Context, dagName, count)
			if err == nil && len(statuses) >= count {
				done <- os.WriteFile(path, []byte("release"), 0o600)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		_ = os.WriteFile(path, []byte("release"), 0o600)
		done <- fmt.Errorf("timed out waiting for %d recent statuses for %s", count, dagName)
	}()
	return done
}
