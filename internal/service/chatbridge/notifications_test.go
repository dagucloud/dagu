// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chatbridge

import (
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
)

func TestNotificationSeenKeyIncludesStatus(t *testing.T) {
	t.Parallel()

	waiting := &ir.DAGRunStatus{DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Waiting}
	succeeded := &ir.DAGRunStatus{DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Succeeded}

	assert.NotEqual(t, NotificationSeenKey(waiting), NotificationSeenKey(succeeded))
}

func TestNotificationBatcher_SuccessBurstFlushesSingleDigest(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(10*time.Millisecond, 20*time.Millisecond)
	defer batcher.Stop()

	require.True(t, batcher.Enqueue("dest-1", testNotificationEvent(&ir.DAGRunStatus{Name: "briefing", DAGRunID: "run-1", AttemptID: "a1", Status: ir.Succeeded})))
	require.True(t, batcher.Enqueue("dest-1", testNotificationEvent(&ir.DAGRunStatus{Name: "briefing", DAGRunID: "run-2", AttemptID: "a2", Status: ir.Succeeded})))
	require.True(t, batcher.Enqueue("dest-1", testNotificationEvent(&ir.DAGRunStatus{Name: "sync", DAGRunID: "run-3", AttemptID: "a3", Status: ir.PartiallySucceeded})))

	ready := waitForReadyBatch(t, batcher)
	assert.Equal(t, "dest-1", ready.Destination)
	assert.Equal(t, NotificationClassSuccessDigest, ready.Batch.Class)
	assert.Len(t, ready.Batch.Events, 3)
	text := FormatNotificationBatch(ready.Batch)
	assert.Contains(t, text, "DAG completion digest")
	assert.Contains(t, text, "briefing: succeeded x2")
	assert.Contains(t, text, "sync: partially_succeeded x1")
}

func TestNotificationBatcher_ReplacesWaitingWithSuccessBeforeFlush(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(15*time.Millisecond, 25*time.Millisecond)
	defer batcher.Stop()

	require.True(t, batcher.Enqueue("dest-1", testNotificationEvent(&ir.DAGRunStatus{Name: "briefing", DAGRunID: "run-1", AttemptID: "a1", Status: ir.Waiting})))
	require.True(t, batcher.Enqueue("dest-1", testNotificationEvent(&ir.DAGRunStatus{Name: "briefing", DAGRunID: "run-1", AttemptID: "a1", Status: ir.Succeeded})))

	ready := waitForReadyBatch(t, batcher)
	assert.Equal(t, NotificationClassSuccessDigest, ready.Batch.Class)
	require.Len(t, ready.Batch.Events, 1)
	assert.Equal(t, ir.Succeeded, ready.Batch.Events[0].Status.Status)
}

func TestNotificationBatcher_DuplicateStatusDoesNotDuplicateBatch(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(20*time.Millisecond, 40*time.Millisecond)
	defer batcher.Stop()

	status := &ir.DAGRunStatus{Name: "briefing", DAGRunID: "run-1", AttemptID: "a1", Status: ir.Failed, Error: "boom"}
	require.True(t, batcher.Enqueue("dest-1", testNotificationEvent(status)))
	require.True(t, batcher.Enqueue("dest-1", testNotificationEvent(status)))

	ready := waitForReadyBatch(t, batcher)
	assert.Equal(t, NotificationClassUrgent, ready.Batch.Class)
	require.Len(t, ready.Batch.Events, 1)
	assert.Equal(t, ir.Failed, ready.Batch.Events[0].Status.Status)
}

func TestNotificationBatcher_SkipsFailedRunWithAutoRetryRemaining(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(10*time.Millisecond, 20*time.Millisecond)
	defer batcher.Stop()

	status := &ir.DAGRunStatus{
		Name:           "briefing",
		DAGRunID:       "run-1",
		AttemptID:      "a1",
		Status:         ir.Failed,
		Error:          "boom",
		AutoRetryCount: 0,
		AutoRetryLimit: 2,
	}

	assert.False(t, batcher.Enqueue("dest-1", testNotificationEvent(status)))
	require.Never(t, func() bool {
		return len(batcher.TakeReady()) > 0
	}, 50*time.Millisecond, 5*time.Millisecond)
}

func TestNotificationBatcher_RunningEventsUseInformationalClass(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(10*time.Millisecond, 20*time.Millisecond)
	defer batcher.Stop()

	event := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "a1",
		Status:    ir.Running,
	})
	event.Type = eventstore.TypeDAGRunRunning

	require.True(t, batcher.Enqueue("dest-1", event))

	ready := waitForReadyBatch(t, batcher)
	assert.Equal(t, NotificationClassInformational, ready.Batch.Class)
	require.Len(t, ready.Batch.Events, 1)
	assert.Equal(t, eventstore.TypeDAGRunRunning, ready.Batch.Events[0].Type)
	assert.Contains(t, FormatNotificationBatch(ready.Batch), "DAG activity updates")
}

