// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package slotindex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func slotAt(day, hour int) time.Time {
	return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC)
}

func TestStoreEventsInRange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := slotindex.New(t.TempDir())

	for _, event := range []persis.SlotEvent{
		{Kind: persis.SlotEventRun, DAGName: "etl", Slot: slotAt(1, 2), RunID: "a", TriggerType: ir.TriggerTypeScheduler},
		{Kind: persis.SlotEventRun, DAGName: "etl", Slot: slotAt(2, 2), RunID: "b", TriggerType: ir.TriggerTypeScheduler},
		{Kind: persis.SlotEventFinal, DAGName: "etl", Slot: slotAt(2, 2), RunID: "b", Status: ir.Succeeded},
		{Kind: persis.SlotEventRun, DAGName: "etl", Slot: slotAt(3, 2), RunID: "c", TriggerType: ir.TriggerTypeScheduler},
		{Kind: persis.SlotEventRun, DAGName: "other", Slot: slotAt(2, 2), RunID: "x"},
	} {
		require.NoError(t, store.Record(ctx, event))
	}

	events, err := store.Events(ctx, "etl", slotAt(2, 0), slotAt(3, 1))
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, persis.SlotEventRun, events[0].Kind)
	assert.Equal(t, "b", events[0].RunID)
	assert.Equal(t, persis.SlotEventFinal, events[1].Kind)
	assert.Equal(t, ir.Succeeded, events[1].Status)
	assert.False(t, events[0].RecordedAt.IsZero())
}

func TestStoreStoresSlotsInUTC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := slotindex.New(t.TempDir())
	tokyo := time.FixedZone("JST", 9*60*60)
	// 2026-10-02 01:00 in Tokyo is October 1 in UTC.
	require.NoError(t, store.Record(ctx, persis.SlotEvent{
		Kind: persis.SlotEventRun, DAGName: "etl", Slot: time.Date(2026, 10, 2, 1, 0, 0, 0, tokyo), RunID: "a",
	}))

	events, err := store.Events(ctx, "etl", slotAt(1, 0), slotAt(1, 23))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, slotAt(1, 16), events[0].Slot)
}

// A line cut short by a crash during an append does not hide the events
// around it.
func TestStoreSkipsTornLine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := slotindex.New(dir)
	require.NoError(t, store.Record(ctx, persis.SlotEvent{Kind: persis.SlotEventRun, DAGName: "etl", Slot: slotAt(1, 2), RunID: "a"}))

	matches, err := filepath.Glob(filepath.Join(dir, "*", "2026-10-01.jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	f, err := os.OpenFile(matches[0], os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"kind":"run","dagName":"etl","slo` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, store.Record(ctx, persis.SlotEvent{Kind: persis.SlotEventRun, DAGName: "etl", Slot: slotAt(1, 3), RunID: "b"}))

	events, err := store.Events(ctx, "etl", slotAt(1, 0), slotAt(1, 23))
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "a", events[0].RunID)
	assert.Equal(t, "b", events[1].RunID)
}

func TestStorePrunesWholeDaysBeforeCutoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := slotindex.New(t.TempDir())
	for day := 1; day <= 3; day++ {
		require.NoError(t, store.Record(ctx, persis.SlotEvent{Kind: persis.SlotEventRun, DAGName: "etl", Slot: slotAt(day, 2), RunID: fmt.Sprint(day)}))
	}

	require.NoError(t, store.Prune(ctx, "etl", slotAt(2, 12)))

	events, err := store.Events(ctx, "etl", slotAt(1, 0), slotAt(3, 23))
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "2", events[0].RunID, "the day of the cutoff is kept")
	assert.Equal(t, "3", events[1].RunID)
}

func TestStoreImportMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := slotindex.New(t.TempDir())

	imported, err := store.Imported(ctx, "etl")
	require.NoError(t, err)
	assert.False(t, imported)

	require.NoError(t, store.MarkImported(ctx, "etl"))
	imported, err = store.Imported(ctx, "etl")
	require.NoError(t, err)
	assert.True(t, imported)

	imported, err = store.Imported(ctx, "other")
	require.NoError(t, err)
	assert.False(t, imported)
}

// Writers in different processes each open their own store; none of their
// events may be lost or interleaved.
func TestStoreConcurrentWritersKeepEveryEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	const writers, perWriter = 8, 25

	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			store := slotindex.New(dir)
			for i := range perWriter {
				err := store.Record(ctx, persis.SlotEvent{
					Kind: persis.SlotEventRun, DAGName: "etl", Slot: slotAt(1, 2), RunID: fmt.Sprintf("%d-%d", w, i),
				})
				assert.NoError(t, err)
			}
		})
	}
	wg.Wait()

	events, err := slotindex.New(dir).Events(ctx, "etl", slotAt(1, 0), slotAt(1, 23))
	require.NoError(t, err)
	assert.Len(t, events, writers*perWriter)
}
