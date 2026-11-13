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

func TestScheduleWindowAt(t *testing.T) {
	t.Parallel()

	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)

	cronEntry := func(t *testing.T, expr string) ir.Schedule {
		t.Helper()
		schedule, err := ir.NewCronSchedule(expr)
		require.NoError(t, err)
		return schedule
	}
	profileEntry := func(t *testing.T, expr, profile string) ir.Schedule {
		t.Helper()
		schedule := cronEntry(t, expr)
		schedule.Profile = profile
		return schedule
	}
	utc := func(year int, month time.Month, day, hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	}

	tests := []struct {
		name      string
		schedules func(t *testing.T) []ir.Schedule
		profile   string
		loc       *time.Location
		slot      time.Time
		want      ir.ScheduleWindow
	}{
		{
			name:      "previous and next fire of one schedule",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "*/30 * * * *")} },
			loc:       time.UTC,
			slot:      utc(2026, 3, 13, 12, 0),
			want:      ir.ScheduleWindow{Start: utc(2026, 3, 13, 11, 30), Next: utc(2026, 3, 13, 12, 30), Timezone: "UTC"},
		},
		{
			name:      "zone named by the schedule",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "CRON_TZ=Asia/Tokyo 0 2 * * *")} },
			loc:       time.UTC,
			slot:      utc(2026, 11, 9, 17, 0),
			want:      ir.ScheduleWindow{Start: utc(2026, 11, 8, 17, 0), Next: utc(2026, 11, 10, 17, 0), Timezone: "Asia/Tokyo"},
		},
		{
			name:      "unzoned schedule evaluated in the server zone",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "0 2 * * *")} },
			loc:       tokyo,
			slot:      utc(2026, 11, 9, 17, 0),
			want:      ir.ScheduleWindow{Start: utc(2026, 11, 8, 17, 0), Next: utc(2026, 11, 10, 17, 0), Timezone: "Asia/Tokyo"},
		},
		{
			// A cadence split over entries yields contiguous windows.
			name: "previous fire across schedules",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{cronEntry(t, "0 9 * * 1-5"), cronEntry(t, "0 12 * * 6")}
			},
			loc:  time.UTC,
			slot: utc(2026, 11, 9, 9, 0), // Monday
			want: ir.ScheduleWindow{Start: utc(2026, 11, 7, 12, 0), Next: utc(2026, 11, 10, 9, 0), Timezone: "UTC"},
		},
		{
			name: "schedules of another profile ignored",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{profileEntry(t, "*/10 * * * *", "dev"), cronEntry(t, "0 * * * *")}
			},
			profile: "prod",
			loc:     time.UTC,
			slot:    utc(2026, 3, 13, 12, 0),
			want:    ir.ScheduleWindow{Start: utc(2026, 3, 13, 11, 0), Next: utc(2026, 3, 13, 13, 0), Timezone: "UTC"},
		},
		{
			name: "schedules of the run's profile included",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{profileEntry(t, "*/10 * * * *", "dev"), cronEntry(t, "0 * * * *")}
			},
			profile: "dev",
			loc:     time.UTC,
			slot:    utc(2026, 3, 13, 12, 0),
			want:    ir.ScheduleWindow{Start: utc(2026, 3, 13, 11, 50), Next: utc(2026, 3, 13, 12, 10), Timezone: "UTC"},
		},
		{
			name: "one-off schedule never bounds a window",
			schedules: func(t *testing.T) []ir.Schedule {
				oneOff, err := ir.NewOneOffSchedule("2026-03-13T11:30:00Z")
				require.NoError(t, err)
				return []ir.Schedule{cronEntry(t, "0 * * * *"), oneOff}
			},
			loc:  time.UTC,
			slot: utc(2026, 3, 13, 12, 0),
			want: ir.ScheduleWindow{Start: utc(2026, 3, 13, 11, 0), Next: utc(2026, 3, 13, 13, 0), Timezone: "UTC"},
		},
		{
			name: "fires dropped by a calendar are not fires",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{boundSchedule(t, jpBanking(), map[string]any{"expression": "0 9 * * *"})}
			},
			loc:  time.UTC,
			slot: utc(2027, 1, 12, 9, 0), // Tuesday after a Monday holiday
			want: ir.ScheduleWindow{Start: utc(2027, 1, 8, 9, 0), Next: utc(2027, 1, 13, 9, 0), Timezone: "UTC"},
		},
		{
			name:      "interval schedule is zone independent",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "@every 90m")} },
			loc:       tokyo,
			slot:      utc(2026, 3, 13, 12, 0),
			want:      ir.ScheduleWindow{Start: utc(2026, 3, 13, 10, 30), Next: utc(2026, 3, 13, 13, 30), Timezone: "UTC"},
		},
		{
			name:      "yearly schedule",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "0 0 1 1 *")} },
			loc:       time.UTC,
			slot:      utc(2027, 1, 1, 0, 0),
			want:      ir.ScheduleWindow{Start: utc(2026, 1, 1, 0, 0), Next: utc(2028, 1, 1, 0, 0), Timezone: "UTC"},
		},
		{
			// A slot from before a schedule edit is no longer a fire time.
			name:      "slot at a time no schedule fires",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cronEntry(t, "CRON_TZ=Asia/Tokyo 0 * * * *")} },
			loc:       time.UTC,
			slot:      utc(2026, 3, 13, 12, 20),
			want:      ir.ScheduleWindow{Start: utc(2026, 3, 13, 12, 0), Next: utc(2026, 3, 13, 13, 0), Timezone: "Asia/Tokyo"},
		},
		{
			name:      "no recurring schedule",
			schedules: func(*testing.T) []ir.Schedule { return nil },
			loc:       paris,
			slot:      utc(2026, 3, 13, 12, 0),
			want:      ir.ScheduleWindow{Timezone: "Europe/Paris"},
		},
		{
			name:      "zone with no name labelled by its offset",
			schedules: func(*testing.T) []ir.Schedule { return nil },
			loc:       time.FixedZone("", 9*60*60),
			slot:      utc(2026, 3, 13, 12, 0),
			want:      ir.ScheduleWindow{Timezone: "UTC+09:00"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ir.ScheduleWindowAt(tt.schedules(t), tt.profile, tt.loc, tt.slot)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPreviousFire(t *testing.T) {
	t.Parallel()

	twiceYearly, err := ir.NewCronSchedule("0 0 1 1,7 *")
	require.NoError(t, err)
	hourly, err := ir.NewCronSchedule("0 * * * *")
	require.NoError(t, err)
	oneOff, err := ir.NewOneOffSchedule("2026-06-30T23:30:00Z")
	require.NoError(t, err)
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ir.PreviousFire([]ir.Schedule{twiceYearly, oneOff}, at), "one-off schedules are not recurring fires")
	assert.Equal(t, time.Date(2026, 6, 30, 23, 0, 0, 0, time.UTC),
		ir.PreviousFire([]ir.Schedule{twiceYearly, hourly}, at), "the latest fire across schedules")
	assert.True(t, ir.PreviousFire(nil, at).IsZero())
}

func TestFiringScheduleExpression(t *testing.T) {
	t.Parallel()

	cron := func(t *testing.T, expr, profile string) ir.Schedule {
		t.Helper()
		schedule, err := ir.NewCronSchedule(expr)
		require.NoError(t, err)
		schedule.Profile = profile
		return schedule
	}
	unbound := func(t *testing.T) ir.Schedule {
		t.Helper()
		schedule, err := ir.ParseScheduleValue(
			map[string]any{"expression": "0 2 * * *", "calendar": "jp-banking"},
			ir.ScheduleParseOptions{AllowCalendar: true},
		)
		require.NoError(t, err)
		return schedule
	}
	// 2027-01-02 is a Saturday and 2027-01-04 a Monday.
	saturday := time.Date(2027, 1, 2, 2, 0, 0, 0, time.UTC)
	monday := time.Date(2027, 1, 4, 2, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		schedules func(t *testing.T) []ir.Schedule
		profile   string
		at        time.Time
		want      string
	}{
		{
			name:      "fires",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cron(t, "0 2 * * *", "")} },
			at:        monday,
			want:      "0 2 * * *",
		},
		{
			name:      "off schedule",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{cron(t, "0 2 * * *", "")} },
			at:        monday.Add(30 * time.Minute),
		},
		{
			name: "skips another profile's schedule",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{cron(t, "0 2 * * *", "staging"), cron(t, "0 2 */1 * *", "")}
			},
			profile: "prod",
			at:      monday,
			want:    "0 2 */1 * *",
		},
		{
			name: "bound calendar excludes a weekend",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{boundSchedule(t, jpBanking(), map[string]any{"expression": "0 2 * * *"})}
			},
			at: saturday,
		},
		{
			name: "bound calendar includes a business day",
			schedules: func(t *testing.T) []ir.Schedule {
				return []ir.Schedule{boundSchedule(t, jpBanking(), map[string]any{"expression": "0 2 * * *"})}
			},
			at:   monday,
			want: "0 2 * * *",
		},
		{
			name:      "unbound calendar matches by cron",
			schedules: func(t *testing.T) []ir.Schedule { return []ir.Schedule{unbound(t)} },
			at:        saturday,
			want:      "0 2 * * *",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ir.FiringScheduleExpression(tc.schedules(t), tc.profile, time.UTC, tc.at))
		})
	}
}
