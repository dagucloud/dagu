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

// Missed is the slots of a DAG that no run counts for and the scheduler did
// not skip, with why the spans that hold them happened, in the order of the
// slots, and when the newest of those spans ended.
type Missed struct {
	Slots       int
	First, Last time.Time
	Reasons     []schedulerstate.SpanReason
	Until       time.Time
}

// spanBounds are the first and last instants whose slots a span holds.
type spanBounds struct {
	lo, hi time.Time
	span   schedulerstate.Span
}

// FindMissed finds the missed slots of dag among the oldest MaxMissedSlots
// that came due strictly inside spans; the tick at a span's end runs its
// own. It leaves out slots older than the DAG's slot retention, slots up to
// its first recorded slot, before which it was not on its schedule, and
// after an outage the slots its catch-up window, catchup, replays.
func FindMissed(ctx context.Context, reader HistoryReader, dag *ir.DAG, sched SlotSchedule, spans []schedulerstate.Span, catchup time.Duration, now time.Time) (Missed, error) {
	var cutoff time.Time
	if dag.SlotRetentionDays > 0 {
		cutoff = now.AddDate(0, 0, -dag.SlotRetentionDays)
	}
	var bounds []spanBounds
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
	// The fire before the first slot shows whether the DAG was on its
	// schedule; one older than its retained history is taken to be.
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
	merged := slices.SortedFunc(slices.Values(bounds), func(a, b spanBounds) int { return a.lo.Compare(b.lo) })
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
