// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// maxBoundarySteps bounds the fires walked to find either end of a range of
// dates, beyond the first estimate.
const maxBoundarySteps = 100000

// boundarySlack widens both ends of a range found by walking fires. A month
// or year shift that clamps to a shorter month can move an entry's date back
// by up to three days from one fire to the next, so a walk can stop before
// the last fire whose date is in the range.
const boundarySlack = 4 * 24 * time.Hour

// maxBoundaryFires bounds the fires a range of dates checks beyond the slots
// it may select, for the widened ends.
const maxBoundaryFires = 200000

// slotDateContext is what the run of the slot at resolves its dates from,
// as that run resolves them.
func slotDateContext(sched SlotSchedule, at time.Time) ir.DateContext {
	cal, _ := ir.SlotCalendar(sched.Schedules, sched.Profile, sched.Location, at)
	return ir.DateContext{
		ScheduledAt: at,
		Window:      ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, at),
		Location:    sched.Location,
		Calendar:    cal,
	}
}

// SlotDates resolves the dates the run of the slot at gets, or nil when the
// DAG declares none or they do not resolve.
func SlotDates(dag *ir.DAG, sched SlotSchedule, at time.Time) []ir.DateValue {
	if len(dag.Dates) == 0 {
		return nil
	}
	dates, err := ir.ResolveDates(dag.Dates, slotDateContext(sched, at))
	if err != nil {
		return nil
	}
	return dates
}

// dateSpan is the earliest and latest calendar date of an entry's values for
// one slot, each in the entry's zone. A list with no values has none.
type dateSpan struct {
	first, last ir.CivilDate
	ok          bool
}

// entryDates returns the calendar dates of the entry's values for the slot
// at, each in the entry's zone.
func (r PlanRequest) entryDates(at time.Time) ([]ir.CivilDate, error) {
	instants, err := ir.ResolveDateInstants(r.DAG.Dates, r.By, slotDateContext(r.Schedule, at))
	if err != nil {
		return nil, err
	}
	dates := make([]ir.CivilDate, len(instants))
	for i, t := range instants {
		dates[i] = ir.DateOf(t)
	}
	return dates, nil
}

// inRange reports whether a date falls in [FromDate, ToDate].
func (r PlanRequest) inRange(d ir.CivilDate) bool {
	return !d.Before(r.FromDate) && (r.ToDate.IsZero() || !r.ToDate.Before(d))
}

func (r PlanRequest) entrySpan(at time.Time) (dateSpan, error) {
	dates, err := r.entryDates(at)
	if err != nil {
		return dateSpan{}, err
	}
	var span dateSpan
	for _, d := range dates {
		if !span.ok || d.Before(span.first) {
			span.first = d
		}
		if !span.ok || span.last.Before(d) {
			span.last = d
		}
		span.ok = true
	}
	return span, nil
}

// slotsByDate returns the fires before liveFrom whose entry has a value
// dated in [FromDate, ToDate]. An entry's dates move forward with its slot,
// apart from short clamps at month ends, so the selected fires form one run
// but for its ends: each end is found by stepping fire by fire from an
// estimate and widened by boundarySlack, and every fire in between is then
// checked value by value.
func slotsByDate(req PlanRequest, liveFrom time.Time) ([]time.Time, error) {
	sched := req.Schedule
	fromStart := time.Date(req.FromDate.Year, req.FromDate.Month, req.FromDate.Day, 0, 0, 0, 0, sched.Location)
	first, err := req.firstSlotFrom(fromStart)
	if err != nil || first.IsZero() {
		return nil, err
	}
	last := liveFrom.Add(-time.Nanosecond)
	if !req.ToDate.IsZero() {
		toEnd := time.Date(req.ToDate.Year, req.ToDate.Month, req.ToDate.Day+1, 0, 0, 0, 0, sched.Location)
		if last, err = req.lastSlotUntil(toEnd, last); err != nil || last.IsZero() {
			return nil, err
		}
	}
	// The ends are widened before they are compared, since a clamp can leave
	// the walked ends crossed while fires between the widened ends match.
	first = first.Add(-boundarySlack)
	if widened := last.Add(boundarySlack); widened.Before(liveFrom) {
		last = widened
	} else {
		last = liveFrom.Add(-time.Nanosecond)
	}
	if !first.Before(liveFrom) || last.Before(first) {
		return nil, nil
	}
	fires, more := ir.ScheduleFiresBetween(sched.Schedules, sched.Profile, sched.Location, first, last, maxSlotsOf(req.MaxSlots)+maxBoundaryFires)
	if more {
		return nil, invalid("the range has more than %d slots, the bound set by scheduler.backfill_max_slots", maxSlotsOf(req.MaxSlots))
	}
	var selected []time.Time
	for _, at := range fires {
		dates, err := req.entryDates(at)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(dates, req.inRange) {
			selected = append(selected, at)
		}
	}
	if len(selected) > maxSlotsOf(req.MaxSlots) {
		return nil, invalid("%d slots exceed %d, the bound set by scheduler.backfill_max_slots", len(selected), maxSlotsOf(req.MaxSlots))
	}
	return selected, nil
}

