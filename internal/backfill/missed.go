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
// why the periods that hold them happened, in the order of the slots, and when
// the newest of those periods ended.
type Missed struct {
	Slots       int
	First, Last time.Time
	Reasons     []schedulerstate.PeriodReason
	Until       time.Time
}

// periodBounds are the first and last instants whose slots a period holds.
type periodBounds struct {
	lo, hi time.Time
	period schedulerstate.Period
}

// FindMissed counts the newest MaxMissedSlots fire times of sched that came
// due strictly inside periods; the tick at a period's end runs its own. It leaves
// out slots older than the DAG's slot retention, and the slots of the
// scheduler's outages and pauses up to firstSeen, when the scheduler first
// saw the DAG; without firstSeen, those periods count none.
func FindMissed(dag *ir.DAG, sched SlotSchedule, periods []schedulerstate.Period, firstSeen, now time.Time) Missed {
	var cutoff time.Time
	if dag.SlotRetentionDays > 0 {
		cutoff = now.AddDate(0, 0, -dag.SlotRetentionDays)
	}
	var bounds []periodBounds
	for _, period := range periods {
		b := periodBounds{lo: period.From.Add(time.Nanosecond), hi: period.To.Add(-time.Nanosecond), period: period}
		if b.lo.Before(cutoff) {
			b.lo = cutoff
		}
		if period.Reason != schedulerstate.PeriodSuspended {
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
			if !slices.Contains(missed.Reasons, b.period.Reason) {
				missed.Reasons = append(missed.Reasons, b.period.Reason)
			}
			if b.period.To.After(missed.Until) {
				missed.Until = b.period.To
			}
		}
	}
	return missed
}

// firesIn returns the newest MaxMissedSlots fire times inside the bounds,
// oldest first.
func firesIn(sched SlotSchedule, bounds []periodBounds) []time.Time {
	// The bounds joined where they overlap, oldest first.
	var merged []periodBounds
	for _, b := range slices.SortedFunc(slices.Values(bounds), func(a, b periodBounds) int { return a.lo.Compare(b.lo) }) {
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
