// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmd

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// service-stop reaches the start-all process serving the same data
// directory, and only that one.
func TestServiceStopRequest(t *testing.T) {
	served := t.TempDir()
	requests, err := serviceStopRequests(served)
	require.NoError(t, err)

	other := t.TempDir()
	otherRequests, err := serviceStopRequests(other)
	require.NoError(t, err)

	require.NoError(t, requestServiceStop(served, time.Second))
	select {
	case sig := <-requests:
		require.Equal(t, os.Interrupt, sig)
	case <-time.After(10 * time.Second):
		t.Fatal("the stop request did not arrive")
	}
	select {
	case <-otherRequests:
		t.Fatal("the stop request reached another data directory")
	case <-time.After(100 * time.Millisecond):
	}

	// A repeated request during shutdown still arrives.
	require.NoError(t, requestServiceStop(served, time.Second))
	select {
	case <-requests:
	case <-time.After(10 * time.Second):
		t.Fatal("the repeated stop request did not arrive")
	}
}

// Without a listener, service-stop ends processes only when the service
// wrapper started it; run from a terminal it must not touch its siblings.
func TestServiceStopWithoutListenerOutsideTheService(t *testing.T) {
	err := requestServiceStop(t.TempDir(), time.Second)
	require.ErrorContains(t, err, "listens for a stop request")
}

// A stop waits for the supervised process up to its timeout: one that exits
// in time exits on its own, and one still running then is ended.
func TestServiceStopEndsAProcessThatOutlivesTheTimeout(t *testing.T) {
	quick := exec.Command("cmd", "/c", "exit 0")
	require.NoError(t, quick.Start())
	slow := exec.Command("ping", "-n", "60", "127.0.0.1")
	require.NoError(t, slow.Start())
	t.Cleanup(func() { _ = slow.Process.Kill() })

	processes := []windows.Handle{openForStop(t, quick), openForStop(t, slow)}
	began := time.Now()
	require.NoError(t, awaitExit(processes, 3*time.Second))
	require.Less(t, time.Since(began), 30*time.Second, "the wait outlasted its timeout")

	require.NoError(t, quick.Wait())
	require.Error(t, slow.Wait())
	require.Equal(t, 1, slow.ProcessState.ExitCode(), "the process still running was ended")
}

// openForStop opens cmd's process as service-stop opens the one it stops.
func openForStop(t *testing.T, cmd *exec.Cmd) windows.Handle {
	t.Helper()
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)) //nolint:gosec // process IDs fit in uint32
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(process) })
	return process
}
