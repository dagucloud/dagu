// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newQueueStore(t *testing.T) *store.QueueStore {
	t.Helper()
	return store.NewQueueStore(testutil.NewMemoryBackend().Collection("queue"))
}

func queueRef(name, id string) exec.DAGRunRef {
	return exec.NewDAGRunRef(name, id)
}

func requireQueuedRef(t *testing.T, item exec.QueuedItemData) exec.DAGRunRef {
	t.Helper()
	ref, err := item.Data()
	require.NoError(t, err)
	return *ref
}

func TestQueueStore_EnqueueListAndDequeue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newQueueStore(t)

	require.NoError(t, s.Enqueue(ctx, "main", exec.QueuePriorityLow, queueRef("dag-low", "run-low")))
	require.NoError(t, s.Enqueue(ctx, "main", exec.QueuePriorityHigh, queueRef("dag-high", "run-high")))

	n, err := s.Len(ctx, "main")
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	items, err := s.List(ctx, "main")
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, queueRef("dag-high", "run-high"), requireQueuedRef(t, items[0]))
	assert.Equal(t, queueRef("dag-low", "run-low"), requireQueuedRef(t, items[1]))

	first, err := s.DequeueByName(ctx, "main")
	require.NoError(t, err)
	assert.NotContains(t, first.ID(), "main/")
	assert.Equal(t, queueRef("dag-high", "run-high"), requireQueuedRef(t, first))

	second, err := s.DequeueByName(ctx, "main")
	require.NoError(t, err)
	assert.Equal(t, queueRef("dag-low", "run-low"), requireQueuedRef(t, second))

	_, err = s.DequeueByName(ctx, "main")
	assert.ErrorIs(t, err, exec.ErrQueueEmpty)
}

func TestQueueStore_EnqueueRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newQueueStore(t)

	assert.ErrorContains(t, s.Enqueue(ctx, "", exec.QueuePriorityLow, queueRef("dag", "run")), "queue name is required")
	assert.ErrorContains(t, s.Enqueue(ctx, "main", exec.QueuePriorityLow, queueRef("", "run")), "dag-run reference is required")
	assert.ErrorContains(t, s.Enqueue(ctx, "main", exec.QueuePriorityLow, queueRef("dag", "")), "dag-run reference is required")
	assert.ErrorContains(t, s.Enqueue(ctx, "main", exec.QueuePriority(99), queueRef("dag", "run")), "invalid queue priority")
}

func TestQueueStore_ListCursor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newQueueStore(t)

	require.NoError(t, s.Enqueue(ctx, "cursor-q", exec.QueuePriorityHigh, queueRef("dag-high", "run-high")))
	require.NoError(t, s.Enqueue(ctx, "cursor-q", exec.QueuePriorityLow, queueRef("dag-low-1", "run-low-1")))
	require.NoError(t, s.Enqueue(ctx, "cursor-q", exec.QueuePriorityLow, queueRef("dag-low-2", "run-low-2")))

	firstPage, err := s.ListCursor(ctx, "cursor-q", "", 2)
	require.NoError(t, err)
	require.Len(t, firstPage.Items, 2)
	assert.True(t, firstPage.HasMore)
	assert.NotEmpty(t, firstPage.NextCursor)
	assert.Equal(t, queueRef("dag-high", "run-high"), requireQueuedRef(t, firstPage.Items[0]))
	assert.Equal(t, queueRef("dag-low-1", "run-low-1"), requireQueuedRef(t, firstPage.Items[1]))

	secondPage, err := s.ListCursor(ctx, "cursor-q", firstPage.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, secondPage.Items, 1)
	assert.False(t, secondPage.HasMore)
	assert.Empty(t, secondPage.NextCursor)
	assert.Equal(t, queueRef("dag-low-2", "run-low-2"), requireQueuedRef(t, secondPage.Items[0]))

	_, err = s.ListCursor(ctx, "cursor-q", "not-a-valid-cursor", 10)
	assert.ErrorIs(t, err, exec.ErrInvalidCursor)
}

