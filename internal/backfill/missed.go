// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"cmp"
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
// Until clamped, an outage's bounds still hold the slots catch-up replays.
type periodBounds struct {
	lo, hi    time.Time
	period    schedulerstate.Period
	unclamped bool
}

// Catchup is what the scheduler's catch-up runs of a DAG's slots that came
// due while no scheduler ran: the newest Max of those within Window before
// it ran again. A zero Catchup runs none.
type Catchup struct {
	Window time.Duration
	Max    int
}

// FindMissed counts the newest MaxMissedSlots fire times of sched that came
// due strictly inside periods; the tick at a period's end runs its own. It leaves
// out slots older than the DAG's slot retention, the slots of an outage that
// catchup replays, and the slots of the scheduler's outages and pauses up to
// firstSeen, when the scheduler first saw the DAG; without firstSeen, those
// periods count none.
func FindMissed(dag *ir.DAG, sched SlotSchedule, periods []schedulerstate.Period, catchup Catchup, firstSeen, now time.Time) Missed {
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
		b.unclamped = period.Reason == schedulerstate.PeriodDown
		if !b.hi.Before(b.lo) {
			bounds = append(bounds, b)
		}
	}

	fires := catchup.firesIn(sched, bounds)
	if len(fires) == 0 {
		return Missed{}
	}
	missed := Missed{Slots: len(fires), First: fires[0], Last: fires[len(fires)-1]}
	// The bounds that hold a counted fire, by the index of the oldest, name
	// the reasons in the order of the fires.
	type holder struct {
		fire   int
		reason schedulerstate.PeriodReason
	}
	var holders []holder
	for i := range bounds {
		b := &bounds[i]
		if b.hi.Before(missed.First) {
			continue
		}
		catchup.clamp(sched, b)
		j, _ := slices.BinarySearchFunc(fires, b.lo, time.Time.Compare)
		if j == len(fires) || fires[j].After(b.hi) {
			continue
		}
		holders = append(holders, holder{fire: j, reason: b.period.Reason})
		if b.period.To.After(missed.Until) {
			missed.Until = b.period.To
		}
	}
	slices.SortStableFunc(holders, func(a, b holder) int { return cmp.Compare(a.fire, b.fire) })
	for _, h := range holders {
		if !slices.Contains(missed.Reasons, h.reason) {
			missed.Reasons = append(missed.Reasons, h.reason)
		}
	}
	return missed
}

// clamp ends an outage's bounds before the oldest slot catch-up replays, once.
func (c Catchup) clamp(sched SlotSchedule, b *periodBounds) {
	if !b.unclamped {
		return
	}
	b.unclamped = false
	if replayed, ok := c.firstReplayed(sched, b.period); ok && !replayed.After(b.hi) {
		b.hi = replayed.Add(-time.Nanosecond)
	}
}

// firstReplayed returns the oldest slot catch-up replays when the scheduler
// runs again at the end of down, or false when it replays none: the newest
// Max fires after the window before that end, through the end itself.
func (c Catchup) firstReplayed(sched SlotSchedule, down schedulerstate.Period) (time.Time, bool) {
	if c.Window <= 0 || c.Max <= 0 {
		return time.Time{}, false
	}
	fires := newestFires(sched, down.To.Add(-c.Window).Add(time.Nanosecond), down.To, c.Max)
	if len(fires) == 0 {
		return time.Time{}, false
	}
	return fires[0], true
}

// firesIn returns the newest MaxMissedSlots fire times inside the bounds,
// oldest first. It reads them newest first, and clamps an outage's bounds
// only as the read reaches them.
func (c Catchup) firesIn(sched SlotSchedule, bounds []periodBounds) []time.Time {
	var runs [][]time.Time
	n := 0
	// end is the newest instant left to read.
	var end time.Time
	for _, b := range bounds {
		if b.hi.After(end) {
			end = b.hi
		}
	}
	for n < MaxMissedSlots {
		// The bound that reaches newest below end.
		i, hi := -1, time.Time{}
		for j, b := range bounds {
			top := b.hi
			if top.After(end) {
				top = end
			}
			if !top.Before(b.lo) && (i < 0 || top.After(hi)) {
				i, hi = j, top
			}
		}
		if i < 0 {
			break
		}
		if bounds[i].unclamped {
			c.clamp(sched, &bounds[i])
			continue
		}
		fires := newestFires(sched, bounds[i].lo, hi, MaxMissedSlots-n)
		runs = append(runs, fires)
		n += len(fires)
		end = bounds[i].lo.Add(-time.Nanosecond)
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
