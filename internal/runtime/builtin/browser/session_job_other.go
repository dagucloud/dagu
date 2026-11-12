// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package browser

// jobWarning says why a session's browser may not outlive the command that
// opened it; only Windows job objects end it.
func jobWarning() string {
	return ""
}
