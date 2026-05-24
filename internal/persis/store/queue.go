// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

const (
	queueCursorVersion = 1
	queueDateTimeUTC   = "20060102_150405"
	queuePollInterval  = 2 * time.Second
)

var (
	queueItemIDPattern = regexp.MustCompile(`^item_(high|low)_(\d{8}_\d{6})_(\d{9})Z_(.*)$`)
)

var _ exec.QueueStore = (*QueueStore)(nil)

// QueueStore implements [exec.QueueStore] on top of a [persis.Collection].
// Records are keyed as "{queueName}/{itemID}", while item IDs exposed through
// exec.QueuedItemData intentionally stay as "{itemID}" for caller compatibility.
type QueueStore struct {
	col persis.Collection
}

// NewQueueStore creates a QueueStore backed by col.
func NewQueueStore(col persis.Collection) *QueueStore {
	return &QueueStore{col: col}
}

type queueItemPayload struct {
	FileName string         `json:"fileName"`
	DAGRun   exec.DAGRunRef `json:"dagRun"`
	QueuedAt time.Time      `json:"queuedAt"`
}

type queueItem struct {
	id       string
	queue    string
	priority exec.QueuePriority
	queuedAt time.Time
	dagRun   exec.DAGRunRef
	recordID string
}

var _ exec.QueuedItemData = (*queueItem)(nil)

func (i *queueItem) ID() string {
	if i == nil {
		return ""
	}
	return i.id
}

func (i *queueItem) Data() (*exec.DAGRunRef, error) {
	if i == nil {
		return nil, fmt.Errorf("queue item is nil")
	}
	ref := i.dagRun
	return &ref, nil
}

