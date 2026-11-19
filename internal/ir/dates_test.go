// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveDates(t *testing.T) {
	t.Parallel()

	tokyo := mustZone(t, "Asia/Tokyo")
	newYork := mustZone(t, "America/New_York")
	weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
	withHoliday := *weekdays
	withHoliday.Name = "jp"
	withHoliday.Holidays = map[ir.CivilDate]string{{Year: 2026, Month: time.November, Day: 23}: "Labor Thanksgiving Day"}

	// slotContext is the context of a daily 02:00 Tokyo slot whose window
	// starts at the previous fire.
	slotContext := func(slot, previous time.Time) ir.DateContext {
		return ir.DateContext{
			ScheduledAt: slot,
			Window:      ir.ScheduleWindow{Start: previous, Next: slot.AddDate(0, 0, 1), Timezone: "Asia/Tokyo"},
			Location:    time.UTC,
			Calendar:    &withHoliday,
		}
	}
	tuesday := time.Date(2026, 11, 10, 2, 0, 0, 0, tokyo)
	monday := time.Date(2026, 11, 9, 2, 0, 0, 0, tokyo)
	friday := time.Date(2026, 11, 6, 2, 0, 0, 0, tokyo)

	tests := []struct {
		name  string
		specs []ir.DateSpec
		dc    ir.DateContext
		want  []ir.DateValue
	}{
		{
			name: "RFC example",
			specs: []ir.DateSpec{
				{Name: "target_day", Shift: []string{"-1d"}},
				{Name: "target_compact", From: "target_day", Format: "date_compact"},
				{Name: "month_start", Shift: []string{"-1d", "month_start"}},
				{Name: "prev_month_end", Shift: []string{"month_start", "-1d"}},
				{Name: "last_open_day", Shift: []string{"-1bd"}},
				{Name: "window_start", From: "interval_start", Format: "rfc3339"},
				{Name: "window_days", Each: "1d"},
				{Name: "loaded_at", Format: "%Y/%m/%d %H:%M", Timezone: "UTC"},
			},
			dc: slotContext(tuesday, monday),
			want: []ir.DateValue{
				{Name: "target_day", Value: "2026-11-09"},
				{Name: "target_compact", Value: "20261109"},
				{Name: "month_start", Value: "2026-11-01"},
				{Name: "prev_month_end", Value: "2026-10-31"},
				{Name: "last_open_day", Value: "2026-11-09"},
				{Name: "window_start", Value: "2026-11-09T02:00:00+09:00"},
				{Name: "window_days", List: true, Values: []string{"2026-11-09"}},
				{Name: "loaded_at", Value: "2026/11/09 17:00"},
			},
		},
		{
			name: "Monday's window covers the weekend",
			specs: []ir.DateSpec{
				{Name: "target_day", Shift: []string{"-1d"}},
				{Name: "window_days", Each: "1d"},
				{Name: "open_days", Each: "1bd"},
			},
			dc: slotContext(monday, friday),
			want: []ir.DateValue{
				{Name: "target_day", Value: "2026-11-08"},
				{Name: "window_days", List: true, Values: []string{"2026-11-06", "2026-11-07", "2026-11-08"}},
				{Name: "open_days", List: true, Values: []string{"2026-11-06"}},
			},
		},
		{
			name: "business days skip holidays",
			specs: []ir.DateSpec{
				{Name: "or_prev", Shift: []string{"bd_or_prev"}},
				{Name: "or_next", Shift: []string{"bd_or_next"}},
				{Name: "after_friday", From: "or_prev", Shift: []string{"+1bd"}},
			},
			dc: slotContext(time.Date(2026, 11, 23, 2, 0, 0, 0, tokyo), time.Date(2026, 11, 20, 2, 0, 0, 0, tokyo)),
			want: []ir.DateValue{
				{Name: "or_prev", Value: "2026-11-20"},
				{Name: "or_next", Value: "2026-11-24"},
				{Name: "after_friday", Value: "2026-11-24"},
			},
		},
		{
			name: "month ends on a weekday calendar",
			specs: []ir.DateSpec{
				{Name: "prev_month_end", Shift: []string{"month_start", "-1d"}},
				{Name: "last_open_day", Shift: []string{"month_start", "+1mo", "-1d", "bd_or_prev"}},
			},
			dc: ir.DateContext{ScheduledAt: time.Date(2026, 3, 15, 2, 0, 0, 0, time.UTC), Location: time.UTC, Calendar: weekdays},
			want: []ir.DateValue{
				{Name: "prev_month_end", Value: "2026-02-28"},
				{Name: "last_open_day", Value: "2026-03-31"},
			},
		},
		{
			name: "the business day before a Monday is Friday",
			specs: []ir.DateSpec{
				{Name: "last_open_day", Shift: []string{"-1bd"}},
				{Name: "month_last_open_day", Shift: []string{"month_start", "+1mo", "-1d", "bd_or_prev"}, From: "may"},
				{Name: "may", Shift: []string{"+4d"}},
			},
			dc: ir.DateContext{ScheduledAt: time.Date(2026, 5, 11, 2, 0, 0, 0, time.UTC), Location: time.UTC, Calendar: weekdays},
			want: []ir.DateValue{
				{Name: "last_open_day", Value: "2026-05-08"},
				{Name: "month_last_open_day", Value: "2026-05-29"},
				{Name: "may", Value: "2026-05-15"},
			},
		},
		{
			name: "a month shift clamps to a shorter month",
			specs: []ir.DateSpec{
				{Name: "prev_month", Shift: []string{"-1mo"}},
				{Name: "next_year", Shift: []string{"+1y"}},
			},
			dc: ir.DateContext{ScheduledAt: time.Date(2028, 3, 31, 0, 0, 0, 0, time.UTC), Location: time.UTC},
			want: []ir.DateValue{
				{Name: "prev_month", Value: "2028-02-29"},
				{Name: "next_year", Value: "2029-03-31"},
			},
		},
		{
			name: "a day shift keeps the wall-clock time across daylight saving",
			specs: []ir.DateSpec{
				{Name: "day_before", Shift: []string{"-1d"}, Format: "datetime"},
				{Name: "hours_before", Shift: []string{"-24h"}, Format: "datetime"},
			},
			dc: ir.DateContext{
				ScheduledAt: time.Date(2026, 3, 9, 1, 30, 0, 0, newYork),
				Window:      ir.ScheduleWindow{Timezone: "America/New_York"},
			},
			want: []ir.DateValue{
				{Name: "day_before", Value: "2026-03-08 01:30:00"},
				{Name: "hours_before", Value: "2026-03-08 00:30:00"},
			},
		},
		{
			name: "units start in the zone, and weeks start on Monday",
			specs: []ir.DateSpec{
				{Name: "week", Shift: []string{"week_start"}, Format: "datetime"},
				{Name: "hour", Shift: []string{"hour_start"}, Format: "datetime"},
				{Name: "year", Shift: []string{"year_start"}},
			},
			dc: ir.DateContext{ScheduledAt: time.Date(2026, 11, 15, 9, 45, 0, 0, time.UTC), Location: time.UTC},
			want: []ir.DateValue{
				{Name: "week", Value: "2026-11-09 00:00:00"},
				{Name: "hour", Value: "2026-11-15 09:00:00"},
				{Name: "year", Value: "2026-01-01"},
			},
		},
		{
			name: "an entry takes the zone, not the format, of the entry it builds on",
			specs: []ir.DateSpec{
				{Name: "utc", Format: "datetime", Timezone: "UTC"},
				{Name: "utc_day", From: "utc"},
				{Name: "tokyo_day", From: "utc", Timezone: "Asia/Tokyo"},
			},
			dc: slotContext(time.Date(2026, 11, 10, 8, 0, 0, 0, tokyo), monday),
			want: []ir.DateValue{
				{Name: "utc", Value: "2026-11-09 23:00:00"},
				{Name: "utc_day", Value: "2026-11-09"},
				{Name: "tokyo_day", Value: "2026-11-10"},
			},
		},
		{
			name: "a run without a slot uses the minute it was created",
			specs: []ir.DateSpec{
				{Name: "today", Format: "datetime"},
				{Name: "end", From: "interval_end", Shift: []string{"-1d"}},
			},
			dc: ir.DateContext{CreatedAt: time.Date(2026, 11, 12, 10, 15, 42, 0, time.UTC), Location: time.UTC},
			want: []ir.DateValue{
				{Name: "today", Value: "2026-11-12 10:15:00"},
				{Name: "end", Value: "2026-11-11"},
			},
		},
		{
			name: "named and strftime formats",
			specs: []ir.DateSpec{
				{Name: "unix", Format: "unix"},
				{Name: "ordinal", Format: "%j %u %w %V %G %%"},
				{Name: "next", From: "next_scheduled_at", Format: "%F %T %Z"},
			},
			dc: slotContext(tuesday, monday),
			want: []ir.DateValue{
				{Name: "unix", Value: "1794243600"},
				{Name: "ordinal", Value: "314 2 2 46 2026 %"},
				{Name: "next", Value: "2026-11-11 02:00:00 JST"},
			},
		},
		{
			name: "a window bound by a zone offset",
			specs: []ir.DateSpec{
				{Name: "slot", Format: "datetime"},
			},
			dc: ir.DateContext{
				ScheduledAt: time.Date(2026, 11, 10, 0, 30, 0, 0, time.UTC),
				Window:      ir.ScheduleWindow{Timezone: "UTC+09:00"},
			},
			want: []ir.DateValue{
				{Name: "slot", Value: "2026-11-10 09:30:00"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Empty(t, ir.ValidateDates(tt.specs))
			got, err := ir.ResolveDates(tt.specs, tt.dc)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveDatesFails(t *testing.T) {
	t.Parallel()

	tokyo := mustZone(t, "Asia/Tokyo")
	slot := time.Date(2026, 11, 10, 2, 0, 0, 0, tokyo)
	daily := ir.DateContext{
		ScheduledAt: slot,
		Window:      ir.ScheduleWindow{Start: slot.AddDate(0, 0, -1), Timezone: "Asia/Tokyo"},
	}
	noSlot := ir.DateContext{CreatedAt: slot, Location: tokyo}

	tests := []struct {
		name string
		spec ir.DateSpec
		dc   ir.DateContext
		want string
	}{
		{
			name: "a window date on a run without a slot",
			spec: ir.DateSpec{Name: "since", From: "interval_start"},
			dc:   noSlot,
			want: "dates.since: interval_start needs the window of a schedule slot, and the run has no slot",
		},
		{
			name: "a list on a run without a slot",
			spec: ir.DateSpec{Name: "days", Each: "1d"},
			dc:   noSlot,
			want: "dates.days: interval_start needs the window of a schedule slot",
		},
		{
			name: "business days without a calendar",
			spec: ir.DateSpec{Name: "open", Shift: []string{"-1bd"}},
			dc:   daily,
			want: "dates.open: shift -1bd: it uses business days, and the run has no calendar",
		},
		{
			name: "a slot the schedule fires nothing after",
			spec: ir.DateSpec{Name: "next", From: "next_scheduled_at"},
			dc:   daily,
			want: "dates.next: the schedule fires no time after the slot",
		},
		{
			name: "a list longer than the parallel step takes",
			spec: ir.DateSpec{Name: "minutes", Each: "1min", Format: "datetime"},
			dc:   daily,
			want: "dates.minutes: each 1min gives more than 1000 dates",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			specs := []ir.DateSpec{tt.spec}
			require.Empty(t, ir.ValidateDates(specs))
			_, err := ir.ResolveDates(specs, tt.dc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestValidateDates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		specs []ir.DateSpec
		want  string
	}{
		{
			name:  "the unit m is neither minutes nor months",
			specs: []ir.DateSpec{{Name: "a", Shift: []string{"-1m"}}},
			want:  `dates.a: shift: unknown unit "m" in "-1m": use min for minutes or mo for months`,
		},
		{
			name:  "a shift needs a sign",
			specs: []ir.DateSpec{{Name: "a", Shift: []string{"1d"}}},
			want:  `dates.a: shift: shift "1d" needs a sign, such as +1d or -1d`,
		},
		{
			name:  "unknown operation",
			specs: []ir.DateSpec{{Name: "a", Shift: []string{"month_end"}}},
			want:  `dates.a: shift: unknown operation "month_end"`,
		},
		{
			name:  "an amount over the limit",
			specs: []ir.DateSpec{{Name: "a", Shift: []string{"-10001d"}}},
			want:  `amount in "-10001d" must be at most 10000`,
		},
		{
			name:  "a step has no sign",
			specs: []ir.DateSpec{{Name: "a", Each: "+1d"}},
			want:  `dates.a: each: step "+1d" takes no sign: use 1d`,
		},
		{
			name:  "a step under a day needs a format",
			specs: []ir.DateSpec{{Name: "a", Each: "2h"}},
			want:  `dates.a: each: a step of "2h" repeats dates in the default format: set format`,
		},
		{
			name:  "a list cannot start from another instant",
			specs: []ir.DateSpec{{Name: "a", Each: "1d", From: "scheduled_at"}},
			want:  "dates.a: from: cannot be combined with each",
		},
		{
			name:  "unknown from",
			specs: []ir.DateSpec{{Name: "a", From: "yesterday"}},
			want:  `dates.a: from: unknown "yesterday"`,
		},
		{
			name: "from a list",
			specs: []ir.DateSpec{
				{Name: "days", Each: "1d"},
				{Name: "a", From: "days"},
			},
			want: `dates.a: from: "days" is a list`,
		},
		{
			name: "a cycle",
			specs: []ir.DateSpec{
				{Name: "a", From: "b"},
				{Name: "b", From: "a"},
			},
			want: "dates.a: entries build on each other in a cycle: a -> b -> a",
		},
		{
			name:  "a reserved name",
			specs: []ir.DateSpec{{Name: "interval_start"}},
			want:  "dates.interval_start: the name is reserved for an instant of the run",
		},
		{
			name:  "an invalid name",
			specs: []ir.DateSpec{{Name: "1st"}},
			want:  "dates.1st: invalid name",
		},
		{
			name:  "a format that is neither named nor strftime",
			specs: []ir.DateSpec{{Name: "a", Format: "YYYYMMDD"}},
			want:  `dates.a: format: unknown format "YYYYMMDD"`,
		},
		{
			name:  "an unknown strftime directive",
			specs: []ir.DateSpec{{Name: "a", Format: "%Y%Q"}},
			want:  `dates.a: format: unknown directive %Q in "%Y%Q"`,
		},
		{
			name:  "an unknown zone",
			specs: []ir.DateSpec{{Name: "a", Timezone: "Mars/Olympus"}},
			want:  `dates.a: timezone: unknown zone "Mars/Olympus"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := ir.ValidateDates(tt.specs)
			require.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), tt.want)
		})
	}
}

func TestDatesNeedingWindowAndBusinessDays(t *testing.T) {
	t.Parallel()

	specs := []ir.DateSpec{
		{Name: "day", Shift: []string{"-1d"}},
		{Name: "since", From: "interval_start"},
		{Name: "since_day", From: "since", Shift: []string{"day_start"}},
		{Name: "days", Each: "1bd"},
		{Name: "open", Shift: []string{"bd_or_prev"}},
	}
	assert.Equal(t, []string{"since", "since_day", "days"}, ir.DatesNeedingWindow(specs))
	assert.Equal(t, []string{"days", "open"}, ir.DatesUsingBusinessDays(specs))
}

func TestDateValueText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "2026-11-09", ir.DateValue{Name: "a", Value: "2026-11-09"}.Text())
	assert.Equal(t, `["2026-11-06","2026-11-07"]`,
		ir.DateValue{Name: "a", List: true, Values: []string{"2026-11-06", "2026-11-07"}}.Text())
	assert.Equal(t, "[]", ir.DateValue{Name: "a", List: true}.Text())
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}
