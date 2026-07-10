// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package fileutil

import "os"

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}

func installFileNoReplace(source, target string) error {
	if err := os.Link(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}

func syncDir(path string) error {
	dir, err := os.Open(path) //nolint:gosec // path is an existing destination directory
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
