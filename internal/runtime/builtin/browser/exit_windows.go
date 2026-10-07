// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package browser

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

const (
	// helperExitGrace is how long the browser's helpers get to leave on their
	// own after the runtime has closed the browser before they are ended.
	helperExitGrace = 2 * time.Second
	// helperExitTimeout bounds the wait for the helpers after the runtime has
	// closed the browser.
	helperExitTimeout = 10 * time.Second
)

// browserProcessTree is the browser and the helpers that were running under
// it when closing began. Windows has no process group that tells the helpers
// apart, and they can keep the profile open after the browser process has
// exited, so each one is tracked by process ID and start time.
type browserProcessTree struct {
	// browser is the browser process ID, or 0 once the browser has exited.
	// The browser is waited for but never ended here, so a browser still
	// writing its profile after the runtime returns finishes on its own.
	browser int
	// startedAt maps each recorded process ID to its start time.
	startedAt map[int]int64
}

// recordBrowserProcessTree records the browser started at startedAt as
// process pid, and every process running under it. The result is nil when
// nothing can be tracked: a browser whose start time is unknown, because its
// ID cannot be told from a reused one, or an ID that a later process has
// taken. When the browser has already exited, the helpers it left behind are
// still recorded: those started under its ID after it did.
func recordBrowserProcessTree(pid int, startedAt int64) *browserProcessTree {
	if startedAt <= 0 {
		return nil
	}
	tree := &browserProcessTree{startedAt: map[int]int64{}}
	children := childProcesses()
	switch {
	case processRunning(pid, startedAt):
		tree.browser = pid
		tree.startedAt[pid] = startedAt
	case procutil.IsAlive(pid):
		return nil
	}
	pending := children[pid]
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if _, seen := tree.startedAt[next]; seen {
			continue
		}
		nextStartedAt, ok := procutil.StartTime(next)
		if !ok || nextStartedAt < startedAt {
			continue
		}
		tree.startedAt[next] = nextStartedAt
		pending = append(pending, children[next]...)
	}
	return tree
}

// childProcesses maps each process ID to the IDs of its live children.
func childProcesses() map[int][]int {
	children := map[int][]int{}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return children
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parent, child := int(entry.ParentProcessID), int(entry.ProcessID)
		children[parent] = append(children[parent], child)
	}
	return children
}

// exitEndsClose reports that the tree exiting does not end the close. The
// runtime ends the whole process tree it finds, including helpers started
// after the tree was recorded, so the close waits for it.
func (*browserProcessTree) exitEndsClose() bool {
	return false
}

// exited reports whether every recorded process has exited. A process ID
// that now belongs to a process with another start time counts as exited.
func (t *browserProcessTree) exited() bool {
	return len(t.running()) == 0
}

// running returns the recorded processes that have not exited.
func (t *browserProcessTree) running() []int {
	var running []int
	for pid, startedAt := range t.startedAt {
		if processRunning(pid, startedAt) {
			running = append(running, pid)
		}
	}
	slices.Sort(running)
	return running
}

// processRunning reports whether pid still belongs to the process that
// started at startedAt, or to any live process when startedAt is unknown.
func processRunning(pid int, startedAt int64) bool {
	if !procutil.IsAlive(pid) {
		return false
	}
	matched, _, ok := procutil.MatchesStartTime(pid, startedAt)
	return !ok || matched
}

// awaitExit waits for the recorded processes to exit after the runtime has
// closed the browser. The runtime ends the tree it finds at that moment, so
// a helper whose parent exited first can be left behind; helpers still
// running helperExitGrace after the browser has gone are ended here.
func (t *browserProcessTree) awaitExit(ctx context.Context) error {
	ticker := time.NewTicker(exitPollInterval)
	defer ticker.Stop()
	grace := time.After(helperExitGrace)
	timeout := time.After(helperExitTimeout)
	graceOver, helpersEnded := false, false
	for {
		if t.exited() {
			return nil
		}
		if graceOver && !helpersEnded && !t.browserRunning() {
			t.endHelpers()
			helpersEnded = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-grace:
			graceOver = true
			grace = nil
		case <-timeout:
			return fmt.Errorf("browser processes still running: %v", t.running())
		case <-ticker.C:
		}
	}
}

func (t *browserProcessTree) browserRunning() bool {
	return t.browser != 0 && processRunning(t.browser, t.startedAt[t.browser])
}

// endHelpers ends every recorded helper that is still running. Each process
// is checked and ended through one handle, so an ID reused between the check
// and the end cannot name another process.
func (t *browserProcessTree) endHelpers() {
	for pid, startedAt := range t.startedAt {
		if pid != t.browser {
			terminateProcess(pid, startedAt)
		}
	}
}

// terminateProcess ends process pid when it is still the one that started at
// startedAt.
func terminateProcess(pid int, startedAt int64) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // process IDs come from the process snapshot and fit in uint32.
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle) //nolint:errcheck
	if actual, ok := procutil.HandleStartTime(handle); !ok || actual != startedAt {
		return
	}
	_ = windows.TerminateProcess(handle, 1)
}
