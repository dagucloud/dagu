// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func TestQueueCursorHelpersRejectInvalidState(t *testing.T) {
	t.Parallel()

	items := []*queueItem{{id: "item-a"}, {id: "item-b"}}

	assert.Empty(t, encodeQueueCursor("queue-a", 0, ""))

	raw := encodeQueueCursor("queue-a", 1, "item-a")
	_, err := decodeQueueCursor("queue-b", raw)
	assert.ErrorIs(t, err, exec.ErrInvalidCursor)

	start, err := resolveQueueCursorStart(items, queueReadCursor{Offset: 99, AfterItemID: "item-a"})
	require.NoError(t, err)
	assert.Equal(t, 1, start)

	_, err = resolveQueueCursorStart(items, queueReadCursor{Offset: -1})
	assert.ErrorIs(t, err, exec.ErrInvalidCursor)

	_, err = resolveQueueCursorStart(items, queueReadCursor{Offset: 1})
	assert.ErrorIs(t, err, exec.ErrInvalidCursor)

	_, err = resolveQueueCursorStart(items, queueReadCursor{Offset: 2, AfterItemID: "missing"})
	assert.ErrorIs(t, err, exec.ErrInvalidCursor)
}

func TestQueueItemHelpersHandleInvalidRecords(t *testing.T) {
	t.Parallel()

	var nilItem *queueItem
	assert.Empty(t, nilItem.ID())
	_, err := nilItem.Data()
	assert.ErrorContains(t, err, "queue item is nil")

	_, err = queueItemFromRecord(nil)
	assert.ErrorContains(t, err, "nil record")

	_, err = queueItemFromRecord(&persis.Record{ID: "item_without_queue"})
	assert.ErrorContains(t, err, "invalid record ID")

	now := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	data, err := persis.Encode(queueItemPayload{
		FileName: "bad-name.json",
		DAGRun:   exec.DAGRunRef{},
		QueuedAt: now,
	})
	require.NoError(t, err)

	item, err := queueItemFromRecord(&persis.Record{
		ID:        "queue-a/item_low_20260102_030405_000000006Z_run",
		Data:      data,
		CreatedAt: now,
	})
	require.NoError(t, err)
	_, err = item.Data()
	assert.ErrorContains(t, err, "invalid dag-run")
}

func TestQueueItemMetadataAndNormalization(t *testing.T) {
	t.Parallel()

	fallback := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)

	priority, queuedAt := queueItemMetadata("item_high_20260102_030405_000000007Z_run", fallback)
	assert.Equal(t, exec.QueuePriorityHigh, priority)
	assert.Equal(t, time.Date(2026, 1, 2, 3, 4, 5, 7, time.UTC), queuedAt)

	priority, queuedAt = queueItemMetadata("not-a-queue-item", fallback)
	assert.Equal(t, exec.QueuePriorityLow, priority)
	assert.Equal(t, fallback, queuedAt)

	assert.Equal(t, "item.json.bak", normalizeQueueItemID("ignored/item.json.bak"))
	assert.Empty(t, normalizeQueueItemID(""))
}

func TestPollingQueueWatcherPublishesAndStops(t *testing.T) {
	t.Parallel()

	var state atomic.Int64
	initialSnapshot := make(chan struct{})
	var initialSnapshotOnce sync.Once
	watcher := newPollingQueueWatcher(time.Millisecond, func(context.Context) (string, error) {
		initialSnapshotOnce.Do(func() {
			close(initialSnapshot)
		})
		return strconv.FormatInt(state.Load(), 10), nil
	})
	//nolint:staticcheck // The watcher accepts nil and normalizes it to context.Background.
	notifyCh, err := watcher.Start(nil)
	require.NoError(t, err)
	<-initialSnapshot
	state.Store(1)

	select {
	case <-notifyCh:
	case <-time.After(100 * time.Millisecond):
		require.FailNow(t, "queue watcher did not publish")
	}

	done := make(chan struct{})
	go func() {
		//nolint:staticcheck // Stop also accepts nil and normalizes it to context.Background.
		watcher.Stop(nil)
		watcher.Stop(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		require.FailNow(t, "queue watcher did not stop")
	}
}

func TestPollingQueueWatcherUsesDefaultInterval(t *testing.T) {
	t.Parallel()

	watcher := newPollingQueueWatcher(0, nil)
	assert.Equal(t, queuePollInterval, watcher.interval)
}

func TestQueueStoreNextQueueItemIDSkipsExistingTimestamp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	queueName := "main"
	dagRun := exec.NewDAGRunRef("dag", "run-same")
	priority := exec.QueuePriorityLow
	start := time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)

	s := NewQueueStore(testutil.NewMemoryBackend().Collection("queue"))
	firstID := newQueueItemID(priority, dagRun.ID, start)
	data, err := persis.Encode(queueItemPayload{
		FileName: firstID + ".json",
		DAGRun:   dagRun,
		QueuedAt: start,
	})
	require.NoError(t, err)
	require.NoError(t, s.col.Put(ctx, &persis.Record{
		ID:        queueRecordID(queueName, firstID),
		Data:      data,
		CreatedAt: start,
		UpdatedAt: start,
	}))

	gotID, gotQueuedAt, err := s.nextQueueItemID(ctx, queueName, priority, dagRun.ID, start)
	require.NoError(t, err)

	wantQueuedAt := start.Add(time.Nanosecond)
	assert.Equal(t, newQueueItemID(priority, dagRun.ID, wantQueuedAt), gotID)
	assert.Equal(t, wantQueuedAt, gotQueuedAt)
}
