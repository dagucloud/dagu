// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"regexp"
)

// CalendarDayFilter restricts which scheduled days actually dispatch when a
// business calendar is attached to a DAG.
type CalendarDayFilter string

const (
	// CalendarDayFilterNone applies no day filter: only holidays are skipped.
	CalendarDayFilterNone CalendarDayFilter = ""
	// CalendarDayFilterBusinessDays dispatches only on business days
	// (neither a weekend day nor a holiday in the calendar).
	CalendarDayFilterBusinessDays CalendarDayFilter = "business-days"
	// CalendarDayFilterLastBusinessDay dispatches only when the scheduled date
	// is the last business day of its month.
	CalendarDayFilterLastBusinessDay CalendarDayFilter = "last-business-day"
	// CalendarDayFilterFirstBusinessDay dispatches only when the scheduled date
	// is the first business day of its month.
	CalendarDayFilterFirstBusinessDay CalendarDayFilter = "first-business-day"
)

// ParseCalendarDayFilter parses a calendar day filter value.
func ParseCalendarDayFilter(s string) (CalendarDayFilter, error) {
	switch CalendarDayFilter(s) {
	case CalendarDayFilterNone, CalendarDayFilterBusinessDays,
		CalendarDayFilterLastBusinessDay, CalendarDayFilterFirstBusinessDay:
		return CalendarDayFilter(s), nil
	default:
		return CalendarDayFilterNone, fmt.Errorf(
			"invalid calendar day filter %q: must be one of %q, %q, %q",
			s, CalendarDayFilterBusinessDays, CalendarDayFilterLastBusinessDay, CalendarDayFilterFirstBusinessDay,
		)
	}
}

// CalendarHolidayPolicy controls what happens when a scheduled time falls on a
// non-business day of the attached calendar.
type CalendarHolidayPolicy string

const (
	// CalendarHolidaySkip drops the scheduled run entirely.
	CalendarHolidaySkip CalendarHolidayPolicy = "skip"
)

// ParseCalendarHolidayPolicy parses a holiday policy value. An empty string
// defaults to CalendarHolidaySkip.
func ParseCalendarHolidayPolicy(s string) (CalendarHolidayPolicy, error) {
	switch CalendarHolidayPolicy(s) {
	case "", CalendarHolidaySkip:
		return CalendarHolidaySkip, nil
	default:
		return CalendarHolidaySkip, fmt.Errorf(
			"invalid calendar holiday policy %q: only %q is supported", s, CalendarHolidaySkip,
		)
	}
}

// calendarNameRegex validates business calendar names, which double as the
// calendar definition filename stem.
var calendarNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidCalendarName reports whether name is a valid business calendar name.
func ValidCalendarName(name string) bool {
	return calendarNameRegex.MatchString(name)
}

// CalendarConfig attaches a registered business calendar to a DAG's schedule.
// The scheduler consults the calendar at dispatch time, so calendar edits take
// effect without reloading the DAG.
type CalendarConfig struct {
	// Name is the registered calendar name (the definition filename stem).
	Name string `json:"name"`
	// Days optionally restricts dispatch to a business-day pattern.
	Days CalendarDayFilter `json:"days,omitempty"`
	// OnHoliday controls behavior when the scheduled date is not runnable.
	OnHoliday CalendarHolidayPolicy `json:"onHoliday,omitempty"`
}