func TestQueueStore_DequeueByDAGRunIDAndDeleteByItemIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newQueueStore(t)
	target := queueRef("dag-target", "run-target")
	other := queueRef("dag-other", "run-other")

	require.NoError(t, s.Enqueue(ctx, "main", exec.QueuePriorityLow, target))
	require.NoError(t, s.Enqueue(ctx, "main", exec.QueuePriorityHigh, target))
	require.NoError(t, s.Enqueue(ctx, "main", exec.QueuePriorityLow, other))

	removed, err := s.DequeueByDAGRunID(ctx, "main", target)
	require.NoError(t, err)
	require.Len(t, removed, 2)
	assert.Equal(t, target, requireQueuedRef(t, removed[0]))
	assert.Equal(t, target, requireQueuedRef(t, removed[1]))

	remaining, err := s.List(ctx, "main")
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, other, requireQueuedRef(t, remaining[0]))

	deleted, err := s.DeleteByItemIDs(ctx, "main", []string{remaining[0].ID(), "missing-item"})
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	n, err := s.Len(ctx, "main")
	require.NoError(t, err)
	assert.Zero(t, n)

	_, err = s.DequeueByDAGRunID(ctx, "main", target)
	assert.ErrorIs(t, err, exec.ErrQueueItemNotFound)
}

func TestQueueStore_DeleteByItemIDsNormalizesFilePaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newQueueStore(t)

	require.NoError(t, s.Enqueue(ctx, "main", exec.QueuePriorityLow, queueRef("dag", "run")))
	items, err := s.List(ctx, "main")
	require.NoError(t, err)
	require.Len(t, items, 1)

	deleted, err := s.DeleteByItemIDs(ctx, "main", []string{
		filepath.Join("ignored", items[0].ID()+".json"),
		" ",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	n, err := s.Len(ctx, "main")
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestQueueStore_QueueWatcher(t *testing.T) {
	t.Parallel()

	s := newQueueStore(t)
	assert.NotNil(t, s.QueueWatcher(context.Background()))
}

func TestQueueStore_AllQueueListAndListByDAGName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newQueueStore(t)

	require.NoError(t, s.Enqueue(ctx, "queue-a", exec.QueuePriorityLow, queueRef("dag-shared", "run-a-low")))
	require.NoError(t, s.Enqueue(ctx, "queue-b", exec.QueuePriorityHigh, queueRef("dag-other", "run-b-high")))
	require.NoError(t, s.Enqueue(ctx, "queue-a", exec.QueuePriorityHigh, queueRef("dag-shared", "run-a-high")))

	queues, err := s.QueueList(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"queue-a", "queue-b"}, queues)

	byDAG, err := s.ListByDAGName(ctx, "queue-a", "dag-shared")
	require.NoError(t, err)
	require.Len(t, byDAG, 2)
	assert.Equal(t, queueRef("dag-shared", "run-a-high"), requireQueuedRef(t, byDAG[0]))
	assert.Equal(t, queueRef("dag-shared", "run-a-low"), requireQueuedRef(t, byDAG[1]))

	none, err := s.ListByDAGName(ctx, "queue-a", "missing-dag")
	require.NoError(t, err)
	assert.Empty(t, none)

	all, err := s.All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, queueRef("dag-shared", "run-a-high"), requireQueuedRef(t, all[0]))
	assert.Equal(t, queueRef("dag-other", "run-b-high"), requireQueuedRef(t, all[1]))
	assert.Equal(t, queueRef("dag-shared", "run-a-low"), requireQueuedRef(t, all[2]))
}

func TestQueueStore_ConcurrentDequeueIsExclusive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newQueueStore(t)
	require.NoError(t, s.Enqueue(ctx, "main", exec.QueuePriorityHigh, queueRef("dag", "run")))

	var claimed atomic.Int32
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.DequeueByName(ctx, "main")
			switch {
			case err == nil:
				claimed.Add(1)
			case errors.Is(err, exec.ErrQueueEmpty):
			default:
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), claimed.Load())
}

func TestQueueStore_ReadsLegacyFileQueueItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	queueName := "legacy-q"
	itemFile := "item_high_20260101_000000_000000001Z_run-legacy.json"
	queuedAt := time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)
	raw := `{"fileName":"` + itemFile + `","dagRun":{"name":"legacy-dag","id":"run-legacy"},"queuedAt":"` + queuedAt.Format(time.RFC3339Nano) + `"}`

	itemPath := filepath.Join(root, queueName, itemFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(itemPath), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, queueName, ".queue-index.json"), []byte(`{"version":1,"high":["`+itemFile+`"]}`), 0o600))
	require.NoError(t, os.WriteFile(itemPath, []byte(raw), 0o600))

	s := store.NewQueueStore(file.NewCollection(root))
	items, err := s.List(ctx, queueName)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "item_high_20260101_000000_000000001Z_run-legacy", items[0].ID())
	assert.Equal(t, queueRef("legacy-dag", "run-legacy"), requireQueuedRef(t, items[0]))

	claimed, err := s.DequeueByName(ctx, queueName)
	require.NoError(t, err)
	assert.Equal(t, items[0].ID(), claimed.ID())
	assert.NoFileExists(t, itemPath)
}
