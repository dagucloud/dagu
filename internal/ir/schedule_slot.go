// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ScheduleSlotLatest names the latest fire time at or before now.
const ScheduleSlotLatest = "latest"

const (
	slotDateLayout        = "2006-01-02"
	slotLocalMinuteLayout = "2006-01-02T15:04"
)

// ScheduleSlotRequest names the schedule slot a run is started for.
type ScheduleSlotRequest struct {
	// Value is "latest", a date, a time without an offset, or an RFC 3339
	// time with an offset.
	Value string
	// Schedules are the DAG's start schedules, with calendars bound.
	Schedules []Schedule
	// Profile selects the profile-scoped schedules that are active.
	Profile string
	// Location is the zone of schedules that name none.
	Location *time.Location
	// Now bounds the slot: a later slot is rejected unless AllowFuture.
	Now         time.Time
	AllowFuture bool
}

// ScheduleSlot is a resolved schedule slot.
type ScheduleSlot struct {
	At     time.Time
	Window ScheduleWindow
	// Warning is set when At is not a fire time of the active schedules.
	Warning string
}

// ResolveScheduleSlot resolves the slot a request names.
func ResolveScheduleSlot(req ScheduleSlotRequest) (ScheduleSlot, error) {
	loc := req.Location
	if loc == nil {
		loc = time.Local
	}
	active := activeWindowSchedules(req.Schedules, req.Profile)
	value := strings.TrimSpace(req.Value)

	at, err := parseScheduleSlot(value, active, loc, req.Now)
	if err != nil {
		return ScheduleSlot{}, err
	}
	if !req.AllowFuture && at.After(req.Now) {
		msg := fmt.Sprintf("scheduled time %s is later than now", formatSlotTime(at, active, loc))
		if latest := latestFire(active, loc, req.Now); !latest.IsZero() {
			msg += fmt.Sprintf("; the latest fire time is %s", formatSlotTime(latest, active, loc))
		}
		return ScheduleSlot{}, errors.New(msg)
	}

	slot := ScheduleSlot{At: at.UTC(), Window: windowOf(active, loc, at)}
	if len(active) > 0 && !slices.ContainsFunc(active, func(s Schedule) bool { return firesAt(s, at.In(loc)) }) {
		slot.Warning = fmt.Sprintf("%s is not a fire time of the schedule; nearest fire times: %s",
			formatSlotTime(at, active, loc), nearestFires(slot.Window, active, loc))
	}
	return slot, nil
}

func parseScheduleSlot(value string, active []Schedule, loc *time.Location, now time.Time) (time.Time, error) {
	switch value {
	case "":
		return time.Time{}, fmt.Errorf("scheduled time is empty")
	case ScheduleSlotLatest:
		if len(active) == 0 {
			return time.Time{}, fmt.Errorf("%q needs a recurring start schedule", ScheduleSlotLatest)
		}
		latest := latestFire(active, loc, now)
		if latest.IsZero() {
			return time.Time{}, fmt.Errorf("the schedule has not fired before now")
		}
		return latest, nil
	}

	if at, err := time.Parse(time.RFC3339, value); err == nil {
		return minuteSlot(at, value)
	}

	zone, err := slotZone(active, loc)
	if err != nil {
		return time.Time{}, err
	}
	if day, err := time.ParseInLocation(slotDateLayout, value, zone); err == nil {
		if len(active) == 0 {
			return time.Time{}, fmt.Errorf("a date needs a recurring start schedule; give a time such as %sT00:00", value)
		}
		return fireOnDate(active, loc, day)
	}
	if at, err := time.ParseInLocation(slotLocalMinuteLayout, value, zone); err == nil {
		// A wall-clock time skipped by a daylight saving change names no
		// instant; time.Date would move it to another hour.
		if at.Format(slotLocalMinuteLayout) != value {
			return time.Time{}, fmt.Errorf("scheduled time %q does not exist in %s; give the time with an offset", value, zone)
		}
		return at, nil
	}
	return time.Time{}, fmt.Errorf("invalid scheduled time %q: use %q, a date (2006-01-02), a time (2006-01-02T15:04), or RFC 3339 with an offset",
		value, ScheduleSlotLatest)
}

