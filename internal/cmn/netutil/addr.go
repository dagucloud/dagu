// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package netutil holds helpers for network errors.
package netutil

import (
	"errors"
	"runtime"
	"syscall"
)

// wsaEAddrInUse is the error Windows reports for an address already in use.
const wsaEAddrInUse = syscall.Errno(10048)

// IsAddrInUse reports whether err comes from binding an address already in
// use. Windows reports WSAEADDRINUSE, which syscall.EADDRINUSE does not match
// there.
func IsAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	return runtime.GOOS == "windows" && errors.Is(err, wsaEAddrInUse)
}
