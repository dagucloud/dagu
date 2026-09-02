// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec037_schedule_test

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
		"valid_weekly.yaml",
		"valid_monthly.yaml",
		"valid_yearly.yaml",
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

	t.Run("unknown descriptor is rejected", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "invalid_unknown_descriptor.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("schedule")
	})
}

func TestHourlyDescriptorMatchesEquivalentCronExpression(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("ls", "-n")
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
