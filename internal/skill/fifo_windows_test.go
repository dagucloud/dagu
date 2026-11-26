// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package skill_test

import "testing"

// makeFIFO skips the test: Windows has no named pipe in a folder.
func makeFIFO(t *testing.T, _ string) {
	t.Helper()
	t.Skip("Windows cannot create a named pipe in a folder")
}
