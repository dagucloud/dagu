// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dispatch_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/stretchr/testify/assert"
)

func TestAttemptKeyForStatus(t *testing.T) {
	t.Parallel()

	t.Run("ReconstructsLegacyRootAttemptKeyWithoutRootField", func(t *testing.T) {
		t.Parallel()

		status := &dagrun.DAGRunStatus{
			Name:      "root-dag",
			DAGRunID:  "run-123",
			AttemptID: "attempt-1",
		}

		assert.Equal(
			t,
			dagrun.GenerateAttemptKey("root-dag", "run-123", "root-dag", "run-123", "attempt-1"),
			dispatch.AttemptKeyForStatus(status, ""),
		)
	})

	t.Run("DoesNotFabricateSubDAGAttemptKeyWithoutRootField", func(t *testing.T) {
		t.Parallel()

		status := &dagrun.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run-123",
			Parent:    dagrun.NewDAGRunRef("root-dag", "run-123"),
			AttemptID: "attempt-1",
		}

		assert.Empty(t, dispatch.AttemptKeyForStatus(status, ""))
	})
}

func TestDAGRunStatusEffectiveClaimKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "attempt-key", (dagrun.DAGRunStatus{AttemptKey: "attempt-key"}).EffectiveClaimKey())
	assert.Equal(t, "claim-key", (dagrun.DAGRunStatus{
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
