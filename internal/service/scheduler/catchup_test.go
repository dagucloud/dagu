// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseSchedule(t *testing.T, expr string) ir.Schedule {
	t.Helper()
	parsed, err := cron.ParseStandard(expr)
	require.NoError(t, err)
	return ir.Schedule{
		Expression: expr,
		Parsed:     parsed,
	}
}

func TestComputeReplayFrom(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		catchupWindow     time.Duration
		lastTick          time.Time
		lastScheduledTime time.Time
		want              time.Time
	}{
		{
			name:              "window is the constraint",
			catchupWindow:     6 * time.Hour,
			lastTick:          time.Time{},
			lastScheduledTime: time.Time{},
			want:              now.Add(-6 * time.Hour),
		},
		{
			name:              "lastTick is most recent",
			catchupWindow:     6 * time.Hour,
			lastTick:          now.Add(-2 * time.Hour),
			lastScheduledTime: time.Time{},
			want:              now.Add(-2 * time.Hour),
		},
		{
			name:              "lastScheduledTime is most recent",
			catchupWindow:     6 * time.Hour,
			lastTick:          now.Add(-5 * time.Hour),
			lastScheduledTime: now.Add(-1 * time.Hour),
			want:              now.Add(-1 * time.Hour),
		},
		{
			name:              "all zero except window",
			catchupWindow:     24 * time.Hour,
			lastTick:          time.Time{},
			lastScheduledTime: time.Time{},
			want:              now.Add(-24 * time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeReplayFrom(tt.catchupWindow, tt.lastTick, tt.lastScheduledTime, now)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestComputeMissedIntervals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		schedules  []string
		replayFrom time.Time
		replayTo   time.Time
		wantCount  int
		wantFirst  time.Time
		wantLast   time.Time
	}{
		{
			name:       "hourly schedule 3h gap",
			schedules:  []string{"0 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  3,
			wantFirst:  time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		},
		{
			name:       "daily schedule 3d gap",
			schedules:  []string{"0 9 * * *"},
			replayFrom: time.Date(2026, 2, 4, 9, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  3,
			wantFirst:  time.Date(2026, 2, 5, 9, 0, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC),
		},
		{
			name:       "no missed intervals",
			schedules:  []string{"0 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  0,
		},
		{
			name:       "multiple schedules merged and deduped",
			schedules:  []string{"0 * * * *", "30 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 9, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC),
			wantCount:  4,
			wantFirst:  time.Date(2026, 2, 7, 9, 30, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC),
		},
		{
			name:       "replayFrom equals a schedule point is excluded",
			schedules:  []string{"0 * * * *"},
			replayFrom: time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC),
			replayTo:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
			wantCount:  2,
			wantFirst:  time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC),
			wantLast:   time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var schedules []ir.Schedule
			for _, expr := range tt.schedules {
				schedules = append(schedules, mustParseSchedule(t, expr))
			}

			got := ComputeMissedIntervals(schedules, tt.replayFrom, tt.replayTo)
			require.Len(t, got, tt.wantCount)

			if tt.wantCount > 0 {
				assert.Equal(t, tt.wantFirst, got[0])
				assert.Equal(t, tt.wantLast, got[len(got)-1])

				// Verify chronological order
				for i := 1; i < len(got); i++ {
					assert.True(t, got[i].After(got[i-1]),
						"expected %v after %v", got[i], got[i-1])
				}
			}
		})
	}
}

func TestComputeMissedIntervals_CappedAtMax(t *testing.T) {
	t.Parallel()

	// Per-minute schedule over 30 days = 43,200 intervals, should be capped at MaxMissedRuns
	schedules := []ir.Schedule{mustParseSchedule(t, "* * * * *")}
	replayFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	replayTo := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	got := ComputeMissedIntervals(schedules, replayFrom, replayTo)
	assert.LessOrEqual(t, len(got), MaxMissedRuns)
	// Should keep the most recent runs (closest to replayTo)
	if len(got) > 0 {
		assert.True(t, got[len(got)-1].Before(replayTo) || got[len(got)-1].Equal(replayTo))
	}
}

// Queued catch-up runs every slot it holds, also those a late tick's window
// has moved past, and the late tick records the time it missed as one
// period in which no scheduler ran.
func TestTickPlanner_LateTickKeepsQueuedCatchup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		late   time.Time
		queued []time.Time
	}{
		{
			name:   "queue reaches the window",
			late:   at(14, 0),
			queued: []time.Time{at(11, 0), at(12, 0), at(13, 0), at(14, 0)},
		},
		{
			name:   "queue before the window",
			late:   at(15, 0),
			queued: []time.Time{at(11, 0), at(12, 0), at(14, 0), at(15, 0)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(9, 0))})
			tp.cfg.Enqueue = enqueueOK
			dag := &ir.DAG{Name: "hourly", CatchupWindow: 2 * time.Hour, Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
			require.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(tp, dag.Name))
			tp.Advance(at(12, 0))
			periods := &periodRecorder{}
			tp.cfg.Periods = periods

			tp.resume(t.Context(), tc.late)
			assert.Equal(t, tc.queued, bufferedSlots(tp, dag.Name))
			recorded, _, err := periods.Ended(t.Context(), "")
			require.NoError(t, err)
			assert.Equal(t, []schedulerstate.Period{{From: at(12, 0), To: tc.late, Reason: schedulerstate.PeriodDown}}, recorded)
		})
	}
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 2, 7, hour, minute, 0, 0, time.UTC)
}

