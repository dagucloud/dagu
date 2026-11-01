// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package resource

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_AddAndGet(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(time.Hour, 10*time.Second)

	// Add distinct values to verify correct metric mapping
	store.Add(10.0, 20.0, 30.0, 1.5, 16_000_000_000, 4_000_000_000, 500_000_000_000, 200_000_000_000)
	store.Add(15.0, 25.0, 35.0, 2.0, 16_000_000_000, 5_000_000_000, 500_000_000_000, 210_000_000_000)

	history := store.GetHistory(time.Hour)

	require.Len(t, history.CPU, 2)
	require.Len(t, history.Memory, 2)
	require.Len(t, history.Disk, 2)
	require.Len(t, history.Load, 2)

	// Verify ordering
	assert.Equal(t, 10.0, history.CPU[0].Value)
	assert.Equal(t, 15.0, history.CPU[1].Value)

	// Verify correct metric assignment
	assert.Equal(t, 20.0, history.Memory[0].Value)
	assert.Equal(t, 30.0, history.Disk[0].Value)
	assert.Equal(t, 1.5, history.Load[0].Value)

	// Verify absolute values (latest snapshot)
	assert.Equal(t, uint64(16_000_000_000), history.MemoryTotalBytes)
	assert.Equal(t, uint64(5_000_000_000), history.MemoryUsedBytes)
	assert.Equal(t, uint64(500_000_000_000), history.DiskTotalBytes)
	assert.Equal(t, uint64(210_000_000_000), history.DiskUsedBytes)
}

func TestMemoryStore_GetHistoryFiltering(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore(time.Hour, 10*time.Second)

		// Add old data
		store.Add(1.0, 1.0, 1.0, 1.0, 0, 0, 0, 0)

		// Deterministic: synctest.Test intercepts time.Sleep to advance the fake clock.
		time.Sleep(2100 * time.Millisecond)

		// Add new data
		store.Add(2.0, 2.0, 2.0, 2.0, 0, 0, 0, 0)

		// Long duration returns all points
		history := store.GetHistory(time.Minute)
		assert.Len(t, history.CPU, 2)

		// 1-second duration excludes old point (which is 2+ seconds old)
		historyShort := store.GetHistory(time.Second)
		require.Len(t, historyShort.CPU, 1)
		assert.Equal(t, 2.0, historyShort.CPU[0].Value)
	})
}

func TestMemoryStore_EmptyHistory(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(time.Hour, 10*time.Second)
	history := store.GetHistory(time.Hour)

	assert.Empty(t, history.CPU)
	assert.Empty(t, history.Memory)
	assert.Empty(t, history.Disk)
	assert.Empty(t, history.Load)
}

func TestMemoryStore_DiskUsedPercent(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(time.Hour, 10*time.Second)
	_, ok := store.DiskUsedPercent()
	assert.False(t, ok, "nothing recorded yet")

	store.Add(0, 0, 91.5, 0, 0, 0, 500_000_000_000, 457_500_000_000)
	used, ok := store.DiskUsedPercent()
	require.True(t, ok)
	assert.Equal(t, 91.5, used)

	// A failed disk read records no total.
	store.Add(0, 0, 0, 0, 0, 0, 0, 0)
	_, ok = store.DiskUsedPercent()
	assert.False(t, ok)
}

func TestMemoryStore_GetHistoryReturnsCopy(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore(time.Hour, 10*time.Second)
	store.Add(1.0, 1.0, 1.0, 1.0, 0, 0, 0, 0)

	history1 := store.GetHistory(time.Hour)
	history2 := store.GetHistory(time.Hour)

	// Modify history1
	history1.CPU[0].Value = 999.0

	// history2 should not be affected
	assert.Equal(t, 1.0, history2.CPU[0].Value)
}

func TestNewMemoryStore_Capacity(t *testing.T) {
	t.Parallel()

	// 1 hour retention with 10s interval = 360 points + 10 buffer = 370
	store := NewMemoryStore(time.Hour, 10*time.Second)
	assert.Equal(t, 370, len(store.buffer.buffer))

	// Invalid interval defaults to 10s
	store2 := NewMemoryStore(time.Hour, 0)
	assert.Equal(t, 370, len(store2.buffer.buffer))

	// 1 hour retention with 5s interval = 720 points + 10 buffer = 730
	store3 := NewMemoryStore(time.Hour, 5*time.Second)
	assert.Equal(t, 730, len(store3.buffer.buffer))
}
