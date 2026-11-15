// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"context"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// MaxMissedSlots bounds the slots FindMissed looks at.
const MaxMissedSlots = 10000

// Missed is what a DAG missed in the spans in which its slots could not run:
// the slots that no run counts for and that the scheduler did not skip.
type Missed struct {
	Slots       int
	First, Last time.Time
	// Reasons say why the spans that hold those slots happened, in the order
	// of the slots.
	Reasons []schedulerstate.SpanReason
	// Until is the end of the newest span that holds one of them.
	Until time.Time
}

// spanBounds are the first and last instants whose slots a span holds.
type spanBounds struct {
	lo, hi time.Time
	span   schedulerstate.Span
}

// FindMissed finds the slots of dag that came due inside spans, among the
// oldest MaxMissedSlots, and that neither ran by any path nor were skipped
// by the scheduler. A span holds the slots strictly between its ends: the
// tick at its end runs its own. Slots older than the DAG's slot retention
// are left out, as their history is gone, and so are slots up to the DAG's
// first recorded slot, as it was not on its schedule yet; a DAG counts as
// on its schedule where its retained history starts. After an outage the
// scheduler replays the slots of the DAG's catch-up window, catchup, which
// are left to it.
func FindMissed(ctx context.Context, reader HistoryReader, dag *ir.DAG, sched SlotSchedule, spans []schedulerstate.Span, catchup time.Duration, now time.Time) (Missed, error) {
	var cutoff time.Time
	if dag.SlotRetentionDays > 0 {
		cutoff = now.AddDate(0, 0, -dag.SlotRetentionDays)
	}
	bounds := make([]spanBounds, 0, len(spans))
	for _, span := range spans {
		b := spanBounds{lo: span.From.Add(time.Nanosecond), hi: span.To.Add(-time.Nanosecond), span: span}
		if b.lo.Before(cutoff) {
			b.lo = cutoff
		}
		if span.Reason == schedulerstate.SpanDown && catchup > 0 && span.To.Add(-catchup).Before(b.hi) {
			b.hi = span.To.Add(-catchup)
		}
		if !b.hi.Before(b.lo) {
			bounds = append(bounds, b)
		}
	}
	fires := firesIn(sched, bounds)
	if len(fires) == 0 {
		return Missed{}, nil
	}
	// The history starts at the fire before the first slot, which shows
	// whether the DAG was on its schedule then.
	from := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, fires[0].Add(-time.Nanosecond)).Start
	if from.IsZero() {
		from = fires[0]
	}
	history, err := LoadHistory(ctx, reader, dag, sched, from, fires[len(fires)-1])
	if err != nil {
		return Missed{}, err
	}
	var first time.Time
	if from.Before(cutoff) {
		first = from
	}
	skipped := make(map[time.Time]bool)
	for _, event := range history.Events {
		if first.IsZero() || event.Slot.Before(first) {
			first = event.Slot
		}
		if event.Kind == persis.SlotEventSkipped {
			skipped[event.Slot.UTC()] = true
		}
	}

	var missed Missed
	for _, state := range SlotStates(fires, sched, history) {
		if first.IsZero() || !state.At.After(first) || state.Status != SlotMissing || skipped[state.At.UTC()] {
			continue
		}
		if missed.Slots == 0 {
			missed.First = state.At
		}
		missed.Slots++
		missed.Last = state.At
		for _, b := range bounds {
			if state.At.Before(b.lo) || state.At.After(b.hi) {
				continue
			}
			if !slices.Contains(missed.Reasons, b.span.Reason) {
				missed.Reasons = append(missed.Reasons, b.span.Reason)
			}
			if b.span.To.After(missed.Until) {
				missed.Until = b.span.To
			}
		}
	}
	return missed, nil
}

// firesIn returns the fire times inside the bounds, oldest first, up to
// MaxMissedSlots.
func firesIn(sched SlotSchedule, bounds []spanBounds) []time.Time {
	merged := slices.Clone(bounds)
	slices.SortFunc(merged, func(a, b spanBounds) int { return a.lo.Compare(b.lo) })
	var fires []time.Time
	for i := 0; i < len(merged) && len(fires) < MaxMissedSlots; i++ {
		lo, hi := merged[i].lo, merged[i].hi
		for i+1 < len(merged) && !merged[i+1].lo.After(hi) {
			i++
			if merged[i].hi.After(hi) {
				hi = merged[i].hi
			}
		}
		more, _ := ir.ScheduleFiresBetween(sched.Schedules, sched.Profile, sched.Location, lo, hi, MaxMissedSlots-len(fires))
		fires = append(fires, more...)
	}
	return fires
}
