// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package workbook

import (
	"errors"
	"os"
	"syscall"
)

// Windows error codes for a file another process holds open.
const (
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// lockProbeSupported is true where a lock file can be opened to learn
// whether another process holds it.
const lockProbeSupported = true

// isSharingViolation reports whether another process holds the file open in
// a way that blocks the operation.
func isSharingViolation(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation)
}

// lockFileHeld reports whether another process keeps the lock file open.
// Excel holds its ~$ file with no sharing while the workbook is open, so an
// attempt to open it fails with a sharing violation; a leftover file from a
// crash opens normally.
func lockFileHeld(lock string) bool {
	f, err := os.Open(lock) //nolint:gosec // the lock file sits beside the workbook the step names
	if err != nil {
		return isSharingViolation(err)
	}
	_ = f.Close()
	return false
}

// deleteAccess is the DELETE access right, which syscall does not name.
const deleteAccess = 0x00010000

// renameRefusedByHolder reports whether a rename over target failed because
// another process holds it open without sharing deletes, the way Excel
// holds a workbook: it shares reads only, so the temporary file of an atomic
// save is written but the rename over the original is refused. Windows
// reports that refusal as access denied, the same code as a read-only file
// or a permission problem, so the hold is probed: opening the file for
// deletion with every share mode fails with a sharing violation only while
// such a handle exists.
func renameRefusedByHolder(target string, err error) bool {
	if !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
		return false
	}
	name, convErr := syscall.UTF16PtrFromString(target)
	if convErr != nil {
		return false
	}
	share := uint32(syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE)
	handle, openErr := syscall.CreateFile(name, deleteAccess, share, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if openErr != nil {
		return isSharingViolation(openErr)
	}
	_ = syscall.CloseHandle(handle)
	return false
}
