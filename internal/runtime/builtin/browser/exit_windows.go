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
	// own after the browser has gone before they are ended.
	helperExitGrace = 2 * time.Second
	// helperExitTimeout bounds the wait for the browser and its helpers after
	// the runtime has closed the browser.
	helperExitTimeout = 10 * time.Second
	// treeProcessAccess is the access the tree needs to a process: to read
	// its parent and start time, to wait for its exit, and to end it.
	treeProcessAccess = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.PROCESS_TERMINATE | windows.SYNCHRONIZE
)

// browserProcessTree is the browser and the helpers that were running under
// it when closing began. Windows has no process group that tells the helpers
// apart, and they can keep the profile open after the browser process has
// exited. Each process is held through an open handle, which keeps its ID
// from being reused while the tree exists, so a process checked once stays
// the same process until it is ended or released.
type browserProcessTree struct {
	members []treeProcess
}

// treeProcess is one process held by the tree.
type treeProcess struct {
	pid     int
	handle  windows.Handle
	browser bool
}

// recordBrowserProcessTree records the browser started at startedAt as
// process pid, and every process running under it. The result is nil when
// nothing can be tracked: a browser whose start time is unknown, because its
// ID cannot be told from a reused one, or an ID that a later process has
// taken. When the browser has already exited, the helpers it left behind are
// still recorded: those started under its ID after it did. The browser itself
// is waited for but never ended here, so a browser still writing its profile
// after the runtime returns finishes on its own.
func recordBrowserProcessTree(pid int, startedAt int64) *browserProcessTree {
	if startedAt <= 0 {
		return nil
	}
	tree := &browserProcessTree{}
	if handle, ok := openProcess(pid); ok {
		actual, known := procutil.HandleStartTime(handle)
		if !known || actual != startedAt {
			_ = windows.CloseHandle(handle)
			if procutil.IsAlive(pid) {
				return nil
			}
		} else {
			tree.members = append(tree.members, treeProcess{pid: pid, handle: handle, browser: true})
		}
	}
	// The snapshot only proposes candidates. Each one is verified through
	// its own handle: its parent must be a tree member and it must have
	// started no earlier than the browser.
	children := childProcesses()
	seen := map[int]bool{pid: true}
	pending := []int{pid}
	for len(pending) > 0 {
		parent := pending[0]
		pending = pending[1:]
		for _, child := range children[parent] {
			if seen[child] {
				continue
			}
			seen[child] = true
			handle, ok := openProcess(child)
			if !ok {
				continue
			}
			childStartedAt, known := procutil.HandleStartTime(handle)
			if !known || childStartedAt < startedAt || parentProcessID(handle) != parent {
				_ = windows.CloseHandle(handle)
				continue
			}
			tree.members = append(tree.members, treeProcess{pid: child, handle: handle})
			pending = append(pending, child)
		}
	}
	return tree
}

func openProcess(pid int) (windows.Handle, bool) {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return 0, false
	}
	handle, err := windows.OpenProcess(treeProcessAccess, false, uint32(pid))
	return handle, err == nil
}

// parentProcessID returns the ID of the process that started the process
// behind handle, or 0 when it cannot be read.
func parentProcessID(handle windows.Handle) int {
	var info windows.PROCESS_BASIC_INFORMATION
	var size uint32
	if err := windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), &size); err != nil {
		return 0
	}
	return int(info.InheritedFromUniqueProcessId)
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

// exited reports whether every recorded process has exited.
func (t *browserProcessTree) exited() bool {
	return len(t.running()) == 0
}

// running returns the recorded processes that have not exited.
func (t *browserProcessTree) running() []int {
	var running []int
	for _, member := range t.members {
		if procutil.HandleIsAlive(member.handle) {
			running = append(running, member.pid)
		}
	}
	slices.Sort(running)
	return running
}

func (t *browserProcessTree) browserRunning() bool {
	for _, member := range t.members {
		if member.browser && procutil.HandleIsAlive(member.handle) {
			return true
		}
	}
	return false
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

// endHelpers ends every recorded helper that is still running, through the
// handle that has held it since it was recorded.
func (t *browserProcessTree) endHelpers() {
	for _, member := range t.members {
		if !member.browser && procutil.HandleIsAlive(member.handle) {
			_ = windows.TerminateProcess(member.handle, 1)
		}
	}
}

// release closes the held process handles.
func (t *browserProcessTree) release() {
	for _, member := range t.members {
		_ = windows.CloseHandle(member.handle)
	}
	t.members = nil
}
