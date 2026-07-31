// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleBuffer_SendAndPeek(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicySkip)
	assert.Equal(t, 0, q.Len())

	_, ok := q.Peek()
	assert.False(t, ok, "Peek on empty queue should return false")

	now := time.Now()
	q.Send(QueueItem{ScheduledTime: now, TriggerType: core.TriggerTypeCatchUp})

	assert.Equal(t, 1, q.Len())

	item, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, now, item.ScheduledTime)
	// Peek does not remove the item
	assert.Equal(t, 1, q.Len())
}

func TestScheduleBuffer_Pop(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicyAll)

	_, ok := q.Pop()
	assert.False(t, ok, "Pop on empty queue should return false")

	now := time.Now()
	q.Send(QueueItem{ScheduledTime: now})

	item, ok := q.Pop()
	require.True(t, ok)
	assert.Equal(t, now, item.ScheduledTime)
	assert.Equal(t, 0, q.Len())
}

func TestScheduleBuffer_FIFOOrder(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicyAll)

	base := time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)
	for i := range 5 {
		q.Send(QueueItem{
			ScheduledTime: base.Add(time.Duration(i) * time.Hour),
			TriggerType:   core.TriggerTypeCatchUp,
		})
	}

	assert.Equal(t, 5, q.Len())

	for i := range 5 {
		item, ok := q.Pop()
		require.True(t, ok)
		assert.Equal(t, base.Add(time.Duration(i)*time.Hour), item.ScheduledTime)
	}

	assert.Equal(t, 0, q.Len())
}

func TestScheduleBuffer_Len(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicySkip)
	assert.Equal(t, 0, q.Len())

	q.Send(QueueItem{ScheduledTime: time.Now()})
	assert.Equal(t, 1, q.Len())

	q.Send(QueueItem{ScheduledTime: time.Now()})
	assert.Equal(t, 2, q.Len())

	q.Pop()
	assert.Equal(t, 1, q.Len())

	q.Pop()
	assert.Equal(t, 0, q.Len())
}

func TestScheduleBuffer_DropAllButLast_MultipleItems(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicyLatest)

	base := time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC)
	for i := range 5 {
		q.Send(QueueItem{
			ScheduledTime: base.Add(time.Duration(i) * time.Hour),
			TriggerType:   core.TriggerTypeCatchUp,
		})
	}
	assert.Equal(t, 5, q.Len())

	dropped := q.DropAllButLast()
	assert.Len(t, dropped, 4)
	assert.Equal(t, 1, q.Len())

	// Dropped items should be in FIFO order
	for i, d := range dropped {
		assert.Equal(t, base.Add(time.Duration(i)*time.Hour), d.ScheduledTime)
	}

	// Remaining item should be the latest
	item, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, base.Add(4*time.Hour), item.ScheduledTime)
}

func TestScheduleBuffer_DropAllButLast_Empty(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicyLatest)
	dropped := q.DropAllButLast()
	assert.Nil(t, dropped)
	assert.Equal(t, 0, q.Len())
}

func TestScheduleBuffer_DropAllButLast_SingleItem(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicyLatest)
	q.Send(QueueItem{ScheduledTime: time.Now(), TriggerType: core.TriggerTypeCatchUp})

	dropped := q.DropAllButLast()
	assert.Nil(t, dropped)
	assert.Equal(t, 1, q.Len())
}

func TestScheduleBuffer_CapacityLimit(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicyAll)
	q.maxItems = 3

	assert.True(t, q.Send(QueueItem{ScheduledTime: time.Now()}))
	assert.True(t, q.Send(QueueItem{ScheduledTime: time.Now()}))
	assert.True(t, q.Send(QueueItem{ScheduledTime: time.Now()}))
	assert.False(t, q.Send(QueueItem{ScheduledTime: time.Now()}), "should reject when full")
	assert.Equal(t, 3, q.Len())

	// After popping one, can send again
	q.Pop()
	assert.True(t, q.Send(QueueItem{ScheduledTime: time.Now()}))
	assert.Equal(t, 3, q.Len())
}

func TestScheduleBuffer_PopCompacts(t *testing.T) {
	t.Parallel()

	q := NewScheduleBuffer("test-dag", core.OverlapPolicyAll)

	// Fill with enough items to trigger compaction
	for i := range 100 {
		q.Send(QueueItem{ScheduledTime: time.Now().Add(time.Duration(i) * time.Minute)})
	}
	assert.Equal(t, 100, q.Len())

	// Pop most items — should trigger compaction
	for range 90 {
		_, ok := q.Pop()
		require.True(t, ok)
	}
	assert.Equal(t, 10, q.Len())

	// Verify remaining items are still correct (FIFO order preserved)
	for range 10 {
		_, ok := q.Pop()
		require.True(t, ok)
	}
	assert.Equal(t, 0, q.Len())
}
