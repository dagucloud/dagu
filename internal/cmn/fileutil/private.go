// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"io/fs"
)

// privateDirMode is the mode of a directory only its owner may use.
const privateDirMode = 0o700

// MkdirPrivate creates dir and its missing parents, and makes dir usable by
// its owner only, also when it existed: mode 0700 on Unix, and on Windows an
// access list that grants only the current user, which files created in it
// inherit.
func MkdirPrivate(dir string) error {
	if err := MkdirAll(dir, privateDirMode); err != nil {
		return err
	}
	if err := makePrivate(dir); err != nil {
		// A path error keeps the path apart from the cause, so a caller
		// can report the cause alone.
		if _, ok := errors.AsType[*fs.PathError](err); ok {
			return err
		}
		return &fs.PathError{Op: "restrict to owner", Path: dir, Err: err}
	}
	return nil
}
