// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import "time"

// ScheduleWindow is the stretch of time a schedule slot owns: from the
// previous fire of the DAG's active start schedules up to the slot.
type ScheduleWindow struct {
	// Start is the latest fire before the slot, or zero when there is none.
	Start time.Time
	// Next is the earliest fire after the slot, or zero when there is none.
	Next time.Time
	// Timezone names the zone the slot's schedule is evaluated in.
	Timezone string
}

// previousFireLookbacks are the spans searched, shortest first, for a
// schedule's latest fire before a slot. The longest covers yearly schedules
// whose calendar skips a year's only candidate.
var previousFireLookbacks = []time.Duration{
	time.Hour,
	25 * time.Hour,
	32 * 24 * time.Hour,
	367 * 24 * time.Hour,
	5 * 366 * 24 * time.Hour,
}

// ScheduleWindowAt returns the window of the slot at the given time. The
// active schedules are the recurring start schedules for the profile;
// one-off schedules never bound a window. A schedule that names no zone is
// evaluated in loc. Calendar schedules must be bound to their calendars.
func ScheduleWindowAt(schedules []Schedule, profile string, loc *time.Location, slot time.Time) ScheduleWindow {
	if loc == nil {
		loc = time.Local
	}
	return windowOf(activeWindowSchedules(schedules, profile), loc, slot)
}

// windowOf returns the window of the slot among schedules already filtered
// to the active ones.
func windowOf(active []Schedule, loc *time.Location, slot time.Time) ScheduleWindow {
	slot = slot.In(loc)
	var window ScheduleWindow
	// The slot belongs to the first schedule firing at it, else to the
	// schedule of the previous fire.
	firing, previous := -1, -1
	for i, sched := range active {
		if firing < 0 && firesAt(sched, slot) {
			firing = i
		}
		if prev := previousFire(sched, slot); !prev.IsZero() && prev.After(window.Start) {
			window.Start = prev
			previous = i
		}
		if next := sched.Next(slot); !next.IsZero() && (window.Next.IsZero() || next.Before(window.Next)) {
			window.Next = next
		}
	}
	owner := firing
	if owner < 0 {
		owner = previous
	}

	zone := loc
	if owner >= 0 {
		zone = scheduleZone(active[owner], loc)
	}
	window.Timezone = zoneLabel(slot.In(zone))
	window.Start = utcOrZero(window.Start)
	window.Next = utcOrZero(window.Next)
	return window
}

// activeWindowSchedules returns the recurring start schedules that are active
// for the profile: those with no profile or with the profile.
func activeWindowSchedules(schedules []Schedule, profile string) []Schedule {
	var active []Schedule
	for _, sched := range schedules {
		if sched.IsCron() && (sched.Profile == "" || sched.Profile == profile) {
			active = append(active, sched)
		}
	}
	return active
}

// previousFire returns the schedule's latest fire strictly before slot, or
// zero when none falls within the longest lookback.
func previousFire(sched Schedule, slot time.Time) time.Time {
	for _, lookback := range previousFireLookbacks {
		var prev time.Time
		for t := sched.Next(slot.Add(-lookback)); !t.IsZero() && t.Before(slot); t = sched.Next(t) {
			prev = t
		}
		if !prev.IsZero() {
			return prev
		}
	}
	return time.Time{}
}

// firesAt reports whether the schedule fires at slot.
func firesAt(sched Schedule, slot time.Time) bool {
	return sched.Next(slot.Add(-time.Second)).Equal(slot)
}

// scheduleZone returns the zone a schedule is evaluated in. An interval
// schedule fires on a grid aligned to the Unix epoch, independent of zone.
func scheduleZone(sched Schedule, loc *time.Location) *time.Location {
	if isIntervalSchedule(sched) {
		return time.UTC
	}
	if zone := sched.Location(); zone != nil {
		return zone
	}
	return loc
}

// zoneLabel names the zone of t: its time zone name, or its UTC offset when
// the zone has none, as with the host's local zone.
func zoneLabel(t time.Time) string {
	if name := t.Location().String(); name != "" && name != "Local" {
		return name
	}
	return "UTC" + t.Format("-07:00")
}

// formatWindowInstant formats a window bound as stored on a run: UTC
// RFC 3339, or empty when the bound is unknown.
func formatWindowInstant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// parseWindowInstant parses a window bound stored on a run, or returns zero
// when the bound is unknown.
func parseWindowInstant(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// utcOrZero returns t in UTC, keeping the zero time zero.
func utcOrZero(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}
