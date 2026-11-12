// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec086_backfill_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestSlotRetentionDays(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file        string
		exitCode    int
		stderrParts []string
	}{
		{file: "slot_retention_days.yaml"},
		{file: "slot_retention_days_zero.yaml", exitCode: 1, stderrParts: []string{"slot_retention_days"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			result := harness.NewRunner(t).Run("validate", tc.file)
			result.ExpectExitCode(tc.exitCode)
			result.ExpectStderrContains(tc.stderrParts...)
		})
	}
}
