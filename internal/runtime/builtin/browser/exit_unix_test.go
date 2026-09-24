// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package browser

import (
	"bufio"
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On macOS the runtime's close can wait minutes for the Chrome updater, which
// inherits the browser's output but leaves its process group. Closing waits
// for the browser's whole process group and then returns without the
// runtime.
func TestCloseBrowserWaitsForProcessGroup(t *testing.T) {
	t.Parallel()

	// The group leader stands in for the browser and its child for a helper.
	browser := exec.Command("sh", "-c", "sleep 60 >/dev/null 2>&1 & echo ready; wait")
	browser.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := browser.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, browser.Start())
	pid := browser.Process.Pid
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready\n", ready)
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	reaped := make(chan struct{})
	go func() {
		_ = browser.Wait()
		close(reaped)
	}()
	runtimeBlocked := make(chan struct{})
	t.Cleanup(func() { close(runtimeBlocked) })

	closed := make(chan error, 1)
	go func() {
		closed <- closeBrowser(context.Background(), pid, func(context.Context) error {
			<-runtimeBlocked
			return nil
		})
	}()

	require.NoError(t, syscall.Kill(pid, syscall.SIGKILL))
	<-reaped
	select {
	case err := <-closed:
		t.Fatalf("returned while a helper was running: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	require.NoError(t, syscall.Kill(-pid, syscall.SIGKILL))
	select {
	case err := <-closed:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after the process group exited")
	}
}
