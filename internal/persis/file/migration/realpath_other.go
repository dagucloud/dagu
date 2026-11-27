// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package migration

import "path/filepath"

// realPath returns the path of the directory or file at path once every
// link on the way to it is followed.
func realPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
