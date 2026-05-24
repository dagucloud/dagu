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
	queueCursorVersion = 1
	queueDateTimeUTC   = "20060102_150405"
	queuePollInterval  = 2 * time.Second
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
	itemID := newQueueItemID(priority, dagRun.ID, now)
	payload := queueItemPayload{
		FileName: itemID + ".json",
		DAGRun:   dagRun,
		QueuedAt: now,
	}
	data, enc, err := persis.Encode(payload)
	if err != nil {
		return fmt.Errorf("queue store: encode item: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.col.Put(ctx, &persis.Record{
		ID:        queueRecordID(name, itemID),
		Data:      data,
		Encoding:  enc,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return err
	}

	s.addQueueIndexItemLocked(ctx, name, priority, itemID)
	return nil
}

// DequeueByName retrieves and removes the next item from the queue.
func (s *QueueStore) DequeueByName(ctx context.Context, name string) (exec.QueuedItemData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.col.Claim(ctx, persis.ListQuery{Prefix: queueItemPrefix(name)})
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, exec.ErrQueueEmpty
		}
		return nil, err
	}
	item, err := queueItemFromRecord(rec)
	if err != nil {
		if restoreErr := s.col.Put(ctx, rec); restoreErr != nil {
			return nil, fmt.Errorf("queue store: decode claimed item: %w; restore failed: %v", err, restoreErr)
		}
		return nil, err
	}
	s.removeQueueIndexItemsLocked(ctx, name, item.ID())
	return item, nil
}

// DequeueByDAGRunID removes all queued items matching dagRun from the named queue.
func (s *QueueStore) DequeueByDAGRunID(ctx context.Context, name string, dagRun exec.DAGRunRef) ([]exec.QueuedItemData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items, err := s.listQueue(ctx, name)
	if err != nil {
		return nil, err
	}

	removed := make([]exec.QueuedItemData, 0)
	for _, item := range items {
		if item.dagRun != dagRun {
			continue
		}
		if err := s.col.Delete(ctx, item.recordID); err != nil && !errors.Is(err, persis.ErrNotFound) {
			return removed, err
		}
		removed = append(removed, item)
	}
	if len(removed) == 0 {
		return nil, exec.ErrQueueItemNotFound
	}
	removedIDs := make([]string, 0, len(removed))
	for _, item := range removed {
		removedIDs = append(removedIDs, item.ID())
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

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCursorLocked(ctx, name, decoded, limit)
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
		if item.dagRun.Name == dagName {
			filtered = append(filtered, item)
		}
	}
	return queueItemsAsData(filtered), nil
}

// QueueList lists queue names that currently have at least one item record.
func (s *QueueStore) QueueList(ctx context.Context) ([]string, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, rec := range recs {
		queueName, ok := queueNameFromItemRecordID(rec.ID)
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
	recs, err := listAll(ctx, s.col, q)
	if err != nil {
		return nil, err
	}
	items := make([]*queueItem, 0, len(recs))
	for _, rec := range recs {
		if !isQueueItemRecordID(rec.ID) {
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

type deleteIfExistsCollection interface {
	DeleteIfExists(ctx context.Context, id string) (bool, error)
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
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		if isQueueItemRecordID(rec.ID) {
			ids = append(ids, rec.ID)
		}
	}
	sort.Strings(ids)
	return strings.Join(ids, "\n"), nil
}
