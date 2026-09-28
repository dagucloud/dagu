// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec063_schedule_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestScheduleDescriptors(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"valid_hourly.yaml",
		"valid_daily.yaml",
		"valid_midnight.yaml",
		"valid_weekly.yaml",
		"valid_monthly.yaml",
		"valid_yearly.yaml",
		"valid_annually.yaml",
		"valid_every.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStderr("")
		})
	}

	rejectedCases := []string{
		"invalid_unknown_descriptor.yaml",
		"invalid_reboot.yaml",
		"invalid_every_seconds.yaml",
		"invalid_every_timezone.yaml",
	}
	for _, file := range rejectedCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains("schedule")
		})
	}
}

// Named time zones must resolve on a host without a time zone database, such
// as Windows without Go installed. Pointing GOROOT at an empty directory
// removes the fallback to the build machine's Go installation.
func TestNamedTimeZone(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{"GOROOT=" + t.TempDir(), "ZONEINFO="}
	result := dagu.RunWithEnv(env, "validate", "valid_named_timezone.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderr("")
}

func TestHourlyMatchesCron(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"DAGU_DAGS_DIR=."}, "ls", "-n")
	result.ExpectExitCode(0)

	nextRun := parseNextRunColumn(result.Stdout())

	descriptorNext, ok := nextRun["equivalent_hourly_descriptor"]
	if !ok {
		t.Fatalf("no NEXT_RUN row for equivalent_hourly_descriptor in output:\n%s", result.Stdout())
	}
	cronNext, ok := nextRun["equivalent_hourly_cron"]
	if !ok {
		t.Fatalf("no NEXT_RUN row for equivalent_hourly_cron in output:\n%s", result.Stdout())
	}

	if descriptorNext == "-" || cronNext == "-" {
		t.Fatalf("expected both DAGs to have a next run, got descriptor=%q cron=%q", descriptorNext, cronNext)
	}
	if descriptorNext != cronNext {
		t.Fatalf("@hourly next run %q does not match equivalent cron expression next run %q", descriptorNext, cronNext)
	}
}

// parseNextRunColumn maps DAG name -> NEXT_RUN cell from `dagu ls -n` output.
// Both columns are single tokens (a name, an RFC 3339 timestamp, or "-"), so
// splitting each row on whitespace is sufficient regardless of tabwriter padding.
func parseNextRunColumn(stdout string) map[string]string {
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	result := make(map[string]string, len(lines))
	for _, line := range lines[1:] { // skip header row
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		result[fields[0]] = fields[1]
	}
	return result
}
