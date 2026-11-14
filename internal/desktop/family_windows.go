// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownProcessFamily returns this process and the chain of processes that
// started it, as far as the system still lists them.
func ownProcessFamily() map[uint32]bool {
	family := map[uint32]bool{}
	pid := windows.GetCurrentProcessId()
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		family[pid] = true
		return family
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	parents := map[uint32]uint32{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parents[entry.ProcessID] = entry.ParentProcessID
	}
	// A finished ancestor's id can be reused by an unrelated process; the
	// chain is kept short so that costs at most a refused action.
	for depth := 0; pid != 0 && depth < 16 && !family[pid]; depth++ {
		family[pid] = true
		pid = parents[pid]
	}
	return family
}
