// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
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
	return store.NewSpanStore(testutil.NewMemoryBackend().Collection(persis.CollectionSlotSpans))
}

// spanAt is an hour of a day counted from 25 days ago, inside the 30 days
// the store keeps.
func spanAt(day, hour int) time.Time {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -25)
	return start.AddDate(0, 0, day-1).Add(time.Duration(hour) * time.Hour)
}

// A suspension's span lasts from the suspend to the resume, and only an
// ended span is given.
func TestSpanStoreMarksASpan(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()

	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, true, spanAt(1, 9)))
	spans, err := s.Ended(ctx, "etl")
	require.NoError(t, err)
	assert.Empty(t, spans, "a lasting span is not given")

	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, true, spanAt(1, 10)))
	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, false, spanAt(1, 12)))
	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, false, spanAt(1, 13)))

	spans, err = s.Ended(ctx, "etl")
	require.NoError(t, err)
	assert.Equal(t, []schedulerstate.Span{
		{From: spanAt(1, 9), To: spanAt(1, 12), Reason: schedulerstate.SpanSuspended},
	}, spans)
}

// A DAG's spans join the scheduler's, which hold for every DAG, and not
// another DAG's.
func TestSpanStoreGivesTheSchedulerSpans(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()

	require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: spanAt(2, 9), To: spanAt(2, 11), Reason: schedulerstate.SpanDown}))
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, true, spanAt(1, 9)))
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, false, spanAt(1, 10)))
	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, true, spanAt(1, 12)))
	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, false, spanAt(1, 13)))
	require.NoError(t, s.Add(ctx, "other", schedulerstate.Span{From: spanAt(1, 0), To: spanAt(1, 1), Reason: schedulerstate.SpanSuspended}))

	spans, err := s.Ended(ctx, "etl")
	require.NoError(t, err)
	assert.Equal(t, []schedulerstate.Span{
		{From: spanAt(1, 9), To: spanAt(1, 10), Reason: schedulerstate.SpanPaused},
		{From: spanAt(1, 12), To: spanAt(1, 13), Reason: schedulerstate.SpanSuspended},
		{From: spanAt(2, 9), To: spanAt(2, 11), Reason: schedulerstate.SpanDown},
	}, spans)
}

// A write drops the spans that ended more than 30 days before it, and keeps
// the newest 100.
func TestSpanStoreKeepsRecentSpans(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()
	old := schedulerstate.Span{From: spanAt(1, 0), To: spanAt(1, 1), Reason: schedulerstate.SpanDown}
	require.NoError(t, s.Add(ctx, "", old))

	start := old.To.Add(30 * 24 * time.Hour)
	for i := range 101 {
		from := start.Add(time.Duration(i) * time.Hour)
		require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: from, To: from.Add(time.Minute), Reason: schedulerstate.SpanDown}))
	}

	spans, err := s.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, spans, 100)
	assert.Equal(t, start.Add(time.Hour), spans[0].From, "the oldest span over the bound is dropped")
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

// The bound on spans keeps a pause that lasts, so its resume still ends it.
func TestSpanStoreKeepsALastingSpanPastTheBound(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, true, spanAt(1, 0)))
	for i := range 100 {
		from := spanAt(2, 0).Add(time.Duration(i) * time.Hour)
		require.NoError(t, s.Add(ctx, "", schedulerstate.Span{From: from, To: from.Add(time.Minute), Reason: schedulerstate.SpanDown}))
	}
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, false, spanAt(7, 0)))

	spans, err := s.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, spans, 100)
	assert.Equal(t, schedulerstate.Span{From: spanAt(1, 0), To: spanAt(7, 0), Reason: schedulerstate.SpanPaused}, spans[0])
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
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, true, spanAt(2, 0)))
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, false, spanAt(2, 1)))

	spans, err := s.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, spans, 100)
	assert.Equal(t, schedulerstate.SpanDown, spans[0].Reason, "the pause ended first and is dropped")
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

// A pause or resume that changes nothing, as a second pause in a row, does
// not write the record again.
func TestSpanStoreSkipsUnchangedRecord(t *testing.T) {
	t.Parallel()
	col := &swapCounter{Collection: testutil.NewMemoryBackend().Collection(persis.CollectionSlotSpans)}
	s := store.NewSpanStore(col)
	ctx := t.Context()

	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, true, spanAt(1, 9)))
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, true, spanAt(1, 10)))
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, false, spanAt(1, 11)))
	require.NoError(t, s.Mark(ctx, "", schedulerstate.SpanPaused, false, spanAt(1, 12)))

	assert.Equal(t, 1, col.swaps, "only the resume changes the record")
}

// The spans one process records are read by another, as the API records a
// suspension and the scheduler an outage.
func TestSpanStoreSharesSpansThroughFiles(t *testing.T) {
	t.Parallel()
	paths := config.PathsConfig{DataDir: t.TempDir()}
	writer := store.NewSpanStore(file.NewBackend(paths).Collection(persis.CollectionSlotSpans))
	reader := store.NewSpanStore(file.NewBackend(paths).Collection(persis.CollectionSlotSpans))
	ctx := t.Context()

	require.NoError(t, writer.Mark(ctx, "nested/etl", schedulerstate.SpanSuspended, true, spanAt(1, 9)))
	require.NoError(t, writer.Mark(ctx, "nested/etl", schedulerstate.SpanSuspended, false, spanAt(1, 10)))

	spans, err := reader.Ended(ctx, "nested/etl")
	require.NoError(t, err)
	assert.Equal(t, []schedulerstate.Span{
		{From: spanAt(1, 9), To: spanAt(1, 10), Reason: schedulerstate.SpanSuspended},
	}, spans)
}

// A DAG's dismissal needs no span of its own, as when only the scheduler's
// spans held its slots. A suspension and a dismissal of one DAG each keep
// what the other recorded, and a later dismissal replaces an earlier one.
func TestSpanStoreKeepsADismissal(t *testing.T) {
	t.Parallel()
	s := newSpanStore()
	ctx := t.Context()

	first := schedulerstate.Dismissal{Through: spanAt(1, 9), By: "alice"}
	require.NoError(t, s.Dismiss(ctx, "etl", first))
	got, err := s.Dismissed(ctx, "etl")
	require.NoError(t, err)
	assert.Equal(t, first, got)

	latest := schedulerstate.Dismissal{Through: spanAt(1, 11), By: "bob"}
	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, true, spanAt(1, 10)))
	require.NoError(t, s.Dismiss(ctx, "etl", latest))
	require.NoError(t, s.Mark(ctx, "etl", schedulerstate.SpanSuspended, false, spanAt(1, 12)))

	got, err = s.Dismissed(ctx, "etl")
	require.NoError(t, err)
	assert.Equal(t, latest, got)
	spans, err := s.Ended(ctx, "etl")
	require.NoError(t, err)
	assert.Equal(t, []schedulerstate.Span{
		{From: spanAt(1, 10), To: spanAt(1, 12), Reason: schedulerstate.SpanSuspended},
	}, spans)

	other, err := s.Dismissed(ctx, "report")
	require.NoError(t, err)
	assert.Zero(t, other, "another DAG's slots are not dismissed")
}
