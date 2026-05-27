// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filegithubdispatch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_ListMissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()

	store := New(filepath.Join(t.TempDir(), "tracker"))

	jobs, err := store.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

func TestStore_UpsertListDelete(t *testing.T) {
	t.Parallel()

	store := New(filepath.Join(t.TempDir(), "tracker"))
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)

	require.NoError(t, store.Upsert(t.Context(), TrackedJob{
		JobID:     "job-2",
		DAGName:   "deploy.yaml",
		DAGRunID:  "run-2",
		Phase:     "accepted",
		UpdatedAt: now,
	}))
	require.NoError(t, store.Upsert(t.Context(), TrackedJob{
		JobID:     "job-1",
		DAGName:   "ci.yaml",
		DAGRunID:  "run-1",
		Phase:     "pending_accept",
		UpdatedAt: now,
	}))
	require.NoError(t, store.Upsert(t.Context(), TrackedJob{
		JobID:     "job-1",
		DAGName:   "ci.yaml",
		DAGRunID:  "run-1",
		Phase:     "accepted",
		UpdatedAt: now.Add(time.Minute),
	}))

	jobs, err := store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	assert.Equal(t, []TrackedJob{
		{JobID: "job-1", DAGName: "ci.yaml", DAGRunID: "run-1", Phase: "accepted", UpdatedAt: now.Add(time.Minute)},
		{JobID: "job-2", DAGName: "deploy.yaml", DAGRunID: "run-2", Phase: "accepted", UpdatedAt: now},
	}, jobs)

	require.NoError(t, store.Delete(t.Context(), "job-1"))
	jobs, err = store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "job-2", jobs[0].JobID)
}

func TestStore_ReturnsContextError(t *testing.T) {
	t.Parallel()

	store := New(filepath.Join(t.TempDir(), "tracker"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, store.Upsert(ctx, TrackedJob{JobID: "job-1"}), context.Canceled)

	jobs, err := store.List(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, jobs)

	require.ErrorIs(t, store.Delete(ctx, "job-1"), context.Canceled)
}
