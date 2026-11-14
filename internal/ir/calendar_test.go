// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"encoding/json"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jpBanking is a small Japanese bank calendar for January to October 2027.
func jpBanking() *ir.Calendar {
	cal := &ir.Calendar{
		Name: "jp-banking",
		Holidays: map[ir.CivilDate]string{
			date(2027, 1, 1):  "",
			date(2027, 1, 11): "Coming of Age Day",
		},
		ExtraBusinessDays: map[ir.CivilDate]struct{}{
			date(2027, 10, 9): {},
		},
	}
	for day := time.Monday; day <= time.Friday; day++ {
		cal.Weekdays[day] = true
	}
	return cal
}

func date(year int, month time.Month, day int) ir.CivilDate {
	return ir.CivilDate{Year: year, Month: month, Day: day}
}

func boundSchedule(t *testing.T, cal *ir.Calendar, entry map[string]any) ir.Schedule {
	t.Helper()
	entry["calendar"] = cal.Name
	schedule, err := ir.ParseScheduleValue(entry, ir.ScheduleParseOptions{AllowAt: true, AllowCalendar: true})
	require.NoError(t, err)
	require.NoError(t, schedule.BindCalendar(cal))
	return schedule
}

func fireTimes(fires []ir.ScheduleFire) []time.Time {
	times := make([]time.Time, len(fires))
	for i, fire := range fires {
		times[i] = fire.Time
	}
	return times
}

func TestCalendarBusinessDays(t *testing.T) {
	t.Parallel()

	cal := jpBanking()

	assert.False(t, cal.IsBusinessDay(date(2027, 1, 1)), "holiday")
	assert.False(t, cal.IsBusinessDay(date(2027, 1, 2)), "Saturday")
	assert.True(t, cal.IsBusinessDay(date(2027, 1, 4)), "Monday")
	assert.True(t, cal.IsBusinessDay(date(2027, 10, 9)), "extra business day on a Saturday")
	assert.True(t, cal.IsExtraBusinessDay(date(2027, 10, 9)))
	assert.False(t, cal.IsExtraBusinessDay(date(2027, 1, 4)))

	holiday, ok := cal.Holiday(date(2027, 1, 11))
	require.True(t, ok)
	assert.Equal(t, "Coming of Age Day", holiday.Name)

	// January 2027 starts on the Friday holiday, so the first business day
	// is Monday the 4th, and Friday the 29th is the last.
	fromStart, fromEnd := cal.BusinessDayOrdinals(date(2027, 1, 4))
	assert.Equal(t, 1, fromStart)
	assert.Equal(t, -19, fromEnd)
	fromStart, fromEnd = cal.BusinessDayOrdinals(date(2027, 1, 29))
	assert.Equal(t, 19, fromStart)
	assert.Equal(t, -1, fromEnd)
	fromStart, fromEnd = cal.BusinessDayOrdinals(date(2027, 1, 30))
	assert.Zero(t, fromStart)
	assert.Zero(t, fromEnd)
}

func TestCalendarMonth(t *testing.T) {
	t.Parallel()

	cal := jpBanking()
	cal.Holidays[date(2027, 1, 3)] = "Sunday holiday"
	cal.Holidays[date(2027, 10, 11)] = "Sports Day"
	cal.ExtraBusinessDays[date(2027, 10, 11)] = struct{}{}

	january := cal.Month(2027, time.January)
	require.Len(t, january, 31)
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 1, 1), Status: ir.DayHoliday}, january[0])
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 1, 2), Status: ir.DayNonBusiness}, january[1])
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 1, 3), Status: ir.DayHoliday, HolidayName: "Sunday holiday"}, january[2])
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 1, 4), Status: ir.DayBusiness, FromStart: 1, FromEnd: -19}, january[3])
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 1, 11), Status: ir.DayHoliday, HolidayName: "Coming of Age Day"}, january[10])
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 1, 29), Status: ir.DayBusiness, FromStart: 19, FromEnd: -1}, january[28])

	// October 2027 has 22 business days, counting the extra business days
	// on Saturday the 9th and on the holiday of Monday the 11th.
	october := cal.Month(2027, time.October)
	require.Len(t, october, 31)
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 10, 9), Status: ir.DayExtraBusiness, FromStart: 7, FromEnd: -16}, october[8])
	assert.Equal(t, ir.CalendarDay{Date: date(2027, 10, 11), Status: ir.DayExtraBusiness, HolidayName: "Sports Day", FromStart: 8, FromEnd: -15}, october[10])

	assert.Len(t, cal.Month(2028, time.February), 29, "leap year")
}

