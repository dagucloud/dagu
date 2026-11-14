// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"os"
	"path/filepath"
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

// Listing a DAG's backfills reads that DAG's records only: another DAG's
// record that does not decode leaves the list intact.
func TestStoreListReadsOneDAG(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := filebackfill.New(dir)
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Create(ctx, record("aaaaaaaa", "etl", now)))
	require.NoError(t, store.Create(ctx, record("bbbbbbbb", "other", now)))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bbbbbbbb.json"), []byte("{"), 0o600))

	records, err := store.List(ctx, "etl")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "aaaaaaaa", records[0].ID)
	_, err = store.List(ctx, "")
	assert.Error(t, err, "listing every DAG reads the record that does not decode")
}

// Holders of a DAG's lock, or of a backfill's lock, in different stores
// run one at a time.
func TestStoreLocksSerialize(t *testing.T) {
	t.Parallel()
	for name, lock := range map[string]func(*filebackfill.Store) (func(), error){
		"dag":    func(s *filebackfill.Store) (func(), error) { return s.LockDAG(context.Background(), "etl") },
		"record": func(s *filebackfill.Store) (func(), error) { return s.LockRecord(context.Background(), "aaaaaaaa") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			var inside, overlaps atomic.Int32
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					unlock, err := lock(filebackfill.New(dir))
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
		})
	}
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

// A deleted backfill is gone with its progress, and is no error to delete
// again.
func TestStoreDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := filebackfill.New(dir)
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Create(ctx, record("aaaaaaaa", "etl", now)))
	require.NoError(t, store.Create(ctx, record("bbbbbbbb", "etl", now)))
	require.NoError(t, store.Append(ctx, "aaaaaaaa", backfill.Progress{Kind: backfill.ProgressEnqueued, RunID: "r1", At: now}))

	require.NoError(t, store.Delete(ctx, "aaaaaaaa"))
	_, err := store.Get(ctx, "aaaaaaaa")
	require.ErrorIs(t, err, backfill.ErrNotFound)
	events, err := store.Progress(ctx, "aaaaaaaa")
	require.NoError(t, err)
	assert.Empty(t, events)
	for _, dag := range []string{"etl", ""} {
		records, err := store.List(ctx, dag)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "bbbbbbbb", records[0].ID)
	}
	open, err := store.Open(ctx)
	require.NoError(t, err)
	require.Len(t, open, 1)
	require.NoError(t, store.Delete(ctx, "aaaaaaaa"))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), "aaaaaaaa")
	}
}

// Progress appended after a crash cut the last event short is read back.
func TestStoreAppendsAfterATornEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := filebackfill.New(dir)
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Create(ctx, record("aaaaaaaa", "etl", now)))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "aaaaaaaa.progress.jsonl"), []byte(`{"slot":"2026-10-01T02:00:00Z","ki`), 0o600))

	slot := time.Date(2026, 10, 2, 2, 0, 0, 0, time.UTC)
	require.NoError(t, store.Append(ctx, "aaaaaaaa", backfill.Progress{Slot: slot, Kind: backfill.ProgressSkipped, At: now}))
	events, err := store.Progress(ctx, "aaaaaaaa")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, backfill.ProgressSkipped, events[0].Kind)
}

// summaryOf returns the summary the store lists for id, if any.
func summaryOf(t *testing.T, store *filebackfill.Store, id string) (backfill.Summary, bool) {
	t.Helper()
	summaries, err := store.Summaries(context.Background())
	require.NoError(t, err)
	for _, summary := range summaries {
		if summary.ID == id {
			return summary, true
		}
	}
	return backfill.Summary{}, false
}

// An open backfill has a summary, and one that ended has it only when it
// failed, with the counts last saved, until it is cleared.
func TestStoreSummaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := filebackfill.New(t.TempDir())
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, store.Create(ctx, record("aaaaaaaa", "etl", now)))
	require.NoError(t, store.Create(ctx, record("bbbbbbbb", "etl", now)))

	summary, ok := summaryOf(t, store, "aaaaaaaa")
	require.True(t, ok)
	assert.Equal(t, backfill.Summary{ID: "aaaaaaaa", DAGName: "etl", Slots: 1}, summary)

	saved := backfill.Summary{ID: "aaaaaaaa", DAGName: "etl", Slots: 1, Done: 1, Failed: 1}
	require.NoError(t, store.SaveSummary(ctx, saved))
	require.NoError(t, store.End(ctx, "aaaaaaaa", backfill.StatusFailed, now))
	summary, ok = summaryOf(t, store, "aaaaaaaa")
	require.True(t, ok)
	saved.Status = backfill.StatusFailed
	assert.Equal(t, saved, summary)

	require.NoError(t, store.End(ctx, "bbbbbbbb", backfill.StatusSucceeded, now))
	_, ok = summaryOf(t, store, "bbbbbbbb")
	assert.False(t, ok, "a backfill that succeeded needs no mention")

	// A resume opens the failed backfill again and counts its slots anew.
	reopened, err := store.Get(ctx, "aaaaaaaa")
	require.NoError(t, err)
	reopened.Status, reopened.EndedAt = "", time.Time{}
	require.NoError(t, store.Update(ctx, reopened))
	summary, ok = summaryOf(t, store, "aaaaaaaa")
	require.True(t, ok)
	assert.Equal(t, backfill.Summary{ID: "aaaaaaaa", DAGName: "etl", Slots: 1}, summary)

	require.NoError(t, store.ClearSummary(ctx, "aaaaaaaa"))
	_, ok = summaryOf(t, store, "aaaaaaaa")
	assert.False(t, ok)
	require.ErrorIs(t, store.ClearSummary(ctx, "../etl"), backfill.ErrNotFound)
}
