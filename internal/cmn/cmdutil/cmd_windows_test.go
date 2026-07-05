// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestKillProcessTree_Integration starts a dummy process and kills it using killProcessTree.
func TestKillProcessTree_Integration(t *testing.T) {
	// Start a harmless process that sleeps for a while
	cmd := exec.Command("cmd", "/C", "timeout", "/T", "30", "/NOBREAK")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	pid := uint32(cmd.Process.Pid)
	t.Logf("Started test process with PID %d", pid)

	// Give it a moment to fully start
	time.Sleep(500 * time.Millisecond)

	// Try to kill it
	err := killProcessTree(pid)
	if err != nil {
		t.Fatalf("killProcessTree returned error: %v", err)
	}

	// Wait for process to exit
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-time.After(3 * time.Second):
		t.Fatal("process did not exit after killProcessTree")
	case err := <-done:
		if err != nil {
			t.Logf("process terminated as expected: %v", err)
		} else {
			t.Log("process terminated successfully")
		}
	}

	// Verify the process handle no longer exists
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err == nil {
		defer windows.CloseHandle(h)
		t.Fatalf("expected process to be gone, but OpenProcess succeeded")
	}
}
