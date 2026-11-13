// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"os"
)

// AppendLines appends lines, each ended with a newline, to the file at path,
// creating it with perm. A last line that a crash cut short is ended first,
// so a reader that skips malformed lines still reads the new ones.
func AppendLines(path string, perm os.FileMode, lines ...[]byte) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, perm) //nolint:gosec // callers build path from their own layout
	if err != nil {
		return err
	}
	torn, err := endsTorn(f)
	if err != nil {
		_ = f.Close()
		return err
	}
	var buf []byte
	if torn {
		buf = append(buf, '\n')
	}
	for _, line := range lines {
		buf = append(append(buf, line...), '\n')
	}
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// endsTorn reports whether a non-empty file lacks a final newline.
func endsTorn(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}
