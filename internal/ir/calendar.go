// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"fmt"
	"regexp"
	"time"
)

const (
	// CalendarNone opts a schedule out of its DAG's default calendar.
	CalendarNone = "none"
	// CalendarWeekdays names the built-in calendar of Monday to Friday with
	// no holidays.
	CalendarWeekdays = "weekdays"
)

// calendarNameRe matches calendar names, which are also file names.
var calendarNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidateCalendarName reports whether name can name a calendar.
func ValidateCalendarName(name string) error {
	if !calendarNameRe.MatchString(name) {
		return fmt.Errorf("invalid calendar name %q: use letters, digits, '-' and '_'", name)
	}
	return nil
}

// CivilDate is a calendar date without a time of day or zone.
type CivilDate struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the date of t in t's location.
func DateOf(t time.Time) CivilDate {
	year, month, day := t.Date()
	return CivilDate{Year: year, Month: month, Day: day}
}

// ParseCivilDate parses a date in YYYY-MM-DD form.
func ParseCivilDate(value string) (CivilDate, error) {
	t, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return CivilDate{}, fmt.Errorf("invalid date %q: use YYYY-MM-DD", value)
	}
	return DateOf(t), nil
}

// String returns the date in YYYY-MM-DD form.
func (d CivilDate) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// IsZero reports whether d is the zero date.
func (d CivilDate) IsZero() bool {
	return d == CivilDate{}
}

// Before reports whether d is earlier than other.
func (d CivilDate) Before(other CivilDate) bool {
	return d.midnight().Before(other.midnight())
}

// AddDays returns the date n days after d.
func (d CivilDate) AddDays(n int) CivilDate {
	return DateOf(time.Date(d.Year, d.Month, d.Day+n, 0, 0, 0, 0, time.UTC))
}

// Weekday returns the day of the week of d.
func (d CivilDate) Weekday() time.Weekday {
	return d.midnight().Weekday()
}

func (d CivilDate) midnight() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// Holiday is a named date that is not a business day.
type Holiday struct {
	Date CivilDate
	// Name may be empty.
	Name string
}

// Calendar is a named set of business days.
type Calendar struct {
	Name        string
	Description string
	// Weekdays lists, by time.Weekday, the days of the week that are
	// business days unless they are holidays.
	Weekdays [7]bool
	// Holidays maps each holiday to its name, which may be empty.
	Holidays map[CivilDate]string
	// ExtraBusinessDays are business days whatever their weekday, even when
	// they are also holidays.
	ExtraBusinessDays map[CivilDate]struct{}
	// Through is the last date the holidays are maintained for. It is zero
	// when the calendar does not say.
	Through CivilDate
	// Digest identifies the calendar's contents.
	Digest string
}

// BuiltinCalendar returns the built-in calendar with the given name.
func BuiltinCalendar(name string) (*Calendar, bool) {
	if name != CalendarWeekdays {
		return nil, false
	}
	cal := &Calendar{
		Name:        CalendarWeekdays,
		Description: "Monday to Friday, no holidays",
		Digest:      "builtin:" + CalendarWeekdays,
	}
	for day := time.Monday; day <= time.Friday; day++ {
		cal.Weekdays[day] = true
	}
	return cal, true
}

// IsBusinessDay reports whether d is a business day.
func (c *Calendar) IsBusinessDay(d CivilDate) bool {
	if _, ok := c.ExtraBusinessDays[d]; ok {
		return true
	}
	if _, ok := c.Holidays[d]; ok {
		return false
	}
	return c.Weekdays[d.Weekday()]
}

// Holiday returns the holiday the calendar lists on d, if it lists one. An
// extra business day on the same date still makes d a business day.
func (c *Calendar) Holiday(d CivilDate) (Holiday, bool) {
	name, ok := c.Holidays[d]
	return Holiday{Date: d, Name: name}, ok
}

// IsExtraBusinessDay reports whether d is a business day only because the
// calendar lists it as one.
func (c *Calendar) IsExtraBusinessDay(d CivilDate) bool {
	if _, ok := c.ExtraBusinessDays[d]; !ok {
		return false
	}
	_, holiday := c.Holidays[d]
	return holiday || !c.Weekdays[d.Weekday()]
}

// BusinessDayOrdinals returns the position of business day d among the
// business days of its month, counted from the start, where 1 is the first,
// and from the end, where -1 is the last. Both are 0 when d is not a
// business day.
func (c *Calendar) BusinessDayOrdinals(d CivilDate) (fromStart, fromEnd int) {
	if !c.IsBusinessDay(d) {
		return 0, 0
	}
	first := CivilDate{Year: d.Year, Month: d.Month, Day: 1}
	for day := first; day.Month == d.Month; day = day.AddDays(1) {
		if !c.IsBusinessDay(day) {
			continue
		}
		switch {
		case day.Before(d):
			fromStart++
		case d.Before(day):
			fromEnd--
		}
	}
	return fromStart + 1, fromEnd - 1
}
