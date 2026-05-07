// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package sock

import (
	"net"

	"golang.org/x/sys/windows"
)

// unsupportedListenErrorForTest returns a Windows unsupported-listen error.
func unsupportedListenErrorForTest() error {
	return &net.OpError{
		Op:  "listen",
		Net: "unix",
		Err: windows.WSAEAFNOSUPPORT,
	}
}
