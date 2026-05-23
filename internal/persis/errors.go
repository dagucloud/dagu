// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import "errors"

// Sentinel errors returned by [Collection] methods.
// Use errors.Is for matching; backends may wrap these with additional context.
var (
	// ErrNotFound is returned by Get and Claim when no matching record exists.
	ErrNotFound = errors.New("persis: record not found")

	// ErrConflict is returned by CompareAndSwap when the current Data does not
	// match the expected value.
	ErrConflict = errors.New("persis: compare-and-swap conflict")
)
