// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec097_derived_dates_test

import (
	"testing"

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
