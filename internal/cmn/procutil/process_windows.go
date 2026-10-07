// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package procutil

import (
	"time"

	"golang.org/x/sys/windows"
)

const maxPIDUint32 = 1<<32 - 1

func isAlive(pid int) bool {
	if !canUseWindowsPID(pid) {
		return false
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)) //nolint:gosec // canUseWindowsPID bounds pid to the uint32 range.
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(handle) //nolint:errcheck
	return HandleIsAlive(handle)
}

// HandleIsAlive reports whether the process behind handle is still running.
// The handle needs SYNCHRONIZE access. A process is running until its handle
// is signaled; the exit code alone cannot tell, since a process may exit with
// the code that also means still active.
func HandleIsAlive(handle windows.Handle) bool {
	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return true
	}
	return event != windows.WAIT_OBJECT_0
}

func canLookupStartTime(pid int) bool {
	return canUseWindowsPID(pid)
}

func canUseWindowsPID(pid int) bool {
	return pid > 0 && uint64(pid) <= maxPIDUint32
}

func startTime(pid int) (int64, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // callers gate on canLookupStartTime, which bounds pid to the uint32 range.
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(handle) //nolint:errcheck
	return HandleStartTime(handle)
}

// HandleStartTime returns the creation time of the process behind handle as
// Unix milliseconds, in the same form as StartTime. The handle needs
// PROCESS_QUERY_LIMITED_INFORMATION access.
func HandleStartTime(handle windows.Handle) (int64, bool) {
	var creationTime windows.Filetime
	var exitTime windows.Filetime
	var kernelTime windows.Filetime
	var userTime windows.Filetime
	if err := windows.GetProcessTimes(handle, &creationTime, &exitTime, &kernelTime, &userTime); err != nil {
		return 0, false
	}

	startedAt := creationTime.Nanoseconds() / int64(time.Millisecond)
	if startedAt <= 0 {
		return 0, false
	}
	return startedAt, true
}
