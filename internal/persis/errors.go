// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import "errors"

// Sentinel errors returned by [Collection] methods.
// Use errors.Is for matching; backends may wrap these with additional context.
var (
	// ErrNotFound is returned when a requested record does not exist.
	ErrNotFound = errors.New("persis: record not found")

	// ErrConflict is returned when a conditional mutation cannot be applied.
	ErrConflict = errors.New("persis: compare-and-swap conflict")

	// ErrCorrupt is returned when a record exists but its stored representation
	// cannot be decoded by the backend.
	ErrCorrupt = errors.New("persis: corrupt record")
)
