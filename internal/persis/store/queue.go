// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
)

const (
	queueCursorVersion       = 1
	queueDateTimeUTC         = "20060102_150405"
	queueItemIDMaxCollisions = 1000
	queuePollInterval        = 2 * time.Second
)

var _ queue.QueueStore = (*QueueStore)(nil)

// QueueStore implements [queue.QueueStore] on top of a [persis.Collection].
// Records are keyed as "{queueName}/{itemID}", while item IDs exposed through
// queue.QueuedItemData intentionally stay as "{itemID}" for caller compatibility.
type QueueStore struct {
	col     persis.Collection
	indices map[string]*queueReadIndexCache
	mu      sync.Mutex
}

// NewQueueStore creates a QueueStore backed by col.
func NewQueueStore(col persis.Collection) *QueueStore {
	return &QueueStore{
		col:     col,
		indices: make(map[string]*queueReadIndexCache),
	}
}

// Enqueue adds a DAG-run reference to the named queue.
func (s *QueueStore) Enqueue(ctx context.Context, name string, priority queue.QueuePriority, dagRun ir.DAGRunRef) error {
	if name == "" {
		return fmt.Errorf("queue store: queue name is required")
	}
	if dagRun.Name == "" || dagRun.ID == "" {
		return fmt.Errorf("queue store: dag-run reference is required")
	}
	if priority != queue.QueuePriorityHigh && priority != queue.QueuePriorityLow {
		return fmt.Errorf("queue store: invalid queue priority %d", priority)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	itemID, err := s.createQueueItem(ctx, name, priority, dagRun, time.Now().UTC())
	if err != nil {
		return err
	}
	s.addQueueIndexItemLocked(ctx, name, priority, itemID)
	return nil
}

func (s *QueueStore) createQueueItem(
	ctx context.Context,
	name string,
	priority queue.QueuePriority,
	dagRun ir.DAGRunRef,
	start time.Time,
) (string, error) {
	start = start.UTC()
	for attempt := range queueItemIDMaxCollisions {
		queuedAt := start.Add(time.Duration(attempt) * time.Nanosecond)
		itemID := newQueueItemID(priority, dagRun.ID, queuedAt)
		data, err := persis.Encode(queueItemPayload{
			FileName: itemID + ".json",
			DAGRun:   dagRun,
			QueuedAt: queuedAt,
		})
		if err != nil {
			return "", fmt.Errorf("queue store: encode item: %w", err)
		}
		err = s.col.Create(ctx, &persis.Record{
			ID:        queueRecordID(name, itemID),
			Data:      data,
			CreatedAt: queuedAt,
			UpdatedAt: queuedAt,
		})
		if err == nil {
			return itemID, nil
		}
		if !errors.Is(err, persis.ErrConflict) {
			return "", err
		}
	}
	return "", fmt.Errorf("queue store: could not allocate unique item ID for dag-run %q", dagRun.ID)
}

// DequeueByDAGRunID removes all queued items matching dagRun from the named queue.
func (s *QueueStore) DequeueByDAGRunID(ctx context.Context, name string, dagRun ir.DAGRunRef) ([]queue.QueuedItemData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items, err := s.listQueue(ctx, name)
	if err != nil {
		return nil, err
	}

	removed := make([]queue.QueuedItemData, 0)
	removedIDs := make([]string, 0)
	for _, item := range items {
		if item.dataErr != nil || item.dagRun != dagRun {
			continue
		}
		deleted, err := s.deleteQueueRecord(ctx, item.recordID)
		if err != nil {
			return nil, err
		}
		if !deleted {
			continue
		}
		removed = append(removed, item)
		removedIDs = append(removedIDs, item.ID())
	}
	if len(removed) == 0 {
		return nil, queue.ErrQueueItemNotFound
	}
	s.removeQueueIndexItemsLocked(ctx, name, removedIDs...)
	return removed, nil
}

// DeleteByItemIDs removes exact queue item IDs from the named queue.
func (s *QueueStore) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	deletedIDs := make([]string, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		itemID = normalizeQueueItemID(itemID)
		if itemID == "" {
			continue
		}
		recordID := queueRecordID(name, itemID)
		ok, err := s.deleteQueueRecord(ctx, recordID)
		if err != nil {
			return deleted, err
		}
		if ok {
			deleted++
			deletedIDs = append(deletedIDs, itemID)
		}
	}
	s.removeQueueIndexItemsLocked(ctx, name, deletedIDs...)
	return deleted, nil
}

