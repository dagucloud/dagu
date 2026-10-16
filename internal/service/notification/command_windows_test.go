// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package notification

import (
	"io"
	"os"
	"testing"
	"time"
	"unsafe"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestNotificationCommandStopKillsChildProcess(t *testing.T) {
	prepared, err := prepareNotificationCommand("ping -n 30 127.0.0.1 >NUL")
	require.NoError(t, err)
	t.Cleanup(prepared.cleanup)

	proc, err := startNotificationProcess(prepared.shell, prepared.args, os.Environ(), io.Discard)
	require.NoError(t, err)
	t.Cleanup(func() { _ = proc.Release() })

	pid := uint32(proc.PID())
	var children []uint32
	deadline := time.Now().Add(5 * time.Second)
	for len(children) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		children = notificationChildProcessIDs(t, pid)
	}
	require.NotEmpty(t, children, "command spawned no child to stop")

	_, err = proc.Stop(cmdutil.StopRequest{
		Intent: cmdutil.ForceTermination(),
		Reason: cmdutil.StopReasonTimeout,
	})
	require.NoError(t, err)
	_ = proc.Wait()

	waitNotificationProcessGone(t, pid, "command process")
	for _, child := range children {
		waitNotificationProcessGone(t, child, "child process")
	}
}

func notificationChildProcessIDs(t *testing.T, pid uint32) []uint32 {
	t.Helper()

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	require.NoError(t, err)
	defer func() { _ = windows.CloseHandle(snapshot) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	require.NoError(t, windows.Process32First(snapshot, &entry))

	var children []uint32
	for {
		if entry.ParentProcessID == pid && entry.ProcessID != pid {
			children = append(children, entry.ProcessID)
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return children
}

func waitNotificationProcessGone(t *testing.T, pid uint32, what string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, pid)
		if err != nil {
			return
		}
		_ = windows.CloseHandle(handle)
		if time.Now().After(deadline) {
			t.Fatalf("expected %s (PID %d) to be gone", what, pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
