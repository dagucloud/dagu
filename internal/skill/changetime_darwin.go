// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"io/fs"
	"syscall"
	"time"
)

// changeTime returns the time of the last status change of a file. No call of
// a user sets it, unlike the modification time.
func changeTime(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(0, st.Ctimespec.Nano())
	}
	return info.ModTime()
}
