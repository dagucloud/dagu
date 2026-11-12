// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func record(id, dagName string, createdAt time.Time) *backfill.Record {
	slot := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	return &backfill.Record{
		ID:         id,
		DAGName:    dagName,
		DAGFile:    dagName + ".yaml",
		Definition: []byte("schedule: \"0 2 * * *\"\n"),
		Options:    backfill.Options{From: slot, Rerun: backfill.RerunNone, MaxConcurrency: 1, OnFailure: backfill.OnFailureStop},
		CreatedAt:  createdAt,
		Slots:      []backfill.RecordSlot{{At: slot}},
		Revision:   "rev",
	}
}

func TestStoreCreateGetList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := filebackfill.New(t.TempDir())
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, store.Create(ctx, record("bbbbbbbb", "etl", now.Add(time.Minute))))
	require.NoError(t, store.Create(ctx, record("aaaaaaaa", "etl", now)))
	require.NoError(t, store.Create(ctx, record("cccccccc", "other", now)))
	require.ErrorIs(t, store.Create(ctx, record("aaaaaaaa", "etl", now)), backfill.ErrExists)

	got, err := store.Get(ctx, "bbbbbbbb")
	require.NoError(t, err)
	assert.Equal(t, record("bbbbbbbb", "etl", now.Add(time.Minute)), got)
	_, err = store.Get(ctx, "missing1")
	require.ErrorIs(t, err, backfill.ErrNotFound)

	records, err := store.List(ctx, "etl")
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "aaaaaaaa", records[0].ID, "oldest first")
	all, err := store.List(ctx, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

// Holders of a DAG's lock in different stores run one at a time.
func TestStoreLockDAGSerializes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	var inside, overlaps atomic.Int32

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			unlock, err := filebackfill.New(dir).LockDAG(ctx, "etl")
			if !assert.NoError(t, err) {
				return
			}
			if inside.Add(1) > 1 {
				overlaps.Add(1)
			}
			time.Sleep(10 * time.Millisecond)
			inside.Add(-1)
			unlock()
		})
	}
	wg.Wait()
	assert.Zero(t, overlaps.Load())
}

func TestStoreProgressAndEnd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := filebackfill.New(t.TempDir())
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Create(ctx, record("aaaaaaaa", "etl", now)))
	require.ErrorIs(t, store.Append(ctx, "missing1", backfill.Progress{}), backfill.ErrNotFound)

	slot := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	require.NoError(t, store.Append(ctx, "aaaaaaaa", backfill.Progress{Slot: slot, Kind: backfill.ProgressEnqueued, RunID: "r1", At: now}))
	require.NoError(t, store.Append(ctx, "aaaaaaaa",
		backfill.Progress{Slot: slot.AddDate(0, 0, 1), Kind: backfill.ProgressSkipped, At: now},
		backfill.Progress{Slot: slot.AddDate(0, 0, 2), Kind: backfill.ProgressNotRun, At: now},
	))
	events, err := store.Progress(ctx, "aaaaaaaa")
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, backfill.ProgressEnqueued, events[0].Kind)
	assert.Equal(t, "r1", events[0].RunID)
	assert.Equal(t, backfill.ProgressNotRun, events[2].Kind)

	open, err := store.Open(ctx)
	require.NoError(t, err)
	require.Len(t, open, 1)

	require.NoError(t, store.End(ctx, "aaaaaaaa", backfill.StatusFailed, now.Add(time.Hour)))
	open, err = store.Open(ctx)
	require.NoError(t, err)
	assert.Empty(t, open, "an ended backfill is not open")
	ended, err := store.Get(ctx, "aaaaaaaa")
	require.NoError(t, err)
	assert.False(t, ended.Open())
	assert.Equal(t, backfill.StatusFailed, ended.Status)
	records, err := store.List(ctx, "etl")
	require.NoError(t, err)
	assert.Len(t, records, 1, "progress files are not records")
}
