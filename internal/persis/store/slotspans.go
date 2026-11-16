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
	spanRetention    = 30 * 24 * time.Hour // how long after its end a span is kept
	maxSpans         = 100                 // the most spans kept for one name
	schedulerSpansID = "scheduler"
)

type spanRecord struct {
	Spans []schedulerstate.Span `json:"spans,omitempty"`
}

// SpanStore keeps the spans in which slots could not run in a collection,
// one record for the scheduler and one for each DAG that has spans, and
// gives with them the operator's pauses the pause record keeps.
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

// Mark begins a span of reason unless one lasts, or ends the one that lasts.
func (s *SpanStore) Mark(ctx context.Context, dag string, reason schedulerstate.SpanReason, held bool, at time.Time) error {
	return s.update(ctx, dag, at, func(spans []schedulerstate.Span) []schedulerstate.Span {
		i := slices.IndexFunc(spans, func(span schedulerstate.Span) bool {
			return span.Reason == reason && span.To.IsZero()
		})
		if held && i < 0 {
			return append(spans, schedulerstate.Span{From: at, Reason: reason})
		}
		if !held && i >= 0 {
			spans[i].To = at
		}
		return spans
	})
}

// Add records a span that has ended.
func (s *SpanStore) Add(ctx context.Context, dag string, span schedulerstate.Span) error {
	return s.update(ctx, dag, span.To, func(spans []schedulerstate.Span) []schedulerstate.Span {
		return append(spans, span)
	})
}

// Ended returns the ended spans of the scheduler, the operator's pauses, and
// the spans of the DAG, oldest first.
func (s *SpanStore) Ended(ctx context.Context, dag string) ([]schedulerstate.Span, error) {
	spans, _, err := s.load(ctx, schedulerSpansID)
	if err == nil && dag != "" {
		var own []schedulerstate.Span
		own, _, err = s.load(ctx, spansID(dag))
		spans = append(spans, own...)
	}
	if err == nil && s.pauses != nil {
		var pauses []schedulerstate.Span
		pauses, err = s.pauses.endedPauses(ctx)
		spans = append(spans, pauses...)
	}
	cutoff := time.Now().Add(-spanRetention)
	spans = slices.DeleteFunc(spans, func(span schedulerstate.Span) bool {
		return span.To.IsZero() || span.To.Before(cutoff)
	})
	slices.SortStableFunc(spans, func(a, b schedulerstate.Span) int { return a.From.Compare(b.From) })
	return spans, err
}

// update applies change to the spans of a name, then keeps the spans that
// last and the maxSpans that ended last, within spanRetention before now. A
// record keeps its spans in the order they ended, those that last first. A
// record left as it was is not written.
func (s *SpanStore) update(ctx context.Context, dag string, now time.Time, change func([]schedulerstate.Span) []schedulerstate.Span) error {
	id := spansID(dag)
	return retryConflict(ctx, func(ctx context.Context) error {
		spans, current, err := s.load(ctx, id)
		if err != nil {
			return err
		}
		spans, ended := change(spans), 0
		slices.SortStableFunc(spans, func(a, b schedulerstate.Span) int { return a.To.Compare(b.To) })
		for i, span := range slices.Backward(spans) {
			if span.To.IsZero() {
				continue
			}
			if ended++; ended > maxSpans || now.Sub(span.To) > spanRetention {
				spans = slices.Delete(spans, i, i+1)
			}
		}
		if current == nil && len(spans) == 0 {
			return nil
		}
		data, err := persis.Encode(&spanRecord{Spans: spans})
		if err != nil {
			return err
		}
		if current != nil && bytes.Equal(data, current.Data) {
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

// load returns the spans of a record and the record. A record that cannot be
// decoded holds no spans, so the next write replaces it.
func (s *SpanStore) load(ctx context.Context, id string) ([]schedulerstate.Span, *persis.Record, error) {
	current, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("span store: get %s: %w", id, err)
	}
	var record spanRecord
	if err := persis.Decode(current, &record); err != nil {
		return nil, current, nil
	}
	return record.Spans, current, nil
}

// spansID names the record of a DAG's spans, kept in hex so that names that
// differ only in case or hold a path separator stay apart, or the
// scheduler's for the empty name.
func spansID(dag string) string {
	if dag == "" {
		return schedulerSpansID
	}
	return "dags/" + hex.EncodeToString([]byte(dag))
}
