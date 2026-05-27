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

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

const (
	queueCursorVersion       = 1
	queueDateTimeUTC         = "20060102_150405"
	queueItemIDMaxCollisions = 1000
	queuePollInterval        = 2 * time.Second
)

var _ exec.QueueStore = (*QueueStore)(nil)

// QueueStore implements [exec.QueueStore] on top of a [persis.Collection].
// Records are keyed as "{queueName}/{itemID}", while item IDs exposed through
// exec.QueuedItemData intentionally stay as "{itemID}" for caller compatibility.
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
func (s *QueueStore) Enqueue(ctx context.Context, name string, priority exec.QueuePriority, dagRun exec.DAGRunRef) error {
	if name == "" {
		return fmt.Errorf("queue store: queue name is required")
	}
	if dagRun.Name == "" || dagRun.ID == "" {
		return fmt.Errorf("queue store: dag-run reference is required")
	}
	if priority != exec.QueuePriorityHigh && priority != exec.QueuePriorityLow {
		return fmt.Errorf("queue store: invalid queue priority %d", priority)
	}

	now := time.Now().UTC()
	return s.withQueueLock(ctx, name, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		itemID, queuedAt, err := s.nextQueueItemID(ctx, name, priority, dagRun.ID, now)
		if err != nil {
			return err
		}
		payload := queueItemPayload{
			FileName: itemID + ".json",
			DAGRun:   dagRun,
			QueuedAt: queuedAt,
		}
		data, enc, err := persis.Encode(payload)
		if err != nil {
			return fmt.Errorf("queue store: encode item: %w", err)
		}

		if err := s.col.Put(ctx, &persis.Record{
			ID:        queueRecordID(name, itemID),
			Data:      data,
			Encoding:  enc,
			CreatedAt: queuedAt,
			UpdatedAt: queuedAt,
		}); err != nil {
			return err
		}

		s.addQueueIndexItemLocked(ctx, name, priority, itemID)
		return nil
	})
}

func (s *QueueStore) nextQueueItemID(
	ctx context.Context,
	name string,
	priority exec.QueuePriority,
	dagRunID string,
	start time.Time,
) (string, time.Time, error) {
	start = start.UTC()
	for attempt := range queueItemIDMaxCollisions {
		queuedAt := start.Add(time.Duration(attempt) * time.Nanosecond)
		itemID := newQueueItemID(priority, dagRunID, queuedAt)
		_, err := s.col.Get(ctx, queueRecordID(name, itemID))
		if errors.Is(err, persis.ErrNotFound) {
			return itemID, queuedAt, nil
		}
		if err != nil {
			return "", time.Time{}, err
		}
	}
	return "", time.Time{}, fmt.Errorf("queue store: could not allocate unique item ID for dag-run %q", dagRunID)
}

// DequeueByName retrieves and removes the next item from the queue.
func (s *QueueStore) DequeueByName(ctx context.Context, name string) (exec.QueuedItemData, error) {
	var item exec.QueuedItemData
	err := s.withQueueLock(ctx, name, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		rec, err := s.col.Claim(ctx, persis.ListQuery{Prefix: queueItemPrefix(name)})
		if err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				return exec.ErrQueueEmpty
			}
			return err
		}
		queueItem, err := queueItemFromRecord(rec)
		if err != nil {
			return err
		}
		if queueItem.dataErr != nil {
			if restoreErr := s.col.Put(ctx, rec); restoreErr != nil {
				return fmt.Errorf("queue store: decode claimed item: %w; restore failed: %v", queueItem.dataErr, restoreErr)
			}
			return queueItem.dataErr
		}
		s.removeQueueIndexItemsLocked(ctx, name, queueItem.ID())
		item = queueItem
		return nil
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}

