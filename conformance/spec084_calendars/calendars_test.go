// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec084_calendars_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func calendarsEnv(dagu *harness.Runner) []string {
	return []string{"DAGU_CALENDARS_DIR=" + dagu.ProjectPath("calendars")}
}

func TestCalendarScheduleValidation(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"valid_calendar.yaml", "valid_builtin_weekdays.yaml"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv(calendarsEnv(dagu), "validate", file)
			result.ExpectExitCode(0)
			result.ExpectStderr("")
		})
	}

	rejected := map[string]string{
		"invalid_unknown_calendar.yaml":               `unknown calendar "jp-bank"; available: jp-banking, jp-national, weekdays`,
		"invalid_business_days_without_calendar.yaml": "business_days needs a calendar",
		"invalid_business_days_day_field.yaml":        "business_days requires * in the day-of-month and day-of-week fields",
		"invalid_business_days_zero.yaml":             "integers from 1 to 23, or -23 to -1",
		"invalid_calendar_on_stop.yaml":               "only supported for start schedules",
		"invalid_calendar_on_one_off.yaml":            "one-off schedules do not take calendar or business_days",
	}
	for file, message := range rejected {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv(calendarsEnv(dagu), "validate", file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("schedule", message)
		})
	}
}

func TestCalendarFires(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "holidays and business days",
			args: []string{"calendar", "fires", "fires_jp_banking.yaml", "--from", "2026-12-31T00:00:00+09:00", "--count", "12"},
			want: []string{
				"TIME SCHEDULE NOTE",
				"Thu 2026-12-31 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
				"Thu 2026-12-31 23:00 +09:00 CRON_TZ=Asia/Tokyo 0 23 * * * last business day",
				"Mon 2027-01-04 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * * skips Fri 2027-01-01",
				"Tue 2027-01-05 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
				"Wed 2027-01-06 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
				"Wed 2027-01-06 23:00 +09:00 CRON_TZ=Asia/Tokyo 0 23 * * * 3rd business day",
				"Thu 2027-01-07 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
				"Fri 2027-01-08 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
				"Tue 2027-01-12 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * * skips Mon 2027-01-11 (Coming of Age Day)",
				"Wed 2027-01-13 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
				"Thu 2027-01-14 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
				"Fri 2027-01-15 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * *",
			},
		},
		{
			name: "included holiday",
			args: []string{"calendar", "fires", "fires_jp_banking.yaml", "--from", "2027-02-10T19:00:00+09:00", "--count", "1"},
			want: []string{
				"TIME SCHEDULE NOTE",
				"Fri 2027-02-12 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * * skips Thu 2027-02-11 (National Foundation Day)",
			},
		},
		{
			name: "extra business day",
			args: []string{"calendar", "fires", "fires_jp_banking.yaml", "--from", "2027-10-08T19:00:00+09:00", "--count", "1"},
			want: []string{
				"TIME SCHEDULE NOTE",
				"Sat 2027-10-09 18:00 +09:00 CRON_TZ=Asia/Tokyo 0 18 * * * extra business day",
			},
		},
		{
			name: "built-in weekdays",
			args: []string{"calendar", "fires", "fires_weekdays.yaml", "--from", "2028-02-01T00:00:00Z", "--count", "2"},
			want: []string{
				"TIME SCHEDULE NOTE",
				"Tue 2028-02-29 09:00 +00:00 CRON_TZ=UTC 0 9 * * * last business day",
				"Fri 2028-03-31 09:00 +00:00 CRON_TZ=UTC 0 9 * * * last business day",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv(calendarsEnv(dagu), tc.args...)
			result.ExpectExitCode(0)
			if got := normalizedLines(result.Stdout()); !slices.Equal(got, tc.want) {
				t.Fatalf("calendar fires printed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func TestCalendarCheck(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(calendarsEnv(dagu), "calendar", "check")
	result.ExpectExitCode(0)
	got := normalizedLines(result.Stdout())
	want := []string{"ok jp-banking: 3 holidays", "ok jp-national: 1 holidays"}
	if !slices.Equal(got, want) {
		t.Fatalf("calendar check printed %q, want %q", got, want)
	}

	result = dagu.RunWithEnv(calendarsEnv(dagu), "calendar", "check", "badcalendars/broken.yaml")
	result.ExpectNonZeroExitCode()
	if !strings.Contains(result.Stdout(), "FAIL broken:") || !strings.Contains(result.Stdout(), "range ends on 2027-01-03, before it starts on 2027-01-05") {
		t.Fatalf("calendar check printed:\n%s", result.Stdout())
	}
}

// The next run reported by `dagu ls -n` must skip holidays. The calendar
// makes today and the next two days holidays, so the result does not depend
// on the day the test runs.
func TestListSkipsHolidays(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	today := time.Now().UTC()
	var holidays strings.Builder
	for offset := range 3 {
		holidays.WriteString("  - " + today.AddDate(0, 0, offset).Format(time.DateOnly) + "\n")
	}
	dagu.WriteFile("listing/calendars/maintenance.yaml",
		"weekdays: [MON, TUE, WED, THU, FRI, SAT, SUN]\nholidays:\n"+holidays.String())
	dagu.WriteFile("listing/dags/nightly.yaml",
		"schedule:\n  - expression: \"CRON_TZ=UTC 0 12 * * *\"\n    calendar: maintenance\nsteps:\n  - run: echo scheduled\n")

	result := dagu.RunWithEnv([]string{
		"DAGU_DAGS_DIR=" + dagu.ProjectPath("listing/dags"),
		"DAGU_CALENDARS_DIR=" + dagu.ProjectPath("listing/calendars"),
	}, "ls", "-n")
	result.ExpectExitCode(0)

	want := time.Date(today.Year(), today.Month(), today.Day()+3, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	for _, line := range normalizedLines(result.Stdout()) {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "nightly" {
			if fields[1] != want {
				t.Fatalf("nightly next run %s, want %s", fields[1], want)
			}
			return
		}
	}
	t.Fatalf("no row for nightly in:\n%s", result.Stdout())
}

// normalizedLines collapses the column padding of tabular output.
func normalizedLines(stdout string) []string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimRight(stdout, "\n"), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			lines = append(lines, strings.Join(fields, " "))
		}
	}
	return lines
}