func bufferedSlots(tp *TickPlanner, dagName string) []time.Time {
	var slots []time.Time
	if buffer := tp.buffers[dagName]; buffer != nil {
		for _, item := range buffer.items {
			slots = append(slots, item.ScheduledTime)
		}
	}
	return slots
}

// enqueueOK accepts every catch-up enqueue.
func enqueueOK(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error {
	return nil
}

// restartPlanner starts a planner at now from the state the previous one last
// saved, as the next scheduler does, with a recovery window of recovery. It
// records the periods in which no scheduler ran, as recordedPeriods gives them.
func restartPlanner(t *testing.T, previous *mockStateStore, now time.Time, recovery time.Duration, dag *ir.DAG) (*TickPlanner, *mockStateStore) {
	t.Helper()
	store := &mockStateStore{state: previous.lastSaved()}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return now }
	tp.cfg.RecoveryWindow = func() time.Duration { return recovery }
	tp.cfg.Enqueue = enqueueOK
	tp.cfg.Periods = &periodRecorder{}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	return tp, store
}

// recordedPeriods returns the periods a planner that restartPlanner started
// recorded.
func recordedPeriods(t *testing.T, tp *TickPlanner) []schedulerstate.Period {
	t.Helper()
	recorded, _, err := tp.cfg.Periods.Ended(t.Context(), "")
	require.NoError(t, err)
	return recorded
}

// runTick plans, dispatches, and saves one tick, as the scheduler does.
func runTick(t *testing.T, tp *TickPlanner, tick time.Time) {
	t.Helper()
	for _, run := range tp.Plan(t.Context(), tick) {
		tp.DispatchRun(t.Context(), run)
	}
	tp.Advance(tick)
	tp.Flush(t.Context())
}

