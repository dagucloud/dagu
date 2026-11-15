// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// missedGap returns the gap the planner keeps for a DAG.
func missedGap(tp *TickPlanner, dagName string) *schedulerstate.MissedGap {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	return tp.watermarkState.DAGs[dagName].Missed
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 2, 7, hour, minute, 0, 0, time.UTC)
}

// before is the second before t: the last whose slots a gap ending at the
// tick at t counts, when that tick ran its own.
func before(t time.Time) time.Time {
	return t.Add(-time.Second)
}

// The slots that came due while a DAG was held make its gap once the hold
// ends, from the tick that found it held; the slots of that tick run.
func TestTickPlanner_HoldRecordsGap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		paused bool
		reason schedulerstate.GapReason
	}{
		{name: "suspended", reason: schedulerstate.GapSuspended},
		{name: "paused", paused: true, reason: schedulerstate.GapPaused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(11, 59))})
			held := true
			tp.cfg.IsSuspended = func(context.Context, string) (bool, error) { return held, nil }
			tp.cfg.IsPaused = func(context.Context) (bool, error) { return tc.paused, nil }
			dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

			for _, now := range []time.Time{at(12, 0), at(13, 0), at(14, 0)} {
				require.Empty(t, tp.Plan(t.Context(), now))
			}
			assert.Nil(t, missedGap(tp, dag.Name), "the gap is recorded when the hold ends")

			held = false
			runs := tp.Plan(t.Context(), at(15, 0))
			require.Len(t, runs, 1, "the slot of the tick that ends the hold runs")
			assert.Equal(t, &schedulerstate.MissedGap{
				From: at(12, 0), To: at(15, 0), Through: before(at(15, 0)), Reason: tc.reason, Slots: 3,
			}, missedGap(tp, dag.Name))
		})
	}
}

// A hold during which no slot came due records no gap.
func TestTickPlanner_ShortHoldRecordsNoGap(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(11, 59))})
	held := true
	tp.cfg.IsSuspended = func(context.Context, string) (bool, error) { return held, nil }
	dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	tp.Plan(t.Context(), at(12, 10))
	held = false
	tp.Plan(t.Context(), at(12, 40))
	assert.Nil(t, missedGap(tp, dag.Name))
}

// The slots of a DAG that is not caught up which came due while no
// scheduler ran make its gap; the first tick runs its own.
func TestTickPlanner_InitRecordsDownGap(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(9, 0))})
	dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	assert.Equal(t, &schedulerstate.MissedGap{
		From: at(9, 0), To: at(12, 0), Through: before(at(12, 0)), Reason: schedulerstate.GapDown, Slots: 2,
	}, missedGap(tp, dag.Name))
}

// A DAG that is caught up misses only the slots before its catch-up window.
func TestTickPlanner_InitRecordsGapBeforeCatchupWindow(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(3, 0))})
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error { return nil }
	dag := newHourlyCatchupDAG(t, "caught-up")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	// The window of six hours replays the slots after 06:00.
	assert.Equal(t, &schedulerstate.MissedGap{
		From: at(3, 0), To: at(6, 0), Through: at(6, 0), Reason: schedulerstate.GapDown, Slots: 3,
	}, missedGap(tp, dag.Name))
}

// A short outage within the catch-up window misses nothing.
func TestTickPlanner_InitRecordsNoGapWithinCatchupWindow(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(9, 0))})
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error { return nil }
	dag := newHourlyCatchupDAG(t, "caught-up")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	assert.Nil(t, missedGap(tp, dag.Name))
}

// A tick that comes late, as after the host slept, records the slots it
// missed.
func TestTickPlanner_ResumeRecordsDownGap(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{})
	tp.cfg.QueuesEnabled = false
	dag := &ir.DAG{Name: "quarterly", Schedule: []ir.Schedule{mustParseSchedule(t, "*/15 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	tp.Advance(at(12, 0))

	tp.resume(t.Context(), at(12, 45))

	assert.Equal(t, &schedulerstate.MissedGap{
		From: at(12, 0), To: at(12, 45), Through: before(at(12, 45)), Reason: schedulerstate.GapDown, Slots: 2,
	}, missedGap(tp, dag.Name))
}

// A tick that cannot tell a DAG's schedules keeps its hold, so a later tick
// records the whole gap.
func TestTickPlanner_HoldOutlastsUnresolvedSchedules(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(11, 59))})
	resolver := &testProfileResolver{profile: "prod"}
	tp.cfg.ProfileResolver = resolver
	held := true
	tp.cfg.IsSuspended = func(context.Context, string) (bool, error) { return held, nil }
	dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseProfileSchedule(t, "0 * * * *", "prod")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	tp.Plan(t.Context(), at(12, 0))
	tp.Plan(t.Context(), at(13, 0))
	held = false
	resolver.err = errors.New("profile store unavailable")
	tp.Plan(t.Context(), at(14, 0))
	assert.Nil(t, missedGap(tp, dag.Name))

	resolver.err = nil
	tp.Plan(t.Context(), at(15, 0))
	assert.Equal(t, &schedulerstate.MissedGap{
		From: at(12, 0), To: at(15, 0), Through: before(at(15, 0)), Reason: schedulerstate.GapSuspended, Slots: 3,
	}, missedGap(tp, dag.Name))
}

