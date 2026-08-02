// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package buscal implements business calendars: named sets of weekend days and
// holidays that the scheduler consults to decide whether a scheduled DAG run
// should dispatch on a given date.
package buscal

import (
	"fmt"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/dagucloud/dagu/v2/internal/core"
)

// dateLayout is the calendar date format used for holiday entries.
const dateLayout = "2006-01-02"

// Calendar is an immutable, evaluated business calendar.
type Calendar struct {
	// Name is the calendar identity, taken from the definition filename stem.
	Name string
	// Location is the timezone in which calendar dates are interpreted.
	Location *time.Location

	weekend  map[time.Weekday]bool
	holidays map[string]bool
}

// definition is the YAML shape of a calendar file.
type definition struct {
	// Timezone is an IANA timezone name. Empty means the process-local zone.
	Timezone string `yaml:"timezone,omitempty"`
	// Weekends lists non-working weekday names. Defaults to saturday/sunday.
	Weekends *[]string `yaml:"weekends,omitempty"`
	// Holidays lists non-working dates in YYYY-MM-DD form.
	Holidays []string `yaml:"holidays,omitempty"`
}

var weekdayNames = map[string]time.Weekday{
	"sunday":    time.Sunday,
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
}

// Parse parses a calendar definition. The name becomes the calendar identity
// and must be a valid calendar name.
func Parse(name string, data []byte) (*Calendar, error) {
	if !core.ValidCalendarName(name) {
		return nil, fmt.Errorf("invalid calendar name %q", name)
	}

	var def definition
	if err := yaml.UnmarshalWithOptions(data, &def, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("invalid calendar definition %q: %w", name, err)
	}

	loc := time.Local
	if def.Timezone != "" {
		var err error
		loc, err = time.LoadLocation(def.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q in calendar %q: %w", def.Timezone, name, err)
		}
	}

	weekend := map[time.Weekday]bool{time.Saturday: true, time.Sunday: true}
	if def.Weekends != nil {
		weekend = map[time.Weekday]bool{}
		for _, dayName := range *def.Weekends {
			day, ok := weekdayNames[dayName]
			if !ok {
				return nil, fmt.Errorf("invalid weekend day %q in calendar %q: use full lowercase day names (e.g. \"saturday\")", dayName, name)
			}
			weekend[day] = true
		}
	}
	if len(weekend) >= 7 {
		return nil, fmt.Errorf("calendar %q declares every weekday as weekend", name)
	}

	holidays := make(map[string]bool, len(def.Holidays))
	for _, holidayDate := range def.Holidays {
		// Validate without a location: in zones where midnight does not
		// exist on a DST transition day, location-aware parsing normalizes
		// the instant and can shift the date key by one day.
		if _, err := time.Parse(dateLayout, holidayDate); err != nil {
			return nil, fmt.Errorf("invalid holiday date %q in calendar %q: must be YYYY-MM-DD", holidayDate, name)
		}
		holidays[holidayDate] = true
	}

	return &Calendar{
		Name:     name,
		Location: loc,
		weekend:  weekend,
		holidays: holidays,
	}, nil
}

// IsHoliday reports whether t falls on a declared holiday date.
func (c *Calendar) IsHoliday(t time.Time) bool {
	return c.holidays[t.In(c.Location).Format(dateLayout)]
}

// IsWeekend reports whether t falls on a weekend day.
func (c *Calendar) IsWeekend(t time.Time) bool {
	return c.weekend[t.In(c.Location).Weekday()]
}

// IsBusinessDay reports whether t is neither a weekend day nor a holiday.
func (c *Calendar) IsBusinessDay(t time.Time) bool {
	return !c.IsWeekend(t) && !c.IsHoliday(t)
}

// sameDate reports whether a and b fall on the same calendar date.
func (c *Calendar) sameDate(a, b time.Time) bool {
	a, b = a.In(c.Location), b.In(c.Location)
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// LastBusinessDayOfMonth returns the last business day of t's month, or false
// if the month contains no business day.
func (c *Calendar) LastBusinessDayOfMonth(t time.Time) (time.Time, bool) {
	t = t.In(c.Location)
	year, month, _ := t.Date()
	// Day 0 of the next month is the last day of this month.
	day := time.Date(year, month+1, 0, 0, 0, 0, 0, c.Location)
	for day.Month() == month {
		if c.IsBusinessDay(day) {
			return day, true
		}
		day = day.AddDate(0, 0, -1)
	}
	return time.Time{}, false
}

// FirstBusinessDayOfMonth returns the first business day of t's month, or
// false if the month contains no business day.
func (c *Calendar) FirstBusinessDayOfMonth(t time.Time) (time.Time, bool) {
	t = t.In(c.Location)
	year, month, _ := t.Date()
	day := time.Date(year, month, 1, 0, 0, 0, 0, c.Location)
	for day.Month() == month {
		if c.IsBusinessDay(day) {
			return day, true
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, false
}

// Decision is the outcome of evaluating a scheduled time against a calendar.
type Decision struct {
	// Skip reports whether the scheduled run must not dispatch.
	Skip bool
	// Reason is a human-readable explanation when Skip is true.
	Reason string
}

// Evaluate decides whether a run scheduled at t should dispatch under the
// given day filter. Without a filter, only holidays block dispatch; weekday
// selection is left to the cron expression.
func (c *Calendar) Evaluate(t time.Time, filter core.CalendarDayFilter) Decision {
	date := t.In(c.Location).Format(dateLayout)
	switch filter {
	case core.CalendarDayFilterBusinessDays:
		if !c.IsBusinessDay(t) {
			return Decision{Skip: true, Reason: fmt.Sprintf("%s is not a business day in calendar %q", date, c.Name)}
		}
	case core.CalendarDayFilterLastBusinessDay:
		last, ok := c.LastBusinessDayOfMonth(t)
		if !ok || !c.sameDate(t, last) {
			return Decision{Skip: true, Reason: fmt.Sprintf("%s is not the last business day of the month in calendar %q", date, c.Name)}
		}
	case core.CalendarDayFilterFirstBusinessDay:
		first, ok := c.FirstBusinessDayOfMonth(t)
		if !ok || !c.sameDate(t, first) {
			return Decision{Skip: true, Reason: fmt.Sprintf("%s is not the first business day of the month in calendar %q", date, c.Name)}
		}
	case core.CalendarDayFilterNone:
		if c.IsHoliday(t) {
			return Decision{Skip: true, Reason: fmt.Sprintf("%s is a holiday in calendar %q", date, c.Name)}
		}
	default:
		// Fail closed: an unrecognized filter (a newer config read by an
		// older binary, or an unvalidated embed-API config) must not
		// dispatch as if no filter were set.
		return Decision{Skip: true, Reason: fmt.Sprintf("unknown day filter %q in calendar %q", string(filter), c.Name)}
	}
	return Decision{}
}
