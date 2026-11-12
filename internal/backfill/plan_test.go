// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func planRequest(t *testing.T, rerun backfill.Rerun) backfill.PlanRequest {
	t.Helper()
	sched := dailyAt(t, "0 2 * * *")
	return backfill.PlanRequest{
		DAG:      &ir.DAG{Name: "etl", Schedule: sched.Schedules},
		Schedule: sched,
		From:     at(1, 0),
		To:       at(4, 23),
		Rerun:    rerun,
		Now:      at(20, 0),
	}
}

func actions(plan backfill.Plan) []backfill.Action {
	var got []backfill.Action
	for _, slot := range plan.Slots {
		got = append(got, slot.Action)
	}
	return got
}

// The four slots are missing, succeeded, failed, and active, in that order.
func TestMakePlanAppliesRerun(t *testing.T) {
	t.Parallel()

	states := func(slots []time.Time) []backfill.Slot {
		return []backfill.Slot{
			{At: slots[0], Status: backfill.SlotMissing},
			{At: slots[1], Status: backfill.SlotSucceeded, RunID: "ok"},
			{At: slots[2], Status: backfill.SlotFailed, RunID: "bad"},
			{At: slots[3], Status: backfill.SlotActive, RunID: "busy"},
		}
	}
	for _, tc := range []struct {
		rerun backfill.Rerun
		want  []backfill.Action
	}{
		{backfill.RerunNone, []backfill.Action{backfill.ActionRun, backfill.ActionSkipSucceeded, backfill.ActionSkipFailed, backfill.ActionSkipActive}},
		{backfill.RerunFailed, []backfill.Action{backfill.ActionRun, backfill.ActionSkipSucceeded, backfill.ActionRun, backfill.ActionSkipActive}},
		{backfill.RerunAll, []backfill.Action{backfill.ActionRun, backfill.ActionRun, backfill.ActionRun, backfill.ActionSkipActive}},
	} {
		t.Run(string(tc.rerun), func(t *testing.T) {
			t.Parallel()
			req := planRequest(t, tc.rerun)
			rng, err := backfill.SlotsOf(req)
			require.NoError(t, err)
			require.Len(t, rng.Slots, 4)

			plan := backfill.MakePlan(req, rng, states(rng.Slots), nil)

			assert.Equal(t, tc.want, actions(plan))
			assert.Equal(t, map[backfill.Rerun]int{backfill.RerunNone: 1, backfill.RerunFailed: 2, backfill.RerunAll: 3}, plan.RunCounts)
			if plan.Slots[1].Action != backfill.ActionRun {
				assert.Equal(t, "ok", plan.Slots[1].RunID, "a skip names the run it relies on")
			}
			assert.Equal(t, at(1, 2), plan.Slots[1].Window.Start, "each slot carries its window")
		})
	}
}

// Slots named with --slot run whatever their history, unless active, and a
// slot an open backfill still holds is active.
func TestMakePlanNamedAndPendingSlots(t *testing.T) {
	t.Parallel()

	req := planRequest(t, backfill.RerunNone)
	req.From, req.To = time.Time{}, time.Time{}
	req.Slots = []time.Time{at(3, 2), at(2, 2), at(3, 2)}
	rng, err := backfill.SlotsOf(req)
	require.NoError(t, err)
	require.Equal(t, []time.Time{at(2, 2), at(3, 2)}, rng.Slots, "named slots are sorted and deduplicated")

	tokyo := dailyAt(t, "CRON_TZ=Asia/Tokyo 0 11 * * *")
	zoned := req
	zoned.Schedule = tokyo
	zoned.Slots = []time.Time{at(2, 2)}
	zonedRange, err := backfill.SlotsOf(zoned)
	require.NoError(t, err)
	assert.Equal(t, "Asia/Tokyo", zonedRange.Slots[0].Location().String(), "a named slot reads in its schedule's zone")

	plan := backfill.MakePlan(req, rng, []backfill.Slot{
		{At: rng.Slots[0], Status: backfill.SlotSucceeded},
		{At: rng.Slots[1], Status: backfill.SlotSucceeded, RunID: "earlier"},
	}, map[time.Time]bool{at(3, 2): true})

	assert.Equal(t, []backfill.Action{backfill.ActionRun, backfill.ActionSkipActive}, actions(plan))
	assert.Empty(t, plan.Slots[1].RunID, "an open backfill holds the slot, not its earlier run")
}

func TestSlotsOfChecksTheRange(t *testing.T) {
	t.Parallel()

	t.Run("to is clamped to before the current minute", func(t *testing.T) {
		t.Parallel()
		req := planRequest(t, backfill.RerunNone)
		req.Now = at(3, 2).Add(30 * time.Second)
		req.To = at(10, 0)
		rng, err := backfill.SlotsOf(req)
		require.NoError(t, err)
		assert.True(t, rng.Clamped)
		assert.True(t, rng.To.Before(at(3, 2)))
		assert.Equal(t, []time.Time{at(1, 2), at(2, 2)}, rng.Slots, "the slot firing now is left to the live schedule")
	})
	t.Run("from later than now", func(t *testing.T) {
		t.Parallel()
		req := planRequest(t, backfill.RerunNone)
		req.From = at(21, 0)
		_, err := backfill.SlotsOf(req)
		require.ErrorContains(t, err, "later than now")
	})
	t.Run("named slot later than now", func(t *testing.T) {
		t.Parallel()
		req := planRequest(t, backfill.RerunNone)
		req.Slots = []time.Time{at(21, 2)}
		_, err := backfill.SlotsOf(req)
		require.ErrorContains(t, err, "later than now")
	})
	t.Run("more slots than the bound", func(t *testing.T) {
		t.Parallel()
		req := planRequest(t, backfill.RerunNone)
		req.MaxSlots = 3
		_, err := backfill.SlotsOf(req)
		require.ErrorContains(t, err, "more than 3 slots")
	})
}

func TestPlanRevisionFollowsActions(t *testing.T) {
	t.Parallel()

	req := planRequest(t, backfill.RerunNone)
	rng, err := backfill.SlotsOf(req)
	require.NoError(t, err)
	missing := make([]backfill.Slot, len(rng.Slots))
	for i, slot := range rng.Slots {
		missing[i] = backfill.Slot{At: slot}
	}
	first := backfill.MakePlan(req, rng, missing, nil)
	again := backfill.MakePlan(req, rng, missing, nil)
	changed := backfill.MakePlan(req, rng, missing, map[time.Time]bool{at(2, 2): true})

	assert.Equal(t, first.Revision, again.Revision)
	assert.NotEqual(t, first.Revision, changed.Revision)

	// A submit records each slot's window, so a moved schedule changes the
	// revision even where every action stays the same.
	moved := req
	moved.Schedule = dailyAt(t, "0 2 */2 * *")
	assert.NotEqual(t, first.Revision, backfill.MakePlan(moved, rng, missing, nil).Revision)
}
