// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package skill

import (
	"io/fs"
	"syscall"
)

// linkCount returns the number of hard links of a regular file.
func linkCount(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink) //nolint:unconvert // The type of Nlink depends on the platform.
	}
	return 1
}
