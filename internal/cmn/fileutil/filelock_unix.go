// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package fileutil

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes a flock lock, which belongs to the open file, so two opens
// conflict also within one process.
func lockFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec // A file descriptor fits in an int.
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrLocked
	}
	return err
}

func unlockFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN) //nolint:gosec // A file descriptor fits in an int.
}