func TestDiffCalendars(t *testing.T) {
	t.Parallel()

	before := jpBanking()
	assert.Empty(t, ir.DiffCalendars(before, jpBanking()))

	after := jpBanking()
	after.Holidays[date(2027, 1, 1)] = "New Year's Day"
	delete(after.Holidays, date(2027, 1, 11))
	after.Holidays[date(2027, 3, 22)] = "Vernal Equinox Day"
	after.Weekdays[time.Saturday] = true
	after.Weekdays[time.Friday] = false

	changes := ir.DiffCalendars(before, after)
	assert.Equal(t, []time.Weekday{time.Saturday}, changes.WeekdaysAdded)
	assert.Equal(t, []time.Weekday{time.Friday}, changes.WeekdaysRemoved)
	assert.Equal(t, []ir.DateChange{
		{
			Before: ir.CalendarDay{Date: date(2027, 1, 1), Status: ir.DayHoliday},
			After:  ir.CalendarDay{Date: date(2027, 1, 1), Status: ir.DayHoliday, HolidayName: "New Year's Day"},
		},
		{
			Before: ir.CalendarDay{Date: date(2027, 1, 11), Status: ir.DayHoliday, HolidayName: "Coming of Age Day"},
			After:  ir.CalendarDay{Date: date(2027, 1, 11), Status: ir.DayBusiness},
		},
		{
			Before: ir.CalendarDay{Date: date(2027, 3, 22), Status: ir.DayBusiness},
			After:  ir.CalendarDay{Date: date(2027, 3, 22), Status: ir.DayHoliday, HolidayName: "Vernal Equinox Day"},
		},
		// A business day either way, but no longer only because it is listed.
		{
			Before: ir.CalendarDay{Date: date(2027, 10, 9), Status: ir.DayExtraBusiness},
			After:  ir.CalendarDay{Date: date(2027, 10, 9), Status: ir.DayBusiness},
		},
	}, changes.Dates)
}

func TestBuiltinWeekdaysCalendar(t *testing.T) {
	t.Parallel()

	cal, ok := ir.BuiltinCalendar(ir.CalendarWeekdays)
	require.True(t, ok)
	assert.True(t, cal.IsBusinessDay(date(2027, 1, 1)), "no holidays")
	assert.False(t, cal.IsBusinessDay(date(2027, 1, 2)))

	_, ok = ir.BuiltinCalendar("jp-banking")
	assert.False(t, ok)
}

