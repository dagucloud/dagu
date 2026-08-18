// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestKillProcessTree_Integration starts a dummy process tree and kills it using
// killProcessTree.
func TestKillProcessTree_Integration(t *testing.T) {
	// Start a harmless process that runs for a while. cmd.exe launches ping.exe
	// as a separate process, so the root has a child to recurse into.
	cmd := exec.Command("cmd", "/C", "ping", "-n", "30", "127.0.0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	pid := uint32(cmd.Process.Pid)
	t.Logf("Started test process with PID %d", pid)

	// Wait for cmd.exe to spawn ping.exe so the root has a child to recurse
	// into. Poll rather than sleep a fixed amount: CI runners can be slow to
	// spawn the child and reflect the parent relationship in the snapshot.
	var children []uint32
	deadline := time.Now().Add(5 * time.Second)
	for len(children) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		children = childProcessIDs(t, pid)
	}
	if len(children) == 0 {
		t.Fatalf("test process %d spawned no children, so the tree walk would not be exercised", pid)
	}
	t.Logf("Test process has child PIDs %v", children)

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

	// Verify the whole tree is gone, not just the root
	waitProcessGone(t, pid, "root process")
	for _, child := range children {
		waitProcessGone(t, child, "child process")
	}
}

// childProcessIDs returns the process IDs whose recorded creator is pid.
func childProcessIDs(t *testing.T, pid uint32) []uint32 {
	t.Helper()

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatalf("CreateToolhelp32Snapshot failed: %v", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		t.Fatalf("Process32First failed: %v", err)
	}

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

// waitProcessGone fails the test unless pid stops being openable within a few seconds.
func waitProcessGone(t *testing.T, pid uint32, what string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, pid)
		if err != nil {
			return
		}
		_ = windows.CloseHandle(h)

		if time.Now().After(deadline) {
			t.Fatalf("expected %s (PID %d) to be gone, but OpenProcess succeeded", what, pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
