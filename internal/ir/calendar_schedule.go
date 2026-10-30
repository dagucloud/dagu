// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"time"

	"github.com/robfig/cron/v3"
)

const (
	// calendarSearchYears bounds the search for the next business-day fire,
	// as robfig/cron bounds its own search.
	calendarSearchYears = 5
	// dstProbe is far enough past a skipped midnight to read the zone's
	// offset after the jump.
	dstProbe = 3 * time.Hour
)

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

// next returns the first fire after t, in t's location, and the holidays
// it passed on which the cron expression alone would have fired. Holidays
// are reported only for schedules without business days, where every other
// business day fires.
func (s calendarSchedule) next(t time.Time) (time.Time, []Holiday) {
	var (
		skipped []Holiday
		month   monthDays
	)
	limit := t.AddDate(calendarSearchYears, 0, 0)
	candidate := s.base.Next(t)
	for !candidate.IsZero() && candidate.Before(limit) {
		day := s.dateOf(candidate)
		// Every candidate on a dropped date is dropped, so the search resumes
		// at the next date that can fire.
		var resume CivilDate
		if len(s.businessDays) == 0 {
			if s.calendar.IsBusinessDay(day) {
				return candidate.In(t.Location()), skipped
			}
			if holiday, ok := s.calendar.Holiday(day); ok {
				skipped = append(skipped, holiday)
			}
			resume = day.AddDays(1)
		} else {
			if !month.covers(day) {
				month = s.matchingDays(day)
			}
			if month.allowed[day.Day] {
				return candidate.In(t.Location()), skipped
			}
			resume = month.after(day)
		}
		candidate = s.firstCandidateFrom(candidate, resume)
	}
	return time.Time{}, nil
}

// firstCandidateFrom returns the first candidate on or after day, a date
// after the date of candidate.
func (s calendarSchedule) firstCandidateFrom(candidate time.Time, day CivilDate) time.Time {
	next := s.base.Next(s.startOfDay(candidate, day).Add(-time.Second))
	if !next.After(candidate) {
		// Defensive: a jump that does not advance falls back to the next
		// candidate, so the search always ends.
		next = s.base.Next(candidate)
	}
	return next
}

// monthDays marks the days of one month on which a schedule with business
// days fires.
type monthDays struct {
	year  int
	month time.Month
	// allowed is indexed by day of the month.
	allowed [32]bool
}

func (m monthDays) covers(day CivilDate) bool {
	return m.year == day.Year && m.month == day.Month
}

// after returns the first allowed day after day in the month, or the first
// day of the next month.
func (m monthDays) after(day CivilDate) CivilDate {
	for d := day.Day + 1; d < len(m.allowed); d++ {
		if m.allowed[d] {
			return CivilDate{Year: m.year, Month: m.month, Day: d}
		}
	}
	return DateOf(time.Date(m.year, m.month+1, 1, 0, 0, 0, 0, time.UTC))
}

// matchingDays lists the business days of day's month in order once, then
// marks those at the listed positions.
func (s calendarSchedule) matchingDays(day CivilDate) monthDays {
	m := monthDays{year: day.Year, month: day.Month}
	var business [31]int
	count := 0
	for d := (CivilDate{Year: day.Year, Month: day.Month, Day: 1}); d.Month == day.Month; d = d.AddDays(1) {
		if s.calendar.IsBusinessDay(d) {
			business[count] = d.Day
			count++
		}
	}
	for _, position := range s.businessDays {
		index := position - 1
		if position < 0 {
			index = count + position
		}
		if index >= 0 && index < count {
			m.allowed[business[index]] = true
		}
	}
	return m
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

func (s calendarSchedule) dateOf(t time.Time) CivilDate {
	if s.location != nil {
		t = t.In(s.location)
	}
	return DateOf(t)
}

// startOfDay returns the first instant of day in the zone dates are read
// in. When daylight saving skips midnight, time.Date normalizes it back to
// the previous day, so the result moves forward by the skipped interval.
func (s calendarSchedule) startOfDay(reference time.Time, day CivilDate) time.Time {
	loc := s.location
	if loc == nil {
		loc = reference.Location()
	}
	start := time.Date(day.Year, day.Month, day.Day, 0, 0, 0, 0, loc)
	if DateOf(start).Before(day) {
		_, before := start.Zone()
		_, after := start.Add(dstProbe).Zone()
		start = start.Add(time.Duration(after-before) * time.Second)
	}
	return start
}
