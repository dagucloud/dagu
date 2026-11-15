// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"strings"
	"syscall"
)

// opensDevice reports whether Windows maps a file of this name, inside any
// directory, to a device. Windows 11 maps only NUL there; older versions map
// every reserved name.
func opensDevice(name string) bool {
	path, err := syscall.FullPath(`C:\dir\` + name)
	return err == nil && strings.HasPrefix(path, `\\.\`)
}
