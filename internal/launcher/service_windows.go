// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/windows"
)

// serviceWrapperExe is the service wrapper the Dagu installer runs Dagu
// under.
const serviceWrapperExe = "dagu-service.exe"

// serviceParent is the process the commands this process launches regard as
// their parent, or zero when they regard this process as their parent.
var serviceParent atomic.Uintptr

// LaunchOutsideService readies this process, when the Dagu service wrapper
// runs it, to launch runs that a stop of the service leaves running. The
// wrapper stops the service by ending this process and every process whose
// parent it is, at any depth, so launched commands regard the wrapper as
// their parent instead. They execute a copy of executable kept in copyDir,
// which LaunchOutsideService returns, so they leave executable free to
// replace or remove. It returns "" when the wrapper does not run this
// process, and launches stay as they are.
func LaunchOutsideService(executable, copyDir string) (string, error) {
	wrapper, err := openServiceWrapper()
	if err != nil || wrapper == 0 {
		return "", err
	}
	copied, err := copyRunBinary(executable, copyDir)
	if err != nil {
		_ = windows.CloseHandle(wrapper)
		return "", err
	}
	// The handle stays open for the life of the process.
	serviceParent.Store(uintptr(wrapper))
	return copied, nil
}

// openServiceWrapper opens this process's parent, with the access a process
// created under it needs, when that parent is the service wrapper that
// started this process. It returns zero otherwise.
func openServiceWrapper() (windows.Handle, error) {
	pid := uint32(os.Getppid()) //nolint:gosec // process IDs fit in uint32
	parent, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_CREATE_PROCESS|windows.PROCESS_DUP_HANDLE, false, pid)
	if err != nil {
		// A parent that has exited, or that this process may not create
		// processes under, is not a wrapper to launch under.
		return 0, nil
	}
	wrapper, err := isServiceWrapper(parent)
	if err != nil || !wrapper {
		_ = windows.CloseHandle(parent)
		return 0, err
	}
	return parent, nil
}

// isServiceWrapper reports whether parent, the process with this process's
// parent ID, is the service wrapper that started this process.
func isServiceWrapper(parent windows.Handle) (bool, error) {
	size := uint32(windows.MAX_LONG_PATH)
	path := make([]uint16, size)
	if err := windows.QueryFullProcessImageName(parent, 0, &path[0], &size); err != nil {
		return false, fmt.Errorf("read the parent process image: %w", err)
	}
	if !strings.EqualFold(filepath.Base(windows.UTF16ToString(path[:size])), serviceWrapperExe) {
		return false, nil
	}
	// Windows keeps a process's parent ID after the parent exits and may give
	// it to a later process, which then started after this one.
	parentStarted, err := startTime(parent)
	if err != nil {
		return false, err
	}
	selfStarted, err := startTime(windows.CurrentProcess())
	if err != nil {
		return false, err
	}
	return parentStarted <= selfStarted, nil
}

// startTime returns when process started, in nanoseconds since 1601.
func startTime(process windows.Handle) (int64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return 0, fmt.Errorf("read a process start time: %w", err)
	}
	return created.Nanoseconds(), nil
}

// launchOutsideService has cmd regard the process LaunchOutsideService chose
// as its parent, if it chose one.
func launchOutsideService(cmd *exec.Cmd) {
	parent := serviceParent.Load()
	if parent == 0 {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.ParentProcess = syscall.Handle(parent)
	// A command takes its console from this process and its handles from its
	// parent, which has no handle to that console, so it gets a console of
	// its own to start with.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
