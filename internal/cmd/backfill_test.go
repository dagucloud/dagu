// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The text plan shows the first and last three slots of a long plan.
func TestRenderBackfillPlan(t *testing.T) {
	t.Parallel()

	plan := backfill.Plan{DAGName: "etl", Rerun: backfill.RerunNone}
	for day := 1; day <= 8; day++ {
		at := time.Date(2026, 10, day, 2, 0, 0, 0, time.UTC)
		action := backfill.ActionRun
		if day == 2 {
			action = backfill.ActionSkipSucceeded
		}
		plan.Slots = append(plan.Slots, backfill.PlanSlot{
			At:     at,
			Window: ir.ScheduleWindow{Start: at.AddDate(0, 0, -1), Timezone: "UTC"},
			Action: action,
		})
	}
	var out bytes.Buffer
	require.NoError(t, renderBackfillPlan(&out, plan, backfill.Options{MaxConcurrency: 2, OnFailure: backfill.OnFailureStop},
		backfill.Outlook{MaxConcurrency: 2, Concurrency: 2, QueueLimit: 4, RunDuration: 10 * time.Minute}, []string{"No scheduler is running."}))
	text := out.String()

	assert.Contains(t, text, "Backfill etl, UTC, --rerun none")
	assert.Contains(t, text, "Fires       2026-10-01 02:00 to 2026-10-08 02:00")
	assert.Contains(t, text, "8 slots: run 7, skip succeeded 1")
	assert.Contains(t, text, "Concurrency 2 (queue limit 4), on failure stop")
	assert.Contains(t, text, "Estimate 40m from recent runs", "seven runs, two at a time")
	assert.Contains(t, text, "2026-10-03 02:00")
	assert.NotContains(t, text, "2026-10-04 02:00  2026-10-03")
	assert.Contains(t, text, "...")
	assert.Contains(t, text, "2026-10-06 02:00")
	assert.True(t, strings.HasSuffix(text, "Warning: No scheduler is running.\n"))
}

// The loads line covers the slots the plan runs, not those it skips.
func TestLoadsRangeCountsRuns(t *testing.T) {
	t.Parallel()

	slot := func(day int, action backfill.Action) backfill.PlanSlot {
		return backfill.PlanSlot{
			At:     time.Date(2026, 10, day, 2, 0, 0, 0, time.UTC),
			Action: action,
			Dates:  []ir.DateValue{{Name: "target_day", Value: fmt.Sprintf("2026-10-%02d", day-1)}},
		}
	}
	plan := backfill.Plan{Slots: []backfill.PlanSlot{
		slot(2, backfill.ActionSkipSucceeded),
		slot(3, backfill.ActionRun),
		slot(4, backfill.ActionRun),
		slot(5, backfill.ActionSkipActive),
	}}
	from, to := loadsRange(plan, "target_day")
	assert.Equal(t, "2026-10-02", from)
	assert.Equal(t, "2026-10-03", to)

	plan.Slots = plan.Slots[:1]
	from, to = loadsRange(plan, "target_day")
	assert.Empty(t, from+to, "a plan that runs nothing loads nothing")
}
