// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dispatch_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestAttemptKeyForStatus(t *testing.T) {
	t.Parallel()

	t.Run("ReconstructsLegacyRootAttemptKeyWithoutRootField", func(t *testing.T) {
		t.Parallel()

		status := &ir.DAGRunStatus{
			Name:      "root-dag",
			DAGRunID:  "run-123",
			AttemptID: "attempt-1",
		}

		assert.Equal(
			t,
			ir.GenerateAttemptKey("root-dag", "run-123", "root-dag", "run-123", "attempt-1"),
			dispatch.AttemptKeyForStatus(status, ""),
		)
	})

	t.Run("DoesNotFabricateSubDAGAttemptKeyWithoutRootField", func(t *testing.T) {
		t.Parallel()

		status := &ir.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run-123",
			Parent:    ir.NewDAGRunRef("root-dag", "run-123"),
			AttemptID: "attempt-1",
		}

		assert.Empty(t, dispatch.AttemptKeyForStatus(status, ""))
	})
}

func TestDAGRunStatusEffectiveClaimKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "attempt-key", (ir.DAGRunStatus{AttemptKey: "attempt-key"}).EffectiveClaimKey())
	assert.Equal(t, "claim-key", (ir.DAGRunStatus{
		AttemptKey: "attempt-key",
		ClaimKey:   "claim-key",
	}).EffectiveClaimKey())
}

func TestDAGRunLeaseMatchesClaim(t *testing.T) {
	t.Parallel()

	lease := &dispatch.DAGRunLease{AttemptKey: "claim-key", WorkerID: "worker-1"}
	assert.True(t, lease.MatchesClaim("claim-key", "worker-1"))
	assert.False(t, lease.MatchesClaim("other-claim", "worker-1"))
	assert.False(t, lease.MatchesClaim("claim-key", "worker-2"))
}

func TestWorkerHeartbeatFresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	threshold := 30 * time.Second
	tests := []struct {
		name              string
		lastHeartbeatTime time.Time
		threshold         time.Duration
		want              bool
	}{
		{name: "fresh", lastHeartbeatTime: now.Add(-time.Second), threshold: threshold, want: true},
		{name: "threshold boundary", lastHeartbeatTime: now.Add(-threshold), threshold: threshold, want: true},
		{name: "stale", lastHeartbeatTime: now.Add(-threshold - time.Millisecond), threshold: threshold, want: false},
		{name: "missing timestamp", threshold: threshold, want: false},
		{name: "missing threshold", lastHeartbeatTime: now, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			record := dispatch.WorkerHeartbeatRecord{}
			if !tt.lastHeartbeatTime.IsZero() {
				record.LastHeartbeatAt = tt.lastHeartbeatTime.UnixMilli()
			}
			assert.Equal(t, tt.want, dispatch.WorkerHeartbeatFresh(record, now, tt.threshold))
		})
	}
}

func TestWorkerHeartbeatMatches(t *testing.T) {
	t.Parallel()

	record := dispatch.WorkerHeartbeatRecord{
		WorkerID: "worker-1",
		Labels: map[string]string{
			"region": "east",
			"type":   "gpu",
		},
	}
	tests := []struct {
		name           string
		selector       map[string]string
		targetWorkerID string
		want           bool
	}{
		{name: "no requirements", want: true},
		{name: "matching selector", selector: map[string]string{"type": "gpu"}, want: true},
		{name: "matching target", targetWorkerID: "worker-1", want: true},
		{name: "selector mismatch", selector: map[string]string{"type": "cpu"}, want: false},
		{name: "missing label", selector: map[string]string{"pool": "batch"}, want: false},
		{name: "target mismatch", targetWorkerID: "worker-2", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, dispatch.WorkerHeartbeatMatches(record, tt.selector, tt.targetWorkerID))
		})
	}
}