func TestCalendarSchedule(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)

	t.Run("skips weekends and holidays", func(t *testing.T) {
		t.Parallel()

		schedule := boundSchedule(t, jpBanking(), map[string]any{"expression": "0 18 * * *"})
		dag := &ir.DAG{Schedule: []ir.Schedule{schedule}}

		fires := dag.UpcomingFires(from, 6)

		assert.Equal(t, []time.Time{
			time.Date(2027, 1, 4, 18, 0, 0, 0, time.UTC),
			time.Date(2027, 1, 5, 18, 0, 0, 0, time.UTC),
			time.Date(2027, 1, 6, 18, 0, 0, 0, time.UTC),
			time.Date(2027, 1, 7, 18, 0, 0, 0, time.UTC),
			time.Date(2027, 1, 8, 18, 0, 0, 0, time.UTC),
			time.Date(2027, 1, 12, 18, 0, 0, 0, time.UTC),
		}, fireTimes(fires))
		assert.Equal(t, []ir.Holiday{{Date: date(2027, 1, 1)}}, fires[0].SkippedHolidays)
		assert.Equal(t, []ir.Holiday{{Date: date(2027, 1, 11), Name: "Coming of Age Day"}}, fires[5].SkippedHolidays)
		assert.Empty(t, fires[1].SkippedHolidays)
	})

	t.Run("fires on an extra business day", func(t *testing.T) {
		t.Parallel()

		schedule := boundSchedule(t, jpBanking(), map[string]any{"expression": "0 18 * * *"})
		dag := &ir.DAG{Schedule: []ir.Schedule{schedule}}

		fires := dag.UpcomingFires(time.Date(2027, 10, 8, 19, 0, 0, 0, time.UTC), 1)

		require.Len(t, fires, 1)
		assert.Equal(t, time.Date(2027, 10, 9, 18, 0, 0, 0, time.UTC), fires[0].Time)
		assert.True(t, fires[0].ExtraBusinessDay)
	})

	t.Run("keeps the listed business days of each month", func(t *testing.T) {
		t.Parallel()

		schedule := boundSchedule(t, jpBanking(), map[string]any{
			"expression":    "0 23 * * *",
			"business_days": []any{3, -1},
		})
		dag := &ir.DAG{Schedule: []ir.Schedule{schedule}}

		fires := dag.UpcomingFires(from, 4)

		assert.Equal(t, []time.Time{
			time.Date(2027, 1, 6, 23, 0, 0, 0, time.UTC),
			time.Date(2027, 1, 29, 23, 0, 0, 0, time.UTC),
			time.Date(2027, 2, 3, 23, 0, 0, 0, time.UTC),
			time.Date(2027, 2, 26, 23, 0, 0, 0, time.UTC),
		}, fireTimes(fires))
		assert.Equal(t, []int{3, -1, 3, -1}, []int{
			fires[0].BusinessDay, fires[1].BusinessDay, fires[2].BusinessDay, fires[3].BusinessDay,
		})
		// Holidays before the third business day are not skipped fires.
		assert.Empty(t, fires[0].SkippedHolidays)
	})

	t.Run("counts from the end of a leap February", func(t *testing.T) {
		t.Parallel()

		weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
		schedule := boundSchedule(t, weekdays, map[string]any{
			"expression":    "0 9 * * *",
			"business_days": []any{-1},
		})

		next := schedule.Next(time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC))

		assert.Equal(t, time.Date(2028, 2, 29, 9, 0, 0, 0, time.UTC), next)
	})

	// 01:00 on Monday 11 January in Tokyo is still Sunday in UTC; the date
	// that counts is the one in the zone the cron expression names.
	t.Run("reads dates in the schedule zone", func(t *testing.T) {
		t.Parallel()

		tokyo, err := time.LoadLocation("Asia/Tokyo")
		require.NoError(t, err)
		schedule := boundSchedule(t, jpBanking(), map[string]any{"expression": "CRON_TZ=Asia/Tokyo 0 1 * * *"})

		next := schedule.Next(time.Date(2027, 1, 10, 12, 0, 0, 0, time.UTC))

		assert.True(t, next.Equal(time.Date(2027, 1, 12, 1, 0, 0, 0, tokyo)), next)
	})

	// Sunday 5 September 2027 starts with a daylight saving jump in
	// Santiago, so its midnight does not exist; the search must still move
	// past the rejected Saturday.
	t.Run("crosses a midnight daylight saving jump", func(t *testing.T) {
		t.Parallel()

		santiago, err := time.LoadLocation("America/Santiago")
		require.NoError(t, err)
		weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
		schedule := boundSchedule(t, weekdays, map[string]any{"expression": "CRON_TZ=America/Santiago * * * * *"})

		next := make(chan time.Time, 1)
		go func() { next <- schedule.Next(time.Date(2027, 9, 3, 23, 59, 0, 0, santiago)) }()
		select {
		case got := <-next:
			assert.True(t, got.Equal(time.Date(2027, 9, 6, 0, 0, 0, 0, santiago)), got)
		case <-time.After(5 * time.Second):
			t.Fatal("Next did not return")
		}
	})

	// Catch-up deduplicates fire times by value, so a fire found after
	// skipping days must keep the zone of the time it was searched from.
	t.Run("returns fires in the zone of the search start", func(t *testing.T) {
		t.Parallel()

		weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
		schedule := boundSchedule(t, weekdays, map[string]any{"expression": "CRON_TZ=Asia/Tokyo 0 18 * * *"})

		next := schedule.Next(time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC))

		assert.Equal(t, time.Date(2027, 1, 4, 9, 0, 0, 0, time.UTC), next)
	})

	t.Run("stops when no day qualifies", func(t *testing.T) {
		t.Parallel()

		closed := &ir.Calendar{Name: "closed"}
		schedule := boundSchedule(t, closed, map[string]any{"expression": "* * * * *"})

		assert.True(t, schedule.Next(from).IsZero())
	})
}

// For random calendars and expressions, a schedule never fires on a day
// that is not a business day, and the preview lists exactly the fires that
// repeated Next calls produce.
func TestCalendarScheduleProperties(t *testing.T) {
	t.Parallel()

	expressions := []string{"0 18 * * *", "30 */6 * * *", "0 9 * * 1-5", "15 3 1,15,28 * *", "@every 90m"}
	rng := rand.New(rand.NewPCG(1, 2))
	start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	for range 50 {
		cal := &ir.Calendar{
			Name:              "random",
			Holidays:          map[ir.CivilDate]string{},
			ExtraBusinessDays: map[ir.CivilDate]struct{}{},
		}
		for day := range cal.Weekdays {
			cal.Weekdays[day] = rng.IntN(4) > 0
		}
		for range 40 {
			cal.Holidays[ir.DateOf(start.AddDate(0, 0, rng.IntN(365)))] = ""
		}
		for range 5 {
			cal.ExtraBusinessDays[ir.DateOf(start.AddDate(0, 0, rng.IntN(365)))] = struct{}{}
		}
		entry := map[string]any{"expression": expressions[rng.IntN(len(expressions))]}
		if rng.IntN(2) == 0 && entry["expression"] == "0 18 * * *" {
			entry["business_days"] = []any{1 + rng.IntN(5), -1 - rng.IntN(5)}
		}
		schedule := boundSchedule(t, cal, entry)
		dag := &ir.DAG{Schedule: []ir.Schedule{schedule}}

		fires := dag.UpcomingFires(start, 20)
		cursor := start
		for _, fire := range fires {
			require.True(t, cal.IsBusinessDay(ir.DateOf(fire.Time)), "%v fired on %v", entry, fire.Time)
			next := schedule.Next(cursor)
			require.True(t, next.Equal(fire.Time), "%v: preview %v, Next %v", entry, fire.Time, next)
			cursor = next
		}
	}
}

