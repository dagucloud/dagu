// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttemptOwnershipStatusDecision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("accepts active status from same attempt", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Running},
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Running},
			statusDecisionOptions{},
		)

		assert.True(t, accepted)
		assert.Empty(t, reason)
	})

	t.Run("rejects superseded attempt", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&ir.DAGRunStatus{AttemptID: "attempt-2", AttemptKey: "attempt-key-2", Status: ir.Running},
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Running},
			statusDecisionOptions{},
		)

		assert.False(t, accepted)
		assert.Equal(t, remoteAttemptRejectedSuperseded, reason)
	})

	t.Run("rejects active update after terminal status when lease is gone", func(t *testing.T) {
		t.Parallel()

		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		ownership := newAttemptOwnership(attemptOwnershipConfig{
			LeaseStore:          leaseStore,
			StaleLeaseThreshold: time.Minute,
			Now:                 func() time.Time { return time.Unix(100, 0).UTC() },
		})

		accepted, reason := ownership.statusDecision(ctx,
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Failed},
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Running},
			statusDecisionOptions{},
		)

		assert.False(t, accepted)
		assert.Equal(t, remoteAttemptRejectedLeaseInactive, reason)
	})

	t.Run("accepts duplicate terminal status", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Succeeded},
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Succeeded},
			statusDecisionOptions{},
		)

		assert.True(t, accepted)
		assert.Empty(t, reason)
	})

	t.Run("rejects terminal change without cancellation request", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Failed},
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Aborted},
			statusDecisionOptions{},
		)

		assert.False(t, accepted)
		assert.Equal(t, remoteAttemptRejectedTerminal, reason)
	})

	t.Run("accepts cancellation terminal status after lease failure", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Failed},
			&ir.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: ir.Aborted},
			statusDecisionOptions{CancellationRequested: true},
		)

		assert.True(t, accepted)
		assert.Empty(t, reason)
	})
}

func TestAttemptOwnershipSyncFromStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "distributed")
	leaseStore := newTestDAGRunLeaseStore(baseDir)
	activeStore := newTestActiveDistributedRunStore(baseDir)

	oldTime := time.Unix(90, 0).UTC()
	now := time.Unix(100, 0).UTC()
	ownership := newAttemptOwnership(attemptOwnershipConfig{
		Owner:               dispatch.CoordinatorEndpoint{ID: "coord-b", Host: "127.0.0.1", Port: 1234},
		LeaseStore:          leaseStore,
		ActiveRunStore:      activeStore,
		StaleLeaseThreshold: time.Minute,
		Now:                 func() time.Time { return now },
	})

	run := ir.NewDAGRunRef("test-dag", "run-1")
	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey:      "attempt-key-1",
		DAGRun:          run,
		Root:            run,
		AttemptID:       "attempt-1",
		QueueName:       "existing-queue",
		WorkerID:        "worker-1",
		Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		ClaimedAt:       oldTime.UnixMilli(),
		LastHeartbeatAt: oldTime.UnixMilli(),
	}))

	status := &ir.DAGRunStatus{
		Name:       run.Name,
		DAGRunID:   run.ID,
		Root:       run,
		AttemptID:  "attempt-1",
		AttemptKey: "attempt-key-1",
		Status:     ir.Running,
		WorkerID:   "worker-1",
	}
	activeUpdatedLowerBound := time.Now().UTC().UnixMilli()
	ownership.syncFromStatus(ctx, "", status, "")
	activeUpdatedUpperBound := time.Now().UTC().UnixMilli()

	lease, err := leaseStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, oldTime.UnixMilli(), lease.ClaimedAt)
	assert.Equal(t, now.UnixMilli(), lease.LastHeartbeatAt)
	assert.Equal(t, "existing-queue", lease.QueueName)
	assert.Equal(t, "worker-1", lease.WorkerID)
	assert.Equal(t, dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234}, lease.Owner)

	record, err := activeStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, run, record.DAGRun)
	assert.Equal(t, run, record.Root)
	assert.Equal(t, "attempt-1", record.AttemptID)
	assert.Equal(t, "worker-1", record.WorkerID)
	assert.Equal(t, ir.Running, record.Status)
	assert.GreaterOrEqual(t, record.UpdatedAt, activeUpdatedLowerBound)
	assert.LessOrEqual(t, record.UpdatedAt, activeUpdatedUpperBound)

	status.Status = ir.Queued
	activeUpdatedLowerBound = time.Now().UTC().UnixMilli()
	ownership.syncFromStatus(ctx, "worker-1", status, "")
	activeUpdatedUpperBound = time.Now().UTC().UnixMilli()

	lease, err = leaseStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, now.UnixMilli(), lease.LastHeartbeatAt)
	record, err = activeStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, record.Status)
	assert.GreaterOrEqual(t, record.UpdatedAt, activeUpdatedLowerBound)
	assert.LessOrEqual(t, record.UpdatedAt, activeUpdatedUpperBound)

	status.Status = ir.Succeeded
	ownership.syncFromStatus(ctx, "worker-1", status, "")

	_, err = leaseStore.Get(ctx, "attempt-key-1")
	assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
	_, err = activeStore.Get(ctx, "attempt-key-1")
	assert.ErrorIs(t, err, dispatch.ErrActiveRunNotFound)
}

