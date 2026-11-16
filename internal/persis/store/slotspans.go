// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

const (
	spanRetention = 30 * 24 * time.Hour // how long after its end a span is kept
	maxSpans      = 100                 // the most spans kept for one name
	downSpansID   = "down"
)

// spanRecord is the ended spans of a name, in the order they ended. It is an
// object so that a DAG's record can hold more than its spans.
type spanRecord struct {
	Spans []schedulerstate.Span `json:"spans,omitempty"`
}

// SpanStore keeps the ended spans in which slots could not run in a
// collection: the scheduler's outages in one record and each DAG's
// suspensions in its own. It gives with them the operator's pauses the pause
// record keeps.
type SpanStore struct {
	col    persis.Collection
	pauses *SchedulerPauseStore
}

var _ schedulerstate.SpanStore = (*SpanStore)(nil)

// NewSpanStore creates a span store backed by col that gives the ended
// pauses of pauses, when it is not nil.
func NewSpanStore(col persis.Collection, pauses *SchedulerPauseStore) *SpanStore {
	return &SpanStore{col: col, pauses: pauses}
}

// Add records a span that has ended, unless one that began at the same time
// is recorded. A record keeps the maxSpans spans that ended last within
// spanRetention before the write.
func (s *SpanStore) Add(ctx context.Context, dag string, span schedulerstate.Span) error {
	return s.update(ctx, spansID(dag), func(record *spanRecord) {
		if slices.ContainsFunc(record.Spans, func(recorded schedulerstate.Span) bool { return recorded.From.Equal(span.From) }) {
			return
		}
		record.Spans = recentSpans(append(record.Spans, span), time.Now())
	})
}

// Ended returns the spans of the scheduler's outages, the operator's pauses,
// and the DAG's suspensions that ended within spanRetention, oldest first.
func (s *SpanStore) Ended(ctx context.Context, dag string) ([]schedulerstate.Span, error) {
	down, _, err := s.load(ctx, downSpansID)
	spans := down.Spans
	if err == nil && dag != "" {
		var own spanRecord
		own, _, err = s.load(ctx, spansID(dag))
		spans = append(spans, own.Spans...)
	}
	if err == nil && s.pauses != nil {
		var pauses []schedulerstate.Span
		pauses, err = s.pauses.endedPauses(ctx)
		spans = append(spans, pauses...)
	}
	cutoff := time.Now().Add(-spanRetention)
	spans = slices.DeleteFunc(spans, func(span schedulerstate.Span) bool { return span.To.Before(cutoff) })
	slices.SortStableFunc(spans, func(a, b schedulerstate.Span) int { return a.From.Compare(b.From) })
	return spans, err
}

// update applies change to a record and writes it, through a compare and
// swap, unless it is left as it was.
func (s *SpanStore) update(ctx context.Context, id string, change func(*spanRecord)) error {
	return retryConflict(ctx, func(ctx context.Context) error {
		record, current, err := s.load(ctx, id)
		if err != nil {
			return err
		}
		change(&record)
		data, err := persis.Encode(&record)
		if err != nil {
			return err
		}
		if current == nil && len(record.Spans) == 0 || current != nil && bytes.Equal(data, current.Data) {
			return nil
		}
		stamp := time.Now().UTC()
		return createOrSwap(ctx, s.col, current, &persis.Record{ID: id, Data: data, CreatedAt: stamp, UpdatedAt: stamp})
	})
}

// recentSpans keeps the maxSpans spans that ended last, within spanRetention
// before now, in the order they ended.
func recentSpans(spans []schedulerstate.Span, now time.Time) []schedulerstate.Span {
	slices.SortStableFunc(spans, func(a, b schedulerstate.Span) int { return a.To.Compare(b.To) })
	spans = slices.DeleteFunc(spans, func(span schedulerstate.Span) bool { return now.Sub(span.To) > spanRetention })
	return spans[max(0, len(spans)-maxSpans):]
}

// load returns a record and what it was read from. A record that cannot be
// decoded is empty, so the next write replaces it.
func (s *SpanStore) load(ctx context.Context, id string) (spanRecord, *persis.Record, error) {
	current, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return spanRecord{}, nil, nil
	}
	if err != nil {
		return spanRecord{}, nil, fmt.Errorf("span store: get %s: %w", id, err)
	}
	var record spanRecord
	if err := persis.Decode(current, &record); err != nil {
		return spanRecord{}, current, nil
	}
	return record, current, nil
}

// spansID names the record of a DAG's spans, kept in hex so that names that
// differ only in case or hold a path separator stay apart, or the
// scheduler's outages for the empty name.
func spansID(dag string) string {
	if dag == "" {
		return downSpansID
	}
	return "dags/" + hex.EncodeToString([]byte(dag))
}
