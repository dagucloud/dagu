// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	persistestutil "github.com/dagucloud/dagu/v2/internal/persis/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// countingWorkerNodes counts the records written, so a test can tell a
// heartbeat that writes from one that does not.
type countingWorkerNodes struct {
	inventory.WorkerNodeStore
	records atomic.Int32
}

func (s *countingWorkerNodes) Record(ctx context.Context, node inventory.WorkerNode) error {
	s.records.Add(1)
	return s.WorkerNodeStore.Record(ctx, node)
}

// workerNodeFixture is the state that the coordinators of one installation
// share.
type workerNodeFixture struct {
	nodes      *countingWorkerNodes
	heartbeats dispatch.WorkerHeartbeatStore
}

func newWorkerNodeFixture(t *testing.T) workerNodeFixture {
	t.Helper()
	backend := persistestutil.NewMemoryBackend()
	nodes, err := store.NewWorkerNodeStore(backend.Collection(persis.CollectionWorkerNodes))
	require.NoError(t, err)
	return workerNodeFixture{
		nodes:      &countingWorkerNodes{WorkerNodeStore: nodes},
		heartbeats: store.NewWorkerHeartbeatStore(backend.Collection(persis.CollectionWorkerHeartbeats)),
	}
}

func (f workerNodeFixture) coordinator() *Handler {
	return NewHandler(HandlerConfig{
		WorkerNodeStore:         f.nodes,
		WorkerHeartbeatStore:    f.heartbeats,
		StaleHeartbeatThreshold: time.Minute,
	})
}

func heartbeat(t *testing.T, h *Handler, workerID string, labels map[string]string) {
	t.Helper()
	_, err := h.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{
		WorkerId: workerID,
		Host:     "gw.internal",
		Labels:   labels,
	})
	require.NoError(t, err)
}

// ageHeartbeat makes a worker's last heartbeat as old as age.
func (f workerNodeFixture) ageHeartbeat(t *testing.T, workerID string, age time.Duration) time.Time {
	t.Helper()
	last := time.Now().Add(-age).Truncate(time.Millisecond)
	record, err := f.heartbeats.Get(context.Background(), workerID)
	require.NoError(t, err)
	record.LastHeartbeatAt = last.UnixMilli()
	require.NoError(t, f.heartbeats.Upsert(context.Background(), *record))
	return last
}

// A heartbeat writes the worker node only when it is new or its labels
// changed, and a worker without a valid node name, as a worker without
// worker.id reports, is not recorded.
func TestWorkerNodeRecordedOnChange(t *testing.T) {
	t.Parallel()
	f := newWorkerNodeFixture(t)
	h := f.coordinator()

	for range 3 {
		heartbeat(t, h, "tokyo-gw", map[string]string{"site": "tokyo", "web": ""})
	}
	assert.Equal(t, int32(1), f.nodes.records.Load())

	heartbeat(t, h, "tokyo-gw", map[string]string{"site": "tokyo"})
	assert.Equal(t, int32(2), f.nodes.records.Load())

	heartbeat(t, h, "build-host@4242", nil)
	assert.Equal(t, int32(2), f.nodes.records.Load())

	nodes, err := f.nodes.List(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "tokyo-gw", nodes[0].Name)
	assert.Equal(t, "gw.internal", nodes[0].Host)
	assert.Equal(t, []string{"site=tokyo"}, nodes[0].Labels.Strings())
	assert.True(t, nodes[0].Online)
}

// A worker whose heartbeats stop goes offline, last seen at its last
// heartbeat, and is online again at its next heartbeat.
func TestWorkerNodeGoesOffline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newWorkerNodeFixture(t)
	h := f.coordinator()
	heartbeat(t, h, "tokyo-gw", map[string]string{"web": ""})
	heartbeat(t, h, "osaka-gw", nil)

	last := f.ageHeartbeat(t, "tokyo-gw", 2*time.Minute)
	h.reconcileWorkerNodes(ctx)

	gone, err := f.nodes.Get(ctx, "tokyo-gw")
	require.NoError(t, err)
	assert.False(t, gone.Online)
	assert.True(t, gone.LastSeenAt.Equal(last), "last seen %v, want %v", gone.LastSeenAt, last)
	assert.Equal(t, []string{"web"}, gone.Labels.Strings())
	up, err := f.nodes.Get(ctx, "osaka-gw")
	require.NoError(t, err)
	assert.True(t, up.Online)

	heartbeat(t, h, "tokyo-gw", map[string]string{"web": ""})
	back, err := f.nodes.Get(ctx, "tokyo-gw")
	require.NoError(t, err)
	assert.True(t, back.Online)
}

// Heartbeats reach any coordinator. A worker that one coordinator marked
// offline, and that then reaches another coordinator whose last record of
// it is online, is recorded online again once that coordinator reconciles.
func TestWorkerNodeOnlineAgainAcrossCoordinators(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newWorkerNodeFixture(t)
	a, b := f.coordinator(), f.coordinator()
	heartbeat(t, a, "tokyo-gw", nil)

	f.ageHeartbeat(t, "tokyo-gw", 2*time.Minute)
	b.reconcileWorkerNodes(ctx)
	heartbeat(t, a, "tokyo-gw", nil)
	a.reconcileWorkerNodes(ctx)
	heartbeat(t, a, "tokyo-gw", nil)

	node, err := f.nodes.Get(ctx, "tokyo-gw")
	require.NoError(t, err)
	assert.True(t, node.Online)
}
