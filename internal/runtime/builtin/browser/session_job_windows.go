// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package browser

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobWarning says when this process runs in a job object that ends every
// process in it, the session's browser among them, once the job closes.
func jobWarning() string {
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	// A zero job handle queries the job of the calling process, and fails
	// when the process is in none.
	err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil) //nolint:gosec // the buffer is the structure the call fills
	if err != nil || info.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		return ""
	}
	return "this command runs in a job object that ends its processes when it closes, so the session's browser closes with that job; " +
		"run the session's commands outside it to keep the browser open between them"
}
