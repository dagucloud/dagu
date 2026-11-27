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

	// Windows keeps a process's parent ID after the parent exits and may give
	// that ID to a later process, so only a process that started no earlier
	// than pid is its child, and none is matched by parent ID alone. The handle
	// keeps pid from naming a later process during the walk and still reads its
	// creation time after it exits.
	var started int64
	var known bool
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		defer func() { _ = windows.CloseHandle(h) }()
		started, known = procutil.HandleStartTime(h)
	}
	if !known {
		return terminateProcess(pid)
	}

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
		if entry.ParentProcessID == pid && entry.ProcessID != pid && startedSince(entry.ProcessID, started) {
			// Recursively kill children first
			if err := killProcessTree(entry.ProcessID); err != nil {
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

// startedSince reports whether the process with pid is known to have started
// no earlier than the given time, in the form procutil.HandleStartTime returns.
// A process that has exited still reports its creation time while any handle
// keeps it.
func startedSince(pid uint32, at int64) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	started, ok := procutil.HandleStartTime(h)
	return ok && started >= at
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
