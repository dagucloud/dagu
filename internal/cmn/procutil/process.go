// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package procutil

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
	return startTime(pid)
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
