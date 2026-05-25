// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package file

import "os"

func replaceFileOnce(src, dst string) error {
	return os.Rename(src, dst)
}
