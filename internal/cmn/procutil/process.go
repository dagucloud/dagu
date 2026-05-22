// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package procutil

import "github.com/shirou/gopsutil/v4/process"

const maxPIDInt32 = 1<<31 - 1

// IsAlive reports whether pid currently refers to a live OS process.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return isAlive(pid)
}

// StartTime returns the process creation time as Unix milliseconds.
func StartTime(pid int) (int64, bool) {
	if pid <= 0 || pid > maxPIDInt32 {
		return 0, false
	}
	proc, err := process.NewProcess(int32(pid)) //nolint:gosec // pid is bounded by maxPIDInt32
	if err != nil {
		return 0, false
	}
	startedAt, err := proc.CreateTime()
	if err != nil || startedAt <= 0 {
		return 0, false
	}
	return startedAt, true
}

// MatchesStartTime reports whether pid still refers to the process that
// started at expectedStartedAt. actualStartedAt is set when lookup succeeds.
func MatchesStartTime(pid int, expectedStartedAt int64) (matched bool, actualStartedAt int64, ok bool) {
	if expectedStartedAt <= 0 {
		return false, 0, false
	}
	actualStartedAt, ok = StartTime(pid)
	if !ok {
		return false, 0, false
	}
	return actualStartedAt == expectedStartedAt, actualStartedAt, true
}