type queueReadCursor struct {
	Version     int    `json:"version"`
	Queue       string `json:"queue"`
	Offset      int    `json:"offset"`
	AfterItemID string `json:"afterItemId"`
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
	return s.col.Put(ctx, &persis.Record{
		ID:        queueRecordID(name, itemID),
		Data:      data,
		Encoding:  enc,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// DequeueByName retrieves and removes the next item from the queue.
func (s *QueueStore) DequeueByName(ctx context.Context, name string) (exec.QueuedItemData, error) {
	rec, err := s.col.Claim(ctx, persis.ListQuery{Prefix: queuePrefix(name)})
	if err != nil {
		if errors.Is(err, persis.ErrNotFound) {
			return nil, exec.ErrQueueEmpty
		}
		return nil, err
	}
	item, err := queueItemFromRecord(rec)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// DequeueByDAGRunID removes all queued items matching dagRun from the named queue.
func (s *QueueStore) DequeueByDAGRunID(ctx context.Context, name string, dagRun exec.DAGRunRef) ([]exec.QueuedItemData, error) {
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
	return removed, nil
}

// DeleteByItemIDs removes exact queue item IDs from the named queue.
func (s *QueueStore) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	deleted := 0
	for _, itemID := range itemIDs {
		itemID = normalizeQueueItemID(itemID)
		if itemID == "" {
			continue
		}
		recordID := queueRecordID(name, itemID)
		if _, err := s.col.Get(ctx, recordID); err != nil {
			if errors.Is(err, persis.ErrNotFound) {
				continue
			}
			return deleted, err
		}
		if err := s.col.Delete(ctx, recordID); err != nil && !errors.Is(err, persis.ErrNotFound) {
			return deleted, err
		}
		deleted++
	}
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
	items, err := s.listQueue(ctx, name)
	if err != nil {
		return exec.CursorResult[exec.QueuedItemData]{}, err
	}
	decoded, err := decodeQueueCursor(name, cursor)
	if err != nil {
		return exec.CursorResult[exec.QueuedItemData]{}, err
	}
	start, err := resolveQueueCursorStart(items, decoded)
	if err != nil {
		return exec.CursorResult[exec.QueuedItemData]{}, err
	}

	end := min(start+limit, len(items))
	pageItems := queueItemsAsData(items[start:end])
	hasMore := end < len(items)
	nextCursor := ""
	if hasMore && len(pageItems) > 0 {
		nextCursor = encodeQueueCursor(name, end, pageItems[len(pageItems)-1].ID())
	}
	return exec.CursorResult[exec.QueuedItemData]{
		Items:      pageItems,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
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

// QueueList lists queue names that currently have at least one valid item.
func (s *QueueStore) QueueList(ctx context.Context) ([]string, error) {
	items, err := s.listAllQueueItems(ctx, persis.ListQuery{})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, item := range items {
		if item.queue != "" {
			seen[item.queue] = struct{}{}
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
func (s *QueueStore) QueueWatcher(_ context.Context) exec.QueueWatcher {
	return newPollingQueueWatcher(queuePollInterval)
}

func (s *QueueStore) listQueue(ctx context.Context, name string) ([]*queueItem, error) {
	return s.listAllQueueItems(ctx, persis.ListQuery{Prefix: queuePrefix(name)})
}

func (s *QueueStore) listAllQueueItems(ctx context.Context, q persis.ListQuery) ([]*queueItem, error) {
	recs, err := listAll(ctx, s.col, q)
	if err != nil {
		return nil, err
	}
	items := make([]*queueItem, 0, len(recs))
	for _, rec := range recs {
		item, err := queueItemFromRecord(rec)
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	sortQueueItems(items)
	return items, nil
}

func queueItemFromRecord(rec *persis.Record) (*queueItem, error) {
	if rec == nil {
		return nil, fmt.Errorf("queue store: nil record")
	}
	queueName, fallbackItemID, ok := splitQueueRecordID(rec.ID)
	if !ok {
		return nil, fmt.Errorf("queue store: invalid record ID %q", rec.ID)
	}

	var payload queueItemPayload
	if err := persis.Decode(rec, &payload); err != nil {
		return nil, fmt.Errorf("queue store: decode %q: %w", rec.ID, err)
	}

	itemID := normalizeQueueItemID(payload.FileName)
	if itemID == "" {
		itemID = fallbackItemID
	}
	priority, queuedAt := queueItemMetadata(itemID, rec.CreatedAt)
	if !payload.QueuedAt.IsZero() {
		queuedAt = payload.QueuedAt.UTC()
	}
	if payload.DAGRun.Name == "" || payload.DAGRun.ID == "" {
		return nil, fmt.Errorf("queue store: invalid dag-run in %q", rec.ID)
	}

	return &queueItem{
		id:       itemID,
		queue:    queueName,
		priority: priority,
		queuedAt: queuedAt,
		dagRun:   payload.DAGRun,
		recordID: rec.ID,
	}, nil
}

func queueItemsAsData(items []*queueItem) []exec.QueuedItemData {
	out := make([]exec.QueuedItemData, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func sortQueueItems(items []*queueItem) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		if left.queue != right.queue {
			return left.queue < right.queue
		}
		if !left.queuedAt.Equal(right.queuedAt) {
			return left.queuedAt.Before(right.queuedAt)
		}
		return left.id < right.id
	})
}

func queuePrefix(name string) string {
	if name == "" {
		return ""
	}
	return name + "/"
}

func queueRecordID(name, itemID string) string {
	return queuePrefix(name) + normalizeQueueItemID(itemID)
}

func splitQueueRecordID(id string) (queueName, itemID string, ok bool) {
	idx := strings.LastIndex(id, "/")
	if idx < 0 || idx == len(id)-1 {
		return "", "", false
	}
	return id[:idx], normalizeQueueItemID(id[idx+1:]), true
}

func normalizeQueueItemID(itemID string) string {
	itemID = strings.TrimSpace(itemID)
	itemID = filepath.Base(itemID)
	itemID = strings.TrimSuffix(itemID, ".json")
	if itemID == "." || itemID == string(filepath.Separator) {
		return ""
	}
	return itemID
}

func newQueueItemID(priority exec.QueuePriority, dagRunID string, t time.Time) string {
	label := "low"
	if priority == exec.QueuePriorityHigh {
		label = "high"
	}
	t = t.UTC()
	return fmt.Sprintf("item_%s_%s_%09dZ_%s_%s",
		label,
		t.Format(queueDateTimeUTC),
		t.Nanosecond(),
		dagRunID,
		uuid.NewString(),
	)
}

func queueItemMetadata(itemID string, fallback time.Time) (exec.QueuePriority, time.Time) {
	priority := exec.QueuePriorityLow
	queuedAt := fallback.UTC()
	matches := queueItemIDPattern.FindStringSubmatch(itemID)
	if len(matches) != 5 {
		return priority, queuedAt
	}
	if matches[1] == "high" {
		priority = exec.QueuePriorityHigh
	}
	parsed, err := time.Parse(queueDateTimeUTC, matches[2])
	if err != nil {
		return priority, queuedAt
	}
	nanos, err := time.ParseDuration(matches[3] + "ns")
	if err != nil {
		return priority, queuedAt
	}
	return priority, parsed.Add(nanos).UTC()
}

func encodeQueueCursor(name string, offset int, itemID string) string {
	if itemID == "" {
		return ""
	}
	return exec.EncodeSearchCursor(queueReadCursor{
		Version:     queueCursorVersion,
		Queue:       name,
		Offset:      offset,
		AfterItemID: itemID,
	})
}

func decodeQueueCursor(name, raw string) (queueReadCursor, error) {
	if raw == "" {
		return queueReadCursor{Version: queueCursorVersion, Queue: name}, nil
	}
	var cursor queueReadCursor
	if err := exec.DecodeSearchCursor(raw, &cursor); err != nil {
		return queueReadCursor{}, err
	}
	if cursor.Version != queueCursorVersion || cursor.Queue != name {
		return queueReadCursor{}, exec.ErrInvalidCursor
	}
	return cursor, nil
}

func resolveQueueCursorStart(items []*queueItem, cursor queueReadCursor) (int, error) {
	if cursor.Offset < 0 {
		return 0, exec.ErrInvalidCursor
	}
	if cursor.AfterItemID == "" {
		if cursor.Offset != 0 {
			return 0, exec.ErrInvalidCursor
		}
		return 0, nil
	}
	if cursor.Offset > 0 && cursor.Offset <= len(items) && items[cursor.Offset-1].ID() == cursor.AfterItemID {
		return cursor.Offset, nil
	}
	for i, item := range items {
		if item.ID() == cursor.AfterItemID {
			return i + 1, nil
		}
	}
	return 0, exec.ErrInvalidCursor
}

type pollingQueueWatcher struct {
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newPollingQueueWatcher(interval time.Duration) *pollingQueueWatcher {
	if interval <= 0 {
		interval = queuePollInterval
	}
	return &pollingQueueWatcher{
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

func (w *pollingQueueWatcher) Start(ctx context.Context) (<-chan struct{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	notifyCh := make(chan struct{}, 1)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-ticker.C:
				select {
				case notifyCh <- struct{}{}:
				default:
				}
			}
		}
	}()
	return notifyCh, nil
}

func (w *pollingQueueWatcher) Stop(ctx context.Context) {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
	case <-done:
	}
}
