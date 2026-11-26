// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newWorkerNodeStore(t *testing.T) *store.WorkerNodeStore {
	t.Helper()
	s, err := store.NewWorkerNodeStore(testutil.NewMemoryBackend().Collection(persis.CollectionWorkerNodes), nil)
	require.NoError(t, err)
	return s
}

func workerNode(name string, online bool, labels ...string) inventory.WorkerNode {
	return inventory.WorkerNode{
		Name:       name,
		Host:       name + ".internal",
		Labels:     ir.NewLabels(labels),
		Online:     online,
		LastSeenAt: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC),
	}
}

func TestWorkerNodeStore_RecordAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newWorkerNodeStore(t)

	require.NoError(t, s.Record(ctx, workerNode("tokyo-gw", true, "web", "site=tokyo")))
	require.NoError(t, s.Record(ctx, workerNode("osaka-gw", false)))
	// A record replaces the one of its name, compared without case.
	replaced := workerNode("Tokyo-GW", true, "site=tokyo")
	require.NoError(t, s.Record(ctx, replaced))

	nodes, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "Tokyo-GW", nodes[0].Name)
	assert.Equal(t, "osaka-gw", nodes[1].Name)
	assert.Equal(t, []string{"site=tokyo"}, nodes[0].Labels.Strings())

	got, err := s.Get(ctx, "tokyo-gw")
	require.NoError(t, err)
	assert.Equal(t, replaced, got)

	_, err = s.Get(ctx, "nagoya-gw")
	assert.ErrorIs(t, err, inventory.ErrWorkerNodeNotFound)
}

func TestWorkerNodeStore_MarkOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newWorkerNodeStore(t)
	seen := workerNode("gw", true, "web")
	require.NoError(t, s.Record(ctx, seen))

	lastSeen := seen.LastSeenAt.Add(time.Minute)
	require.NoError(t, s.MarkOffline(ctx, seen, lastSeen))

	got, err := s.Get(ctx, "gw")
	require.NoError(t, err)
	assert.False(t, got.Online)
	assert.True(t, got.LastSeenAt.Equal(lastSeen))
	assert.Equal(t, []string{"web"}, got.Labels.Strings())
}

// A worker recorded again after it was read, with other labels, keeps the
// newer record, so a coordinator never marks offline a record it did not
// see.
func TestWorkerNodeStore_MarkOfflineKeepsNewerRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newWorkerNodeStore(t)
	seen := workerNode("gw", true, "web")
	require.NoError(t, s.Record(ctx, seen))
	newer := workerNode("gw", true, "db")
	require.NoError(t, s.Record(ctx, newer))

	require.NoError(t, s.MarkOffline(ctx, seen, seen.LastSeenAt.Add(time.Minute)))

	got, err := s.Get(ctx, "gw")
	require.NoError(t, err)
	assert.Equal(t, newer, got)

	// A forgotten worker is not recorded again.
	require.NoError(t, s.MarkOffline(ctx, workerNode("gone", true), time.Now()))
	_, err = s.Get(ctx, "gone")
	assert.ErrorIs(t, err, inventory.ErrWorkerNodeNotFound)
}

func TestWorkerNodeStore_Forget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newWorkerNodeStore(t)
	require.NoError(t, s.Record(ctx, workerNode("online-gw", true)))
	require.NoError(t, s.Record(ctx, workerNode("offline-gw", false)))

	assert.ErrorIs(t, s.Forget(ctx, "online-gw"), inventory.ErrWorkerNodeOnline)
	assert.ErrorIs(t, s.Forget(ctx, "nagoya-gw"), inventory.ErrWorkerNodeNotFound)
	require.NoError(t, s.Forget(ctx, "OFFLINE-GW"))

	nodes, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "online-gw", nodes[0].Name)
}

// A worker recorded as online whose heartbeats stopped is forgotten, so no
// running coordinator is needed to record it offline first. A worker with a
// fresh heartbeat is still refused.
func TestWorkerNodeStore_ForgetAfterHeartbeatsStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	heartbeats := store.NewWorkerHeartbeatStore(backend.Collection(persis.CollectionWorkerHeartbeats))
	s, err := store.NewWorkerNodeStore(backend.Collection(persis.CollectionWorkerNodes), heartbeats)
	require.NoError(t, err)
	for _, hb := range []dispatch.WorkerHeartbeatRecord{
		{WorkerID: "live-gw", LastHeartbeatAt: time.Now().UnixMilli()},
		{WorkerID: "stale-gw", LastHeartbeatAt: time.Now().Add(-time.Hour).UnixMilli()},
	} {
		require.NoError(t, heartbeats.Upsert(ctx, hb))
	}
	for _, name := range []string{"live-gw", "stale-gw", "gone-gw"} {
		require.NoError(t, s.Record(ctx, workerNode(name, true)))
	}

	assert.ErrorIs(t, s.Forget(ctx, "live-gw"), inventory.ErrWorkerNodeOnline)
	require.NoError(t, s.Forget(ctx, "stale-gw"))
	require.NoError(t, s.Forget(ctx, "gone-gw"))

	nodes, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "live-gw", nodes[0].Name)
}
