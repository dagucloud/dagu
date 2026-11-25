// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/shirou/gopsutil/v4/disk"
)

// SameFileSystem reports whether a and b are on one file system. A path
// that does not exist yet is judged by its nearest existing ancestor, the
// file system it would be created on.
func SameFileSystem(a, b string) (bool, error) {
	existingA, err := existingAncestor(a)
	if err != nil {
		return false, err
	}
	existingB, err := existingAncestor(b)
	if err != nil {
		return false, err
	}
	return sameFileSystem(existingA, existingB)
}

// FreeSpace returns the bytes free for an unprivileged writer on the file
// system that holds path, or would hold it once created.
func FreeSpace(path string) (uint64, error) {
	existing, err := existingAncestor(path)
	if err != nil {
		return 0, err
	}
	usage, err := disk.Usage(existing)
	if err != nil {
		return 0, err
	}
	return usage.Free, nil
}

func existingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		_, err := os.Stat(current)
		if err == nil {
			return current, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		current = parent
	}
}
