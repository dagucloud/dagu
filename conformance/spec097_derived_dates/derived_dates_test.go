// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec097_derived_dates_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestDatesValidation(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"valid_dates.yaml", "valid_calendar_per_entry.yaml"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			result := harness.NewRunner(t).Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStderr("")
		})
	}

	rejected := map[string]string{
		"invalid_calendar_none.yaml":              `last_open_day uses business days, and schedule "0 3 * * *" has no calendar`,
		"invalid_unit_m.yaml":                     "use min for minutes or mo for months",
		"invalid_hourly_list_without_format.yaml": "repeats dates in the default format: set format",
		"invalid_cycle.yaml":                      "entries build on each other in a cycle: a -> b -> a",
		"invalid_from_list.yaml":                  `"days" is a list`,
		"invalid_format_directive.yaml":           `unknown directive %Q in "%Y%Q"`,
		"invalid_timezone.yaml":                   `unknown zone "Mars/Olympus"`,
	}
	for file, message := range rejected {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			result := harness.NewRunner(t).Run("validate", file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("dates.", message)
		})
	}
}

// A date that needs a window is valid on a DAG without a recurring
// schedule, since a sub-DAG run of a parent with a slot resolves it, but
// validation warns.
func TestDatesNeedingWindowWarn(t *testing.T) {
	t.Parallel()

	result := harness.NewRunner(t).Run("validate", "warn_window_without_schedule.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrContains("dates.since needs a schedule window, and the DAG has no recurring start schedule")
}

// utcServer runs the server in UTC, so that dates follow the schedule's zone
// rather than the server's.
var utcServer = []string{"DAGU_TZ=UTC"}

// sharedHome is utcServer with one Dagu home for every command of a test,
// so that a later command sees the runs of an earlier one.
func sharedHome(t *testing.T) []string {
	t.Helper()
	return append([]string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}, utcServer...)
}

// A run resolves its dates from its slot, in the zone of the slot's
// schedule, and reads them wherever it reads params.
func TestRunDates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		slot string
		want []string
	}{
		{
			// A Monday on a weekday calendar: the window opens on Friday.
			slot: "2026-11-09",
			want: []string{
				"target=2026-11-08",
				"same=2026-11-08",
				"compact=20261108",
				"open=2026-11-06",
				"prev_month_end=2026-10-31",
				"month_last_open=2026-11-30",
				`days=["2026-11-06","2026-11-07","2026-11-08"]`,
				"env=2026-11-08",
			},
		},
		{
			// May 2026 ends on a Sunday.
			slot: "2026-05-15",
			want: []string{
				"target=2026-05-14",
				"same=2026-05-14",
				"compact=20260514",
				"open=2026-05-14",
				"prev_month_end=2026-04-30",
				"month_last_open=2026-05-29",
				`days=["2026-05-14"]`,
				"env=2026-05-14",
			},
		},
	} {
		t.Run(tc.slot, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv(utcServer, "start", "--scheduled-at="+tc.slot, "run_dates.yaml")
			result.ExpectExitCode(0)
			dagu.ExpectFileContent("dates.txt", strings.Join(tc.want, "\n")+"\n")
		})
	}
}

