// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DateContext is what a run's dates are resolved from.
type DateContext struct {
	// ScheduledAt is the run's schedule slot, or zero when it has none.
	ScheduledAt time.Time
	// Window is the window the slot owns.
	Window ScheduleWindow
	// CreatedAt is when the run was created. A run without a slot resolves
	// scheduled_at and interval_end against it, cut to the minute.
	CreatedAt time.Time
	// Location is the zone of entries that name none when the window names
	// no zone. Nil means the host's local zone.
	Location *time.Location
	// Calendar gives the business days, or nil when the run has no
	// calendar.
	Calendar *Calendar
}

// DateValue is a resolved entry of a dates block.
type DateValue struct {
	Name string `json:"name"`
	// Value is the formatted date of a single entry.
	Value string `json:"value,omitempty"`
	// List reports an entry that steps through the window; Values holds its
	// dates, oldest first.
	List   bool     `json:"list,omitempty"`
	Values []string `json:"values,omitempty"`
}

// Text returns the value as ${dates.NAME} inserts it: the date, or a JSON
// array of a list's dates.
func (v DateValue) Text() string {
	if !v.List {
		return v.Value
	}
	values := v.Values
	if values == nil {
		values = []string{}
	}
	data, _ := json.Marshal(values)
	return string(data)
}

// maxBusinessDayScan bounds the days searched for the next business day.
const maxBusinessDayScan = 5 * 366

var errNoCalendar = errors.New("it uses business days, and the run has no calendar")

// ResolveDates resolves a valid dates block for a run, in declaration order.
// An error names the entry that cannot resolve.
func ResolveDates(specs []DateSpec, dc DateContext) ([]DateValue, error) {
	r, err := newDateResolver(specs, dc)
	if err != nil {
		return nil, err
	}
	values := make([]DateValue, 0, len(specs))
	for _, spec := range specs {
		value, err := r.value(spec)
		if err != nil {
			return nil, fmt.Errorf("dates.%s: %w", spec.Name, err)
		}
		values = append(values, value)
	}
	return values, nil
}

// zonedInstant is an entry's instant after its shift, with the zone it was
// shifted in.
type zonedInstant struct {
	at   time.Time
	zone *time.Location
}

type dateResolver struct {
	dc     DateContext
	byName map[string]DateSpec
	// slot is the run's slot, or the minute it was created when it has none.
	slot    time.Time
	hasSlot bool
	zone    *time.Location
	// instants memoizes the entries others build on.
	instants map[string]zonedInstant
}

func newDateResolver(specs []DateSpec, dc DateContext) (*dateResolver, error) {
	zone := dc.Location
	if zone == nil {
		zone = time.Local
	}
	if dc.Window.Timezone != "" {
		loc, err := zoneFromLabel(dc.Window.Timezone)
		if err != nil {
			return nil, fmt.Errorf("dates: the run's zone %q: %w", dc.Window.Timezone, err)
		}
		zone = loc
	}
	r := &dateResolver{
		dc:       dc,
		byName:   make(map[string]DateSpec, len(specs)),
		slot:     dc.ScheduledAt,
		hasSlot:  !dc.ScheduledAt.IsZero(),
		zone:     zone,
		instants: make(map[string]zonedInstant),
	}
	if !r.hasSlot {
		r.slot = dc.CreatedAt.Truncate(time.Minute)
	}
	for _, spec := range specs {
		r.byName[spec.Name] = spec
	}
	return r, nil
}

func (r *dateResolver) value(spec DateSpec) (DateValue, error) {
	if spec.Each != "" {
		instants, zone, err := r.list(spec)
		if err != nil {
			return DateValue{}, err
		}
		values := make([]string, 0, len(instants))
		for _, at := range instants {
			shifted, err := r.shift(spec, at.In(zone))
			if err != nil {
				return DateValue{}, err
			}
			text, err := formatDate(spec.Format, shifted)
			if err != nil {
				return DateValue{}, err
			}
			values = append(values, text)
		}
		return DateValue{Name: spec.Name, List: true, Values: values}, nil
	}
	instant, err := r.instant(spec.Name, 0)
	if err != nil {
		return DateValue{}, err
	}
	text, err := formatDate(spec.Format, instant.at)
	if err != nil {
		return DateValue{}, err
	}
	return DateValue{Name: spec.Name, Value: text}, nil
}

