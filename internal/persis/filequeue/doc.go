// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package filequeue contains the legacy filesystem queue implementation.
//
// Runtime wiring uses internal/persis/store.QueueStore. Keep this package only
// for legacy behavior coverage and compatibility checks while the persistence
// refactor finishes.
package filequeue
