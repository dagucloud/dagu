// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package doc

import (
	"os"
	"syscall"
	"time"
)

// fileCreationTime returns the file's ctime (inode change time) on Linux.
// Note: This is not the true birth time; Linux birth time requires statx() which
// is not available via the standard syscall package. Ctime is used as an approximation.
func fileCreationTime(info os.FileInfo) time.Time {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec))
	}
	return info.ModTime()
}
