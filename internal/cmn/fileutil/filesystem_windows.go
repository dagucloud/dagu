// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package fileutil

import (
	"os"

	"golang.org/x/sys/windows"
)

func sameFileSystem(a, b string) (bool, error) {
	serialA, err := volumeSerialNumber(a)
	if err != nil {
		return false, err
	}
	serialB, err := volumeSerialNumber(b)
	if err != nil {
		return false, err
	}
	return serialA == serialB, nil
}

// volumeSerialNumber identifies the volume holding path, whatever drive
// letter, mount folder, or link reaches it.
func volumeSerialNumber(path string) (uint32, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// FILE_FLAG_BACKUP_SEMANTICS lets CreateFile open a directory.
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	return info.VolumeSerialNumber, nil
}
