// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentsession

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persistestutil "github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupQueueRetainsWorkForOwningHost(t *testing.T) {
	t.Parallel()

	backend := persistestutil.NewMemoryBackend()
	collection, ok := backend.Collection("cleanups").(persis.LockingCollection)
	require.True(t, ok)
	queue := NewCleanupQueue(collection)
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	root := ir.NewDAGRunRef("build", "run-1")
	resource := ir.AgentSessionResource{
		Provider: "opencode", SessionID: "session-1", Directory: "/workspace", OwnerWorkerID: "worker-a",
	}

	require.NoError(t, queue.EnqueueDAGRunRemoval(t.Context(), root, []ir.AgentSessionResource{resource}))
	require.NoError(t, queue.EnqueueDAGRunRemoval(t.Context(), root, []ir.AgentSessionResource{resource}))
	_, err := queue.Claim(t.Context(), "worker-b", time.Minute)
	require.ErrorIs(t, err, persis.ErrNotFound)

	job, err := queue.Claim(t.Context(), "worker-a", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, resource, job.Resource)
	require.NoError(t, queue.Release(t.Context(), "worker-a", job.ID, job.ClaimToken, "host offline"))

	_, err = queue.Claim(t.Context(), "worker-a", time.Minute)
	require.ErrorIs(t, err, persis.ErrNotFound)
	now = now.Add(2 * time.Minute)
	job, err = queue.Claim(t.Context(), "worker-a", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 1, job.Attempts)
	require.NoError(t, queue.Complete(t.Context(), "worker-a", job.ID, job.ClaimToken))

	_, err = queue.Claim(t.Context(), "worker-a", time.Minute)
	require.ErrorIs(t, err, persis.ErrNotFound)
}

func TestCleanupQueueTransfersAbandonedWork(t *testing.T) {
	t.Parallel()

	backend := persistestutil.NewMemoryBackend()
	collection, ok := backend.Collection("cleanups").(persis.LockingCollection)
	require.True(t, ok)
	queue := NewCleanupQueue(collection)
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	root := ir.NewDAGRunRef("build", "run-1")
	resource := ir.AgentSessionResource{Provider: "opencode", SessionID: "session-1", OwnerWorkerID: "worker-a"}
	require.NoError(t, queue.EnqueueDAGRunRemoval(t.Context(), root, []ir.AgentSessionResource{resource}))

	_, err := queue.Claim(t.Context(), "worker-b", time.Minute)
	require.ErrorIs(t, err, persis.ErrNotFound)
	now = now.Add(cleanupOwnerAffinityTTL + time.Second)
	job, err := queue.Claim(t.Context(), "worker-b", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "worker-b", job.ClaimedBy)
	require.ErrorIs(t, queue.Complete(t.Context(), "worker-a", job.ID, job.ClaimToken), persis.ErrConflict)
	require.NoError(t, queue.Complete(t.Context(), "worker-b", job.ID, job.ClaimToken))
}

func TestCleanupQueueRejectsUnconfiguredClaimUpdates(t *testing.T) {
	t.Parallel()

	var queue *CleanupQueue
	require.Error(t, queue.Complete(t.Context(), "worker-a", "job-1", "claim-1"))
	require.Error(t, queue.Release(t.Context(), "worker-a", "job-1", "claim-1", "failed"))
}

func TestProcessNextCleanupWaitsForDAGRunRemoval(t *testing.T) {
	t.Parallel()

	backend := persistestutil.NewMemoryBackend()
	collection, ok := backend.Collection("cleanups").(persis.LockingCollection)
	require.True(t, ok)
	queue := NewCleanupQueue(collection)
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	root := ir.NewDAGRunRef("build", "run-1")
	resource := ir.AgentSessionResource{Provider: "opencode", SessionID: "session-1"}
	require.NoError(t, queue.EnqueueDAGRunRemoval(t.Context(), root, []ir.AgentSessionResource{resource}))

	store := &cleanupDAGRunStore{}
	repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
	deleted := []ir.AgentSessionResource{}
	deleteSession := func(_ context.Context, resource ir.AgentSessionResource) error {
		deleted = append(deleted, resource)
		return nil
	}

	processed, err := ProcessNextCleanup(t.Context(), "local", queue, repository, deleteSession)
	require.NoError(t, err)
	assert.True(t, processed)
	assert.Empty(t, deleted)

	now = now.Add(2 * time.Minute)
	store.findErr = dagrun.ErrDAGRunIDNotFound
	processed, err = ProcessNextCleanup(t.Context(), "local", queue, repository, deleteSession)
	require.NoError(t, err)
	assert.True(t, processed)
	assert.Equal(t, []ir.AgentSessionResource{resource}, deleted)
	_, err = queue.Claim(t.Context(), "local", time.Minute)
	require.ErrorIs(t, err, persis.ErrNotFound)
}

type cleanupDAGRunStore struct {
	testutil.DAGRunStoreStub
	findErr error
}

func (s *cleanupDAGRunStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return dagrun.NewNoopAttempt("attempt-1", nil), nil
}
