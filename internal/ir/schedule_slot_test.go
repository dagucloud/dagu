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

func TestResolveScheduleSlot(t *testing.T) {
	t.Parallel()

	cronEntry := func(t *testing.T, expr string) ir.Schedule {
		t.Helper()
		schedule, err := ir.NewCronSchedule(expr)
		require.NoError(t, err)
		return schedule
	}
	utc := func(year int, month time.Month, day, hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	}
	// 2026-11-11 12:10 in Tokyo.
	now := utc(2026, 11, 11, 3, 10)
	tokyoDaily := func(t *testing.T) []ir.Schedule {
		return []ir.Schedule{cronEntry(t, "CRON_TZ=Asia/Tokyo 0 2 * * *")}
	}

	tests := []struct {
		name        string
		schedules   func(t *testing.T) []ir.Schedule
		value       string
		location    string
		allowFuture bool
		wantAt      time.Time
		wantStart   time.Time
		wantWarning string
		wantErr     string
	}{
		{
			name:      "latest fire at or before now",
			schedules: tokyoDaily,
			value:     "latest",
			wantAt:    utc(2026, 11, 10, 17, 0),
			wantStart: utc(2026, 11, 9, 17, 0),
		},
		{
			name:      "the only fire on a date in the schedule's zone",
			schedules: tokyoDaily,
			value:     "2026-11-09",
			wantAt:    utc(2026, 11, 8, 17, 0),
			wantStart: utc(2026, 11, 7, 17, 0),
		},
		{
			name:      "time without an offset in the schedule's zone",
			schedules: tokyoDaily,
			value:     "2026-11-09T02:00",
			wantAt:    utc(2026, 11, 8, 17, 0),
			wantStart: utc(2026, 11, 7, 17, 0),
		},
		{
			name:      "RFC 3339 instant",
			schedules: tokyoDaily,
			value:     "2026-11-09T02:00:00+09:00",
			wantAt:    utc(2026, 11, 8, 17, 0),
			wantStart: utc(2026, 11, 7, 17, 0),
		},
		{
			name:        "time no schedule fires is accepted with a warning",
			schedules:   tokyoDaily,
			value:       "2026-11-09T03:00",
			wantAt:      utc(2026, 11, 8, 18, 0),
			wantStart:   utc(2026, 11, 8, 17, 0),
			wantWarning: "2026-11-09T03:00:00+09:00 is not a fire time of the schedule; nearest fire times: 2026-11-09T02:00:00+09:00 (before), 2026-11-10T02:00:00+09:00 (after)",
		},
		{
			name:      "slot later than now rejected",
			schedules: tokyoDaily,
			value:     "2026-11-15",
			wantErr:   "scheduled time 2026-11-15T02:00:00+09:00 is later than now; the latest fire time is 2026-11-11T02:00:00+09:00",
		},
		{
			name:        "slot later than now allowed for a dry run",
			schedules:   tokyoDaily,
			value:       "2026-11-15",
			allowFuture: true,
			wantAt:      utc(2026, 11, 14, 17, 0),
			wantStart:   utc(2026, 11, 13, 17, 0),
		},
		{
			name:      "date with several fires rejected",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "0 9,17 * * *")} },
			value:     "2026-11-09",
			wantErr:   "the schedule fires more than once on 2026-11-09 (2026-11-09T09:00:00Z, 2026-11-09T17:00:00Z); give the time",
		},
		{
			name:      "date with no fire rejected",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "0 9 * * 1-5")} },
			value:     "2026-11-08", // Sunday
			wantErr:   "the schedule does not fire on 2026-11-08; nearest fire times: 2026-11-06T09:00:00Z (before), 2026-11-09T09:00:00Z (after)",
		},
		{
			name: "time without an offset rejected when schedules use different zones",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{cronEntry(t, "CRON_TZ=Asia/Tokyo 0 2 * * *"), cronEntry(t, "CRON_TZ=Europe/Paris 0 2 * * *")}
			},
			value:   "2026-11-09T02:00",
			wantErr: "the schedules use different zones (Asia/Tokyo, Europe/Paris); give the time with an offset",
		},
		{
			name:      "latest needs a recurring schedule",
			schedules: func(*testing.T) []ir.Schedule { return nil },
			value:     "latest",
			wantErr:   `"latest" needs a recurring start schedule`,
		},
		{
			name:      "date needs a recurring schedule",
			schedules: func(*testing.T) []ir.Schedule { return nil },
			value:     "2026-11-09",
			wantErr:   "a date needs a recurring start schedule; give a time such as 2026-11-09T00:00",
		},
		{
			name:      "time on a DAG without a schedule read in the server zone",
			schedules: func(*testing.T) []ir.Schedule { return nil },
			value:     "2026-11-09T02:00",
			wantAt:    utc(2026, 11, 9, 2, 0),
		},
		{
			name: "time skipped by a daylight saving change rejected",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{cronEntry(t, "CRON_TZ=America/New_York 30 * * * *")}
			},
			value:   "2026-03-08T02:30",
			wantErr: `scheduled time "2026-03-08T02:30" does not exist in America/New_York`,
		},
		{
			name:      "interval-only schedule reads a time without an offset in UTC",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "@every 1h")} },
			location:  "Asia/Tokyo",
			value:     "2026-11-09T02:00",
			wantAt:    utc(2026, 11, 9, 2, 0),
			wantStart: utc(2026, 11, 9, 1, 0),
		},
		{
			name: "calendar that is not loaded rejected",
			schedules: func(t *testing.T) []ir.Schedule {
				unbound, err := ir.ParseScheduleValue(
					map[string]any{"expression": "0 9 * * *", "calendar": "missing"},
					ir.ScheduleParseOptions{AllowCalendar: true},
				)
				require.NoError(t, err)
				return []ir.Schedule{unbound}
			},
			value:   "latest",
			wantErr: `the schedule names calendar "missing", which is not loaded`,
		},
		{
			name:      "seconds rejected",
			schedules: tokyoDaily,
			value:     "2026-11-09T02:00:30+09:00",
			wantErr:   `invalid scheduled time "2026-11-09T02:00:30+09:00": seconds must be :00`,
		},
		{
			name:      "unknown form rejected",
			schedules: tokyoDaily,
			value:     "yesterday",
			wantErr:   `invalid scheduled time "yesterday"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loc := time.UTC
			if tt.location != "" {
				var err error
				loc, err = time.LoadLocation(tt.location)
				require.NoError(t, err)
			}
			slot, err := ir.ResolveScheduleSlot(ir.ScheduleSlotRequest{
				Value:       tt.value,
				Schedules:   tt.schedules(t),
				Location:    loc,
				Now:         now,
				AllowFuture: tt.allowFuture,
			})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantAt, slot.At)
			assert.Equal(t, tt.wantStart, slot.Window.Start)
			assert.Equal(t, tt.wantWarning, slot.Warning)
		})
	}
}

func TestRecentScheduleFires(t *testing.T) {
	t.Parallel()

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	daily, err := ir.NewCronSchedule("CRON_TZ=Asia/Tokyo 0 2 * * *")
	require.NoError(t, err)
	oneOff, err := ir.NewOneOffSchedule("2026-11-11T01:00:00Z")
	require.NoError(t, err)

	fires := ir.RecentScheduleFires([]ir.Schedule{daily, oneOff}, "", time.UTC,
		time.Date(2026, 11, 11, 3, 10, 0, 0, time.UTC), 3)

	assert.Equal(t, []time.Time{
		time.Date(2026, 11, 11, 2, 0, 0, 0, tokyo),
		time.Date(2026, 11, 10, 2, 0, 0, 0, tokyo),
		time.Date(2026, 11, 9, 2, 0, 0, 0, tokyo),
	}, fires)
	assert.Equal(t, "Asia/Tokyo", fires[0].Location().String())
}

func TestScheduleFiresBetween(t *testing.T) {
	t.Parallel()

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	daily, err := ir.NewCronSchedule("CRON_TZ=Asia/Tokyo 0 2 * * *")
	require.NoError(t, err)
	sameTime, err := ir.NewCronSchedule("CRON_TZ=Asia/Tokyo 0 2 * * *")
	require.NoError(t, err)
	oneOff, err := ir.NewOneOffSchedule("2026-10-02T01:00:00Z")
	require.NoError(t, err)
	schedules := []ir.Schedule{daily, sameTime, oneOff}
	from := time.Date(2026, 10, 2, 2, 0, 0, 0, tokyo)
	to := time.Date(2026, 10, 4, 2, 0, 0, 0, tokyo)

	// Both bounds are inclusive, schedules firing together yield one slot,
	// and a one-off fire is no slot.
	fires, more := ir.ScheduleFiresBetween(schedules, "", time.UTC, from, to, 10)
	assert.False(t, more)
	assert.Equal(t, []time.Time{from, from.AddDate(0, 0, 1), to}, fires)
	assert.Equal(t, "Asia/Tokyo", fires[0].Location().String())

	fires, more = ir.ScheduleFiresBetween(schedules, "", time.UTC, from, to, 2)
	assert.True(t, more)
	assert.Len(t, fires, 2)

	fires, more = ir.ScheduleFiresBetween(schedules, "", time.UTC, from.Add(time.Minute), to.Add(-time.Minute), 10)
	assert.False(t, more)
	assert.Equal(t, []time.Time{from.AddDate(0, 0, 1)}, fires)
}

func TestResolveRangeBound(t *testing.T) {
	t.Parallel()

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	daily, err := ir.NewCronSchedule("CRON_TZ=Asia/Tokyo 0 2 * * *")
	require.NoError(t, err)
	schedules := []ir.Schedule{daily}

	for _, tc := range []struct {
		value string
		bound ir.RangeBound
		want  time.Time
	}{
		{"2026-10-02", ir.RangeStart, time.Date(2026, 10, 2, 0, 0, 0, 0, tokyo)},
		{"2026-10-02", ir.RangeEnd, time.Date(2026, 10, 3, 0, 0, 0, 0, tokyo).Add(-time.Nanosecond)},
		{"2026-10-02T02:00", ir.RangeEnd, time.Date(2026, 10, 2, 2, 0, 0, 0, tokyo)},
		{"2026-10-02T02:00:00Z", ir.RangeStart, time.Date(2026, 10, 2, 2, 0, 0, 0, time.UTC)},
	} {
		got, err := ir.ResolveRangeBound(tc.value, schedules, "", time.UTC, tc.bound)
		require.NoError(t, err, tc.value)
		assert.True(t, tc.want.Equal(got), "%s: want %s, got %s", tc.value, tc.want, got)
	}

	_, err = ir.ResolveRangeBound("October", schedules, "", time.UTC, ir.RangeStart)
	require.ErrorContains(t, err, "invalid time")
}
