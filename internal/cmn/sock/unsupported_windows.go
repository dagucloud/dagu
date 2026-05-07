// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package sock

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isUnsupportedListenError(err error) bool {
	return errors.Is(err, windows.WSAEAFNOSUPPORT) ||
		errors.Is(err, windows.WSAEPROTONOSUPPORT)
}
