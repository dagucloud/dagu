// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"errors"

	"golang.org/x/sys/unix"
)

// protectedDir is a folder whose mode the isolation changed. The descriptor
// keeps the folder for the restore of its mode.
type protectedDir struct {
	fd   int
	mode uint32
}

// restoreModes gives each folder whose mode the isolation changed its mode
// back, through the descriptor that the isolation kept.
func (iso *isolation) restoreModes() error {
	var errs []error
	for path, dir := range iso.passDirs {
		if err := chmodFDPath(dir.fd, path, dir.mode); err != nil {
			errs = append(errs, stepError(iso.step, "change the mode of", path, err))
		}
		_ = unix.Close(dir.fd)
		delete(iso.passDirs, path)
	}
	return errors.Join(errs...)
}
