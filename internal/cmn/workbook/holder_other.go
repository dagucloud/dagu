// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin

package workbook

// workbookHeld is always false where no process probe for Excel exists.
func workbookHeld(string) bool { return false }
