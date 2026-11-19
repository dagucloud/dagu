// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"strings"
	"syscall"
)

// opensDevice reports whether this Windows maps a file of this name, inside
// any directory, to a device.
func opensDevice(name string) bool {
	path, err := syscall.FullPath(`C:\dir\` + name)
	return err == nil && strings.HasPrefix(path, `\\.\`)
}

// reservesDeviceName reports whether some Windows version maps name to a
// device: CON, PRN, AUX, NUL, COM1 to COM9, LPT1 to LPT9, COM and LPT with a
// superscript 1, 2, or 3, CONIN$, or CONOUT$, in any case and with any
// extension or trailing spaces.
func reservesDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if !strings.HasPrefix(base, "COM") && !strings.HasPrefix(base, "LPT") {
		return false
	}
	switch number := base[3:]; number {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "\u00b9", "\u00b2", "\u00b3":
		return true
	}
	return false
}

// unstorableByte reports whether Windows cannot store c in a name. A
// trailing dot or space is dropped, so it counts as well.
func unstorableByte(c byte, last bool) bool {
	return c < 0x20 || strings.IndexByte(`<>:"/\|?*`, c) >= 0 || (last && (c == '.' || c == ' '))
}
