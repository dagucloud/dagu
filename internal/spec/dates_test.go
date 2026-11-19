// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDates(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
schedule: "CRON_TZ=Asia/Tokyo 0 2 * * *"
calendar: weekdays
dates:
  target_day: -1d
  month_start: [-1d, month_start]
  target_compact: { from: target_day, format: date_compact }
  window_days:
    each: 1bd
    shift: day_start
    timezone: UTC
  loaded_at: { format: "%Y/%m/%d %H:%M", timezone: UTC }
steps:
  - run: echo ${dates.target_day}
`))
	require.NoError(t, err)

	assert.Equal(t, []ir.DateSpec{
		{Name: "target_day", Shift: []string{"-1d"}},
		{Name: "month_start", Shift: []string{"-1d", "month_start"}},
		{Name: "target_compact", From: "target_day", Format: "date_compact"},
		{Name: "window_days", Each: "1bd", Shift: []string{"day_start"}, Timezone: "UTC"},
		{Name: "loaded_at", Format: "%Y/%m/%d %H:%M", Timezone: "UTC"},
	}, dag.Dates)
	assert.Empty(t, dag.BuildWarnings)
}

// Aliases and merge keys resolve as anywhere else in the document, and a
// strftime pattern keeps its spaces.
func TestLoadDatesAliases(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
schedule: "0 2 * * *"
dates:
  first_day: &day [-1d, day_start]
  target_day: *day
  base: &base { shift: -1d, timezone: UTC }
  compact:
    <<: *base
    format: " %Y %m\n"
steps:
  - run: echo load
`))
	require.NoError(t, err)

	assert.Equal(t, []ir.DateSpec{
		{Name: "first_day", Shift: []string{"-1d", "day_start"}},
		{Name: "target_day", Shift: []string{"-1d", "day_start"}},
		{Name: "base", Shift: []string{"-1d"}, Timezone: "UTC"},
		{Name: "compact", Shift: []string{"-1d"}, Format: " %Y %m\n", Timezone: "UTC"},
	}, dag.Dates)
}

// Errors name the entry, so editors can mark its line.
func TestDatesErrorPositions(t *testing.T) {
	t.Parallel()

	data := []byte(`schedule: "0 2 * * *"
dates:
  target_day: -1m
  hours:
    each: 1h
  a: { from: b }
  b: { from: a }
steps:
  - run: echo load
`)
	dag, err := spec.LoadYAML(t.Context(), data, spec.WithAllowBuildErrors())
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		`[3:15] field 'dates.target_day': shift: unknown unit "m" in "-1m": use min for minutes or mo for months`,
		`[5:11] field 'dates.hours.each': a step of "1h" repeats dates in the default format: set format`,
	}, spec.ErrorMessages(data, dag.BuildErrors))
}

func TestDatesShapeErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		dates string
		want  string
	}{
		{
			name:  "unknown key",
			dates: "  a: { shift: -1d, frm: scheduled_at }\n",
			want:  `field 'dates.a.frm': unknown key "frm": use from, each, shift, format, timezone`,
		},
		{
			name:  "a number",
			dates: "  a: 1\n",
			want:  "field 'dates.a': a date is a shift such as -1d",
		},
		{
			name:  "an empty field",
			dates: "  a: { format: \"\" }\n",
			want:  "field 'dates.a.format': must be a non-empty string",
		},
		{
			name:  "a shift that is not a string",
			dates: "  a: [-1d, 2]\n",
			want:  "field 'dates.a': each shift operation must be a non-empty string",
		},
		{
			name:  "a cycle",
			dates: "  a: { from: b }\n  b: { from: a }\n",
			want:  "field 'dates.a': entries build on each other in a cycle: a -> b -> a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte("schedule: \"0 2 * * *\"\ndates:\n"+tc.dates+"steps:\n  - run: echo load\n"))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// Business days need a calendar for every slot a run can have: each
// recurring start schedule's, or the DAG's when it has no such schedule.
func TestDatesBusinessDayCalendar(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "an entry without a calendar",
			yaml: "calendar: weekdays\nschedule:\n  - \"0 2 * * *\"\n  - expression: \"0 3 * * *\"\n    calendar: none\n",
			want: `field 'dates.last_open_day': last_open_day uses business days, and schedule "0 3 * * *" has no calendar`,
		},
		{
			name: "no schedule and no calendar",
			yaml: "",
			want: "last_open_day uses business days, and the DAG has neither a recurring start schedule nor a calendar",
		},
		{
			name: "every entry names its calendar",
			yaml: "schedule:\n  - expression: \"0 2 * * *\"\n    calendar: weekdays\n",
		},
		{
			name: "the DAG's calendar applies to each entry",
			yaml: "calendar: weekdays\nschedule: \"0 2 * * *\"\n",
		},
		{
			name: "a DAG without a schedule uses its calendar",
			yaml: "calendar: weekdays\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := spec.LoadYAML(t.Context(), []byte(tc.yaml+"dates:\n  last_open_day: -1bd\nsteps:\n  - run: echo load\n"))
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// A date that needs a window is valid on a DAG without a recurring
// schedule, since a parent's slot gives a sub-DAG run one, but warns.
func TestDatesNeedingWindowWarn(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
schedule:
  - at: "2026-11-10T02:00:00+09:00"
dates:
  day: -1d
  since: { from: interval_start }
steps:
  - run: echo load
`))
	require.NoError(t, err)

	assert.Equal(t, []string{
		"dates.since needs a schedule window, and the DAG has no recurring start schedule: " +
			"only a sub-DAG run of a parent with a schedule slot can resolve it",
	}, dag.BuildWarnings)
}