// A param whose eval reads a date follows the slot, and so does the env
// that reads the param. A value the caller gives changes the param, not the
// date.
func TestParamEvalReadsDates(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedHome(t)
	result := dagu.RunWithEnv(env, "start", "--run-id=spec097-eval", "--scheduled-at=2026-11-10", "param_eval_dates.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("param-dates.txt", "param=2026-11-09 env=2026-11-09 date=2026-11-09 run=spec097-eval\n")

	override := dagu.RunWithEnv(env, "start", "--run-id=spec097-eval-override", "--scheduled-at=2026-11-10", "param_eval_dates.yaml", "--", "date=2026-11-01")
	override.ExpectExitCode(0)
	dagu.ExpectFileContent("param-dates.txt", "param=2026-11-01 env=2026-11-01 date=2026-11-09 run=spec097-eval-override\n")
}

// A list feeds parallel, which runs one child per date.
func TestDatesListFansOut(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(utcServer, "start", "--scheduled-at=2026-11-09", "fan_out_days.yaml")
	result.ExpectExitCode(0)
	for _, day := range []string{"2026-11-06", "2026-11-07", "2026-11-08"} {
		dagu.ExpectFileContent("day-"+day+".txt", "loaded\n")
	}
}

// A sub-DAG resolves its own dates, and the params that read them, from its
// parent's slot, while a start of the same DAG with no slot is refused and
// leaves no run.
func TestDatesFromParentSlot(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedHome(t)
	result := dagu.RunWithEnv(env, "start", "--scheduled-at=2026-11-10", "parent_slot.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("child-dates.txt", "since=2026-11-09 02:00:00 day=2026-11-09 param=2026-11-09\n")

	refused := dagu.RunWithEnv(env, "start", "window_without_schedule.yaml")
	refused.ExpectNonZeroExitCode()
	refused.ExpectStderrContains("dates.since: interval_start needs the window of a schedule slot", "the DAG has no schedule")
	dagu.ExpectNoFile("since.txt")
	status := dagu.RunWithEnv(env, "status", "window_without_schedule.yaml")
	status.ExpectNonZeroExitCode()
}

// A retry reads the dates its run recorded, even after the DAG changed.
func TestRetryKeepsDates(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedHome(t)
	const runID = "spec097-retry"
	first := dagu.RunWithEnv(env, "start", "--run-id="+runID, "--scheduled-at=2026-11-10", "retry_dates.yaml")
	first.ExpectNonZeroExitCode()

	dagu.WriteFile("retry_dates.yaml", strings.Join([]string{
		"name: retry_dates",
		"working_dir: .",
		`schedule: "CRON_TZ=Asia/Tokyo 0 2 * * *"`,
		"dates:",
		"  target_day: -2d",
		"steps:",
		"  - id: capture",
		"    run: printf 'edited\\n' >> attempts.txt",
		"",
	}, "\n"))
	dagu.WriteFile("allow.txt", "")
	retry := dagu.RunWithEnv(env, "retry", "--run-id="+runID, "retry_dates")
	retry.ExpectExitCode(0)
	dagu.ExpectFileContent("attempts.txt", "2026-11-09\n2026-11-09\n")
}

// A run of a DAG with dates started without a slot gets the latest fire as
// its slot, so a monthly load started mid-month loads what its 1st loaded.
func TestDefaultSlot(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	slot := time.Date(now.Year(), now.Month(), 1, 2, 0, 0, 0, time.UTC)
	if slot.After(now) {
		slot = slot.AddDate(0, -1, 0)
	}
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(utcServer, "start", "monthly_default_slot.yaml")
	result.ExpectExitCode(0)
	dagu.ExpectFileContent("default-slot.txt", fmt.Sprintf("slot=%s target=%s\n",
		slot.Format(time.RFC3339), slot.AddDate(0, 0, -1).Format(time.DateOnly)))
}

// Every way of starting a slot shows what it loads: dry prints the dates the
// real run records, calendar fires lists them beside each fire, and start
// reports them as it creates the run.
func TestDatePreviews(t *testing.T) {
	t.Parallel()

	fields := func(text string) string { return strings.Join(strings.Fields(text), " ") }

	dagu := harness.NewRunner(t)
	dry := dagu.RunWithEnv(utcServer, "dry", "--scheduled-at=2026-11-09", "run_dates.yaml")
	dry.ExpectExitCode(0)
	for _, want := range []string{
		"Scheduled at: 2026-11-09T02:00:00+09:00 (Asia/Tokyo)",
		"target_day 2026-11-08",
		"last_open_day 2026-11-06",
		"window_days [2026-11-06, 2026-11-07, 2026-11-08]",
	} {
		if !strings.Contains(fields(dry.Stdout()), want) {
			t.Fatalf("dry output does not contain %q:\n%s", want, dry.Stdout())
		}
	}

	fires := dagu.RunWithEnv(utcServer, "calendar", "fires", "run_dates.yaml", "--from", "2026-11-08T00:00:00+09:00", "--count", "2")
	fires.ExpectExitCode(0)
	for _, want := range []string{
		"Mon 2026-11-09 02:00 +09:00 CRON_TZ=Asia/Tokyo 0 2 * * * target_day=2026-11-08",
		"Tue 2026-11-10 02:00 +09:00 CRON_TZ=Asia/Tokyo 0 2 * * * target_day=2026-11-09",
	} {
		if !strings.Contains(fields(fires.Stdout()), want) {
			t.Fatalf("calendar fires output does not contain %q:\n%s", want, fires.Stdout())
		}
	}

	// A profile's schedule fires runs of that profile, so their dates come
	// from that profile's window.
	profiled := dagu.RunWithEnv(utcServer, "calendar", "fires", "profile_dates.yaml", "--from", "2026-11-08T00:00:00Z", "--count", "1")
	profiled.ExpectExitCode(0)
	if want := "since=2026-11-07"; !strings.Contains(profiled.Stdout(), want) {
		t.Fatalf("calendar fires output does not contain %q:\n%s", want, profiled.Stdout())
	}

	start := dagu.RunWithEnv(utcServer, "start", "--scheduled-at=2026-11-09", "run_dates.yaml")
	start.ExpectExitCode(0)
	start.ExpectStderrContains("Dates for slot 2026-11-09T02:00:00+09:00: target_day=2026-11-08")
}
