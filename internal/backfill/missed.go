// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// MaxMissedSlots bounds the slots FindMissed looks at, the newest.
const MaxMissedSlots = 10000

// Missed is the slots of a DAG that came due while it could not run, with
// why the spans that hold them happened, in the order of the slots, and when
// the newest of those spans ended.
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

// FindMissed counts the newest MaxMissedSlots fire times of sched that came
// due strictly inside spans; the tick at a span's end runs its own. It leaves
// out slots older than the DAG's slot retention, and the slots of the
// scheduler's outages and pauses up to firstSeen, when the scheduler first
// saw the DAG; without firstSeen, those spans count none.
func FindMissed(dag *ir.DAG, sched SlotSchedule, spans []schedulerstate.Span, firstSeen, now time.Time) Missed {
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
		if span.Reason != schedulerstate.SpanSuspended {
			if firstSeen.IsZero() {
				continue
			}
			if seen := firstSeen.Add(time.Nanosecond); b.lo.Before(seen) {
				b.lo = seen
			}
		}
		if !b.hi.Before(b.lo) {
			bounds = append(bounds, b)
		}
	}

	var missed Missed
	for _, at := range firesIn(sched, bounds) {
		if missed.Slots == 0 {
			missed.First = at
		}
		missed.Slots++
		missed.Last = at
		for _, b := range bounds {
			if at.Before(b.lo) || at.After(b.hi) {
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
	return missed
}

// firesIn returns the newest MaxMissedSlots fire times inside the bounds,
// oldest first.
func firesIn(sched SlotSchedule, bounds []spanBounds) []time.Time {
	// The bounds joined where they overlap, oldest first.
	var merged []spanBounds
	for _, b := range slices.SortedFunc(slices.Values(bounds), func(a, b spanBounds) int { return a.lo.Compare(b.lo) }) {
		if last := len(merged) - 1; last >= 0 && !b.lo.After(merged[last].hi) {
			if b.hi.After(merged[last].hi) {
				merged[last].hi = b.hi
			}
			continue
		}
		merged = append(merged, b)
	}
	var runs [][]time.Time
	n := 0
	for i := len(merged) - 1; i >= 0 && n < MaxMissedSlots; i-- {
		fires := newestFires(sched, merged[i].lo, merged[i].hi, MaxMissedSlots-n)
		runs = append(runs, fires)
		n += len(fires)
	}
	slices.Reverse(runs)
	return slices.Concat(runs...)
}

// newestFires returns the newest fires in [lo, hi], up to limit, oldest
// first. It reads back from hi in windows sized to hold most of the fires
// it still needs at the density of the last one, at most twice as long,
// and halves a window that holds more, so it reads few more fires than it
// returns however many the range holds.
func newestFires(sched SlotSchedule, lo, hi time.Time, limit int) []time.Time {
	var windows [][]time.Time
	n := 0
	for step, end := time.Minute, hi; n < limit && !end.Before(lo); {
		from := lo
		if end.Sub(lo) > step {
			from = end.Add(-step)
		}
		fires, more := ir.ScheduleFiresBetween(sched.Schedules, sched.Profile, sched.Location, from, end, limit-n)
		if more {
			step /= 2
			continue
		}
		windows = append(windows, fires)
		n += len(fires)
		end = from.Add(-time.Nanosecond)
		grow := 2.0
		if len(fires) > 0 {
			grow = min(grow, 0.75*float64(limit-n)/float64(len(fires)))
		}
		step = time.Duration(float64(step) * grow)
	}
	slices.Reverse(windows)
	return slices.Concat(windows...)
}
