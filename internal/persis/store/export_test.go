// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
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

// AgeDispatchClaimForTest moves a claim far enough into the past to make
// reservation cleanup treat it as expired.
func AgeDispatchClaimForTest(t testing.TB, s *DispatchTaskStore, claimToken string, age time.Duration) {
	t.Helper()
	if s == nil {
		t.Fatal("nil dispatch task store")
		return
	}
	agedAt := time.Now().UTC().Add(-age)

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Get(context.Background(), claimDispatchRecordID(claimToken))
	if err != nil {
		t.Fatalf("get dispatch claim record: %v", err)
	}
	payload, err := dispatchTaskPayloadFromRecord(rec)
	if err != nil {
		t.Fatalf("decode dispatch claim record: %v", err)
	}
	payload.ClaimedAt = agedAt.UnixMilli()

	agedRec, err := s.newDispatchRecord(rec.ID, payload, rec.CreatedAt, agedAt)
	if err != nil {
		t.Fatalf("encode aged dispatch claim record: %v", err)
	}
	if err := s.col.Put(context.Background(), agedRec); err != nil {
		t.Fatalf("put aged dispatch claim record: %v", err)
	}
	if s.index != nil {
		s.index.addClaim(agedRec, payload)
	}
}
