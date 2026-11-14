// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package workpath resolves the file paths an action reads or writes.
package workpath

import (
	"os"
	"path/filepath"
)

// Resolve returns path as the action should open it. A relative path names a
// file under workDir, the step's working directory. An absolute path, a
// rooted or drive-relative Windows path, an empty path, or any path when
// workDir is empty is returned unchanged.
func Resolve(workDir, path string) string {
	if path == "" || workDir == "" || filepath.IsAbs(path) ||
		os.IsPathSeparator(path[0]) || filepath.VolumeName(path) != "" {
		return path
	}
	return filepath.Join(workDir, path)
}
