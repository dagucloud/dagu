// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"slices"
	"time"

	"github.com/robfig/cron/v3"
)

// calendarSearchYears bounds the search for the next business-day fire, as
// robfig/cron bounds its own search.
const calendarSearchYears = 5

// calendarSchedule fires on the candidates of a cron schedule that fall on
// business days of a calendar and, when business days are listed, on those
// positions in the month.
type calendarSchedule struct {
	base         cron.Schedule
	calendar     *Calendar
	businessDays []int
	// location is the zone a candidate's date is read in: the zone the cron
	// expression names, or the candidate's own zone when it names none.
	location *time.Location
}

var _ cron.Schedule = calendarSchedule{}

// Next returns the first fire after t.
func (s calendarSchedule) Next(t time.Time) time.Time {
	fire, _ := s.next(t)
	return fire
}

// next returns the first fire after t and the holidays it passed on which
// the cron expression alone would have fired. Holidays are reported only
// for schedules without business days, where every other business day
// fires.
func (s calendarSchedule) next(t time.Time) (time.Time, []Holiday) {
	var skipped []Holiday
	limit := t.AddDate(calendarSearchYears, 0, 0)
	candidate := s.base.Next(t)
	for !candidate.IsZero() && candidate.Before(limit) {
		day := s.dateOf(candidate)
		if s.keeps(day) {
			return candidate, skipped
		}
		if holiday, ok := s.calendar.Holiday(day); ok && len(s.businessDays) == 0 {
			skipped = append(skipped, holiday)
		}
		// Every candidate on a dropped date is dropped, so resume at the
		// start of the next date.
		candidate = s.base.Next(s.startOfDay(candidate, day.AddDays(1)).Add(-time.Second))
	}
	return time.Time{}, nil
}

// businessDay returns the listed business-day position a fire on day
// matched, or 0 when the schedule lists none.
func (s calendarSchedule) businessDay(day CivilDate) int {
	if len(s.businessDays) == 0 {
		return 0
	}
	fromStart, fromEnd := s.calendar.BusinessDayOrdinals(day)
	for _, want := range s.businessDays {
		if want == fromStart || want == fromEnd {
			return want
		}
	}
	return 0
}

func (s calendarSchedule) keeps(day CivilDate) bool {
	if !s.calendar.IsBusinessDay(day) {
		return false
	}
	if len(s.businessDays) == 0 {
		return true
	}
	fromStart, fromEnd := s.calendar.BusinessDayOrdinals(day)
	return slices.Contains(s.businessDays, fromStart) || slices.Contains(s.businessDays, fromEnd)
}

func (s calendarSchedule) dateOf(t time.Time) CivilDate {
	if s.location != nil {
		t = t.In(s.location)
	}
	return DateOf(t)
}

func (s calendarSchedule) startOfDay(reference time.Time, day CivilDate) time.Time {
	loc := s.location
	if loc == nil {
		loc = reference.Location()
	}
	return time.Date(day.Year, day.Month, day.Day, 0, 0, 0, 0, loc)
}
