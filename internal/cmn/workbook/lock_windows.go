// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package workbook

import (
	"errors"
	"syscall"
)

// Windows error codes for a file another process holds open.
const (
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// isSharingViolation reports whether another process holds the file open in
// a way that blocks the operation.
func isSharingViolation(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation)
}
