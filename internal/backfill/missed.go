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

// MaxMissedSlots bounds the slots FindMissed looks at, the newest.
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

// FindMissed finds the missed slots of dag among the newest MaxMissedSlots
// that came due strictly inside spans; the tick at a span's end runs its
// own. It leaves out slots older than the DAG's slot retention and slots up
// to its first recorded slot, before which it was not on its schedule.
func FindMissed(ctx context.Context, reader HistoryReader, dag *ir.DAG, sched SlotSchedule, spans []schedulerstate.Span, now time.Time) (Missed, error) {
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
		if !b.hi.Before(b.lo) {
			bounds = append(bounds, b)
		}
	}
	fires, near, since := firesIn(sched, bounds)
	if len(fires) == 0 {
		return Missed{}, nil
	}
	// The fire before the slots of the span that holds the oldest one looked
	// at shows whether the DAG was on its schedule; one older than its
	// retained history is taken to be.
	before := fireBefore(sched, since)
	from := fireBefore(sched, fires[0])
	start, end := HistoryRange(sched, from, fires[len(fires)-1])
	events, err := reader.SlotEvents(ctx, dag, start, end)
	if err != nil {
		return Missed{}, err
	}
	runs, err := readRuns(ctx, reader, dag.Name, events, near)
	if err != nil {
		return Missed{}, err
	}
	history := History{Events: events, Runs: runs}
	var first time.Time
	if before.Before(cutoff) {
		first = before
	} else if since.Before(fires[0]) {
		// The cap left out the span's oldest slots. The DAG was on its
		// schedule if it ran the fire before the span, or else from its
		// first slot recorded among those left out.
		lead, _ := HistoryRange(sched, before, before)
		if first, err = firstSlot(ctx, reader, dag, lead, since); err == nil && first.IsZero() {
			first, err = firstSlot(ctx, reader, dag, since, start)
		}
		if err != nil {
			return Missed{}, err
		}
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

// fireBefore returns the fire before t, or t when none is.
func fireBefore(sched SlotSchedule, t time.Time) time.Time {
	if prev := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, t.Add(-time.Nanosecond)).Start; !prev.IsZero() {
		return prev
	}
	return t
}

// firstSlot returns the oldest slot of the DAG's events in [from, to], or
// zero when it has none.
func firstSlot(ctx context.Context, reader HistoryReader, dag *ir.DAG, from, to time.Time) (time.Time, error) {
	events, err := reader.SlotEvents(ctx, dag, from, to)
	var first time.Time
	for _, event := range events {
		if first.IsZero() || event.Slot.Before(first) {
			first = event.Slot
		}
	}
	return first, err
}

// firesIn returns the newest MaxMissedSlots fire times inside the bounds,
// oldest first, for each run of consecutive ones the fires around it,
// oldest first, and the first fire of the bounds that hold the oldest one.
func firesIn(sched SlotSchedule, bounds []spanBounds) ([]time.Time, []ir.ScheduleWindow, time.Time) {
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
	var near []ir.ScheduleWindow
	var since time.Time
	n := 0
	for i := len(merged) - 1; i >= 0 && n < MaxMissedSlots; i-- {
		fires := newestFires(sched, merged[i].lo, merged[i].hi, MaxMissedSlots-n)
		if len(fires) == 0 {
			continue
		}
		runs = append(runs, fires)
		n += len(fires)
		near = append(near, ir.ScheduleWindow{
			Start: ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, fires[0]).Start,
			Next:  ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, fires[len(fires)-1]).Next,
		})
		since = fires[0]
		if n == MaxMissedSlots {
			oldest, _ := ir.ScheduleFiresBetween(sched.Schedules, sched.Profile, sched.Location, merged[i].lo, merged[i].hi, 1)
			since = oldest[0]
		}
	}
	slices.Reverse(runs)
	slices.Reverse(near)
	return slices.Concat(runs...), near, since
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

// recordLag bounds how long before the slot index first records a run the
// run was created: a run records its slot as it writes its first status.
const recordLag = time.Hour

// readRuns reads from run history the status of each run that may count for
// a slot between the fires around a run of slots, near, and whose final
// status the slot index does not keep. It reads only the runs created within
// recordLag of when the index first recorded one of those, so the read grows
// with them and not with the DAG's history. A run not found there is gone.
func readRuns(ctx context.Context, reader HistoryReader, dagName string, events []persis.SlotEvent, near []ir.ScheduleWindow) (map[string]*ir.DAGRunStatus, error) {
	recorded := make(map[string]time.Time)
	final := make(map[string]bool)
	for _, event := range events {
		if event.RunID == "" || !between(near, event.Slot) {
			continue
		}
		if _, ok := recorded[event.RunID]; !ok {
			recorded[event.RunID] = event.RecordedAt
		}
		if event.Kind == persis.SlotEventFinal {
			final[event.RunID] = true
		}
	}
	var created []time.Time
	for id, at := range recorded {
		if !final[id] {
			created = append(created, at)
		}
	}
	slices.SortFunc(created, time.Time.Compare)
	runs := make(map[string]*ir.DAGRunStatus)
	for i := 0; i < len(created); i++ {
		from, to := created[i].Add(-recordLag), created[i].Add(recordLag)
		for i+1 < len(created) && !created[i+1].Add(-recordLag).After(to) {
			i++
			to = created[i].Add(recordLag)
		}
		statuses, err := reader.ListStatuses(ctx, persis.DAGRunListOptions{
			ExactName: dagName,
			From:      persis.NewUTC(from),
			To:        persis.NewUTC(to),
			Unbounded: true,
		})
		if err != nil {
			return nil, err
		}
		for _, status := range statuses {
			if _, ok := recorded[status.DAGRunID]; ok && !final[status.DAGRunID] {
				runs[status.DAGRunID] = status
			}
		}
	}
	return runs, nil
}

// between reports whether t falls strictly between the fires around one of
// the runs of slots, whose fires before and after both grow from one run to
// the next.
func between(near []ir.ScheduleWindow, t time.Time) bool {
	i, _ := slices.BinarySearchFunc(near, t, func(w ir.ScheduleWindow, t time.Time) int {
		if w.Start.Before(t) {
			return -1
		}
		return 1
	})
	return i > 0 && (near[i-1].Next.IsZero() || t.Before(near[i-1].Next))
}
