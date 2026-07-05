// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"golang.org/x/sys/windows"
)

func TestNormalizeLongWindowsDir(t *testing.T) {
	short := `C:\dagu\work`
	if got := cmdutil.NormalizeLongWindowsDirForTest(short); got != short {
		t.Fatalf("short path changed: got %q, want %q", got, short)
	}

	longTail := strings.TrimSuffix(strings.Repeat(`nested\`, 40), `\`)
	long := `C:\dagu\` + longTail
	got := cmdutil.NormalizeLongWindowsDirForTest(long)
	if !strings.HasPrefix(got, `\\?\C:\dagu\`) {
		t.Fatalf("long path was not extended: got %q", got)
	}

	alreadyExtended := `\\?\C:\dagu\` + longTail
	if got := cmdutil.NormalizeLongWindowsDirForTest(alreadyExtended); got != alreadyExtended {
		t.Fatalf("extended path changed: got %q, want %q", got, alreadyExtended)
	}

	unc := `\\server\share\` + longTail
	got = cmdutil.NormalizeLongWindowsDirForTest(unc)
	if !strings.HasPrefix(got, `\\?\UNC\server\share\`) {
		t.Fatalf("long UNC path was not extended: got %q", got)
	}

	device := `\\.\C:\dagu\` + longTail
	if got := cmdutil.NormalizeLongWindowsDirForTest(device); got != device {
		t.Fatalf("device path changed: got %q, want %q", got, device)
	}
}

func TestStartManagedProcessWithLongDir(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 248 {
		dir = filepath.Join(dir, "nested")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("failed to create long working directory: %v", err)
	}

	cmd := exec.Command("cmd", "/C", "cd")
	cmd.Dir = dir
	proc, err := cmdutil.StartManagedProcess(cmd)
	if err != nil {
		t.Fatalf("failed to start process in long working directory: %v", err)
	}
	defer func() { _ = proc.Release() }()

	if err := cmd.Wait(); err != nil {
		t.Fatalf("process failed in long working directory: %v", err)
	}
}

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
	err := cmdutil.KillProcessTreeForTest(pid)
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