// Failover recovery replays the slots after its boundary, so the slot at
// the boundary is part of the gap.
func TestTickPlanner_InitRecordsGapThroughRecoveryBoundary(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(9, 0))})
	tp.cfg.RecoveryWindow = func() time.Duration { return time.Hour }
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error { return nil }
	dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	assert.Equal(t, &schedulerstate.MissedGap{
		From: at(9, 0), To: at(11, 0), Through: at(11, 0), Reason: schedulerstate.GapDown, Slots: 2,
	}, missedGap(tp, dag.Name))
}

// bufferedSlots returns the slots a DAG's catch-up buffer holds.
func bufferedSlots(tp *TickPlanner, dagName string) []time.Time {
	var slots []time.Time
	if buffer := tp.buffers[dagName]; buffer != nil {
		for _, item := range buffer.items {
			slots = append(slots, item.ScheduledTime)
		}
	}
	return slots
}

// A hold that ended while no scheduler ran lasted to the last tick: its gap
// ends there, and catch-up takes the slots after it, so none is both.
func TestTickPlanner_InitSettlesRestoredHold(t *testing.T) {
	t.Parallel()
	heldSince := at(10, 0)
	state := newMockState(at(11, 0))
	state.DAGs["caught-up"] = schedulerstate.DAGWatermark{
		LastScheduledTime: at(11, 0), HeldSince: &heldSince, HeldBy: schedulerstate.GapSuspended,
	}
	tp, _ := newTestTickPlanner(&mockStateStore{state: state})
	tp.cfg.Clock = func() time.Time { return at(14, 0) }
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error { return nil }
	dag := newHourlyCatchupDAG(t, "caught-up")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	assert.Equal(t, &schedulerstate.MissedGap{
		From: at(10, 0), To: at(11, 0), Through: at(11, 0), Reason: schedulerstate.GapSuspended, Slots: 2,
	}, missedGap(tp, dag.Name))
	for _, slot := range bufferedSlots(tp, dag.Name) {
		assert.True(t, slot.After(at(11, 0)), "catch-up replays %s, which the gap counts", slot)
	}
	tp.mu.RLock()
	assert.Nil(t, tp.watermarkState.DAGs[dag.Name].HeldSince)
	tp.mu.RUnlock()
}

// Recovery replays a slot planned before the failover, so the gap leaves it
// out.
func TestTickPlanner_InitLeavesOutRecoveredPlannedSlot(t *testing.T) {
	t.Parallel()
	state := newMockState(at(10, 59))
	state.DAGs["hourly"] = schedulerstate.DAGWatermark{
		LastScheduledTime: at(10, 0),
		LastSlot:          &schedulerstate.ScheduleSlot{At: at(11, 0), Outcome: schedulerstate.SlotPlanned},
	}
	tp, _ := newTestTickPlanner(&mockStateStore{state: state})
	tp.cfg.RecoveryWindow = func() time.Duration { return time.Hour }
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error { return nil }
	dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

	assert.Contains(t, bufferedSlots(tp, dag.Name), at(11, 0))
	assert.Nil(t, missedGap(tp, dag.Name))
}

// A DAG still held at startup has its catch-up queued; when its hold ends
// before a tick finds it held, catch-up runs those slots, so the gap leaves
// them out.
func TestTickPlanner_HoldGapLeavesOutQueuedCatchup(t *testing.T) {
	t.Parallel()
	heldSince := at(10, 0)
	state := newMockState(at(11, 0))
	state.DAGs["caught-up"] = schedulerstate.DAGWatermark{
		LastScheduledTime: at(11, 0), HeldSince: &heldSince, HeldBy: schedulerstate.GapSuspended,
	}
	tp, _ := newTestTickPlanner(&mockStateStore{state: state})
	tp.cfg.Clock = func() time.Time { return at(14, 0) }
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error { return nil }
	held := true
	tp.cfg.IsSuspended = func(context.Context, string) (bool, error) { return held, nil }
	dag := newHourlyCatchupDAG(t, "caught-up")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	queued := bufferedSlots(tp, dag.Name)
	require.NotEmpty(t, queued)

	held = false
	tp.Plan(t.Context(), at(14, 0))
	gap := missedGap(tp, dag.Name)
	require.NotNil(t, gap)
	assert.Equal(t, 2, gap.Slots, "10:00 and 11:00 passed; catch-up runs %v", queued)
}

