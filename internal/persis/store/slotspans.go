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
	// spanRetention is how long after its end a span is kept.
	spanRetention = 30 * 24 * time.Hour
	// maxSpans bounds the spans kept for one name.
	maxSpans         = 100
	schedulerSpansID = "scheduler"
)

type spanRecord struct {
	Spans []schedulerstate.Span `json:"spans,omitempty"`
}

// SpanStore keeps the spans in which slots could not run in a collection:
// one record for the scheduler and one for each DAG that has spans. Each
// record is written only when a span begins or ends.
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
	return s.update(ctx, dag, at, func(spans []schedulerstate.Span) []schedulerstate.Span {
		i := slices.IndexFunc(spans, func(span schedulerstate.Span) bool {
			return span.Reason == reason && span.To.IsZero()
		})
		switch {
		case held && i < 0:
			spans = append(spans, schedulerstate.Span{From: at, Reason: reason})
		case !held && i >= 0:
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

// Ended returns the ended spans of the scheduler and of the DAG, oldest
// first.
func (s *SpanStore) Ended(ctx context.Context, dag string) ([]schedulerstate.Span, error) {
	ids := []string{schedulerSpansID}
	if dag != "" {
		ids = append(ids, spansID(dag))
	}
	var ended []schedulerstate.Span
	for _, id := range ids {
		_, spans, err := s.load(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, span := range spans {
			if !span.To.IsZero() {
				ended = append(ended, span)
			}
		}
	}
	slices.SortStableFunc(ended, func(a, b schedulerstate.Span) int { return a.From.Compare(b.From) })
	return ended, nil
}

// update applies change to the spans of a name, then drops those that ended
// more than spanRetention before now and those past the newest maxSpans.
func (s *SpanStore) update(ctx context.Context, dag string, now time.Time, change func([]schedulerstate.Span) []schedulerstate.Span) error {
	id := spansID(dag)
	return retryConflict(ctx, func(ctx context.Context) error {
		current, spans, err := s.load(ctx, id)
		if err != nil {
			return err
		}
		before, err := persis.Encode(&spanRecord{Spans: spans})
		if err != nil {
			return err
		}
		next := slices.DeleteFunc(change(slices.Clone(spans)), func(span schedulerstate.Span) bool {
			return !span.To.IsZero() && span.To.Before(now.Add(-spanRetention))
		})
		next = next[max(0, len(next)-maxSpans):]
		data, err := persis.Encode(&spanRecord{Spans: next})
		if err != nil {
			return err
		}
		if bytes.Equal(before, data) {
			return nil
		}
		stamp := time.Now().UTC()
		return createOrSwap(ctx, s.col, current, &persis.Record{ID: id, Data: data, CreatedAt: stamp, UpdatedAt: stamp})
	})
}

// load returns the record of a name and its spans. A record that cannot be
// decoded reads as no spans, so a damaged file is replaced on the next write.
func (s *SpanStore) load(ctx context.Context, id string) (*persis.Record, []schedulerstate.Span, error) {
	current, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("span store: get %s: %w", id, err)
	}
	var record spanRecord
	if err := persis.Decode(current, &record); err != nil {
		return current, nil, nil
	}
	return current, record.Spans, nil
}

// spansID names the record of a DAG's spans, or of the scheduler's for the
// empty name. A DAG's name is kept in hex so that names that differ only in
// case or hold a path separator stay apart.
func spansID(dag string) string {
	if dag == "" {
		return schedulerSpansID
	}
	return "dags/" + hex.EncodeToString([]byte(dag))
}
