// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package buscal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/core"
)

const testDefinition = `
timezone: Asia/Tokyo
holidays:
  - 2026-01-01
  - 2026-01-02
  - 2026-12-31
`

func mustParse(t *testing.T, name, def string) *Calendar {
	t.Helper()
	calendar, err := Parse(name, []byte(def))
	require.NoError(t, err)
	return calendar
}

func date(t *testing.T, value string, loc *time.Location) time.Time {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	require.NoError(t, err)
	return parsed
}

func TestParse(t *testing.T) {
	t.Parallel()

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		calendar := mustParse(t, "default", "")
		assert.True(t, calendar.IsWeekend(date(t, "2026-01-03 09:00", time.Local))) // Saturday
		assert.True(t, calendar.IsWeekend(date(t, "2026-01-04 09:00", time.Local))) // Sunday
		assert.False(t, calendar.IsWeekend(date(t, "2026-01-05 09:00", time.Local)))
		assert.False(t, calendar.IsHoliday(date(t, "2026-01-01 09:00", time.Local)))
	})

	t.Run("CustomWeekends", func(t *testing.T) {
		t.Parallel()
		calendar := mustParse(t, "mideast", "weekends: [friday, saturday]")
		assert.True(t, calendar.IsWeekend(date(t, "2026-01-02 09:00", time.Local)))  // Friday
		assert.False(t, calendar.IsWeekend(date(t, "2026-01-04 09:00", time.Local))) // Sunday
	})

	t.Run("EmptyWeekends", func(t *testing.T) {
		t.Parallel()
		calendar := mustParse(t, "no-weekend", "weekends: []")
		assert.False(t, calendar.IsWeekend(date(t, "2026-01-03 09:00", time.Local))) // Saturday
	})

	t.Run("InvalidWeekday", func(t *testing.T) {
		t.Parallel()
		_, err := Parse("bad", []byte("weekends: [caturday]"))
		assert.ErrorContains(t, err, "invalid weekend day")
	})

	t.Run("AllWeekend", func(t *testing.T) {
		t.Parallel()
		_, err := Parse("bad", []byte("weekends: [sunday, monday, tuesday, wednesday, thursday, friday, saturday]"))
		assert.ErrorContains(t, err, "every weekday as weekend")
	})

	t.Run("InvalidHolidayDate", func(t *testing.T) {
		t.Parallel()
		_, err := Parse("bad", []byte("holidays: [2026-13-01]"))
		assert.ErrorContains(t, err, "invalid holiday date")
	})

	t.Run("InvalidTimezone", func(t *testing.T) {
		t.Parallel()
		_, err := Parse("bad", []byte("timezone: Mars/Olympus"))
		assert.ErrorContains(t, err, "invalid timezone")
	})

	t.Run("UnknownKey", func(t *testing.T) {
		t.Parallel()
		_, err := Parse("bad", []byte("holydays: [2026-01-01]"))
		assert.Error(t, err)
	})

	t.Run("InvalidName", func(t *testing.T) {
		t.Parallel()
		_, err := Parse("../escape", []byte(""))
		assert.ErrorContains(t, err, "invalid calendar name")
	})
}

func TestCalendarTimezone(t *testing.T) {
	t.Parallel()

	calendar := mustParse(t, "jp-banking", testDefinition)
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)

	// 2026-01-01 08:00 in Tokyo is 2025-12-31 23:00 UTC. The holiday check
	// must interpret the instant in the calendar's zone, not the caller's.
	utcInstant := date(t, "2026-01-01 08:00", tokyo).UTC()
	assert.True(t, calendar.IsHoliday(utcInstant))

	// 2026-01-03 07:00 Tokyo (Saturday) is 2026-01-02 22:00 UTC (Friday).
	saturdayInTokyo := date(t, "2026-01-03 07:00", tokyo).UTC()
	assert.True(t, calendar.IsWeekend(saturdayInTokyo))
}