// A scheduler that stops while its catch-up is under way leaves the slots it
// queued and had not run. The next one replays them, though the ticks of the
// first ran past them, and replays nothing a finished or dropped catch-up
// left. Either way it records the time no scheduler ran, when a minute or
// more passed since the last tick.
func TestTickPlanner_ReplaysCatchupAfterStop(t *testing.T) {
	t.Parallel()
	stopped := []schedulerstate.Period{{From: at(12, 0), To: at(12, 3), Reason: schedulerstate.PeriodDown}}
	for _, tc := range []struct {
		name      string
		ticks     []time.Time
		suspended bool
		want      []time.Time
		down      []schedulerstate.Period
	}{
		{name: "under way", ticks: []time.Time{at(12, 0)}, want: []time.Time{at(11, 0), at(12, 0)}, down: stopped},
		{name: "finished", ticks: []time.Time{at(12, 0), at(12, 1), at(12, 2)}},
		{name: "dropped by a suspension", ticks: []time.Time{at(12, 0)}, suspended: true, down: stopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &mockStateStore{state: newMockState(at(9, 0))}
			tp, _ := newTestTickPlanner(store)
			tp.cfg.Enqueue = enqueueOK
			tp.cfg.IsSuspended = func(context.Context, string) (bool, error) { return tc.suspended, nil }
			dag := newHourlyCatchupDAG(t, "hourly")
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
			require.Equal(t, []time.Time{at(10, 0), at(11, 0), at(12, 0)}, bufferedSlots(tp, dag.Name))
			for _, tick := range tc.ticks {
				runTick(t, tp, tick)
			}

			restarted, _ := restartPlanner(t, store, at(12, 3).Add(30*time.Second), 0, dag)
			assert.Equal(t, tc.want, bufferedSlots(restarted, dag.Name))
			assert.Equal(t, tc.down, recordedPeriods(t, restarted))
		})
	}
}

// An edit made while catch-up is under way keeps the queued slots that the
// new schedules still fire, and a restart replays those only. Slots queued
// under schedules changed while no scheduler ran are not replayed. When the
// scheduler first tracked the DAG holds through any edit.
func TestTickPlanner_EditKeepsQueuedCatchup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		schedule string
		live     bool
		want     []time.Time
	}{
		{name: "same schedule while running", schedule: "0 * * * *", live: true, want: []time.Time{at(11, 0), at(12, 0)}},
		{name: "new schedule while running", schedule: "*/30 * * * *", live: true},
		{name: "new schedule while stopped", schedule: "*/30 * * * *"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &mockStateStore{state: newMockState(at(9, 0))}
			tp, _ := newTestTickPlanner(store)
			tp.cfg.Enqueue = enqueueOK
			dag := newHourlyCatchupDAG(t, "hourly")
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
			runTick(t, tp, at(12, 0))

			edited := *dag
			edited.Schedule = []ir.Schedule{mustParseSchedule(t, tc.schedule)}
			if tc.live {
				tp.entryMu.Lock()
				tp.applyEvent(t.Context(), DAGChangeEvent{Type: DAGChangeUpdated, DAGEntry: testDAGEntries(&edited)[0]})
				tp.entryMu.Unlock()
			}
			tp.Flush(t.Context())

			restarted, _ := restartPlanner(t, store, at(12, 1).Add(30*time.Second), 0, &edited)
			assert.Equal(t, tc.want, bufferedSlots(restarted, dag.Name))
			assert.Equal(t, at(12, 0), restarted.dagWatermark(dag.Name).FirstSeen)
		})
	}
}

// A late tick whose window moved past the queue, or past the last scheduled
// slot when nothing is queued, counts the slots between as missed without
// queueing them. A takeover whose recovery window reaches back over them
// does not replay them.
func TestTickPlanner_ReplayLeavesOutSkippedSlots(t *testing.T) {
	t.Parallel()
	caughtUp := newMockState(at(12, 0))
	caughtUp.DAGs["minutely"] = schedulerstate.DAGWatermark{LastScheduledTime: at(12, 0)}
	for _, tc := range []struct {
		name   string
		state  *schedulerstate.State
		queued []time.Time
	}{
		{name: "queue under way", state: newMockState(at(11, 59)), queued: []time.Time{at(12, 0)}},
		{name: "queue empty", state: caughtUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &mockStateStore{state: tc.state}
			tp, _ := newTestTickPlanner(store)
			tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
			tp.cfg.Enqueue = enqueueOK
			dag := &ir.DAG{Name: "minutely", CatchupWindow: time.Minute, Schedule: []ir.Schedule{mustParseSchedule(t, "* * * * *")}}
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
			require.Equal(t, tc.queued, bufferedSlots(tp, dag.Name))
			tp.resume(t.Context(), at(12, 3))
			require.Equal(t, append(tc.queued, at(12, 3)), bufferedSlots(tp, dag.Name))
			tp.Advance(at(12, 3))
			tp.Flush(t.Context())

			takeover, _ := restartPlanner(t, store, at(12, 3).Add(30*time.Second), 2*time.Minute, dag)
			assert.Equal(t, []time.Time{at(12, 3)}, bufferedSlots(takeover, dag.Name), "12:02 was counted as missed, not queued")
		})
	}
}