// DequeueByDAGRunID removes all queued items matching dagRun from the named queue.
func (s *QueueStore) DequeueByDAGRunID(ctx context.Context, name string, dagRun exec.DAGRunRef) ([]exec.QueuedItemData, error) {
	var removed []exec.QueuedItemData
	err := s.withQueueLock(ctx, name, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		items, err := s.listQueue(ctx, name)
		if err != nil {
			return err
		}

		removed = make([]exec.QueuedItemData, 0)
		for _, item := range items {
			if item.dataErr != nil || item.dagRun != dagRun {
				continue
			}
			if err := s.col.Delete(ctx, item.recordID); err != nil && !errors.Is(err, persis.ErrNotFound) {
				return err
			}
			removed = append(removed, item)
		}
		if len(removed) == 0 {
			return exec.ErrQueueItemNotFound
		}
		removedIDs := make([]string, 0, len(removed))
		for _, item := range removed {
			removedIDs = append(removedIDs, item.ID())
		}
		s.removeQueueIndexItemsLocked(ctx, name, removedIDs...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// DeleteByItemIDs removes exact queue item IDs from the named queue.
func (s *QueueStore) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	deleted := 0
	err := s.withQueueLock(ctx, name, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		deletedIDs := make([]string, 0, len(itemIDs))
		for _, itemID := range itemIDs {
			itemID = normalizeQueueItemID(itemID)
			if itemID == "" {
				continue
			}
			recordID := queueRecordID(name, itemID)
			ok, err := s.deleteQueueRecord(ctx, recordID)
			if err != nil {
				return err
			}
			if ok {
				deleted++
				deletedIDs = append(deletedIDs, itemID)
			}
		}
		s.removeQueueIndexItemsLocked(ctx, name, deletedIDs...)
		return nil
	})
	return deleted, err
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
func (s *QueueStore) List(ctx context.Context, name string) ([]exec.QueuedItemData, error) {
	items, err := s.listQueue(ctx, name)
	if err != nil {
		return nil, err
	}
	return queueItemsAsData(items), nil
}

// ListCursor returns one forward-only page of queued items.
func (s *QueueStore) ListCursor(ctx context.Context, name, cursor string, limit int) (exec.CursorResult[exec.QueuedItemData], error) {
	if limit <= 0 {
		limit = 1
	}
	decoded, err := decodeQueueCursor(name, cursor)
	if err != nil {
		return exec.CursorResult[exec.QueuedItemData]{}, err
	}

	var result exec.CursorResult[exec.QueuedItemData]
	err = s.withQueueLock(ctx, name, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		var err error
		result, err = s.listCursorLocked(ctx, name, decoded, limit)
		return err
	})
	return result, err
}

// All returns all queued items across all queues.
func (s *QueueStore) All(ctx context.Context) ([]exec.QueuedItemData, error) {
	items, err := s.listAllQueueItems(ctx, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	return queueItemsAsData(items), nil
}

// ListByDAGName returns all items in a queue for a DAG name.
func (s *QueueStore) ListByDAGName(ctx context.Context, name, dagName string) ([]exec.QueuedItemData, error) {
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
func (s *QueueStore) QueueWatcher(ctx context.Context) exec.QueueWatcher {
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

type queueLockCollection interface {
	WithLock(ctx context.Context, key string, fn func() error) error
}

type recordIDsCollection interface {
	RecordIDs(ctx context.Context, prefix string) ([]string, error)
}

type deleteIfExistsCollection interface {
	DeleteIfExists(ctx context.Context, id string) (bool, error)
}

func (s *QueueStore) withQueueLock(ctx context.Context, name string, fn func() error) error {
	if col, ok := s.col.(queueLockCollection); ok {
		return col.WithLock(ctx, name, fn)
	}
	return fn()
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
	if col, ok := s.col.(deleteIfExistsCollection); ok {
		return col.DeleteIfExists(ctx, recordID)
	}
	if _, err := s.col.Get(ctx, recordID); err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if err := s.col.Delete(ctx, recordID); err != nil {
		return false, err
	}
	return true, nil
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
