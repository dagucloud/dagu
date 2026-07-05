// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"time"

	"github.com/dagucloud/dagu/internal/core"
)

// DefaultMaxBufferItems is the default maximum number of items a ScheduleBuffer
// can hold. Prevents unbounded memory growth.
const DefaultMaxBufferItems = 1000

// QueueItem represents a scheduled or catch-up run to be dispatched.
type QueueItem struct {
	DAG           *core.DAG
	ScheduledTime time.Time
	TriggerType   core.TriggerType
	ScheduleType  ScheduleType
	Schedule      core.Schedule
}

// ScheduleBuffer is a per-DAG in-memory FIFO queue for catch-up runs.
// It holds no concurrency primitives — all access happens from the cronLoop goroutine.
type ScheduleBuffer struct {
	items         []QueueItem
	overlapPolicy core.OverlapPolicy
	dagName       string // retained for debugging and log context
	maxItems      int
}

// NewScheduleBuffer creates a per-DAG queue with the given overlap policy
// and a default max capacity of DefaultMaxBufferItems.
func NewScheduleBuffer(dagName string, policy core.OverlapPolicy) *ScheduleBuffer {
	return &ScheduleBuffer{
		overlapPolicy: policy,
		dagName:       dagName,
		maxItems:      DefaultMaxBufferItems,
	}
}

// Send appends an item to the back of the queue.
// Returns false if the buffer is full.
func (q *ScheduleBuffer) Send(item QueueItem) bool {
	if q.maxItems > 0 && len(q.items) >= q.maxItems {
		return false
	}
	q.items = append(q.items, item)
	return true
}

// Peek returns the front item without removing it.
// Returns false if the queue is empty.
func (q *ScheduleBuffer) Peek() (QueueItem, bool) {
	if len(q.items) == 0 {
		return QueueItem{}, false
	}
	return q.items[0], true
}

// Pop removes and returns the front item.
// Returns false if the queue is empty.
func (q *ScheduleBuffer) Pop() (QueueItem, bool) {
	if len(q.items) == 0 {
		return QueueItem{}, false
	}
	item := q.items[0]
	q.items[0] = QueueItem{} // clear reference for GC
	q.items = q.items[1:]
	// Compact when backing array is >2x the live elements to prevent memory leak.
	if cap(q.items) > 2*len(q.items)+16 {
		q.items = append(make([]QueueItem, 0, len(q.items)), q.items...)
	}
	return item, true
}

// DropAllButLast removes all items except the last one from the queue.
// Returns the dropped items in FIFO order. Returns nil if queue has 0 or 1 items.
func (q *ScheduleBuffer) DropAllButLast() []QueueItem {
	if len(q.items) <= 1 {
		return nil
	}
	dropped := make([]QueueItem, len(q.items)-1)
	copy(dropped, q.items[:len(q.items)-1])
	last := q.items[len(q.items)-1]
	// Clear references for GC
	for i := range q.items {
		q.items[i] = QueueItem{}
	}
	q.items = []QueueItem{last}
	return dropped
}

// Prepend inserts an item at the front of the queue.
// Returns false if the buffer is full.
func (q *ScheduleBuffer) Prepend(item QueueItem) bool {
	if q.maxItems > 0 && len(q.items) >= q.maxItems {
		return false
	}
	q.items = append([]QueueItem{item}, q.items...)
	return true
}

// Len returns the number of items in the queue.
func (q *ScheduleBuffer) Len() int {
	return len(q.items)
}
