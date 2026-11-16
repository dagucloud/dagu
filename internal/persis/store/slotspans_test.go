// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
	return store.NewSpanStore(testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState), nil)
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
	s := store.NewSpanStore(testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState), pauses)
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: spanAt(2, 9), To: spanAt(2, 11)}))
	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: spanAt(1, 12), To: spanAt(1, 13)}))
	require.NoError(t, s.AddSuspended(ctx, "other", schedulerstate.Period{From: spanAt(1, 0), To: spanAt(1, 1)}))

	spans, _, err := s.Ended(ctx, "etl")
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
	col := &swapCounter{Collection: testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState)}
	s := store.NewSpanStore(col, nil)
	ctx := t.Context()

	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: spanAt(1, 9), To: spanAt(1, 10)}))
	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: spanAt(1, 9), To: spanAt(1, 11)}))

	spans, _, err := s.Ended(ctx, "etl")
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
	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: ended.Add(-time.Hour), To: ended}))

	start := spanAt(1, 0)
	for i := range 101 {
		from := start.Add(time.Duration(i) * time.Hour)
		require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: from, To: from.Add(time.Minute)}))
	}

	spans, _, err := s.Ended(ctx, "")
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
	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: ended.Add(-time.Hour), To: ended}))

	spans, _, err := s.Ended(ctx, "")
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
		require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: from, To: from.Add(time.Minute)}))
	}
	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: spanAt(2, 0), To: spanAt(2, 1)}))

	spans, _, err := s.Ended(ctx, "")
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

// The periods one process records are read by another, as the API records a
// suspension and the scheduler an outage, each in a record of its own next to
// the scheduler state, holding only when each period began and ended.
func TestSpanStoreSharesPeriodsThroughFiles(t *testing.T) {
	t.Parallel()
	paths := config.PathsConfig{DataDir: t.TempDir()}
	pauses := store.NewSchedulerPauseStore(file.NewBackend(paths).Collection(persis.CollectionSchedulerState))
	writer := store.NewSpanStore(file.NewBackend(paths).Collection(persis.CollectionSchedulerState), pauses)
	reader := store.NewSpanStore(file.NewBackend(paths).Collection(persis.CollectionSchedulerState), pauses)
	ctx := t.Context()

	down := schedulerstate.Period{From: spanAt(1, 0), To: spanAt(1, 1)}
	suspended := schedulerstate.Period{From: spanAt(1, 9), To: spanAt(1, 10)}
	require.NoError(t, writer.AddDown(ctx, down))
	require.NoError(t, writer.AddSuspended(ctx, "nested/etl", suspended))
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	spans, _, err := reader.Ended(ctx, "nested/etl")
	require.NoError(t, err)
	require.Len(t, spans, 3)
	assert.Equal(t, []schedulerstate.Span{
		{From: down.From, To: down.To, Reason: schedulerstate.SpanDown},
		{From: suspended.From, To: suspended.To, Reason: schedulerstate.SpanSuspended},
	}, spans[:2])
	assert.Equal(t, schedulerstate.SpanPaused, spans[2].Reason)

	dir := file.SchedulerStateDir(paths)
	for name, list := range map[string]string{"down.json": "down", "dags/nested-etl.json": "suspended", "paused.json": "history"} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		require.NoError(t, err, name)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields), name)
		var periods []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fields[list], &periods), name)
		require.Len(t, periods, 1, name)
		assert.ElementsMatch(t, []string{"from", "to"}, slices.Collect(maps.Keys(periods[0])), name)
	}
}

// A dismissal of a DAG's missed slots is kept in the DAG's record, made if
// it has none yet. A later dismissal replaces it, and an earlier one, as
// from a host whose clock is behind, writes nothing.
func TestSpanStoreKeepsTheLaterDismissal(t *testing.T) {
	t.Parallel()
	col := &swapCounter{Collection: testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState)}
	s := store.NewSpanStore(col, nil)
	ctx := t.Context()
	dismissed := func() schedulerstate.Dismissal {
		t.Helper()
		_, dismissal, err := s.Ended(ctx, "etl")
		require.NoError(t, err)
		return dismissal
	}

	require.NoError(t, s.Dismiss(ctx, "etl", schedulerstate.Dismissal{At: spanAt(2, 0), By: "alice"}))
	assert.Equal(t, schedulerstate.Dismissal{At: spanAt(2, 0), By: "alice"}, dismissed())

	require.NoError(t, s.Dismiss(ctx, "etl", schedulerstate.Dismissal{At: spanAt(1, 0), By: "bob"}))
	assert.Zero(t, col.swaps, "an earlier dismissal writes nothing")
	require.NoError(t, s.Dismiss(ctx, "etl", schedulerstate.Dismissal{At: spanAt(3, 0), By: "carol"}))
	assert.Equal(t, schedulerstate.Dismissal{At: spanAt(3, 0), By: "carol"}, dismissed())

	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: spanAt(4, 0), To: spanAt(4, 1)}))
	assert.Equal(t, schedulerstate.Dismissal{At: spanAt(3, 0), By: "carol"}, dismissed(), "a suspension keeps it")
}
