// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDistributedAttemptOwnershipStatusDecision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("accepts active status from same attempt", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&exec.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: core.Running},
			&exec.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: core.Running},
		)

		assert.True(t, accepted)
		assert.Empty(t, reason)
	})

	t.Run("rejects superseded attempt", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&exec.DAGRunStatus{AttemptID: "attempt-2", AttemptKey: "attempt-key-2", Status: core.Running},
			&exec.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: core.Running},
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
			&exec.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: core.Failed},
			&exec.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: core.Running},
		)

		assert.False(t, accepted)
		assert.Equal(t, remoteAttemptRejectedLeaseInactive, reason)
	})

	t.Run("accepts duplicate terminal status", func(t *testing.T) {
		t.Parallel()

		ownership := newAttemptOwnership(attemptOwnershipConfig{})
		accepted, reason := ownership.statusDecision(ctx,
			&exec.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: core.Succeeded},
			&exec.DAGRunStatus{AttemptID: "attempt-1", AttemptKey: "attempt-key-1", Status: core.Succeeded},
		)

		assert.True(t, accepted)
		assert.Empty(t, reason)
	})
}

func TestDistributedAttemptOwnershipSyncFromStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "distributed")
	leaseStore := newTestDAGRunLeaseStore(baseDir)
	activeStore := newTestActiveDistributedRunStore(baseDir)

	oldTime := time.Unix(90, 0).UTC()
	now := time.Unix(100, 0).UTC()
	ownership := newAttemptOwnership(attemptOwnershipConfig{
		Owner:               exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		LeaseStore:          leaseStore,
		ActiveRunStore:      activeStore,
		StaleLeaseThreshold: time.Minute,
		Now:                 func() time.Time { return now },
	})

	run := exec.NewDAGRunRef("test-dag", "run-1")
	require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
		AttemptKey:      "attempt-key-1",
		DAGRun:          run,
		Root:            run,
		AttemptID:       "attempt-1",
		QueueName:       "existing-queue",
		WorkerID:        "worker-1",
		Owner:           exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		ClaimedAt:       oldTime.UnixMilli(),
		LastHeartbeatAt: oldTime.UnixMilli(),
	}))

	status := &exec.DAGRunStatus{
		Name:       run.Name,
		DAGRunID:   run.ID,
		Root:       run,
		AttemptID:  "attempt-1",
		AttemptKey: "attempt-key-1",
		Status:     core.Running,
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
	assert.Equal(t, "coord-a", lease.Owner.ID)

	record, err := activeStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, run, record.DAGRun)
	assert.Equal(t, run, record.Root)
	assert.Equal(t, "attempt-1", record.AttemptID)
	assert.Equal(t, "worker-1", record.WorkerID)
	assert.Equal(t, core.Running, record.Status)
	assert.GreaterOrEqual(t, record.UpdatedAt, activeUpdatedLowerBound)
	assert.LessOrEqual(t, record.UpdatedAt, activeUpdatedUpperBound)

	status.Status = core.Queued
	activeUpdatedLowerBound = time.Now().UTC().UnixMilli()
	ownership.syncFromStatus(ctx, "worker-1", status, "")
	activeUpdatedUpperBound = time.Now().UTC().UnixMilli()

	lease, err = leaseStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, now.UnixMilli(), lease.LastHeartbeatAt)
	record, err = activeStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, core.Queued, record.Status)
	assert.GreaterOrEqual(t, record.UpdatedAt, activeUpdatedLowerBound)
	assert.LessOrEqual(t, record.UpdatedAt, activeUpdatedUpperBound)

	status.Status = core.Succeeded
	ownership.syncFromStatus(ctx, "worker-1", status, "")

	_, err = leaseStore.Get(ctx, "attempt-key-1")
	assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)
	_, err = activeStore.Get(ctx, "attempt-key-1")
	assert.ErrorIs(t, err, exec.ErrActiveRunNotFound)
}

func TestDistributedAttemptOwnershipTaskClaimTracking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := filepath.Join(t.TempDir(), "distributed")
	leaseStore := newTestDAGRunLeaseStore(baseDir)
	activeStore := newTestActiveDistributedRunStore(baseDir)
	clockCalls := 0
	now := time.Unix(100, 0).UTC()

	ownership := newAttemptOwnership(attemptOwnershipConfig{
		Owner:          exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		LeaseStore:     leaseStore,
		ActiveRunStore: activeStore,
		Now: func() time.Time {
			clockCalls++
			return now.Add(time.Duration(clockCalls-1) * time.Second)
		},
	})

	task := &coordinatorv1.Task{
		Target:     "test-dag",
		DagRunId:   "run-1",
		AttemptId:  "attempt-1",
		AttemptKey: "attempt-key-1",
	}
	activeUpdatedLowerBound := time.Now().UTC().UnixMilli()
	require.NoError(t, ownership.recordTaskClaim(ctx, task, "worker-1"))
	activeUpdatedUpperBound := time.Now().UTC().UnixMilli()
	assert.Equal(t, 1, clockCalls)

	lease, err := leaseStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, "attempt-key-1", lease.AttemptKey)
	assert.Equal(t, exec.NewDAGRunRef("test-dag", "run-1"), lease.DAGRun)
	assert.Equal(t, exec.NewDAGRunRef("test-dag", "run-1"), lease.Root)
	assert.Equal(t, "test-dag", lease.QueueName)
	assert.Equal(t, "worker-1", lease.WorkerID)
	assert.Equal(t, "coord-a", lease.Owner.ID)
	assert.Equal(t, now.UnixMilli(), lease.ClaimedAt)
	assert.Equal(t, now.UnixMilli(), lease.LastHeartbeatAt)

	record, err := activeStore.Get(ctx, "attempt-key-1")
	require.NoError(t, err)
	assert.Equal(t, exec.NewDAGRunRef("test-dag", "run-1"), record.DAGRun)
	assert.Equal(t, exec.NewDAGRunRef("test-dag", "run-1"), record.Root)
	assert.Equal(t, "attempt-1", record.AttemptID)
	assert.Equal(t, "worker-1", record.WorkerID)
	assert.Equal(t, core.Queued, record.Status)
	assert.GreaterOrEqual(t, record.UpdatedAt, activeUpdatedLowerBound)
	assert.LessOrEqual(t, record.UpdatedAt, activeUpdatedUpperBound)
}
