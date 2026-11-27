// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"golang.org/x/sys/windows"
)

// treeProcess is a process recorded from the tree under test. The open handle
// keeps its PID from being given to a later process, so waiting on the handle
// observes this process and no other.
type treeProcess struct {
	pid    uint32
	image  string
	handle windows.Handle
}

func (p treeProcess) String() string {
	return fmt.Sprintf("%d (%s)", p.pid, p.image)
}

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

	root := openTreeProcess(t, uint32(cmd.Process.Pid), "cmd.exe")
	t.Cleanup(func() { _ = windows.CloseHandle(root.handle) })
	t.Logf("Started test process %v", root)

	// Wait for cmd.exe to spawn ping.exe. Poll rather than sleep a fixed amount:
	// CI runners can be slow to spawn the child and reflect the parent
	// relationship in the snapshot.
	var children []treeProcess
	deadline := time.Now().Add(5 * time.Second)
	for {
		children = childProcesses(t, root)
		if hasImage(children, "ping.exe") {
			break
		}
		closeProcesses(children)
		if time.Now().After(deadline) {
			t.Fatalf("test process %v did not spawn ping.exe, so the tree walk would not be exercised", root)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(func() { closeProcesses(children) })
	t.Logf("Test process has children %v", children)

	if err := killProcessTree(root.pid); err != nil {
		t.Fatalf("killProcessTree returned error: %v", err)
	}

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

	// Verify the whole tree stopped, not just the root.
	waitProcessExit(t, root, "root process")
	for _, child := range children {
		waitProcessExit(t, child, "child process")
	}
}

// openTreeProcess opens a handle to the running process pid, labeled image.
func openTreeProcess(t *testing.T, pid uint32, image string) treeProcess {
	t.Helper()

	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		t.Fatalf("OpenProcess on PID %d failed: %v", pid, err)
	}
	return treeProcess{pid: pid, image: image, handle: h}
}

// childProcesses returns open handles to the children of parent. Windows keeps
// a process's parent PID after that parent exits and may reuse the PID for
// parent, so a process created before parent only shares the PID and is
// skipped, as killProcessTree skips it.
func childProcesses(t *testing.T, parent treeProcess) []treeProcess {
	t.Helper()

	parentStart, ok := procutil.HandleStartTime(parent.handle)
	if !ok {
		t.Fatalf("failed to read the start time of %v", parent)
	}

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

	var children []treeProcess
	for {
		if entry.ParentProcessID == parent.pid && entry.ProcessID != parent.pid {
			image := windows.UTF16ToString(entry.ExeFile[:])
			h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
			if err == nil {
				child := treeProcess{pid: entry.ProcessID, image: image, handle: h}
				started, ok := procutil.HandleStartTime(h)
				if ok && started < parentStart {
					t.Logf("Skipping %v: it started before %v and only shares a reused parent PID", child, parent)
					_ = windows.CloseHandle(h)
				} else {
					children = append(children, child)
				}
			} else {
				t.Logf("Not tracking %d (%s): %v", entry.ProcessID, image, err)
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return children
}

// hasImage reports whether any of procs runs the executable image.
func hasImage(procs []treeProcess, image string) bool {
	for _, p := range procs {
		if strings.EqualFold(p.image, image) {
			return true
		}
	}
	return false
}

func closeProcesses(procs []treeProcess) {
	for _, p := range procs {
		_ = windows.CloseHandle(p.handle)
	}
}

// waitProcessExit fails the test unless p exits within a few seconds. A process
// has exited once its handle is signaled, even while other handles keep its
// kernel object alive.
func waitProcessExit(t *testing.T, p treeProcess, what string) {
	t.Helper()

	event, err := windows.WaitForSingleObject(p.handle, uint32((3 * time.Second).Milliseconds()))
	if err != nil {
		t.Fatalf("WaitForSingleObject on %v failed: %v", p, err)
	}
	if event != windows.WAIT_OBJECT_0 {
		t.Fatalf("expected %s %v to be gone, but it is still running", what, p)
	}
}
