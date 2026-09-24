// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package browser

import (
	"errors"
	"syscall"
)

// browserExited reports whether the browser started as process pid has
// exited together with the helpers in its process group. Processes that
// leave the group, such as the Chrome crash reporter and updater, are not
// waited for.
func browserExited(pid int) bool {
	if syscall.Kill(pid, 0) == nil {
		return false
	}
	return errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}
