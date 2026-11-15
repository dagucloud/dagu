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
	Spans     []schedulerstate.Span    `json:"spans,omitempty"`
	Dismissal schedulerstate.Dismissal `json:"dismissal,omitzero"`
}

// SpanStore keeps the spans in which slots could not run in a collection,
// one record for the scheduler and one for each DAG that has spans or a
// dismissal.
type SpanStore struct {
	col persis.Collection
}

var _ schedulerstate.SpanStore = (*SpanStore)(nil)

// NewSpanStore creates a span store backed by col.
func NewSpanStore(col persis.Collection) *SpanStore {
	return &SpanStore{col: col}
}

// Mark begins a span of reason unless one lasts, or ends the one that lasts.
func (s *SpanStore) Mark(ctx context.Context, dag string, reason schedulerstate.SpanReason, held bool, at time.Time) error {
	return s.update(ctx, dag, at, func(record *spanRecord) {
		i := slices.IndexFunc(record.Spans, func(span schedulerstate.Span) bool {
			return span.Reason == reason && span.To.IsZero()
		})
		if held && i < 0 {
			record.Spans = append(record.Spans, schedulerstate.Span{From: at, Reason: reason})
		}
		if !held && i >= 0 {
			record.Spans[i].To = at
		}
	})
}

// Add records a span that has ended.
func (s *SpanStore) Add(ctx context.Context, dag string, span schedulerstate.Span) error {
	return s.update(ctx, dag, span.To, func(record *spanRecord) {
		record.Spans = append(record.Spans, span)
	})
}

// Ended returns the ended spans of the scheduler and of the DAG, oldest
// first.
func (s *SpanStore) Ended(ctx context.Context, dag string) ([]schedulerstate.Span, error) {
	record, _, err := s.load(ctx, schedulerSpansID)
	spans := record.Spans
	if err == nil && dag != "" {
		record, _, err = s.load(ctx, spansID(dag))
		spans = append(spans, record.Spans...)
	}
	cutoff := time.Now().Add(-spanRetention)
	spans = slices.DeleteFunc(spans, func(span schedulerstate.Span) bool {
		return span.To.IsZero() || span.To.Before(cutoff)
	})
	slices.SortStableFunc(spans, func(a, b schedulerstate.Span) int { return a.From.Compare(b.From) })
	return spans, err
}

// Dismiss records a dismissal of the DAG's missed slots in place of the one
// before.
func (s *SpanStore) Dismiss(ctx context.Context, dag string, dismissal schedulerstate.Dismissal) error {
	return s.update(ctx, dag, dismissal.Through, func(record *spanRecord) {
		record.Dismissal = dismissal
	})
}

// Dismissed returns the DAG's latest dismissal, or the zero Dismissal.
func (s *SpanStore) Dismissed(ctx context.Context, dag string) (schedulerstate.Dismissal, error) {
	record, _, err := s.load(ctx, spansID(dag))
	return record.Dismissal, err
}

// update applies change to the record of a name, then keeps the spans that
// last and the maxSpans that ended last, within spanRetention before now. A
// record keeps its spans in the order they ended, those that last first. A
// record left as it was is not written.
func (s *SpanStore) update(ctx context.Context, dag string, now time.Time, change func(*spanRecord)) error {
	id := spansID(dag)
	return retryConflict(ctx, func(ctx context.Context) error {
		record, current, err := s.load(ctx, id)
		if err != nil {
			return err
		}
		change(&record)
		spans, ended := record.Spans, 0
		slices.SortStableFunc(spans, func(a, b schedulerstate.Span) int { return a.To.Compare(b.To) })
		for i, span := range slices.Backward(spans) {
			if span.To.IsZero() {
				continue
			}
			if ended++; ended > maxSpans || now.Sub(span.To) > spanRetention {
				spans = slices.Delete(spans, i, i+1)
			}
		}
		record.Spans = spans
		if current == nil && len(spans) == 0 && record.Dismissal.Through.IsZero() {
			return nil
		}
		data, err := persis.Encode(&record)
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

// load returns a record's spans and dismissal, and the record. A record that
// cannot be decoded holds neither, so the next write replaces it.
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
// scheduler's for the empty name.
func spansID(dag string) string {
	if dag == "" {
		return schedulerSpansID
	}
	return "dags/" + hex.EncodeToString([]byte(dag))
}
