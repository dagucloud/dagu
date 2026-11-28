// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package procutil

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"

	"github.com/shirou/gopsutil/v4/process"
)

const maxPIDInt32 = 1<<31 - 1

func isAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return !isZombie(pid)
}

// isZombie reports a process that has exited but that its parent has not
// reaped. An orphan adopted by a PID 1 that never reaps, as in a container
// without an init process, stays a zombie and still answers signal 0. Only
// Linux is checked; elsewhere PID 1 or the OS reaps orphans.
func isZombie(pid int) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) //nolint:gosec // pid is a number.
	if err != nil {
		return false
	}
	// The state follows the command name, which can hold spaces and ")".
	end := bytes.LastIndexByte(stat, ')')
	return end >= 0 && end+2 < len(stat) && stat[end+2] == 'Z'
}

func canLookupStartTime(pid int) bool {
	return pid > 0 && pid <= maxPIDInt32
}

func startTime(pid int) (int64, bool) {
	proc, err := process.NewProcess(int32(pid)) //nolint:gosec // pid is bounded by StartTime.
	if err != nil {
		return 0, false
	}
	startedAt, err := proc.CreateTime()
	if err != nil || startedAt <= 0 {
		return 0, false
	}
	return startedAt, true
}
