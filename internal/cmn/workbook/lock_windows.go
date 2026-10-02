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
