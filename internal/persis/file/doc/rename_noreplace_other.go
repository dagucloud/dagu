// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux && !windows

package doc

func renameNoReplace(oldPath, newPath string) error {
	return renameNoReplaceFallback(oldPath, newPath)
}