func minuteSlot(at time.Time, value string) (time.Time, error) {
	if at.Second() != 0 || at.Nanosecond() != 0 {
		return time.Time{}, fmt.Errorf("invalid scheduled time %q: seconds must be :00", value)
	}
	return at, nil
}

// slotZone returns the zone a time without an offset is read in: the zone
// of the recurring start schedules, UTC when they are all interval
// schedules, else loc. Interval schedules fire independent of zone and name
// none when other schedules do.
func slotZone(active []Schedule, loc *time.Location) (*time.Location, error) {
	var zones []string
	byName := map[string]*time.Location{}
	intervals := 0
	for _, sched := range active {
		if isIntervalSchedule(sched) {
			intervals++
			continue
		}
		zone := scheduleZone(sched, loc)
		name := zone.String()
		if _, ok := byName[name]; !ok {
			byName[name] = zone
			zones = append(zones, name)
		}
	}
	switch len(zones) {
	case 0:
		if intervals > 0 {
			return time.UTC, nil
		}
		return loc, nil
	case 1:
		return byName[zones[0]], nil
	default:
		return nil, fmt.Errorf("the schedules use different zones (%s); give the time with an offset",
			strings.Join(zones, ", "))
	}
}

// fireOnDate returns the only fire on the date in its zone, or an error that
// lists the fires on it, or the nearest ones when there are none.
func fireOnDate(active []Schedule, loc *time.Location, day time.Time) (time.Time, error) {
	end := day.AddDate(0, 0, 1)
	var fires []time.Time
	for t := nextFire(active, loc, day.Add(-time.Second)); !t.IsZero() && t.Before(end); t = nextFire(active, loc, t) {
		fires = append(fires, t)
		if len(fires) > maxListedFires {
			break
		}
	}
	date := day.Format(slotDateLayout)
	switch {
	case len(fires) == 1:
		return fires[0], nil
	case len(fires) == 0:
		window := windowOf(active, loc, day)
		return time.Time{}, fmt.Errorf("the schedule does not fire on %s; nearest fire times: %s",
			date, nearestFires(ScheduleWindow{Start: window.Start, Next: nextFire(active, loc, day)}, active, loc))
	default:
		listed := make([]string, 0, maxListedFires)
		for _, fire := range fires[:min(len(fires), maxListedFires)] {
			listed = append(listed, formatSlotTime(fire, active, loc))
		}
		if len(fires) > maxListedFires {
			listed = append(listed, "...")
		}
		return time.Time{}, fmt.Errorf("the schedule fires more than once on %s (%s); give the time", date, strings.Join(listed, ", "))
	}
}

// maxListedFires bounds the fire times an error lists.
const maxListedFires = 5

func latestFire(active []Schedule, loc *time.Location, now time.Time) time.Time {
	cutoff := now.In(loc).Truncate(time.Minute).Add(time.Minute)
	return windowOf(active, loc, cutoff).Start
}

func nextFire(active []Schedule, loc *time.Location, after time.Time) time.Time {
	var next time.Time
	for _, sched := range active {
		if t := sched.Next(after.In(loc)); !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	return next
}

func nearestFires(window ScheduleWindow, active []Schedule, loc *time.Location) string {
	var parts []string
	if !window.Start.IsZero() {
		parts = append(parts, formatSlotTime(window.Start, active, loc)+" (before)")
	}
	if !window.Next.IsZero() {
		parts = append(parts, formatSlotTime(window.Next, active, loc)+" (after)")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// formatSlotTime formats a slot in the zone of the schedules when they share
// one, else in UTC.
func formatSlotTime(at time.Time, active []Schedule, loc *time.Location) string {
	zone, err := slotZone(active, loc)
	if err != nil {
		zone = time.UTC
	}
	return at.In(zone).Format(time.RFC3339)
}

func isIntervalSchedule(sched Schedule) bool {
	base, ok := sched.baseCron()
	if !ok {
		return false
	}
	_, interval := base.(intervalSchedule)
	return interval
}
