// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
)

// Errors for the queue
var (
	ErrQueueEmpty        = errors.New("queue is empty")
	ErrQueueItemNotFound = errors.New("queue item not found")
)

// QueueStore provides an interface for interacting with the underlying database
// for storing and retrieving queued dag-run items.
type QueueStore interface {
	// Enqueue adds an item to the queue
	Enqueue(ctx context.Context, name string, priority QueuePriority, dagRun DAGRunRef) error
	// DequeueByName retrieves an item from the queue and removes it
	DequeueByName(ctx context.Context, name string) (QueuedItemData, error)
	// DequeueByDAGRunID retrieves items from the queue by dag-run reference and removes them
	DequeueByDAGRunID(ctx context.Context, name string, dagRun DAGRunRef) ([]QueuedItemData, error)
	// DeleteByItemIDs removes the exact queued items identified by their queue item IDs.
	DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error)
	// Len returns the number of items in the queue
	Len(ctx context.Context, name string) (int, error)
	// List returns all items in the queue with the given name
	List(ctx context.Context, name string) ([]QueuedItemData, error)
	// ListCursor returns one forward-only page of queued items for a specific queue.
	ListCursor(ctx context.Context, name, cursor string, limit int) (CursorResult[QueuedItemData], error)
	// All returns all items in the queue
	All(ctx context.Context) ([]QueuedItemData, error)
	// ListByDAGName returns all items that has a specific DAG name
	ListByDAGName(ctx context.Context, name, dagName string) ([]QueuedItemData, error)
	// QueueList lists all queue names that have at least one item in the queue
	QueueList(ctx context.Context) ([]string, error)
	// Watcher returns a QueueWatcher for the queue data
	QueueWatcher(ctx context.Context) QueueWatcher
}

// QueueLeaseStore optionally provides leased queue processing. A leased queue
// item remains durable until the processor explicitly acknowledges it, so a
// crashed processor does not lose the queued run.
type QueueLeaseStore interface {
	ClaimByItemID(ctx context.Context, name, itemID, owner string, leaseTimeout time.Duration) (LeasedQueueItemData, error)
	AckLease(ctx context.Context, name, leaseToken string) error
	ReleaseLease(ctx context.Context, name, leaseToken string) error
}

// LeasedQueueItemData is a queued item claimed by one processor.
type LeasedQueueItemData interface {
	QueuedItemData
	LeaseToken() string
}

// QueueWatcher watches the queue state
type QueueWatcher interface {
	// Start start swatching queue data and signal when a queue state changed
	Start(ctx context.Context) (<-chan struct{}, error)
	// Stop stops watching queue data
	Stop(ctx context.Context)
}

// QueuePriority represents the priority of a queued item
type QueuePriority int

const (
	QueuePriorityHigh QueuePriority = iota
	QueuePriorityLow
)

// QueuedItem is a wrapper for QueuedItemData
type QueuedItem struct {
	QueuedItemData
}

// NewQueuedItem creates a new QueuedItem
func NewQueuedItem(data QueuedItemData) *QueuedItem {
	return &QueuedItem{
		QueuedItemData: data,
	}
}

// QueuedItemData represents a dag-run reference that is queued for execution.
type QueuedItemData interface {
	// ID returns the ID of the queued item
	ID() string
	// Data returns the data of the queued item
	Data() (*DAGRunRef, error)
}

var _ QueueStore = (*MockQueueStore)(nil)

// MockQueueStore is a mock implementation of QueueStore for testing.
type MockQueueStore struct {
	mock.Mock
}

func (m *MockQueueStore) Enqueue(ctx context.Context, name string, priority QueuePriority, dagRun DAGRunRef) error {
	args := m.Called(ctx, name, priority, dagRun)
	return args.Error(0)
}

func (m *MockQueueStore) DequeueByName(ctx context.Context, name string) (QueuedItemData, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) DequeueByDAGRunID(ctx context.Context, name string, dagRun DAGRunRef) ([]QueuedItemData, error) {
	args := m.Called(ctx, name, dagRun)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error) {
	args := m.Called(ctx, name, itemIDs)
	return args.Int(0), args.Error(1)
}

func (m *MockQueueStore) Len(ctx context.Context, name string) (int, error) {
	args := m.Called(ctx, name)
	return args.Int(0), args.Error(1)
}

func (m *MockQueueStore) List(ctx context.Context, name string) ([]QueuedItemData, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) ListCursor(ctx context.Context, name, cursor string, limit int) (CursorResult[QueuedItemData], error) {
	args := m.Called(ctx, name, cursor, limit)
	if args.Get(0) == nil {
		return CursorResult[QueuedItemData]{}, args.Error(1)
	}
	return args.Get(0).(CursorResult[QueuedItemData]), args.Error(1)
}

func (m *MockQueueStore) All(ctx context.Context) ([]QueuedItemData, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) ListByDAGName(ctx context.Context, name, dagName string) ([]QueuedItemData, error) {
	args := m.Called(ctx, name, dagName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]QueuedItemData), args.Error(1)
}

func (m *MockQueueStore) QueueList(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockQueueStore) QueueWatcher(ctx context.Context) QueueWatcher {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(QueueWatcher)
}
