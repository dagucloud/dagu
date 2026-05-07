// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

var (
	_ exec.QueueStore      = (*Store)(nil)
	_ exec.QueueLeaseStore = (*Store)(nil)
)

const (
	queueCursorVersion = 1
	queuePollInterval  = 2 * time.Second
)

// Queue returns the queue store implemented by this PostgreSQL control-plane store.
func (s *Store) Queue() exec.QueueStore {
	return s
}

// Enqueue adds an item to a durable PostgreSQL queue.
func (s *Store) Enqueue(ctx context.Context, name string, priority exec.QueuePriority, dagRun exec.DAGRunRef) error {
	if name == "" {
		return errors.New("queue name must not be empty")
	}
	if priority != exec.QueuePriorityHigh && priority != exec.QueuePriorityLow {
		return fmt.Errorf("invalid queue priority: %d", priority)
	}
	if err := core.ValidateDAGName(dagRun.Name); err != nil {
		return err
	}
	if err := exec.ValidateDAGRunID(dagRun.ID); err != nil {
		return err
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate queue item id: %w", err)
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(queuePayload{
		DAGRun:   dagRun,
		QueuedAt: now,
	})
	if err != nil {
		return fmt.Errorf("marshal queue item payload: %w", err)
	}

	return s.queries.EnqueueQueueItem(ctx, db.EnqueueQueueItemParams{
		ID:         id,
		QueueName:  name,
		Priority:   int32(priority), //nolint:gosec
		DagName:    dagRun.Name,
		DagRunID:   dagRun.ID,
		Data:       payload,
		EnqueuedAt: timestamptz(now),
	})
}

// DequeueByName retrieves and removes the next ready item from the queue.
func (s *Store) DequeueByName(ctx context.Context, name string) (exec.QueuedItemData, error) {
	row, err := s.queries.DequeueQueueItemByName(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, exec.ErrQueueEmpty
	}
	if err != nil {
		return nil, err
	}
	return queueItemFromRow(row), nil
}

// DequeueByDAGRunID retrieves and removes queued items by DAG-run reference.
func (s *Store) DequeueByDAGRunID(ctx context.Context, name string, dagRun exec.DAGRunRef) ([]exec.QueuedItemData, error) {
	rows, err := s.queries.DeleteQueueItemsByDAGRun(ctx, db.DeleteQueueItemsByDAGRunParams{
		QueueName: name,
		DagName:   dagRun.Name,
		DagRunID:  dagRun.ID,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, exec.ErrQueueItemNotFound
	}
	return queueItemsFromRows(rows), nil
}

// DeleteByItemIDs removes the exact queued items identified by their IDs.
func (s *Store) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	ids := make([]uuid.UUID, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		if itemID == "" {
			continue
		}
		id, err := uuid.Parse(itemID)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	rows, err := s.queries.DeleteQueueItemsByIDs(ctx, db.DeleteQueueItemsByIDsParams{
		QueueName: name,
		Ids:       ids,
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// Len returns the number of queued items for a queue.
func (s *Store) Len(ctx context.Context, name string) (int, error) {
	count, err := s.queries.CountQueueItemsByName(ctx, name)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// List returns all queued items for a queue in processing order.
func (s *Store) List(ctx context.Context, name string) ([]exec.QueuedItemData, error) {
	rows, err := s.queries.ListQueueItemsByName(ctx, name)
	if err != nil {
		return nil, err
	}
	return queueItemsFromRows(rows), nil
}

// ListCursor returns one forward-only page of queued items for a specific queue.
func (s *Store) ListCursor(ctx context.Context, name, cursor string, limit int) (exec.CursorResult[exec.QueuedItemData], error) {
	if limit <= 0 {
		limit = 1
	}

	decoded, err := decodePostgresQueueCursor(name, cursor)
	if err != nil {
		return exec.CursorResult[exec.QueuedItemData]{}, err
	}

	rows, err := s.queries.ListQueueItemsByNameCursor(ctx, db.ListQueueItemsByNameCursorParams{
		QueueName:       name,
		HasCursor:       cursor != "",
		AfterPriority:   decoded.Priority,
		AfterEnqueuedAt: timestamptz(decoded.EnqueuedAt),
		AfterID:         decoded.ID,
		RowLimit:        int32(limit + 1), //nolint:gosec
	})
	if err != nil {
		return exec.CursorResult[exec.QueuedItemData]{}, err
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := queueItemsFromRows(rows)

	nextCursor := ""
	if hasMore && len(rows) > 0 {
		nextCursor = encodePostgresQueueCursor(name, rows[len(rows)-1])
	}

	return exec.CursorResult[exec.QueuedItemData]{
		Items:      items,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// All returns all queued items across all queues.
func (s *Store) All(ctx context.Context) ([]exec.QueuedItemData, error) {
	rows, err := s.queries.ListAllQueueItems(ctx)
	if err != nil {
		return nil, err
	}
	return queueItemsFromRows(rows), nil
}

// ListByDAGName returns queued items for a DAG in one queue.
func (s *Store) ListByDAGName(ctx context.Context, name, dagName string) ([]exec.QueuedItemData, error) {
	rows, err := s.queries.ListQueueItemsByDAGName(ctx, db.ListQueueItemsByDAGNameParams{
		QueueName: name,
		DagName:   dagName,
	})
	if err != nil {
		return nil, err
	}
	return queueItemsFromRows(rows), nil
}

// QueueList lists queue names that have at least one item.
func (s *Store) QueueList(ctx context.Context) ([]string, error) {
	return s.queries.ListQueueNames(ctx)
}

// QueueWatcher returns a polling watcher for PostgreSQL queue changes.
func (s *Store) QueueWatcher(context.Context) exec.QueueWatcher {
	return &postgresQueueWatcher{
		interval: queuePollInterval,
		quit:     make(chan struct{}),
		notifyCh: make(chan struct{}, 1),
	}
}

// ClaimByItemID leases one queue item for one processor.
func (s *Store) ClaimByItemID(ctx context.Context, name, itemID, owner string, leaseTimeout time.Duration) (exec.LeasedQueueItemData, error) {
	id, err := uuid.Parse(itemID)
	if err != nil {
		return nil, exec.ErrQueueItemNotFound
	}
	leaseToken, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate queue lease token: %w", err)
	}
	if owner == "" {
		owner = "unknown"
	}
	if leaseTimeout <= 0 {
		leaseTimeout = exec.DefaultStaleLeaseThreshold
	}
	now := time.Now().UTC()

	row, err := s.queries.ClaimQueueItemByID(ctx, db.ClaimQueueItemByIDParams{
		LeaseToken: uuid.NullUUID{
			UUID:  leaseToken,
			Valid: true,
		},
		LeaseOwner:     pgtype.Text{String: owner, Valid: true},
		LeasedAt:       timestamptz(now),
		LeaseExpiresAt: timestamptz(now.Add(leaseTimeout)),
		QueueName:      name,
		ID:             id,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, exec.ErrQueueItemNotFound
	}
	if err != nil {
		return nil, err
	}
	return leasedQueueItemFromRow(row), nil
}

// AckLease removes a leased queue item after successful dispatch.
func (s *Store) AckLease(ctx context.Context, name, leaseToken string) error {
	token, err := parseLeaseToken(leaseToken)
	if err != nil {
		return err
	}
	rows, err := s.queries.AckQueueItemLease(ctx, db.AckQueueItemLeaseParams{
		QueueName:  name,
		LeaseToken: token,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return exec.ErrQueueItemNotFound
	}
	return nil
}

// ReleaseLease clears a lease so another processor can claim the queue item.
func (s *Store) ReleaseLease(ctx context.Context, name, leaseToken string) error {
	token, err := parseLeaseToken(leaseToken)
	if err != nil {
		return err
	}
	rows, err := s.queries.ReleaseQueueItemLease(ctx, db.ReleaseQueueItemLeaseParams{
		QueueName:  name,
		LeaseToken: token,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return exec.ErrQueueItemNotFound
	}
	return nil
}

type queuePayload struct {
	DAGRun   exec.DAGRunRef `json:"dagRun"`
	QueuedAt time.Time      `json:"queuedAt"`
}

type postgresQueuedItem struct {
	id         string
	payload    []byte
	fallback   exec.DAGRunRef
	leaseToken string
}

func queueItemFromRow(row db.DaguQueueItem) exec.QueuedItemData {
	return &postgresQueuedItem{
		id:       row.ID.String(),
		payload:  row.Data,
		fallback: exec.NewDAGRunRef(row.DagName, row.DagRunID),
	}
}

func leasedQueueItemFromRow(row db.DaguQueueItem) exec.LeasedQueueItemData {
	item := &postgresQueuedItem{
		id:       row.ID.String(),
		payload:  row.Data,
		fallback: exec.NewDAGRunRef(row.DagName, row.DagRunID),
	}
	if row.LeaseToken.Valid {
		item.leaseToken = row.LeaseToken.UUID.String()
	}
	return item
}

func queueItemsFromRows(rows []db.DaguQueueItem) []exec.QueuedItemData {
	items := make([]exec.QueuedItemData, 0, len(rows))
	for _, row := range rows {
		items = append(items, queueItemFromRow(row))
	}
	return items
}

func (i *postgresQueuedItem) ID() string {
	return i.id
}

func (i *postgresQueuedItem) Data() (*exec.DAGRunRef, error) {
	if len(i.payload) > 0 {
		var payload queuePayload
		if err := json.Unmarshal(i.payload, &payload); err != nil {
			return nil, fmt.Errorf("unmarshal queue item payload: %w", err)
		}
		if !payload.DAGRun.Zero() {
			return &payload.DAGRun, nil
		}
	}
	return &i.fallback, nil
}

func (i *postgresQueuedItem) LeaseToken() string {
	return i.leaseToken
}

type postgresQueueCursor struct {
	Version    int       `json:"v"`
	QueueName  string    `json:"q"`
	Priority   int32     `json:"p"`
	EnqueuedAt time.Time `json:"t"`
	ID         uuid.UUID `json:"i"`
}

func encodePostgresQueueCursor(queueName string, row db.DaguQueueItem) string {
	return exec.EncodeSearchCursor(postgresQueueCursor{
		Version:    queueCursorVersion,
		QueueName:  queueName,
		Priority:   row.Priority,
		EnqueuedAt: timeFromTimestamptz(row.EnqueuedAt),
		ID:         row.ID,
	})
}

func decodePostgresQueueCursor(queueName, raw string) (postgresQueueCursor, error) {
	if raw == "" {
		return postgresQueueCursor{Version: queueCursorVersion, QueueName: queueName}, nil
	}
	var cursor postgresQueueCursor
	if err := exec.DecodeSearchCursor(raw, &cursor); err != nil {
		return postgresQueueCursor{}, err
	}
	if cursor.Version != queueCursorVersion || cursor.QueueName != queueName || cursor.ID == uuid.Nil || cursor.EnqueuedAt.IsZero() {
		return postgresQueueCursor{}, exec.ErrInvalidCursor
	}
	return cursor, nil
}

func parseLeaseToken(value string) (uuid.NullUUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.NullUUID{}, exec.ErrQueueItemNotFound
	}
	return uuid.NullUUID{UUID: id, Valid: true}, nil
}

type postgresQueueWatcher struct {
	interval time.Duration
	quit     chan struct{}
	notifyCh chan struct{}
	wg       sync.WaitGroup
	once     sync.Once
}

func (w *postgresQueueWatcher) Start(ctx context.Context) (<-chan struct{}, error) {
	w.wg.Go(func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.quit:
				return
			case <-ticker.C:
				select {
				case w.notifyCh <- struct{}{}:
				default:
				}
			}
		}
	})
	return w.notifyCh, nil
}

func (w *postgresQueueWatcher) Stop(context.Context) {
	w.once.Do(func() {
		close(w.quit)
		w.wg.Wait()
	})
}
