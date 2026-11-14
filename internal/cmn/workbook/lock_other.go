// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package workbook

// lockProbeSupported is false where opening a file says nothing about
// other processes holding it.
const lockProbeSupported = false

// isSharingViolation is always false where files have no mandatory locks.
func isSharingViolation(error) bool { return false }

// lockFileHeld is always false where no process can hold a file against
// others; a ~$ lock file there is only a hint.
func lockFileHeld(string) bool { return false }

// renameRefusedByHolder is always false where a rename over an open file
// succeeds.
func renameRefusedByHolder(string, error) bool { return false }
