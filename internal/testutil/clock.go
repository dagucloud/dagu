// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"testing"
	"time"
)

// WaitForClockTick returns once time.Now reads later than when it was called.
// The Windows clock moves in steps of up to about 16 ms, so two readings in a
// row can be equal.
func WaitForClockTick(t *testing.T) {
	t.Helper()
	start := time.Now()
	for !time.Now().After(start) {
		time.Sleep(time.Millisecond)
	}
}