// firstSlotFrom returns the first fire whose entry's latest date is on or
// after the range's first date, starting the search at the fire whose
// entry would fall on fromStart if its offset held.
func (r PlanRequest) firstSlotFrom(fromStart time.Time) (time.Time, error) {
	at, err := r.estimate(fromStart)
	if err != nil || at.IsZero() {
		return time.Time{}, err
	}
	// A list with no values says nothing, so the walk passes it; the
	// slot-by-slot check drops it.
	reaches := func(at time.Time) (bool, error) {
		span, err := r.entrySpan(at)
		return !span.ok || !span.last.Before(r.FromDate), err
	}
	return r.boundary(at, reaches, true)
}

// lastSlotUntil returns the last fire at or before latest whose entry's
// earliest date is on or before the range's last date.
func (r PlanRequest) lastSlotUntil(toEnd, latest time.Time) (time.Time, error) {
	at, err := r.estimate(toEnd)
	if err != nil {
		return time.Time{}, err
	}
	if at.IsZero() || at.After(latest) {
		at = r.fireAtOrBefore(latest)
	}
	if at.IsZero() {
		return time.Time{}, nil
	}
	within := func(at time.Time) (bool, error) {
		if at.After(latest) {
			return false, nil
		}
		span, err := r.entrySpan(at)
		return !span.ok || !r.ToDate.Before(span.first), err
	}
	return r.boundary(at, within, false)
}

// estimate returns the fire whose entry would fall at target if the entry
// kept the offset from its slot it has at the fire nearest target.
func (r PlanRequest) estimate(target time.Time) (time.Time, error) {
	probe := r.fireAtOrAfter(target)
	if probe.IsZero() {
		probe = r.fireAtOrBefore(target)
	}
	if probe.IsZero() {
		return time.Time{}, nil
	}
	instants, err := ir.ResolveDateInstants(r.DAG.Dates, r.By, slotDateContext(r.Schedule, probe))
	if err != nil || len(instants) == 0 {
		return probe, err
	}
	guess := r.fireAtOrAfter(target.Add(probe.Sub(instants[0])))
	if guess.IsZero() {
		return probe, nil
	}
	return guess, nil
}

// boundary walks from a fire to the edge of the fires that hold: for the
// first edge, back while the previous fire holds, else forward to the first
// that does; for the last edge, the mirror image. It returns zero when no
// fire holds.
func (r PlanRequest) boundary(at time.Time, holds func(time.Time) (bool, error), first bool) (time.Time, error) {
	toward, away := r.previousFire, r.nextFire
	if !first {
		toward, away = r.nextFire, r.previousFire
	}
	ok, err := holds(at)
	if err != nil {
		return time.Time{}, err
	}
	for range maxBoundarySteps {
		if ok {
			next := toward(at)
			if next.IsZero() {
				return at, nil
			}
			nextOK, err := holds(next)
			if err != nil {
				return time.Time{}, err
			}
			if !nextOK {
				return at, nil
			}
			at = next
			continue
		}
		at = away(at)
		if at.IsZero() {
			return time.Time{}, nil
		}
		if ok, err = holds(at); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, invalid("by: no edge of the range within %d slots of the estimate", maxBoundarySteps)
}

func (r PlanRequest) nextFire(at time.Time) time.Time {
	return ir.ScheduleWindowAt(r.Schedule.Schedules, r.Schedule.Profile, r.Schedule.Location, at).Next
}

func (r PlanRequest) previousFire(at time.Time) time.Time {
	return ir.ScheduleWindowAt(r.Schedule.Schedules, r.Schedule.Profile, r.Schedule.Location, at).Start
}

func (r PlanRequest) fireAtOrAfter(t time.Time) time.Time {
	fires, _ := ir.ScheduleFiresBetween(r.Schedule.Schedules, r.Schedule.Profile, r.Schedule.Location, t, t.AddDate(5, 0, 0), 1)
	if len(fires) == 0 {
		return time.Time{}
	}
	return fires[0]
}

func (r PlanRequest) fireAtOrBefore(t time.Time) time.Time {
	if fires, _ := ir.ScheduleFiresBetween(r.Schedule.Schedules, r.Schedule.Profile, r.Schedule.Location, t, t, 1); len(fires) == 1 {
		return fires[0]
	}
	return r.previousFire(t)
}
