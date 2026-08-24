// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
)

// Errors for the queue
var (
	ErrQueueItemNotFound = errors.New("queue item not found")
)

// QueueStore provides an interface for interacting with the underlying database
// for storing and retrieving queued dag-run items.
type QueueStore interface {
	// Enqueue adds an item to the queue
	Enqueue(ctx context.Context, name string, priority QueuePriority, dagRun ir.DAGRunRef) error
	// DequeueByDAGRunID retrieves items from the queue by dag-run reference and removes them
	DequeueByDAGRunID(ctx context.Context, name string, dagRun ir.DAGRunRef) ([]QueuedItemData, error)
	// DeleteByItemIDs removes the exact queued items identified by their queue item IDs.
	DeleteByItemIDs(ctx context.Context, name string, itemIDs []string) (int, error)
	// Len returns the number of items in the queue
	Len(ctx context.Context, name string) (int, error)
	// List returns all items in the queue with the given name
	List(ctx context.Context, name string) ([]QueuedItemData, error)
	// GetByItemID returns an exact item from the named queue.
	GetByItemID(ctx context.Context, name, itemID string) (QueuedItemData, error)
	// ListCursor returns one forward-only page of queued items for a specific queue.
	ListCursor(ctx context.Context, name, cursor string, limit int) (pagination.CursorResult[QueuedItemData], error)
	// All returns all items in the queue
	All(ctx context.Context) ([]QueuedItemData, error)
	// ListByDAGName returns all items that has a specific DAG name
	ListByDAGName(ctx context.Context, name, dagName string) ([]QueuedItemData, error)
	// QueueList lists all queue names that have at least one item in the queue
	QueueList(ctx context.Context) ([]string, error)
	// Watcher returns a QueueWatcher for the queue data
	QueueWatcher(ctx context.Context) QueueWatcher
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

// QueuedItemData represents a dag-run reference that is queued for execution.
type QueuedItemData interface {
	// ID returns the ID of the queued item
	ID() string
	// Data returns the data of the queued item
	Data() (*ir.DAGRunRef, error)
}