// instant returns the shifted instant of the named single entry. depth
// bounds the chain of entries, so a cycle fails instead of recursing.
func (r *dateResolver) instant(name string, depth int) (zonedInstant, error) {
	if cached, ok := r.instants[name]; ok {
		return cached, nil
	}
	if depth > len(r.byName) {
		return zonedInstant{}, errors.New("entries build on each other in a cycle")
	}
	spec := r.byName[name]
	start, zone, err := r.base(fromOf(spec), depth)
	if err != nil {
		return zonedInstant{}, err
	}
	if spec.Timezone != "" {
		if zone, err = time.LoadLocation(spec.Timezone); err != nil {
			return zonedInstant{}, fmt.Errorf("unknown zone %q", spec.Timezone)
		}
	}
	at, err := r.shift(spec, start.In(zone))
	if err != nil {
		return zonedInstant{}, err
	}
	result := zonedInstant{at: at, zone: zone}
	r.instants[name] = result
	return result, nil
}

// base returns the instant from names and the zone it carries.
func (r *dateResolver) base(from string, depth int) (time.Time, *time.Location, error) {
	switch from {
	case DateFromScheduledAt, DateFromIntervalEnd:
		return r.slot, r.zone, nil
	case DateFromIntervalStart:
		start, err := r.windowStart()
		return start, r.zone, err
	case DateFromNextScheduledAt:
		if err := r.requireSlot(DateFromNextScheduledAt); err != nil {
			return time.Time{}, nil, err
		}
		if r.dc.Window.Next.IsZero() {
			return time.Time{}, nil, errors.New("the schedule fires no time after the slot")
		}
		return r.dc.Window.Next, r.zone, nil
	}
	if target, ok := r.byName[from]; !ok || target.Each != "" {
		return time.Time{}, nil, fmt.Errorf("from %q names no single entry", from)
	}
	instant, err := r.instant(from, depth+1)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("from %s: %w", from, err)
	}
	return instant.at, instant.zone, nil
}

func (r *dateResolver) windowStart() (time.Time, error) {
	if err := r.requireSlot(DateFromIntervalStart); err != nil {
		return time.Time{}, err
	}
	if r.dc.Window.Start.IsZero() {
		return time.Time{}, errors.New("the schedule fired no time before the slot, so the window has no start")
	}
	return r.dc.Window.Start, nil
}

func (r *dateResolver) requireSlot(field string) error {
	if r.hasSlot {
		return nil
	}
	return fmt.Errorf("%s needs the window of a schedule slot, and the run has no slot", field)
}

// list returns the instants an entry steps through, from interval_start up
// to the slot, with the zone it steps in.
func (r *dateResolver) list(spec DateSpec) ([]time.Time, *time.Location, error) {
	amount, unit, err := parseDateStep(spec.Each)
	if err != nil {
		return nil, nil, err
	}
	start, err := r.windowStart()
	if err != nil {
		return nil, nil, err
	}
	zone := r.zone
	if spec.Timezone != "" {
		if zone, err = time.LoadLocation(spec.Timezone); err != nil {
			return nil, nil, fmt.Errorf("unknown zone %q", spec.Timezone)
		}
	}
	start = start.In(zone)
	if unit == unitBusinessDay {
		if r.dc.Calendar == nil {
			return nil, nil, errNoCalendar
		}
		if start, err = businessDayOr(start, 1, r.dc.Calendar); err != nil {
			return nil, nil, err
		}
	}

	var instants []time.Time
	for at := start; at.Before(r.slot); {
		if len(instants) == MaxDateListValues {
			return nil, nil, fmt.Errorf("each %s gives more than %d dates", spec.Each, MaxDateListValues)
		}
		instants = append(instants, at)
		if unit == unitBusinessDay {
			if at, err = addBusinessDays(at, amount, r.dc.Calendar); err != nil {
				return nil, nil, err
			}
			continue
		}
		// Each date is stepped from the start, so a month step that clamps
		// to a short month does not drift.
		at = addDateAmount(start, amount*len(instants), unit)
	}
	return instants, zone, nil
}

// shift applies an entry's operations to t, in t's zone.
func (r *dateResolver) shift(spec DateSpec, t time.Time) (time.Time, error) {
	for _, text := range spec.Shift {
		op, err := parseDateOp(text)
		if err != nil {
			return time.Time{}, err
		}
		if t, err = applyDateOp(op, t, r.dc.Calendar); err != nil {
			return time.Time{}, fmt.Errorf("shift %s: %w", text, err)
		}
	}
	return t, nil
}

