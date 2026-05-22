// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package procutil

import (
	"time"

	"golang.org/x/sys/windows"
)

const windowsStillActiveExitCode = 259
const maxPIDUint32 = 1<<32 - 1

func isAlive(pid int) bool {
	if !canUseWindowsPID(pid) {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(handle) //nolint:errcheck

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return true
	}
	return exitCode == windowsStillActiveExitCode
}

func canLookupStartTime(pid int) bool {
	return canUseWindowsPID(pid)
}

func canUseWindowsPID(pid int) bool {
	return pid > 0 && uint64(pid) <= maxPIDUint32
}

func startTime(pid int) (int64, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(handle) //nolint:errcheck

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
