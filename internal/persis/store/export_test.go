// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"testing"
	"time"
)

// SetDispatchIndexFullValidationIntervalForTest overrides full validation timing.
func SetDispatchIndexFullValidationIntervalForTest(t testing.TB, interval time.Duration) {
	t.Helper()
	previous := dispatchIndexFullValidationInterval
	dispatchIndexFullValidationInterval = interval
	t.Cleanup(func() {
		dispatchIndexFullValidationInterval = previous
	})
}

// SetDispatchIndexValidationWindowForTest overrides fast validation timing.
func SetDispatchIndexValidationWindowForTest(t testing.TB, window time.Duration) {
	t.Helper()
	previous := dispatchIndexValidationWindow
	dispatchIndexValidationWindow = window
	t.Cleanup(func() {
		dispatchIndexValidationWindow = previous
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
