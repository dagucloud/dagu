// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"io/fs"
	"strconv"

	"golang.org/x/sys/unix"
)

// hasChild reports whether the folder fd has the entry name, of the type mode
// (unix.S_IFDIR, unix.S_IFREG) when mode is not 0. A link is not followed.
func hasChild(fd int, name string, mode uint32) bool {
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false
	}
	return mode == 0 || st.Mode&unix.S_IFMT == mode
}

func chmodFDPath(fd int, path string, mode uint32) error {
	if err := unix.Fchmodat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), mode, 0); err != nil {
		return &fs.PathError{Op: "chmod", Path: path, Err: err}
	}
	return nil
}
