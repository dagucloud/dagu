// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

func NormalizeLongWindowsDirForTest(dir string) string {
	return normalizeLongWindowsDir(dir)
}

func KillProcessTreeForTest(pid uint32) error {
	return killProcessTree(pid)
}
