// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package resource

import (
	"os"
	"syscall"
)

// sameDisk reports whether the two paths are on one filesystem. A path that
// cannot be read counts as the same, so it is not measured twice.
func sameDisk(a, b string) bool {
	da, okA := device(a)
	db, okB := device(b)
	return !okA || !okB || da == db
}

func device(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true //nolint:unconvert,gosec // Dev is int32 on some platforms
}
