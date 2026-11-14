// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"slices"
	"time"
)

// DayStatus classifies a date of a calendar.
type DayStatus string

const (
	// DayBusiness is a business day by its weekday.
	DayBusiness DayStatus = "business"
	// DayNonBusiness is a weekday outside the calendar's business weekdays.
	DayNonBusiness DayStatus = "non_business"
	// DayHoliday is a listed holiday.
	DayHoliday DayStatus = "holiday"
	// DayExtraBusiness is a business day only because the calendar lists
	// it as one.
	DayExtraBusiness DayStatus = "extra_business_day"
)

// CalendarDay describes one date of a calendar.
type CalendarDay struct {
	Date   CivilDate
	Status DayStatus
	// HolidayName names the holiday listed on the date, which may be
	// unnamed. It is set even when the date is an extra business day.
	HolidayName string
	// FromStart and FromEnd give a business day's position in its month:
	// 1 is the first and -1 the last. Both are 0 on other days, and on
	// days that DiffCalendars reports.
	FromStart, FromEnd int
}

// CalendarChanges lists how one version of a calendar differs from another.
type CalendarChanges struct {
	WeekdaysAdded   []time.Weekday
	WeekdaysRemoved []time.Weekday
	// Dates lists, in date order, the listed holidays and extra business
	// days whose status or holiday name differs.
	Dates []DateChange
}

// DateChange is one date as two versions of a calendar describe it.
type DateChange struct {
	Before CalendarDay
	After  CalendarDay
}

// Month describes each date of the month, in order.
func (c *Calendar) Month(year int, month time.Month) []CalendarDay {
	var days []CalendarDay
	var businessDays []int
	for date := (CivilDate{Year: year, Month: month, Day: 1}); date.Month == month; date = date.AddDays(1) {
		day := c.day(date)
		if day.Status == DayBusiness || day.Status == DayExtraBusiness {
			businessDays = append(businessDays, len(days))
		}
		days = append(days, day)
	}
	for i, index := range businessDays {
		days[index].FromStart = i + 1
		days[index].FromEnd = i - len(businessDays)
	}
	return days
}

// DiffCalendars reports how after differs from before. A change of
// weekdays is reported as such rather than as the dates it changes.
func DiffCalendars(before, after *Calendar) CalendarChanges {
	var changes CalendarChanges
	for weekday := time.Sunday; weekday <= time.Saturday; weekday++ {
		switch {
		case after.Weekdays[weekday] && !before.Weekdays[weekday]:
			changes.WeekdaysAdded = append(changes.WeekdaysAdded, weekday)
		case before.Weekdays[weekday] && !after.Weekdays[weekday]:
			changes.WeekdaysRemoved = append(changes.WeekdaysRemoved, weekday)
		}
	}
	for _, date := range listedDates(before, after) {
		was, is := before.day(date), after.day(date)
		if was != is {
			changes.Dates = append(changes.Dates, DateChange{Before: was, After: is})
		}
	}
	return changes
}

// listedDates returns, sorted, the dates either calendar lists as a holiday
// or an extra business day.
func listedDates(calendars ...*Calendar) []CivilDate {
	seen := make(map[CivilDate]struct{})
	for _, cal := range calendars {
		for date := range cal.Holidays {
			seen[date] = struct{}{}
		}
		for date := range cal.ExtraBusinessDays {
			seen[date] = struct{}{}
		}
	}
	dates := make([]CivilDate, 0, len(seen))
	for date := range seen {
		dates = append(dates, date)
	}
	slices.SortFunc(dates, func(a, b CivilDate) int {
		return a.midnight().Compare(b.midnight())
	})
	return dates
}

// day describes date without its position among the month's business days.
func (c *Calendar) day(date CivilDate) CalendarDay {
	day := CalendarDay{Date: date}
	holidayName, holiday := c.Holidays[date]
	day.HolidayName = holidayName
	switch {
	case c.IsExtraBusinessDay(date):
		day.Status = DayExtraBusiness
	case c.IsBusinessDay(date):
		day.Status = DayBusiness
	case holiday:
		day.Status = DayHoliday
	default:
		day.Status = DayNonBusiness
	}
	return day
}
