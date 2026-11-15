// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
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

func spanAt(day, hour int) time.Time {
	return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC)
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
