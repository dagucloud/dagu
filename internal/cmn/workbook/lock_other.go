// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package workbook

// isSharingViolation is always false where files have no mandatory locks.
func isSharingViolation(error) bool { return false }