// A slot a live tick declined, as the DAG was still running, is not queued
// by the catch-up of a later startup, so a restart before that catch-up runs
// does not replay it either.
func TestTickPlanner_ReplayLeavesOutDeclinedSlot(t *testing.T) {
	t.Parallel()
	state := newMockState(at(12, 0))
	state.DAGs["hourly"] = schedulerstate.DAGWatermark{LastScheduledTime: at(12, 0)}
	store := &mockStateStore{state: state}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return true, nil }
	dag := newHourlyCatchupDAG(t, "hourly")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	require.Empty(t, tp.Plan(t.Context(), at(13, 0)), "13:00 is declined")
	tp.Advance(at(13, 0))
	tp.Advance(at(13, 30))
	tp.Flush(t.Context())

	first, firstStore := restartPlanner(t, store, at(15, 10).Add(30*time.Second), 0, dag)
	require.Equal(t, []time.Time{at(14, 0), at(15, 0)}, bufferedSlots(first, dag.Name))

	second, _ := restartPlanner(t, firstStore, at(15, 11).Add(30*time.Second), 0, dag)
	assert.Equal(t, []time.Time{at(14, 0), at(15, 0)}, bufferedSlots(second, dag.Name))
}

// The periodic flusher can save a tick's state after Plan settled a queued
// slot and before Advance saved the shorter queue. A restart from that state
// does not replay the slot, here one the overlap policy skipped.
func TestTickPlanner_ReplayLeavesOutSlotSettledBeforeAdvance(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{state: newMockState(at(9, 0))}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	tp.cfg.Enqueue = enqueueOK
	tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return true, nil }
	dag := newHourlyCatchupDAG(t, "hourly")
	dag.OverlapPolicy = ir.OverlapPolicySkip
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	require.Equal(t, []time.Time{at(10, 0), at(11, 0), at(12, 0)}, bufferedSlots(tp, dag.Name))
	tp.Plan(t.Context(), at(12, 1))
	require.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(tp, dag.Name), "10:00 is skipped")
	tp.Flush(t.Context())

	restarted, _ := restartPlanner(t, store, at(12, 2).Add(30*time.Second), 0, dag)
	assert.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(restarted, dag.Name))
}

// A startup leaves the slots before its catch-up window out of the queue.
// A takeover whose recovery window reaches back over them does not replay
// them while the queue waits for a run of the DAG to finish.
func TestTickPlanner_TakeoverLeavesOutSlotsBeforeWindow(t *testing.T) {
	t.Parallel()
	state := newMockState(at(9, 0))
	state.DAGs["hourly"] = schedulerstate.DAGWatermark{LastScheduledTime: at(9, 0)}
	store := &mockStateStore{state: state}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	tp.cfg.Enqueue = enqueueOK
	tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) { return true, nil }
	dag := &ir.DAG{Name: "hourly", CatchupWindow: 2 * time.Hour, OverlapPolicy: ir.OverlapPolicyAll,
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	require.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(tp, dag.Name))
	runTick(t, tp, at(12, 1))

	takeover, _ := restartPlanner(t, store, at(12, 2).Add(30*time.Second), 4*time.Hour, dag)
	assert.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(takeover, dag.Name))
}
