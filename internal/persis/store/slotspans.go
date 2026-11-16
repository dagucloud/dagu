// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

const (
	periodRetention = 30 * 24 * time.Hour // how long after its end a period is kept
	maxPeriods      = 100                 // the most periods a list keeps
	downRecordID    = "down"
)

// downRecord is the scheduler's outages, in the order they ended.
type downRecord struct {
	Down []schedulerstate.Period `json:"down,omitempty"`
}

// dagRecord is a DAG's suspensions, in the order they ended, and the latest
// dismissal of its missed slots.
type dagRecord struct {
	Suspended []schedulerstate.Period   `json:"suspended,omitempty"`
	Dismissed *schedulerstate.Dismissal `json:"dismissed,omitempty"`
}

// SpanStore keeps the ended periods in which slots could not run as records
// of the scheduler state collection: the scheduler's outages in one, and
// each DAG's suspensions in its own. It gives with them the operator's
// pauses the pause record keeps.
type SpanStore struct {
	col    persis.Collection
	pauses *SchedulerPauseStore
}

var _ schedulerstate.SpanStore = (*SpanStore)(nil)

// NewSpanStore creates a span store on the scheduler state collection col
// that gives the ended pauses of pauses, when it is not nil.
func NewSpanStore(col persis.Collection, pauses *SchedulerPauseStore) *SpanStore {
	return &SpanStore{col: col, pauses: pauses}
}

// AddDown records a time no scheduler ran.
func (s *SpanStore) AddDown(ctx context.Context, down schedulerstate.Period) error {
	return changeRecord(ctx, s.col, downRecordID, func(record *downRecord) bool {
		var added bool
		record.Down, added = appendPeriod(record.Down, down, time.Now())
		return added
	})
}

// AddSuspended records a DAG's ended suspension, or extends the one that
// began at the same time to its later end.
func (s *SpanStore) AddSuspended(ctx context.Context, dag string, suspended schedulerstate.Period) error {
	return changeRecord(ctx, s.col, persis.SchedulerDAGRecordID(dag), func(record *dagRecord) bool {
		var added bool
		record.Suspended, added = appendPeriod(record.Suspended, suspended, time.Now())
		return added
	})
}

// Dismiss records that the DAG's missed slots were dismissed, unless a
// later dismissal is recorded.
func (s *SpanStore) Dismiss(ctx context.Context, dag string, dismissal schedulerstate.Dismissal) error {
	return changeRecord(ctx, s.col, persis.SchedulerDAGRecordID(dag), func(record *dagRecord) bool {
		if record.Dismissed != nil && !dismissal.At.After(record.Dismissed.At) {
			return false
		}
		record.Dismissed = &dismissal
		return true
	})
}

// Ended returns the spans of the scheduler's outages, the operator's pauses,
// and the DAG's suspensions that ended within periodRetention, oldest first,
// and the DAG's latest dismissal. It reads each record once.
func (s *SpanStore) Ended(ctx context.Context, dag string) ([]schedulerstate.Span, schedulerstate.Dismissal, error) {
	var none schedulerstate.Dismissal
	var down downRecord
	if _, err := loadRecord(ctx, s.col, downRecordID, &down); err != nil {
		return nil, none, err
	}
	var own dagRecord
	if _, err := loadRecord(ctx, s.col, persis.SchedulerDAGRecordID(dag), &own); err != nil {
		return nil, none, err
	}
	var pauses []schedulerstate.Period
	if s.pauses != nil {
		var err error
		if pauses, err = s.pauses.endedPauses(ctx); err != nil {
			return nil, none, err
		}
	}
	var spans []schedulerstate.Span
	cutoff := time.Now().Add(-periodRetention)
	for reason, periods := range map[schedulerstate.SpanReason][]schedulerstate.Period{
		schedulerstate.SpanDown:      down.Down,
		schedulerstate.SpanPaused:    pauses,
		schedulerstate.SpanSuspended: own.Suspended,
	} {
		for _, period := range periods {
			if !period.To.Before(cutoff) {
				spans = append(spans, schedulerstate.Span{From: period.From, To: period.To, Reason: reason})
			}
		}
	}
	slices.SortFunc(spans, func(a, b schedulerstate.Span) int {
		if c := a.From.Compare(b.From); c != 0 {
			return c
		}
		return a.To.Compare(b.To)
	})
	if own.Dismissed != nil {
		return spans, *own.Dismissed, nil
	}
	return spans, none, nil
}

// appendPeriod appends period to periods, or extends the one that began at
// the same time to its later end, and keeps the maxPeriods that ended last
// within periodRetention before now, in the order they ended. It reports
// whether periods changed.
func appendPeriod(periods []schedulerstate.Period, period schedulerstate.Period, now time.Time) ([]schedulerstate.Period, bool) {
	periods = slices.Clone(periods)
	if i := slices.IndexFunc(periods, func(held schedulerstate.Period) bool { return held.From.Equal(period.From) }); i >= 0 {
		if !period.To.After(periods[i].To) {
			return periods, false
		}
		periods[i].To = period.To
	} else {
		periods = append(periods, period)
	}
	slices.SortStableFunc(periods, func(a, b schedulerstate.Period) int { return a.To.Compare(b.To) })
	periods = slices.DeleteFunc(periods, func(held schedulerstate.Period) bool { return now.Sub(held.To) > periodRetention })
	return periods[max(0, len(periods)-maxPeriods):], true
}

// changeRecord applies change to a record of col and writes it through a
// compare and swap, unless change reports it left the record as it was.
func changeRecord[T any](ctx context.Context, col persis.Collection, id string, change func(*T) bool) error {
	return retryConflict(ctx, func(ctx context.Context) error {
		var record T
		current, err := loadRecord(ctx, col, id, &record)
		if err != nil || !change(&record) {
			return err
		}
		data, err := persis.Encode(&record)
		if err != nil {
			return err
		}
		stamp := time.Now().UTC()
		return createOrSwap(ctx, col, current, &persis.Record{ID: id, Data: data, CreatedAt: stamp, UpdatedAt: stamp})
	})
}

// loadRecord decodes a record of col into dst and returns what it was read
// from. A record that cannot be decoded leaves dst empty, so the next write
// replaces it.
func loadRecord[T any](ctx context.Context, col persis.Collection, id string, dst *T) (*persis.Record, error) {
	current, err := col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("span store: get %s: %w", id, err)
	}
	if err := persis.Decode(current, dst); err != nil {
		var empty T
		*dst = empty
	}
	return current, nil
}
