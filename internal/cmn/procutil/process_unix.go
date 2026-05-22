// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package procutil

import (
	"errors"
	"syscall"

	"github.com/shirou/gopsutil/v4/process"
)

func isAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
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