func applyDateOp(op dateOp, t time.Time, cal *Calendar) (time.Time, error) {
	if op.usesBusinessDays() && cal == nil {
		return time.Time{}, errNoCalendar
	}
	switch op.kind {
	case opStartOf:
		return startOfUnit(t, op.unit), nil
	case opBusinessDayOrNext:
		return businessDayOr(t, 1, cal)
	case opBusinessDayOrPrev:
		return businessDayOr(t, -1, cal)
	case opAdd:
		if op.unit == unitBusinessDay {
			return addBusinessDays(t, op.amount, cal)
		}
		return addDateAmount(t, op.amount, op.unit), nil
	default:
		return t, nil
	}
}

// addDateAmount adds an amount of a unit other than business days to t,
// which need a calendar. Minutes and hours are elapsed time; the other units
// are calendar units in t's zone that keep the wall-clock time.
func addDateAmount(t time.Time, amount int, unit dateUnit) time.Time {
	switch unit {
	case unitMinute:
		return t.Add(time.Duration(amount) * time.Minute)
	case unitHour:
		return t.Add(time.Duration(amount) * time.Hour)
	case unitDay:
		return addDays(t, amount)
	case unitWeek:
		return addDays(t, 7*amount)
	case unitMonth:
		return addMonths(t, amount)
	case unitYear:
		return addMonths(t, 12*amount)
	case unitBusinessDay:
		return t
	default:
		return t
	}
}

func addDays(t time.Time, days int) time.Time {
	year, month, day := t.Date()
	hour, minute, sec := t.Clock()
	return time.Date(year, month, day+days, hour, minute, sec, t.Nanosecond(), t.Location())
}

// addMonths adds months to t, clamping the day to the last day of a shorter
// month: March 31 minus one month is the last day of February.
func addMonths(t time.Time, months int) time.Time {
	year, month, day := t.Date()
	hour, minute, sec := t.Clock()
	first := time.Date(year, month+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	if last := first.AddDate(0, 1, -1).Day(); day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, hour, minute, sec, t.Nanosecond(), t.Location())
}

// startOfUnit truncates t to the start of its minute, hour, day, week,
// month, or year in t's zone. A week starts on Monday, and a business day
// starts with its day.
func startOfUnit(t time.Time, unit dateUnit) time.Time {
	year, month, day := t.Date()
	loc := t.Location()
	switch unit {
	case unitMinute:
		return time.Date(year, month, day, t.Hour(), t.Minute(), 0, 0, loc)
	case unitHour:
		return time.Date(year, month, day, t.Hour(), 0, 0, 0, loc)
	case unitDay, unitBusinessDay:
		return time.Date(year, month, day, 0, 0, 0, 0, loc)
	case unitWeek:
		sinceMonday := (int(t.Weekday()) + 6) % 7
		return time.Date(year, month, day-sinceMonday, 0, 0, 0, 0, loc)
	case unitMonth:
		return time.Date(year, month, 1, 0, 0, 0, 0, loc)
	case unitYear:
		return time.Date(year, time.January, 1, 0, 0, 0, 0, loc)
	default:
		return t
	}
}

// addBusinessDays moves t by a number of business days, keeping its
// wall-clock time.
func addBusinessDays(t time.Time, days int, cal *Calendar) (time.Time, error) {
	step := 1
	if days < 0 {
		step, days = -1, -days
	}
	for range days {
		next, err := nextBusinessDay(t, step, cal)
		if err != nil {
			return time.Time{}, err
		}
		t = next
	}
	return t, nil
}

// businessDayOr returns t when its date is a business day, else the next
// business day in the direction of step.
func businessDayOr(t time.Time, step int, cal *Calendar) (time.Time, error) {
	if cal.IsBusinessDay(DateOf(t)) {
		return t, nil
	}
	return nextBusinessDay(t, step, cal)
}

// nextBusinessDay returns the first business day after t, or before it when
// step is negative, at t's wall-clock time.
func nextBusinessDay(t time.Time, step int, cal *Calendar) (time.Time, error) {
	for i := 1; i <= maxBusinessDayScan; i++ {
		candidate := addDays(t, step*i)
		if cal.IsBusinessDay(DateOf(candidate)) {
			return candidate, nil
		}
	}
	return time.Time{}, fmt.Errorf("calendar %q has no business day within five years of %s", cal.Name, DateOf(t))
}

// zoneFromLabel returns the zone a window names: a zone name, or a UTC
// offset such as UTC+09:00 for a zone without a name.
func zoneFromLabel(label string) (*time.Location, error) {
	if offset, ok := strings.CutPrefix(label, "UTC"); ok && offset != "" {
		t, err := time.Parse("-07:00", offset)
		if err != nil {
			return nil, fmt.Errorf("invalid UTC offset %q", offset)
		}
		_, seconds := t.Zone()
		return time.FixedZone(label, seconds), nil
	}
	return time.LoadLocation(label)
}
