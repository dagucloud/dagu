// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "time"

const (
	// DefaultWatchInterval is how often a watched folder is read when the
	// DAG names no interval.
	DefaultWatchInterval = 10 * time.Second
	// WatchFileParam is the runtime param carrying the path of the file
	// that started a run.
	WatchFileParam = "WATCH_FILE"
	// WatchNameParam is the runtime param carrying that file's name.
	WatchNameParam = "WATCH_NAME"
)
