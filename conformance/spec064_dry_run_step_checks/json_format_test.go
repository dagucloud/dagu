// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec064_dry_run_step_checks_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestDryFormatJSON(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.Run("start", "json_setup.yaml").ExpectExitCode(0)

	result := dagu.Run("dry", "--format", "json", "json_checks.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrContains(dryRunWarning)

	report := decodeDryChecks(t, result.Stdout())
	require.Equal(t, []dryCheck{
		{
			Step:    "read",
			Field:   "with.sheet",
			Message: `orders.xlsx: sheet "Order" not found; sheets present: Sheet1`,
		},
		{
			Step:    "load",
			Field:   "with.path",
			Message: "missing-9f3c2b1a.txt: file not found",
		},
	}, report.Checks)
}

func TestDryFormatJSONNoChecks(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("dry", "--format", "json", "valid_command.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrNotContains(dryRunWarning)

	report := decodeDryChecks(t, result.Stdout())
	require.Empty(t, report.Checks)
}

type dryCheck struct {
	Step    string `json:"step"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type dryReport struct {
	Checks []dryCheck `json:"checks"`
}

// decodeDryChecks requires stdout to hold one object whose only key is
// checks, an array rather than null.
func decodeDryChecks(t *testing.T, stdout string) dryReport {
	t.Helper()

	var object map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(stdout), &object))
	require.Len(t, object, 1)
	checks, ok := object["checks"]
	require.True(t, ok)
	require.NotEqual(t, "null", string(checks), "checks must be an array")

	var report dryReport
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))
	return report
}
