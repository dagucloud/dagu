// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package sock

import (
	"errors"
	"syscall"
)

// isUnsupportedListenError reports whether AF_UNIX is unavailable.
func isUnsupportedListenError(err error) bool {
	return errors.Is(err, syscall.EAFNOSUPPORT) ||
		errors.Is(err, syscall.EPROTONOSUPPORT)
}
