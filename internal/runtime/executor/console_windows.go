// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package executor

import (
	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding"
)

var procGetOEMCP = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetOEMCP")

// consoleEncoding returns the encoding console programs started by this
// process write their output in, or nil when it is UTF-8 or unknown.
func consoleEncoding() encoding.Encoding {
	// Child processes inherit this process's console. Without one, they get a
	// new console that uses the OEM code page.
	if codePage, err := windows.GetConsoleOutputCP(); err == nil && codePage != 0 {
		return encodingForCodePage(codePage)
	}
	codePage, _, _ := procGetOEMCP.Call()
	return encodingForCodePage(uint32(codePage)) //nolint:gosec // GetOEMCP returns a UINT
}
