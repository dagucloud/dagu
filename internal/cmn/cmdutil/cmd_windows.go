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
func killProcessTree(pid uint32) error {
	// Process ID 0 is the System Idle Process, which is its own parent.
	if pid == 0 {
		return nil
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
		if entry.ParentProcessID == pid && entry.ProcessID != pid {
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