func TestNotificationBatcher_AbortedEventsUseUrgentClass(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(10*time.Millisecond, 20*time.Millisecond)
	defer batcher.Stop()

	event := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "a1",
		Status:    ir.Aborted,
	})
	event.Type = eventstore.TypeDAGRunAborted

	require.True(t, batcher.Enqueue("dest-1", event))

	ready := waitForReadyBatch(t, batcher)
	assert.Equal(t, NotificationClassUrgent, ready.Batch.Class)
	require.Len(t, ready.Batch.Events, 1)
	assert.Equal(t, eventstore.TypeDAGRunAborted, ready.Batch.Events[0].Type)
	assert.Contains(t, FormatNotificationBatch(ready.Batch), "aborted")
}

func TestNotificationBatcher_DrainAndStopReturnsPendingBatchesOrderedAndStopsFlushes(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(80*time.Millisecond, 120*time.Millisecond)
	baseTime := time.Now().UTC()

	successEvent := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "a1",
		Status:    ir.Succeeded,
	})
	successEvent.ObservedAt = baseTime.Add(2 * time.Millisecond)

	urgentOldEvent := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "sync",
		DAGRunID:  "run-2",
		AttemptID: "a2",
		Status:    ir.Failed,
	})
	urgentOldEvent.ObservedAt = baseTime

	urgentNewEvent := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "sync",
		DAGRunID:  "run-3",
		AttemptID: "a3",
		Status:    ir.Waiting,
	})
	urgentNewEvent.ObservedAt = baseTime.Add(time.Millisecond)

	require.True(t, batcher.Enqueue("success-dest", successEvent))
	require.True(t, batcher.Enqueue("urgent-old", urgentOldEvent))
	require.True(t, batcher.Enqueue("urgent-new", urgentNewEvent))

	drained := batcher.DrainAndStop()
	require.Len(t, drained, 3)
	assert.Equal(t, "urgent-old", drained[0].Destination)
	assert.Equal(t, NotificationClassUrgent, drained[0].Batch.Class)
	assert.Equal(t, "urgent-new", drained[1].Destination)
	assert.Equal(t, NotificationClassUrgent, drained[1].Batch.Class)
	assert.Equal(t, "success-dest", drained[2].Destination)
	assert.Equal(t, NotificationClassSuccessDigest, drained[2].Batch.Class)

	require.Never(t, func() bool {
		return len(batcher.TakeReady()) > 0
	}, 200*time.Millisecond, 20*time.Millisecond)
	assert.False(t, batcher.Enqueue("ignored", testNotificationEvent(&ir.DAGRunStatus{
		Name:      "ignored",
		DAGRunID:  "run-4",
		AttemptID: "a4",
		Status:    ir.Succeeded,
	})))
}

func TestNotificationBatcher_DiscardDestinationsRemovesReadyAndBufferedBatches(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(80*time.Millisecond, 20*time.Millisecond)
	defer batcher.Stop()

	require.True(t, batcher.Enqueue("ready-remove", testNotificationEvent(&ir.DAGRunStatus{
		Name:      "briefing",
		DAGRunID:  "run-1",
		AttemptID: "a1",
		Status:    ir.Succeeded,
	})))
	require.True(t, batcher.Enqueue("ready-keep", testNotificationEvent(&ir.DAGRunStatus{
		Name:      "sync",
		DAGRunID:  "run-2",
		AttemptID: "a2",
		Status:    ir.Succeeded,
	})))
	require.True(t, batcher.Enqueue("buffered-remove", testNotificationEvent(&ir.DAGRunStatus{
		Name:      "alerts",
		DAGRunID:  "run-3",
		AttemptID: "a3",
		Status:    ir.Failed,
	})))

	// Synchronously flush success-class buckets to ready
	batcher.flushBucketsLocked(NotificationClassSuccessDigest)

	batcher.mu.Lock()
	require.Len(t, batcher.ready, 2)
	batcher.mu.Unlock()

	batcher.DiscardDestinations([]string{"ready-remove", "buffered-remove"})

	ready := batcher.TakeReady()
	require.Len(t, ready, 1)
	assert.Equal(t, "ready-keep", ready[0].Destination)

	require.Never(t, func() bool {
		return len(batcher.TakeReady()) > 0
	}, 200*time.Millisecond, 20*time.Millisecond)
}

