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
	fires, near := firesIn(sched, bounds)
	if len(fires) == 0 {
		return Missed{}, nil
	}
	// The fire before the first slot shows whether the DAG was on its
	// schedule; one older than its retained history is taken to be.
	from := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, fires[0].Add(-time.Nanosecond)).Start
	if from.IsZero() {
		from = fires[0]
	}
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
// MaxMissedSlots, and for each run of consecutive ones the fires around it,
// oldest first.
func firesIn(sched SlotSchedule, bounds []spanBounds) ([]time.Time, []ir.ScheduleWindow) {
	merged := slices.SortedFunc(slices.Values(bounds), func(a, b spanBounds) int { return a.lo.Compare(b.lo) })
	var fires []time.Time
	var near []ir.ScheduleWindow
	for i := 0; i < len(merged) && len(fires) < MaxMissedSlots; i++ {
		lo, hi := merged[i].lo, merged[i].hi
		for i+1 < len(merged) && !merged[i+1].lo.After(hi) {
			i++
			if merged[i].hi.After(hi) {
				hi = merged[i].hi
			}
		}
		more, _ := ir.ScheduleFiresBetween(sched.Schedules, sched.Profile, sched.Location, lo, hi, MaxMissedSlots-len(fires))
		if len(more) == 0 {
			continue
		}
		fires = append(fires, more...)
		near = append(near, ir.ScheduleWindow{
			Start: ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, more[0]).Start,
			Next:  ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, more[len(more)-1]).Next,
		})
	}
	return fires, near
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
