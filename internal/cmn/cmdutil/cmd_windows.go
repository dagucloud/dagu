// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"golang.org/x/sys/windows"
)

// SetupCommand configures Windows-specific command attributes
func SetupCommand(cmd *exec.Cmd) {
	setupCommand(cmd)
}

// setupCommand configures Windows-specific command attributes
func setupCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// killProcessTree terminates pid and its descendant processes on Windows,
// children before parents. A process that cannot be terminated does not stop the
// rest of the tree from being killed; all such failures are returned together.
// A process counts as a child only when it is known to have started after its
// parent, so when the start of pid cannot be determined, only pid is terminated.
func killProcessTree(pid uint32) error {
	// Process ID 0 is the System Idle Process, which is its own parent.
	if pid == 0 {
		return nil
	}

	h, started, ok := openStarted(pid)
	if !ok {
		return terminateProcess(pid)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return killHeldTree(pid, started)
}

// openStarted opens pid and reads its creation time. The handle keeps pid from
// naming a later process until it is closed, and still reads the creation time
// after the process exits.
func openStarted(pid uint32) (windows.Handle, int64, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, 0, false
	}
	started, ok := procutil.HandleStartTime(h)
	if !ok {
		_ = windows.CloseHandle(h)
		return 0, 0, false
	}
	return h, started, true
}

// killHeldTree is killProcessTree for a pid that the caller holds open and
// that started at started.
func killHeldTree(pid uint32, started int64) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot failed: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	// Find first process
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return fmt.Errorf("Process32First failed: %w", err)
	}

	// Iterate all processes
	var errs []error
	for {
		if entry.ParentProcessID == pid && entry.ProcessID != pid {
			// Recursively kill children first
			if err := killChildTree(entry.ProcessID, started); err != nil {
				errs = append(errs, err)
			}
		}

		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}

	// Finally, kill this process
	if err := terminateProcess(pid); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// killChildTree kills the tree of pid, whose recorded parent started at
// parentStarted. Windows keeps a parent ID after the parent exits and may give
// that ID to a later process, so pid is a child only when it started no
// earlier than the parent; otherwise it is left alone. The handle stays open
// until pid is terminated, so the checked process is the one killed.
func killChildTree(pid uint32, parentStarted int64) error {
	h, started, ok := openStarted(pid)
	if !ok {
		return nil
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if started < parentStarted {
		return nil
	}
	return killHeldTree(pid, started)
}

// terminateProcess force-terminates a single process. A process that has already
// exited is not an error.
func terminateProcess(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	switch {
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		// The process exited and its kernel object is already gone.
		return nil
	case err != nil:
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()

	// The handle carries PROCESS_TERMINATE, so ERROR_ACCESS_DENIED here means the
	// process exited while another open handle kept its kernel object alive.
	if err := windows.TerminateProcess(h, 1); err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	return nil
}
