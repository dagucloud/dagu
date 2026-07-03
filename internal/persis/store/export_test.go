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

// MarkDispatchIndexReconcileDueForTest ages the store index so the next
// reconciliation check is due.
func MarkDispatchIndexReconcileDueForTest(t testing.TB, s *DispatchTaskStore) {
	t.Helper()
	if s == nil {
		t.Fatal("nil dispatch task store")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index == nil {
		t.Fatal("dispatch task index is not initialized")
		return
	}
	interval := dispatchIndexReconcileInterval
	if interval <= 0 {
		s.index.reconciledAt = time.Time{}
		return
	}
	s.index.reconciledAt = time.Now().UTC().Add(-interval)
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
