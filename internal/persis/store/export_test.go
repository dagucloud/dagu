// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"testing"
	"time"
)

// SetDispatchIndexReconcileIntervalForTest overrides dispatch index reconciliation timing.
func SetDispatchIndexReconcileIntervalForTest(t testing.TB, interval time.Duration) {
	t.Helper()
	previous := dispatchIndexReconcileInterval
	dispatchIndexReconcileInterval = interval
	t.Cleanup(func() {
		dispatchIndexReconcileInterval = previous
	})
}

// DispatchNoMatchCacheSizeForTest reports the indexed no-match cache size.
func DispatchNoMatchCacheSizeForTest(s *DispatchTaskStore) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index == nil {
		return 0
	}
	return len(s.index.noMatch)
}
