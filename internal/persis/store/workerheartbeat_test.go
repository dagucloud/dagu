// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newWorkerHeartbeatStore(t *testing.T) *store.WorkerHeartbeatStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("worker_heartbeats")
	return store.NewWorkerHeartbeatStore(col)
}

func newRecord(workerID string) exec.WorkerHeartbeatRecord {
	return exec.WorkerHeartbeatRecord{
		WorkerID:        workerID,
		Labels:          map[string]string{"env": "test"},
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}
}

func TestWorkerHeartbeatUpsertAndGet(t *testing.T) {
	ctx := context.Background()
	s := newWorkerHeartbeatStore(t)
	rec := newRecord("worker-1")

	require.NoError(t, s.Upsert(ctx, rec))

	got, err := s.Get(ctx, "worker-1")
	require.NoError(t, err)
	assert.Equal(t, "worker-1", got.WorkerID)
	assert.Equal(t, "test", got.Labels["env"])
}

func TestWorkerHeartbeatUpsert_Overwrite(t *testing.T) {
	ctx := context.Background()
	s := newWorkerHeartbeatStore(t)
	rec := newRecord("worker-1")
	require.NoError(t, s.Upsert(ctx, rec))

	rec.Labels = map[string]string{"env": "prod"}
	require.NoError(t, s.Upsert(ctx, rec))

	got, err := s.Get(ctx, "worker-1")
	require.NoError(t, err)
	assert.Equal(t, "prod", got.Labels["env"])
}

func TestWorkerHeartbeatGet_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newWorkerHeartbeatStore(t).Get(ctx, "missing")
	assert.ErrorIs(t, err, exec.ErrWorkerHeartbeatNotFound)
}

func TestWorkerHeartbeatList(t *testing.T) {
	ctx := context.Background()
	s := newWorkerHeartbeatStore(t)

	for _, id := range []string{"w1", "w2", "w3"} {
		require.NoError(t, s.Upsert(ctx, newRecord(id)))
	}

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestWorkerHeartbeatDeleteStale(t *testing.T) {
	ctx := context.Background()
	s := newWorkerHeartbeatStore(t)

	old := exec.WorkerHeartbeatRecord{
		WorkerID:        "old-worker",
		LastHeartbeatAt: time.Now().Add(-10 * time.Minute).UTC().UnixMilli(),
	}
	fresh := exec.WorkerHeartbeatRecord{
		WorkerID:        "fresh-worker",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}
	require.NoError(t, s.Upsert(ctx, old))
	require.NoError(t, s.Upsert(ctx, fresh))

	cutoff := time.Now().Add(-5 * time.Minute)
	n, err := s.DeleteStale(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	_, err = s.Get(ctx, "old-worker")
	assert.ErrorIs(t, err, exec.ErrWorkerHeartbeatNotFound)

	_, err = s.Get(ctx, "fresh-worker")
	assert.NoError(t, err)
}

func TestDeleteStale_RefreshDuringCleanup(t *testing.T) {
	ctx := context.Background()
	s := newWorkerHeartbeatStore(t)

	// Upsert with an old heartbeat time so it would qualify for deletion.
	old := exec.WorkerHeartbeatRecord{
		WorkerID:        "refresh-worker",
		LastHeartbeatAt: time.Now().Add(-10 * time.Minute).UTC().UnixMilli(),
	}
	require.NoError(t, s.Upsert(ctx, old))

	// Simulate the worker refreshing its heartbeat before DeleteStale runs.
	refreshed := exec.WorkerHeartbeatRecord{
		WorkerID:        "refresh-worker",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}
	require.NoError(t, s.Upsert(ctx, refreshed))

	cutoff := time.Now().Add(-5 * time.Minute)
	n, err := s.DeleteStale(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "refreshed worker should not be deleted")

	got, err := s.Get(ctx, "refresh-worker")
	require.NoError(t, err)
	assert.Equal(t, "refresh-worker", got.WorkerID)
}