func TestInlineRunSharesClaimLease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "distributed")
	leaseStore := newTestDAGRunLeaseStore(baseDir)
	activeStore := newTestActiveDistributedRunStore(baseDir)
	ownership := newAttemptOwnership(attemptOwnershipConfig{
		LeaseStore:     leaseStore,
		ActiveRunStore: activeStore,
	})

	require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
		AttemptKey:      "claim-key",
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))
	status := &ir.DAGRunStatus{
		Name:       "child",
		DAGRunID:   "child-run",
		AttemptID:  "child-attempt",
		AttemptKey: "child-key",
		ClaimKey:   "claim-key",
		WorkerID:   "worker-1",
		Status:     ir.Running,
	}

	ownership.syncFromStatus(ctx, "worker-1", status, "")

	lease, err := leaseStore.Get(ctx, "claim-key")
	require.NoError(t, err)
	assert.True(t, lease.MatchesClaim(status.EffectiveClaimKey(), status.WorkerID))
	_, err = activeStore.Get(ctx, "child-key")
	require.NoError(t, err)
}

func TestAttemptOwnershipTaskClaimTracking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "distributed")
	leaseStore := newTestDAGRunLeaseStore(baseDir)
	activeStore := newTestActiveDistributedRunStore(baseDir)
	clockCalls := 0
	now := time.Unix(100, 0).UTC()

	ownership := newAttemptOwnership(attemptOwnershipConfig{
		Owner:          dispatch.CoordinatorEndpoint{ID: "coord-b", Host: "127.0.0.1", Port: 1234},
		LeaseStore:     leaseStore,
		ActiveRunStore: activeStore,
		Now: func() time.Time {
			clockCalls++
			return now.Add(time.Duration(clockCalls-1) * time.Second)
		},
	})

	task := &coordinatorv1.Task{
		Target:                "test-dag",
		DagRunId:              "run-1",
		AttemptId:             "attempt-1",
		AttemptKey:            "attempt-key-1",
		WorkspaceBundleDigest: "bundle-digest",
		OwnerCoordinatorId:    "coord-a",
		OwnerCoordinatorHost:  "127.0.0.1",
		OwnerCoordinatorPort:  1234,
	}
	activeUpdatedLowerBound := time.Now().UTC().UnixMilli()
	require.NoError(t, ownership.recordTaskClaim(ctx, task, "worker-1"))
	activeUpdatedUpperBound := time.Now().UTC().UnixMilli()
	assert.Equal(t, 1, clockCalls)

	lease, err := leaseStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, "attempt-key-1", lease.AttemptKey)
	assert.Equal(t, ir.NewDAGRunRef("test-dag", "run-1"), lease.DAGRun)
	assert.Equal(t, ir.NewDAGRunRef("test-dag", "run-1"), lease.Root)
	assert.Equal(t, "test-dag", lease.QueueName)
	assert.Equal(t, "worker-1", lease.WorkerID)
	assert.Equal(t, task.WorkspaceBundleDigest, lease.WorkspaceBundleDigest)
	assert.Equal(t, dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234}, lease.Owner)
	assert.Equal(t, now.UnixMilli(), lease.ClaimedAt)
	assert.Equal(t, now.UnixMilli(), lease.LastHeartbeatAt)

	record, err := activeStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, ir.NewDAGRunRef("test-dag", "run-1"), record.DAGRun)
	assert.Equal(t, ir.NewDAGRunRef("test-dag", "run-1"), record.Root)
	assert.Equal(t, "attempt-1", record.AttemptID)
	assert.Equal(t, "worker-1", record.WorkerID)
	assert.Equal(t, ir.Queued, record.Status)
	assert.GreaterOrEqual(t, record.UpdatedAt, activeUpdatedLowerBound)
	assert.LessOrEqual(t, record.UpdatedAt, activeUpdatedUpperBound)
}

func TestAttemptOwnershipTaskClaimFallsBackToConfiguredOwner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "distributed")
	leaseStore := newTestDAGRunLeaseStore(baseDir)
	owner := dispatch.CoordinatorEndpoint{ID: "coord-b", Host: "127.0.0.1", Port: 1234}
	ownership := newAttemptOwnership(attemptOwnershipConfig{
		Owner:      owner,
		LeaseStore: leaseStore,
	})
	task := &coordinatorv1.Task{
		Target:             "test-dag",
		DagRunId:           "run-1",
		AttemptId:          "attempt-1",
		AttemptKey:         "attempt-key-1",
		OwnerCoordinatorId: "coord-a",
	}

	require.NoError(t, ownership.recordTaskClaim(ctx, task, "worker-1"))
	lease, err := leaseStore.Get(ctx, task.AttemptKey)
	require.NoError(t, err)
	assert.Equal(t, owner, lease.Owner)
}
