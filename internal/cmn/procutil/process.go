// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package procutil

// IsAlive reports whether pid currently refers to a live OS process.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return isAlive(pid)
}
