// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package tools

import (
	"os"

	"golang.org/x/sys/unix"
)

func openLocalRegistry(path string) (*os.File, error) {
	// Nonblocking open handles FIFO replacement too; the descriptor is checked
	// for regular-file status before any read. Go also sets close-on-exec.
	return os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0) //nolint:gosec
}
