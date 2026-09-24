// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package browser

// browserExited always reports false. Windows has no process group that
// tells the browser's helpers apart, and they keep the profile open after
// the browser process exits, so closing waits for the runtime, which ends
// the whole process tree.
func browserExited(int) bool {
	return false
}
