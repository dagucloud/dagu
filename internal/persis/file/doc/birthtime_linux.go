// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package doc

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// fileCreationTime returns the file's birth time on Linux when statx supports it.
func fileCreationTime(path string, info os.FileInfo) time.Time {
	var statx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &statx); err == nil {
		if statx.Mask&unix.STATX_BTIME != 0 && statx.Btime.Sec > 0 {
			return time.Unix(statx.Btime.Sec, int64(statx.Btime.Nsec))
		}
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec))
	}
	return info.ModTime()
}
