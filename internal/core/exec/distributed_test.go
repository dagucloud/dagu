// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
)

func TestAttemptKeyForStatus(t *testing.T) {
	t.Parallel()

	t.Run("ReconstructsLegacyRootAttemptKeyWithoutRootField", func(t *testing.T) {
		t.Parallel()

		status := &exec.DAGRunStatus{
			Name:      "root-dag",
			DAGRunID:  "run-123",
			AttemptID: "attempt-1",
		}

		assert.Equal(
			t,
			exec.GenerateAttemptKey("root-dag", "run-123", "root-dag", "run-123", "attempt-1"),
			exec.AttemptKeyForStatus(status, ""),
		)
	})

	t.Run("DoesNotFabricateSubDAGAttemptKeyWithoutRootField", func(t *testing.T) {
		t.Parallel()

		status := &exec.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run-123",
			Parent:    exec.NewDAGRunRef("root-dag", "run-123"),
			AttemptID: "attempt-1",
		}

		assert.Empty(t, exec.AttemptKeyForStatus(status, ""))
	})
}

func TestDistributedTrackingEffectiveLivenessAttemptKey(t *testing.T) {
	t.Parallel()

	t.Run("Lease", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "attempt-key", (exec.DAGRunLease{AttemptKey: "attempt-key"}).EffectiveLivenessAttemptKey())
		assert.Equal(t, "owner-key", (exec.DAGRunLease{
			AttemptKey:         "attempt-key",
			LivenessAttemptKey: "owner-key",
		}).EffectiveLivenessAttemptKey())
	})

	t.Run("ActiveRun", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "attempt-key", (exec.ActiveDistributedRun{AttemptKey: "attempt-key"}).EffectiveLivenessAttemptKey())
		assert.Equal(t, "owner-key", (exec.ActiveDistributedRun{
			AttemptKey:         "attempt-key",
			LivenessAttemptKey: "owner-key",
		}).EffectiveLivenessAttemptKey())
	})
}