func TestNotificationBatcher_DiscardEventsRemovesReadyAndBufferedEvents(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(80*time.Millisecond, 20*time.Millisecond)
	defer batcher.Stop()

	readyEvent := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "ready",
		DAGRunID:  "run-ready",
		AttemptID: "a1",
		Status:    ir.Succeeded,
	})
	bufferedDrop := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "buffered-drop",
		DAGRunID:  "run-buffered-drop",
		AttemptID: "a1",
		Status:    ir.Failed,
	})
	bufferedKeep := testNotificationEvent(&ir.DAGRunStatus{
		Name:      "buffered-keep",
		DAGRunID:  "run-buffered-keep",
		AttemptID: "a1",
		Status:    ir.Failed,
	})

	require.True(t, batcher.Enqueue("dest-1", readyEvent))
	batcher.flushBucketsLocked(NotificationClassSuccessDigest)
	require.True(t, batcher.Enqueue("dest-1", bufferedDrop))
	require.True(t, batcher.Enqueue("dest-1", bufferedKeep))

	batcher.discardEvents("dest-1", map[string]struct{}{
		readyEvent.Key:   {},
		bufferedDrop.Key: {},
	})

	assert.Empty(t, batcher.TakeReady())
	select {
	case <-batcher.ReadyC():
	default:
	}
	ready := waitForReadyBatch(t, batcher)
	require.Len(t, ready.Batch.Events, 1)
	assert.Equal(t, bufferedKeep.Key, ready.Batch.Events[0].Key)
}

func TestFormatNotificationBatch_CapsVisibleGroups(t *testing.T) {
	t.Parallel()

	events := make([]NotificationEvent, 0, maxNotificationGroups+2)
	base := time.Now()
	for i := range maxNotificationGroups + 2 {
		events = append(events, NotificationEvent{
			Status: &ir.DAGRunStatus{
				Name:      fmt.Sprintf("dag-%d", i),
				DAGRunID:  fmt.Sprintf("run-%d", i),
				AttemptID: "a1",
				Status:    ir.Succeeded,
			},
			ObservedAt: base.Add(-time.Duration(i) * time.Second),
		})
	}

	text := FormatNotificationBatch(NotificationBatch{
		Class:       NotificationClassSuccessDigest,
		Events:      events,
		WindowStart: base.Add(-2 * time.Minute),
		WindowEnd:   base,
	})

	assert.Contains(t, text, "DAG completion digest")
	assert.Contains(t, text, "and 2 more DAG groups")
}

func TestNotificationBatcher_ClonesStatusSnapshot(t *testing.T) {
	t.Parallel()

	batcher := NewNotificationBatcher(10*time.Millisecond, 20*time.Millisecond)
	defer batcher.Stop()

	status := &ir.DAGRunStatus{
		Name:      "briefing",
		Labels:    []string{"workspace=ops"},
		DAGRunID:  "run-1",
		AttemptID: "a1",
		Status:    ir.Failed,
		Error:     "original error",
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "fetch"},
				Status: ir.NodeFailed,
				Error:  "node failed",
			},
		},
		OnFailure: &ir.Node{
			Step:  ir.Step{Name: "notify"},
			Error: "handler failed",
		},
	}
	event := testNotificationEvent(status)
	event.DAGFile = "briefing-file"
	require.True(t, batcher.Enqueue("dest-1", event))

	status.Error = "mutated error"
	status.Labels[0] = "workspace=mutated"
	status.Nodes[0].Error = "mutated node error"
	status.Nodes[0].Step.Name = "mutated"
	status.OnFailure.Error = "mutated handler error"

	ready := waitForReadyBatch(t, batcher)
	require.Len(t, ready.Batch.Events, 1)
	gotEvent := ready.Batch.Events[0]
	assert.Equal(t, "briefing-file", gotEvent.DAGFile)
	got := gotEvent.Status
	require.NotNil(t, got)
	assert.Equal(t, "original error", got.Error)
	assert.Equal(t, []string{"workspace=ops"}, got.Labels)
	require.Len(t, got.Nodes, 1)
	assert.Equal(t, "fetch", got.Nodes[0].Step.Name)
	assert.Equal(t, "node failed", got.Nodes[0].Error)
	require.NotNil(t, got.OnFailure)
	assert.Equal(t, "handler failed", got.OnFailure.Error)
}

func waitForReadyBatch(t *testing.T, batcher *NotificationBatcher) NotificationPendingBatch {
	t.Helper()

	select {
	case <-batcher.ReadyC():
		ready := batcher.TakeReady()
		require.NotEmpty(t, ready)
		return ready[0]
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ready notification batch")
		return NotificationPendingBatch{}
	}
}

func testNotificationEvent(status *ir.DAGRunStatus) NotificationEvent {
	return NotificationEvent{
		Key:        NotificationSeenKey(status),
		Status:     status,
		ObservedAt: time.Now().UTC(),
	}
}
