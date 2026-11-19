// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !unix && !windows

package skill

import "io/fs"

// linkCount returns 1: the platform gives no link count of a file.
func linkCount(string, fs.FileInfo) (uint64, error) { return 1, nil }