func TestEvaluate(t *testing.T) {
	t.Parallel()

	calendar := mustParse(t, "jp-banking", testDefinition)
	tokyo := calendar.Location

	t.Run("NoFilterSkipsHolidayOnly", func(t *testing.T) {
		t.Parallel()
		assert.True(t, calendar.Evaluate(date(t, "2026-01-01 01:00", tokyo), core.CalendarDayFilterNone).Skip)
		// Weekends pass without a filter: weekday selection belongs to cron.
		assert.False(t, calendar.Evaluate(date(t, "2026-01-03 01:00", tokyo), core.CalendarDayFilterNone).Skip)
		assert.False(t, calendar.Evaluate(date(t, "2026-01-05 01:00", tokyo), core.CalendarDayFilterNone).Skip)
	})

	t.Run("BusinessDays", func(t *testing.T) {
		t.Parallel()
		assert.True(t, calendar.Evaluate(date(t, "2026-01-01 01:00", tokyo), core.CalendarDayFilterBusinessDays).Skip)
		assert.True(t, calendar.Evaluate(date(t, "2026-01-03 01:00", tokyo), core.CalendarDayFilterBusinessDays).Skip)
		assert.False(t, calendar.Evaluate(date(t, "2026-01-05 01:00", tokyo), core.CalendarDayFilterBusinessDays).Skip)
	})

	t.Run("LastBusinessDay", func(t *testing.T) {
		t.Parallel()
		// 2026-12-31 is a holiday and 2026-12-27 is a Sunday, so the last
		// business day of December 2026 is Wednesday 2026-12-30.
		assert.False(t, calendar.Evaluate(date(t, "2026-12-30 01:00", tokyo), core.CalendarDayFilterLastBusinessDay).Skip)
		assert.True(t, calendar.Evaluate(date(t, "2026-12-31 01:00", tokyo), core.CalendarDayFilterLastBusinessDay).Skip)
		assert.True(t, calendar.Evaluate(date(t, "2026-12-29 01:00", tokyo), core.CalendarDayFilterLastBusinessDay).Skip)
	})

	t.Run("FirstBusinessDay", func(t *testing.T) {
		t.Parallel()
		// 2026-01-01 and 01-02 are holidays, 01-03/01-04 are the weekend, so
		// the first business day of January 2026 is Monday 2026-01-05.
		assert.False(t, calendar.Evaluate(date(t, "2026-01-05 01:00", tokyo), core.CalendarDayFilterFirstBusinessDay).Skip)
		assert.True(t, calendar.Evaluate(date(t, "2026-01-01 01:00", tokyo), core.CalendarDayFilterFirstBusinessDay).Skip)
		assert.True(t, calendar.Evaluate(date(t, "2026-01-06 01:00", tokyo), core.CalendarDayFilterFirstBusinessDay).Skip)
	})
}

func TestBusinessDayEdges(t *testing.T) {
	t.Parallel()

	calendar := mustParse(t, "jp-banking", testDefinition)
	tokyo := calendar.Location

	last, ok := calendar.LastBusinessDayOfMonth(date(t, "2026-12-15 00:00", tokyo))
	require.True(t, ok)
	assert.Equal(t, "2026-12-30", last.Format("2006-01-02"))

	first, ok := calendar.FirstBusinessDayOfMonth(date(t, "2026-01-15 00:00", tokyo))
	require.True(t, ok)
	assert.Equal(t, "2026-01-05", first.Format("2006-01-02"))

	// A month where every day is blocked yields no business day.
	noBusiness := mustParse(t, "shutdown", `
weekends: [sunday, monday, tuesday, wednesday, thursday, friday]
holidays:
  - 2026-02-07
  - 2026-02-14
  - 2026-02-21
  - 2026-02-28
`)
	_, ok = noBusiness.LastBusinessDayOfMonth(date(t, "2026-02-10 00:00", time.Local))
	assert.False(t, ok)
	_, ok = noBusiness.FirstBusinessDayOfMonth(date(t, "2026-02-10 00:00", time.Local))
	assert.False(t, ok)
}
