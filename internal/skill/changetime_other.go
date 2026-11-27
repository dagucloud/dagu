// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !darwin

package skill

import (
	"io/fs"
	"time"
)

// changeTime returns the modification time. On this platform the cache reads
// no status change time.
func changeTime(info fs.FileInfo) time.Time { return info.ModTime() }
