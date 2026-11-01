// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// DetachAttr returns process attributes that start a command without a
// console and in a process group of its own, so the caller's console and
// its Ctrl+C do not reach it. With breakawayFromJob, the command also
// leaves the caller's job object, so closing the job does not end it;
// starting fails with ERROR_ACCESS_DENIED when the job forbids that.
func DetachAttr(breakawayFromJob bool) *syscall.SysProcAttr {
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	if breakawayFromJob {
		flags |= windows.CREATE_BREAKAWAY_FROM_JOB
	}
	return &syscall.SysProcAttr{CreationFlags: flags}
}
