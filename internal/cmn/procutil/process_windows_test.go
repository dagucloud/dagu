// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package procutil

import (
	"os/exec"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestCanLookupStartTimeAcceptsWindowsUint32PIDs(t *testing.T) {
	t.Parallel()

	if strconv.IntSize < 64 {
		t.Skip("requires an int that can represent Windows uint32 PIDs above int32")
	}

	one := int64(1)
	require.True(t, canLookupStartTime(int(one<<31)))
	require.True(t, canLookupStartTime(int((one<<32)-1)))
	require.False(t, canLookupStartTime(int(one<<32)))
}

// An exited process stays queryable while any handle to it is open, so its
// creation time must not make it match as the running process. The handle held
// here also keeps the PID from being reused.
func TestMatchesStartTimeExitedProcess(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("ping", "-n", "30", "127.0.0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	pid := cmd.Process.Pid

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	require.NoError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })

	startedAt, ok := StartTime(pid)
	require.True(t, ok)
	matched, _, ok := MatchesStartTime(pid, startedAt)
	require.True(t, ok)
	require.True(t, matched)

	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()

	matched, _, _ = MatchesStartTime(pid, startedAt)
	require.False(t, matched)
}
