// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

func newSpanStore() *store.SpanStore {
	return store.NewSpanStore(testutil.NewMemoryBackend().Collection(persis.CollectionSlotSpans), nil)
}

// spanAt is an hour of a day counted from 25 days ago, inside the 30 days
// the store keeps.
func spanAt(day, hour int) time.Time {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -25)
	return start.AddDate(0, 0, day-1).Add(time.Duration(hour) * time.Hour)
}

// A DAG's spans join the scheduler's outages and the operator's pauses,
// which hold for every DAG, and not another DAG's.
func TestSpanStoreGivesTheSpansOfADAG(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pauses := store.NewSchedulerPauseStore(testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState))
	s := store.NewSpanStore(testutil.NewMemoryBackend().Collection(persis.CollectionSlotSpans), pauses)
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: spanAt(2, 9), To: spanAt(2, 11), Reason: schedulerstate.SpanDown}))
	require.NoError(t, s.Add(ctx, "etl", schedulerstate.Span{From: spanAt(1, 12), To: spanAt(1, 13), Reason: schedulerstate.SpanSuspended}))
	require.NoError(t, s.Add(ctx, "other", schedulerstate.Span{From: spanAt(1, 0), To: spanAt(1, 1), Reason: schedulerstate.SpanSuspended}))

	spans, err := s.Ended(ctx, "etl")
	require.NoError(t, err)
	require.Len(t, spans, 3)
	assert.Equal(t, []schedulerstate.Span{
		{From: spanAt(1, 12), To: spanAt(1, 13), Reason: schedulerstate.SpanSuspended},
		{From: spanAt(2, 9), To: spanAt(2, 11), Reason: schedulerstate.SpanDown},
	}, spans[:2])
	assert.Equal(t, schedulerstate.SpanPaused, spans[2].Reason)
}

// A span is recorded once by when it began, so recording it again, as a
// retried resume does, writes nothing.
func TestSpanStoreRecordsASpanOnce(t *testing.T) {
	t.Parallel()
	col := &swapCounter{Collection: testutil.NewMemoryBackend().Collection(persis.CollectionSlotSpans)}
	s := store.NewSpanStore(col, nil)
	ctx := t.Context()

	require.NoError(t, s.Add(ctx, "etl", schedulerstate.Span{From: spanAt(1, 9), To: spanAt(1, 10), Reason: schedulerstate.SpanSuspended}))
	require.NoError(t, s.Add(ctx, "etl", schedulerstate.Span{From: spanAt(1, 9), To: spanAt(1, 11), Reason: schedulerstate.SpanSuspended}))

	spans, err := s.Ended(ctx, "etl")
	require.NoError(t, err)
	assert.Equal(t, []schedulerstate.Span{{From: spanAt(1, 9), To: spanAt(1, 10), Reason: schedulerstate.SpanSuspended}}, spans)
	assert.Zero(t, col.swaps, "the second record writes nothing")
}

// A write keeps the 100 spans that ended last within the 30 days before it.
func TestSpanStoreKeepsRecentSpans(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()
	ended := time.Now().Add(-31 * 24 * time.Hour)
	require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: ended.Add(-time.Hour), To: ended, Reason: schedulerstate.SpanDown}))

	start := spanAt(1, 0)
	for i := range 101 {
		from := start.Add(time.Duration(i) * time.Hour)
		require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: from, To: from.Add(time.Minute), Reason: schedulerstate.SpanDown}))
	}

	spans, err := s.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, spans, 100)
	assert.Equal(t, start.Add(time.Hour), spans[0].From, "the span that ended first is dropped")
}

// A span that ended more than 30 days ago is not given, even before a write
// drops it.
func TestSpanStoreGivesNoExpiredSpan(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()
	ended := time.Now().Add(-31 * 24 * time.Hour)
	require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: ended.Add(-time.Hour), To: ended, Reason: schedulerstate.SpanDown}))

	spans, err := s.Ended(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, spans)
}

// The bound keeps the spans that ended last, whatever order they were
// recorded in, as when the hosts that record them disagree on the time.
func TestSpanStoreKeepsTheSpansThatEndedLast(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()
	for i := range 100 {
		from := spanAt(3, 0).Add(time.Duration(i) * time.Hour)
		require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: from, To: from.Add(time.Minute), Reason: schedulerstate.SpanDown}))
	}
	require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: spanAt(2, 0), To: spanAt(2, 1), Reason: schedulerstate.SpanDown}))

	spans, err := s.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, spans, 100)
	assert.Equal(t, spanAt(3, 0), spans[0].From, "the span that ended first is dropped")
}

// swapCounter counts the records a collection replaces.
type swapCounter struct {
	persis.Collection
	swaps int
}

func (c *swapCounter) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	c.swaps++
	return c.Collection.CompareAndSwap(ctx, id, expected, next)
}

// The spans one process records are read by another, as the API records a
// suspension and the scheduler an outage, each in a file of its own.
func TestSpanStoreSharesSpansThroughFiles(t *testing.T) {
	t.Parallel()
	paths := config.PathsConfig{DataDir: t.TempDir()}
	writer := store.NewSpanStore(file.NewBackend(paths).Collection(persis.CollectionSlotSpans), nil)
	reader := store.NewSpanStore(file.NewBackend(paths).Collection(persis.CollectionSlotSpans), nil)
	ctx := t.Context()

	down := schedulerstate.Span{From: spanAt(1, 0), To: spanAt(1, 1), Reason: schedulerstate.SpanDown}
	suspended := schedulerstate.Span{From: spanAt(1, 9), To: spanAt(1, 10), Reason: schedulerstate.SpanSuspended}
	require.NoError(t, writer.Add(ctx, "", down))
	require.NoError(t, writer.Add(ctx, "nested/etl", suspended))

	spans, err := reader.Ended(ctx, "nested/etl")
	require.NoError(t, err)
	assert.Equal(t, []schedulerstate.Span{down, suspended}, spans)
	assert.FileExists(t, filepath.Join(paths.DataDir, "slot-spans", "down.json"))
	assert.FileExists(t, filepath.Join(paths.DataDir, "slot-spans", "dags", hex.EncodeToString([]byte("nested/etl"))+".json"))
}
