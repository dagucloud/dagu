// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package skill

import (
	"io/fs"

	"golang.org/x/sys/windows"
)

// linkCount returns the number of hard links of the regular file at path.
func linkCount(path string, _ fs.FileInfo) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	// The handle reads attributes only, so it never blocks a writer that holds
	// or replaces the file. It opens a link that replaced the file, never its
	// target.
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 0, &fs.PathError{Op: "stat", Path: path, Err: err}
	}
	return uint64(info.NumberOfLinks), nil
}