func TestCalendarScheduleParsing(t *testing.T) {
	t.Parallel()

	start := ir.ScheduleParseOptions{AllowAt: true, AllowCalendar: true}

	t.Run("fingerprint names the calendar and business days", func(t *testing.T) {
		t.Parallel()

		schedule, err := ir.ParseScheduleValue(map[string]any{
			"expression": "0 23 * * *", "calendar": "jp-banking", "business_days": []any{3, -1},
		}, start)
		require.NoError(t, err)
		assert.Equal(t, "cron:0 23 * * *|calendar:jp-banking|business_days:3,-1", schedule.Fingerprint())
	})

	// Index entries and run snapshots decode schedules from JSON without
	// their calendars, and must not fire on a holiday.
	t.Run("decoded calendar schedules fail closed", func(t *testing.T) {
		t.Parallel()

		schedule := boundSchedule(t, jpBanking(), map[string]any{"expression": "0 18 * * *"})
		data, err := json.Marshal(schedule)
		require.NoError(t, err)
		assert.JSONEq(t, `{"kind":"cron","expression":"0 18 * * *","calendar":"jp-banking"}`, string(data))

		var decoded ir.Schedule
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, "jp-banking", decoded.Calendar)
		assert.True(t, decoded.Next(time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)).IsZero())
		assert.Nil(t, decoded.Parsed)
		require.NoError(t, decoded.BindCalendar(jpBanking()))
		assert.False(t, decoded.Next(time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)).IsZero())
	})

	t.Run("calendar none keeps the plain schedule", func(t *testing.T) {
		t.Parallel()

		schedule, err := ir.ParseScheduleValue(map[string]any{"expression": "0 3 * * *", "calendar": "none"}, start)
		require.NoError(t, err)
		assert.False(t, schedule.HasCalendar())
		assert.False(t, schedule.Next(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero())
	})

	for _, tc := range []struct {
		name  string
		entry map[string]any
		opts  ir.ScheduleParseOptions
		want  string
	}{
		{
			name:  "business days with a day-of-month field",
			entry: map[string]any{"expression": "0 9 1 * *", "calendar": "c", "business_days": []any{1}},
			opts:  start,
			want:  "business_days requires * in the day-of-month and day-of-week fields",
		},
		{
			name:  "business days with an interval",
			entry: map[string]any{"expression": "@every 1h", "calendar": "c", "business_days": []any{1}},
			opts:  start,
			want:  "business_days requires *",
		},
		{
			name:  "business day out of range",
			entry: map[string]any{"expression": "0 9 * * *", "calendar": "c", "business_days": []any{24}},
			opts:  start,
			want:  "integers from 1 to 23, or -23 to -1",
		},
		{
			name:  "business day zero",
			entry: map[string]any{"expression": "0 9 * * *", "calendar": "c", "business_days": []any{0}},
			opts:  start,
			want:  "integers from 1 to 23",
		},
		{
			name:  "duplicate business day",
			entry: map[string]any{"expression": "0 9 * * *", "calendar": "c", "business_days": []any{2, 2}},
			opts:  start,
			want:  "business_days lists 2 twice",
		},
		{
			name:  "calendar on a one-off schedule",
			entry: map[string]any{"at": "2027-01-04T09:00:00Z", "calendar": "c"},
			opts:  start,
			want:  "one-off schedules do not take calendar or business_days",
		},
		{
			name:  "calendar on a stop schedule",
			entry: map[string]any{"expression": "0 18 * * *", "calendar": "c"},
			want:  "only supported for start schedules",
		},
		{
			name:  "invalid calendar name",
			entry: map[string]any{"expression": "0 18 * * *", "calendar": "../jp"},
			opts:  start,
			want:  "invalid calendar name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ir.ParseScheduleValue(tc.entry, tc.opts)
			require.ErrorContains(t, err, tc.want)
		})
	}
}
