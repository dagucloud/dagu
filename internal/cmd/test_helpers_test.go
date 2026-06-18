// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/test"
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
			if len(th.DAGRunMgr.ListRecentStatus(th.Context, dagName, count)) >= count {
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