// heldGap returns a planner whose hourly DAG missed the slots of 12:00,
// 13:00, and 14:00 while it was suspended.
func heldGap(t *testing.T) (*TickPlanner, string) {
	t.Helper()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(11, 59))})
	held := true
	tp.cfg.IsSuspended = func(context.Context, string) (bool, error) { return held, nil }
	dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	for _, now := range []time.Time{at(12, 0), at(13, 0), at(14, 0)} {
		tp.Plan(t.Context(), now)
	}
	held = false
	tp.Plan(t.Context(), at(15, 0))
	require.Equal(t, 3, missedGap(tp, dag.Name).Slots)
	return tp, dag.Name
}

// backfillOf returns a backfill of a DAG's range, from its first slot to
// its last, that runs the slots.
func backfillOf(dagName string, slots ...time.Time) *backfill.Record {
	last := slots[len(slots)-1]
	record := &backfill.Record{
		DAGName:   dagName,
		Options:   backfill.Options{From: slots[0], To: last},
		CreatedAt: last.Add(time.Hour),
	}
	for _, slot := range slots {
		record.Slots = append(record.Slots, backfill.RecordSlot{At: slot})
	}
	return record
}

// A backfill that runs a gap's slots, or skips some within its range, clears
// it. One that runs its first slots leaves the gap the slots after its
// range. One that skips the gap's first slot, names its slots, or selects
// them by a date leaves the gap as it is. A backfill under another profile
// skips no slot of the scheduler's, so the gap keeps the slots from its
// first hole.
func TestTickPlanner_BackfillCoversGap(t *testing.T) {
	t.Parallel()
	held := schedulerstate.MissedGap{From: at(12, 0), To: at(15, 0), Through: before(at(15, 0)), Reason: schedulerstate.GapSuspended, Slots: 3}
	named := backfillOf("hourly", at(12, 0), at(13, 0), at(14, 0))
	named.Options.Slots = []time.Time{at(12, 0), at(13, 0), at(14, 0)}
	byDate := backfillOf("hourly", at(12, 0), at(13, 0), at(14, 0))
	byDate.Options.By = "target_day"
	// 14:00 already ran, and the range defaulted to the submit at 14:30.
	tail := backfillOf("hourly", at(12, 0), at(13, 0))
	tail.Options.To, tail.CreatedAt = time.Time{}, at(14, 30)
	otherProfile := backfillOf("hourly", at(12, 0), at(14, 0))
	otherProfile.Options.Profile = "staging"
	for _, tc := range []struct {
		name   string
		record *backfill.Record
		want   *schedulerstate.MissedGap
	}{
		{name: "every slot", record: backfillOf("hourly", at(12, 0), at(13, 0), at(14, 0))},
		{name: "and the slot that ran", record: backfillOf("hourly", at(11, 0), at(12, 0), at(13, 0), at(14, 0), at(15, 0))},
		{
			name:   "first slots",
			record: backfillOf("hourly", at(12, 0), at(13, 0)),
			want:   &schedulerstate.MissedGap{From: at(14, 0), To: at(15, 0), Through: before(at(15, 0)), Reason: schedulerstate.GapSuspended, Slots: 1},
		},
		// 13:00 already ran, or another open backfill holds it.
		{name: "slots it skips", record: backfillOf("hourly", at(12, 0), at(14, 0))},
		{name: "last slot it skips", record: tail},
		{
			name:   "slots another profile skips",
			record: otherProfile,
			want:   &schedulerstate.MissedGap{From: at(13, 0), To: at(15, 0), Through: before(at(15, 0)), Reason: schedulerstate.GapSuspended, Slots: 2},
		},
		{name: "later slots", record: backfillOf("hourly", at(13, 0), at(14, 0)), want: &held},
		{name: "earlier slots", record: backfillOf("hourly", at(9, 0), at(10, 0)), want: &held},
		{name: "named slots", record: named, want: &held},
		{name: "slots by a date", record: byDate, want: &held},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tp, dagName := heldGap(t)
			changed := tp.coverGap(t.Context(), tc.record)
			assert.Equal(t, tc.want, missedGap(tp, dagName))
			assert.Equal(t, tc.want == nil || tc.want.Slots != held.Slots, changed)
		})
	}
}

