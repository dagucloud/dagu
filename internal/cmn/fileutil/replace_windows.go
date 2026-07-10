// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package fileutil

import (
	"os"

	"golang.org/x/sys/windows"
)

func replaceFile(source, target string) error {
	return moveFile(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func installFileNoReplace(source, target string) error {
	return moveFile(source, target, windows.MOVEFILE_WRITE_THROUGH)
}

func moveFile(source, target string, flags uint32) error {
	sourcePath, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
	}
	targetPath, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
	}

	if err := windows.MoveFileEx(sourcePath, targetPath, flags); err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
	}
	return nil
}

func syncDir(string) error {
	// MoveFileEx with MOVEFILE_WRITE_THROUGH provides the durable rename
	// behavior available through the Windows API.
	return nil
}
