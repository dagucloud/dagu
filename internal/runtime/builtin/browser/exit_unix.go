// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package browser

import (
	"context"
	"errors"
	"syscall"
)

// browserProcessTree is the browser's process group: the browser started as
// its leader and the helpers started in it.
type browserProcessTree struct {
	pid int
}

// recordBrowserProcessTree identifies the process group led by pid. The
// group, not the start time, tells the browser's processes apart.
func recordBrowserProcessTree(pid int, _ int64) *browserProcessTree {
	return &browserProcessTree{pid: pid}
}

// exitEndsClose reports that the group exiting ends the close: the runtime
// may still be waiting on processes that left the group.
func (*browserProcessTree) exitEndsClose() bool {
	return true
}

// exited reports whether the browser has exited together with the helpers
// in its process group. Processes that leave the group, such as the Chrome
// crash reporter and updater, are not waited for.
func (t *browserProcessTree) exited() bool {
	if syscall.Kill(t.pid, 0) == nil {
		return false
	}
	return errors.Is(syscall.Kill(-t.pid, 0), syscall.ESRCH)
}

// awaitExit returns at once. The runtime signals the whole process group, so
// the helpers leave with the browser.
func (*browserProcessTree) awaitExit(context.Context) error {
	return nil
}

// release holds nothing to free.
func (*browserProcessTree) release() {}
