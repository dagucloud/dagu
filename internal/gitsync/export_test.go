// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gitsync

func SafeReadFileWithinBaseForTest(baseDir, targetPath string) ([]byte, error) {
	return safeReadFileWithinBase(baseDir, targetPath)
}
