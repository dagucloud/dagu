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
	// startedAt maps each process ID to its start time, or 0 when unknown.
	startedAt map[int]int64
}

func recordBrowserProcessTree(pid int) *browserProcessTree {
	tree := &browserProcessTree{startedAt: map[int]int64{}}
	children := childProcesses()
	pending := []int{pid}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if _, seen := tree.startedAt[next]; seen {
			continue
		}
		startedAt, _ := procutil.StartTime(next)
		tree.startedAt[next] = startedAt
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
// running after helperExitGrace are ended here.
func (t *browserProcessTree) awaitExit(ctx context.Context) error {
	ticker := time.NewTicker(exitPollInterval)
	defer ticker.Stop()
	endHelpers := time.After(helperExitGrace)
	timeout := time.After(helperExitTimeout)
	for {
		if t.exited() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-endHelpers:
			t.terminate()
			endHelpers = nil
		case <-timeout:
			return fmt.Errorf("browser helpers still running: %v", t.running())
		case <-ticker.C:
		}
	}
}

// terminate ends every recorded process that is still running.
func (t *browserProcessTree) terminate() {
	for _, pid := range t.running() {
		handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // process IDs come from the process snapshot and fit in uint32.
		if err != nil {
			continue
		}
		_ = windows.TerminateProcess(handle, 1)
		_ = windows.CloseHandle(handle)
	}
}
