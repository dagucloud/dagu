// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package wiki

import (
	"os"
	"syscall"
	"time"
)

// fileCreationTime returns the file's creation time on Windows.
func fileCreationTime(_ string, info os.FileInfo) time.Time {
	if stat, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, stat.CreationTime.Nanoseconds())
	}
	return info.ModTime()
}
