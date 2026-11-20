// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 094, "Version check": the coordinator matches a task with the capabilities
// that a worker announces, and a worker with none gets a task that needs none.
func TestWorkerShortfallChecksCapabilities(t *testing.T) {
	t.Parallel()

	old := dispatch.WorkerHeartbeatRecord{WorkerID: "old", Labels: map[string]string{"os": "linux"}}
	current := dispatch.WorkerHeartbeatRecord{WorkerID: "current", Labels: map[string]string{"os": "linux"}, Capabilities: []string{ir.CapabilityAIRelay}}
	needs := &coordinatorv1.Task{RequiredCapabilities: []string{ir.CapabilityAIRelay}}
	plain := &coordinatorv1.Task{}

	assert.ErrorIs(t, workerShortfall([]dispatch.WorkerHeartbeatRecord{old}, needs), errNoMatchingWorkers)
	assert.NoError(t, workerShortfall([]dispatch.WorkerHeartbeatRecord{old, current}, needs))
	assert.NoError(t, workerShortfall([]dispatch.WorkerHeartbeatRecord{old}, plain))
	assert.True(t, anyWorkerMatches([]dispatch.WorkerHeartbeatRecord{current}, nil, needs.RequiredCapabilities, ""))
	assert.False(t, anyWorkerMatches([]dispatch.WorkerHeartbeatRecord{old}, nil, needs.RequiredCapabilities, ""))
}

// A poller that waits holds the capabilities of its worker. The legacy in-memory
// path gives a task only to a poller that announces them.
func TestDispatchToWaitingPollerChecksCapabilities(t *testing.T) {
	t.Parallel()

	h := NewHandler(HandlerConfig{StaleHeartbeatThreshold: time.Minute})
	oldCh := make(chan *coordinatorv1.Task, 1)
	currentCh := make(chan *coordinatorv1.Task, 1)
	h.waitingPollers["old"] = &workerInfo{workerID: "old", pollerID: "old", taskChan: oldCh}
	h.waitingPollers["current"] = &workerInfo{workerID: "current", pollerID: "current", taskChan: currentCh, capabilities: []string{ir.CapabilityAIRelay}}

	task := &coordinatorv1.Task{DagRunId: "run", RequiredCapabilities: []string{ir.CapabilityAIRelay}}
	require.NoError(t, h.dispatchToWaitingPoller(context.Background(), task))
	assert.Empty(t, oldCh, "a poller with no capability gets no task that needs one")
	require.Len(t, currentCh, 1)

	require.ErrorIs(t, h.ensureWaitingWorkerAvailability(context.Background(), nil, task.RequiredCapabilities, ""), errNoMatchingWorkers,
		"no poller waits that announces the capability, and no heartbeat exists")
}

// The heartbeat keeps the capabilities of the worker.
func TestHeartbeatKeepsCapabilities(t *testing.T) {
	t.Parallel()

	h := NewHandler(HandlerConfig{StaleHeartbeatThreshold: time.Minute})
	_, err := h.Heartbeat(context.Background(), &coordinatorv1.HeartbeatRequest{
		WorkerId: "w1", Labels: map[string]string{"os": "linux"}, Capabilities: []string{ir.CapabilityAIRelay},
	})
	require.NoError(t, err)
	workers, err := h.listHealthyWorkers(context.Background())
	require.NoError(t, err)
	require.Len(t, workers, 1)
	assert.Equal(t, []string{ir.CapabilityAIRelay}, workers[0].Capabilities)
}