// Backfills of a gap one part after another clear it, whether its first
// slot came due as the DAG was held or after the last tick before an
// outage.
func TestTickPlanner_BackfillsCoverGapInParts(t *testing.T) {
	t.Parallel()
	t.Run("held", func(t *testing.T) {
		t.Parallel()
		tp, dagName := heldGap(t)
		require.True(t, tp.coverGap(t.Context(), backfillOf(dagName, at(12, 0))))
		require.True(t, tp.coverGap(t.Context(), backfillOf(dagName, at(13, 0))))
		require.Equal(t, 1, missedGap(tp, dagName).Slots)
		require.True(t, tp.coverGap(t.Context(), backfillOf(dagName, at(14, 0))))
		assert.Nil(t, missedGap(tp, dagName))
	})
	t.Run("down", func(t *testing.T) {
		t.Parallel()
		tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(9, 0))})
		dag := &ir.DAG{Name: "hourly", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
		require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
		require.Equal(t, 2, missedGap(tp, dag.Name).Slots)

		require.True(t, tp.coverGap(t.Context(), backfillOf(dag.Name, at(9, 0), at(10, 0))))
		assert.Equal(t, &schedulerstate.MissedGap{
			From: at(10, 0), To: at(12, 0), Through: before(at(12, 0)), Reason: schedulerstate.GapDown, Slots: 1,
		}, missedGap(tp, dag.Name))
		require.True(t, tp.coverGap(t.Context(), backfillOf(dag.Name, at(11, 0))))
		assert.Nil(t, missedGap(tp, dag.Name))
	})
}

// A gap counted to the cap holds more slots than it counts, so a backfill
// of its first part leaves it counting the slots after them again.
func TestTickPlanner_BackfillRecountsCappedGap(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(11, 59))})
	held := true
	tp.cfg.IsSuspended = func(context.Context, string) (bool, error) { return held, nil }
	dag := &ir.DAG{Name: "minutely", Schedule: []ir.Schedule{mustParseSchedule(t, "* * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	tp.Plan(t.Context(), at(12, 0))
	held = false
	// Eight days hold 11,520 slots.
	tp.Plan(t.Context(), at(12, 0).AddDate(0, 0, 8))
	require.Equal(t, schedulerstate.MaxGapSlots, missedGap(tp, dag.Name).Slots)

	var slots []time.Time
	for i := range 100 {
		slots = append(slots, at(12, i))
	}
	require.True(t, tp.coverGap(t.Context(), backfillOf(dag.Name, slots...)))
	assert.Equal(t, schedulerstate.MaxGapSlots, missedGap(tp, dag.Name).Slots)

	// 2,000 more leave 9,420, which the gap then counts exactly, without
	// the slot of the tick that ended the hold, which ran.
	slots = slots[:0]
	for i := 100; i < 2100; i++ {
		slots = append(slots, at(12, i))
	}
	require.True(t, tp.coverGap(t.Context(), backfillOf(dag.Name, slots...)))
	require.Equal(t, 9420, missedGap(tp, dag.Name).Slots)
	slots = slots[:0]
	for i := 2100; i < 11520; i++ {
		slots = append(slots, at(12, i))
	}
	require.True(t, tp.coverGap(t.Context(), backfillOf(dag.Name, slots...)))
	assert.Nil(t, missedGap(tp, dag.Name))
}

// A gap shortened after its DAG's schedule changed counts the slots left
// under the schedule as it is, so a backfill of slots the gap never held
// does not clear the ones it still holds.
func TestTickPlanner_BackfillCoversGapAfterScheduleEdit(t *testing.T) {
	t.Parallel()
	tp, dagName := heldGap(t)
	tp.entryMu.Lock()
	tp.entries[dagName].DAG = &ir.DAG{Name: dagName, Schedule: []ir.Schedule{mustParseSchedule(t, "*/30 * * * *")}}
	tp.entryMu.Unlock()

	require.True(t, tp.coverGap(t.Context(), backfillOf(dagName, at(12, 0), at(12, 30), at(13, 0))))
	gap := missedGap(tp, dagName)
	require.NotNil(t, gap, "14:00 is still missed")
	assert.Equal(t, at(13, 30), gap.From)
}
