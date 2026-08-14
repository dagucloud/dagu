// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"errors"
)

// Errors related to dag-run management
var (
	ErrDAGRunIDNotFound    = errors.New("dag-run ID not found")
	ErrDAGRunIDEmpty       = errors.New("dag-run ID is empty")
	ErrDAGRunAlreadyExists = errors.New("dag-run already exists")
	ErrDAGRunActive        = errors.New("dag-run is active")
	ErrNoStatusData        = errors.New("no status data")
	ErrCorruptedStatusData = errors.New("corrupted status data")
)
