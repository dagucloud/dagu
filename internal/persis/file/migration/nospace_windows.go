// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package migration

import (
	"errors"

	"golang.org/x/sys/windows"
)

// errNoSpace is the error a write to a full file system returns.
var errNoSpace error = windows.ERROR_DISK_FULL

// isNoSpace reports whether err comes from a full file system.
func isNoSpace(err error) bool {
	return errors.Is(err, errNoSpace) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