// Len returns the number of queued items in the named queue.
func (s *QueueStore) Len(ctx context.Context, name string) (int, error) {
	items, err := s.listQueue(ctx, name)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// List returns all queued items in the named queue.
func (s *QueueStore) List(ctx context.Context, name string) ([]queue.QueuedItemData, error) {
	items, err := s.listQueue(ctx, name)
	if err != nil {
		return nil, err
	}
	return queueItemsAsData(items), nil
}

// GetByItemID returns an exact queued item from the named queue.
func (s *QueueStore) GetByItemID(ctx context.Context, name, itemID string) (queue.QueuedItemData, error) {
	itemID = normalizeQueueItemID(itemID)
	if name == "" || itemID == "" {
		return nil, queue.ErrQueueItemNotFound
	}

	recordID := queueRecordID(name, itemID)
	rec, err := s.col.Get(ctx, recordID)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, queue.ErrQueueItemNotFound
	}
	if err != nil {
		return invalidQueueItemFromRecordID(recordID, err)
	}
	return queueItemFromRecord(rec)
}

// ListCursor returns one forward-only page of queued items.
func (s *QueueStore) ListCursor(ctx context.Context, name, cursor string, limit int) (pagination.CursorResult[queue.QueuedItemData], error) {
	if limit <= 0 {
		limit = 1
	}
	decoded, err := decodeQueueCursor(name, cursor)
	if err != nil {
		return pagination.CursorResult[queue.QueuedItemData]{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCursorLocked(ctx, name, decoded, limit)
}

// Revision returns the current ordered membership revision of the named queue.
func (s *QueueStore) Revision(ctx context.Context, name string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.loadOrRebuildQueueIndexLocked(ctx, name)
	if err != nil {
		return 0, err
	}
	if idx.total() == 0 {
		return 0, nil
	}
	return idx.Revision, nil
}

// All returns all queued items across all queues.
func (s *QueueStore) All(ctx context.Context) ([]queue.QueuedItemData, error) {
	items, err := s.listAllQueueItems(ctx, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	return queueItemsAsData(items), nil
}

// ListByDAGName returns all items in a queue for a DAG name.
func (s *QueueStore) ListByDAGName(ctx context.Context, name, dagName string) ([]queue.QueuedItemData, error) {
	items, err := s.listQueue(ctx, name)
	if err != nil {
		return nil, err
	}
	filtered := make([]*queueItem, 0, len(items))
	for _, item := range items {
		if item.dataErr != nil {
			continue
		}
		if item.dagRun.Name == dagName {
			filtered = append(filtered, item)
		}
	}
	return queueItemsAsData(filtered), nil
}

// QueueList lists queue names that currently have at least one item record.
func (s *QueueStore) QueueList(ctx context.Context) ([]string, error) {
	ids, err := s.queueRecordIDs(ctx, "")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, id := range ids {
		queueName, ok := queueNameFromItemRecordID(id)
		if ok {
			seen[queueName] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// QueueWatcher returns a backend-neutral polling watcher.
func (s *QueueStore) QueueWatcher(ctx context.Context) queue.QueueWatcher {
	return newPollingQueueWatcher(queuePollInterval, func(watchCtx context.Context) (string, error) {
		if watchCtx == nil {
			watchCtx = ctx
		}
		return s.queueFingerprint(watchCtx)
	})
}

func (s *QueueStore) listQueue(ctx context.Context, name string) ([]*queueItem, error) {
	return s.listAllQueueItems(ctx, persis.ListQuery{Prefix: queueItemPrefix(name)})
}

func (s *QueueStore) listAllQueueItems(ctx context.Context, q persis.ListQuery) ([]*queueItem, error) {
	ids, err := s.queueRecordIDs(ctx, q.Prefix)
	if err != nil {
		return nil, err
	}
	items := make([]*queueItem, 0, len(ids))
	for _, id := range ids {
		if !isQueueItemRecordID(id) {
			continue
		}
		rec, err := s.col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			continue
		}
		if err != nil {
			item, invalidErr := invalidQueueItemFromRecordID(id, err)
			if invalidErr != nil {
				return nil, invalidErr
			}
			items = append(items, item)
			continue
		}
		item, err := queueItemFromRecord(rec)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sortQueueItems(items)
	return items, nil
}

func (s *QueueStore) queueRecordIDs(ctx context.Context, prefix string) ([]string, error) {
	if col, ok := s.col.(recordIDsCollection); ok {
		return col.RecordIDs(ctx, prefix)
	}
	recs, err := listAll(ctx, s.col, persis.ListQuery{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, rec.ID)
	}
	return ids, nil
}

func (s *QueueStore) deleteQueueRecord(ctx context.Context, recordID string) (bool, error) {
	deleted := false
	err := retryConflict(ctx, func(ctx context.Context) error {
		rec, err := s.col.Get(ctx, recordID)
		if errors.Is(err, persis.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		err = s.col.CompareAndDelete(ctx, rec)
		if errors.Is(err, persis.ErrNotFound) {
			return nil
		}
		if err == nil {
			deleted = true
		}
		return err
	})
	return deleted, err
}

func (s *QueueStore) queueFingerprint(ctx context.Context) (string, error) {
	ids, err := s.queueRecordIDs(ctx, "")
	if err != nil {
		return "", err
	}
	itemIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if isQueueItemRecordID(id) {
			itemIDs = append(itemIDs, id)
		}
	}
	sort.Strings(itemIDs)
	return strings.Join(itemIDs, "\n"), nil
}
