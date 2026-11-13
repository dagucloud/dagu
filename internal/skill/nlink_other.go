// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !unix

package skill

import "io/fs"

// linkCount returns 1: the check of hard links needs a Unix system.
func linkCount(fs.FileInfo) uint64 { return 1 }
