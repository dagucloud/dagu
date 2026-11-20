// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package untrusted

import (
	"io/fs"
	"os"
)

// OpenWorkFile opens path as os.OpenFile does. The restricted user exists on
// Linux only.
func OpenWorkFile(_ string, path string, flags int, perm fs.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags, perm) // #nosec G304 -- the path comes from the workflow.
}
