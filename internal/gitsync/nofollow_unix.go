// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package gitsync

import (
	"os"

	"golang.org/x/sys/unix"
)

func openRootFileNoFollow(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
	return root.OpenFile(name, flag|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, perm)
}
