// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
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

// restartPlanner starts a planner at now from the state and catch-up slots
// the previous one last saved, as the next scheduler does, with a recovery
// window of recovery. It records the periods in which no scheduler ran, as
// recordedPeriods gives them.
func restartPlanner(t *testing.T, previous *mockStateStore, now time.Time, recovery time.Duration, dag *ir.DAG) (*TickPlanner, *mockStateStore) {
	t.Helper()
	store := &mockStateStore{state: previous.lastSaved(), catchup: previous.savedCatchup()}
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

// The slots a startup queues stay out of the scheduler state record, which
// readers of the state copy whole, and a restart still replays them.
func TestTickPlanner_KeepsCatchupOutOfState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newStore := func() *store.SchedulerStateStore {
		return store.NewSchedulerStateStore(file.NewCollection(dir, file.WithIndentedJSON()))
	}
	states := newStore()
	require.NoError(t, states.Save(t.Context(), newMockState(at(6, 0))))
	dag := &ir.DAG{Name: "minutely", CatchupWindow: 6 * time.Hour, Schedule: []ir.Schedule{mustParseSchedule(t, "* * * * *")}}
	tp, _ := newTestTickPlanner(states)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	tp.cfg.Enqueue = enqueueOK
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	require.Len(t, bufferedSlots(tp, dag.Name), 360)
	runTick(t, tp, at(12, 0))
	queued := bufferedSlots(tp, dag.Name)
	require.Len(t, queued, 359)

	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	require.NoError(t, err)
	var record struct {
		DAGs map[string]map[string]json.RawMessage `json:"dags"`
	}
	require.NoError(t, json.Unmarshal(raw, &record))
	require.Contains(t, record.DAGs, dag.Name)
	_, saved := record.DAGs[dag.Name]["catchup"]
	assert.False(t, saved, "state.json holds catch-up slots")

	restarted, _ := newTestTickPlanner(newStore())
	restarted.cfg.Clock = func() time.Time { return at(12, 0).Add(45 * time.Second) }
	require.NoError(t, restarted.Init(t.Context(), testDAGEntries(dag)))
	assert.Equal(t, queued, bufferedSlots(restarted, dag.Name))
}

// A DAG that goes idle between the catch-up check and the live one runs its
// live slot, which moves the last scheduled time past the queue, and its
// next catch-up run moves it back. A restart in between still leaves out a
// slot an edit dropped from the queue meanwhile.
func TestTickPlanner_ReplayAfterWatermarkMovesBack(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{state: newMockState(at(7, 0))}
	tp, _ := newTestTickPlanner(store)
	now := at(12, 0).Add(30 * time.Second)
	tp.cfg.Clock = func() time.Time { return now }
	tp.cfg.Enqueue = enqueueOK
	var running []bool
	tp.cfg.IsRunning = func(context.Context, *ir.DAG) (bool, error) {
		if len(running) == 0 {
			return false, nil
		}
		answer := running[0]
		running = running[1:]
		return answer, nil
	}
	even := mustParseSchedule(t, "0 */2 * * *")
	dag := &ir.DAG{Name: "etl", CatchupWindow: 6 * time.Hour, OverlapPolicy: ir.OverlapPolicyAll,
		Schedule: []ir.Schedule{mustParseSchedule(t, "0 10 * * *"), even}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	require.Equal(t, []time.Time{at(8, 0), at(10, 0), at(12, 0)}, bufferedSlots(tp, dag.Name))
	running = []bool{true, true}
	runTick(t, tp, at(12, 0))

	running = []bool{true, false}
	runTick(t, tp, at(14, 0))
	require.Equal(t, at(14, 0), tp.dagWatermark(dag.Name).LastScheduledTime, "the live slot ran")
	now = at(14, 0).Add(30 * time.Second)
	edited := *dag
	edited.Schedule = []ir.Schedule{even}
	tp.entryMu.Lock()
	tp.applyEvent(t.Context(), DAGChangeEvent{Type: DAGChangeUpdated, DAGEntry: testDAGEntries(&edited)[0]})
	tp.entryMu.Unlock()
	require.Equal(t, []time.Time{at(8, 0), at(12, 0)}, bufferedSlots(tp, dag.Name))
	for _, run := range tp.Plan(t.Context(), at(14, 1)) {
		tp.DispatchRun(t.Context(), run)
	}
	require.Equal(t, at(8, 0), tp.dagWatermark(dag.Name).LastScheduledTime, "the catch-up run moved it back")
	// The periodic flusher saves the tick before its Advance.
	tp.Flush(t.Context())

	restarted, _ := restartPlanner(t, store, at(14, 1).Add(30*time.Second), 0, &edited)
	assert.Equal(t, []time.Time{at(12, 0)}, bufferedSlots(restarted, dag.Name), "10:00 left the queue")
}

// An event that saves one DAG's queue leaves the saved queue of another
// as it was, so a restart replays the slot the other was dispatching.
func TestTickPlanner_ReplaysSlotDispatchedDuringEvent(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{state: newMockState(at(9, 0))}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	tp.cfg.Enqueue = enqueueOK
	etl, report := newHourlyCatchupDAG(t, "etl"), newHourlyCatchupDAG(t, "report")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(etl, report)))
	runTick(t, tp, at(12, 1))
	require.Len(t, tp.Plan(t.Context(), at(12, 2)), 2, "both dispatch 11:00")

	tp.entryMu.Lock()
	tp.applyEvent(t.Context(), DAGChangeEvent{Type: DAGChangeDeleted, DAGEntry: testDAGEntries(report)[0]})
	tp.entryMu.Unlock()
	tp.Flush(t.Context())

	restarted, _ := restartPlanner(t, store, at(12, 2).Add(30*time.Second), 0, etl)
	assert.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(restarted, etl.Name))
}

// A catch-up dispatch that panics, which the scheduler recovers, takes its
// slot off the queue without settling it. A restart replays the queue as
// it stands, without that slot.
func TestTickPlanner_ReplayLeavesOutSlotLostToPanic(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{state: newMockState(at(9, 0))}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	tp.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time, string) error {
		panic("enqueue")
	}
	dag := newHourlyCatchupDAG(t, "hourly")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	for _, run := range tp.Plan(t.Context(), at(12, 1)) {
		require.Panics(t, func() { tp.DispatchRun(t.Context(), run) })
	}
	tp.Advance(at(12, 1))
	tp.Flush(t.Context())

	restarted, _ := restartPlanner(t, store, at(12, 1).Add(30*time.Second), 0, dag)
	assert.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(restarted, dag.Name))
}

// A late first tick queues the slots that came due while startup ran, and
// a restart replays them, though the late tick's LastTick passed them.
func TestTickPlanner_ReplaysSlotsQueuedByLateFirstTick(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{state: newMockState(at(9, 0))}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(11, 59).Add(30 * time.Second) }
	tp.cfg.Enqueue = enqueueOK
	dag := newHourlyCatchupDAG(t, "hourly")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	require.Equal(t, []time.Time{at(10, 0), at(11, 0)}, bufferedSlots(tp, dag.Name))
	runTick(t, tp, at(12, 1))
	require.Equal(t, []time.Time{at(11, 0), at(12, 0)}, bufferedSlots(tp, dag.Name), "the late first tick queued 12:00")

	restarted, _ := restartPlanner(t, store, at(12, 1).Add(30*time.Second), 0, dag)
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

// A startup that runs past the minute Init read makes the first tick late,
// and the slots between come due while no tick runs. The first tick records
// them as a time no scheduler ran, from the minute before the one Init
// counted on, which leaves out the 11:00 a takeover's recovery replays.
// Catch-up queues them.
func TestTickPlanner_LateFirstTickKeepsSkippedSlots(t *testing.T) {
	t.Parallel()
	skipped := schedulerstate.Period{From: at(11, 58), To: at(12, 1), Reason: schedulerstate.PeriodDown}
	for _, tc := range []struct {
		name     string
		lastTick time.Time
		recovery time.Duration
		window   time.Duration
		queued   []time.Time
		down     []schedulerstate.Period
	}{
		{
			name:     "restart",
			lastTick: at(9, 0),
			down:     []schedulerstate.Period{{From: at(9, 0), To: at(11, 59), Reason: schedulerstate.PeriodDown}, skipped},
		},
		{
			name: "first scheduler",
			down: []schedulerstate.Period{skipped},
		},
		{
			name:     "takeover",
			lastTick: at(9, 0),
			recovery: time.Hour,
			down:     []schedulerstate.Period{{From: at(9, 0), To: at(10, 59).Add(30 * time.Second), Reason: schedulerstate.PeriodDown}, skipped},
		},
		{
			name:     "with catch-up",
			lastTick: at(9, 0),
			window:   6 * time.Hour,
			queued:   []time.Time{at(11, 0), at(12, 0)},
			down:     []schedulerstate.Period{{From: at(9, 0), To: at(11, 59), Reason: schedulerstate.PeriodDown}, skipped},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(tc.lastTick)})
			tp.cfg.Clock = func() time.Time { return at(11, 59).Add(30 * time.Second) }
			tp.cfg.RecoveryWindow = func() time.Duration { return tc.recovery }
			tp.cfg.Enqueue = enqueueOK
			tp.cfg.Periods = &periodRecorder{}
			dag := &ir.DAG{Name: "hourly", CatchupWindow: tc.window, Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))

			runTick(t, tp, at(12, 1))
			assert.Equal(t, tc.queued, bufferedSlots(tp, dag.Name))
			assert.Equal(t, tc.down, recordedPeriods(t, tp))
		})
	}
}

// Init settles the slots before the minute it counts the first tick on,
// though a write during startup lasts into the next minute. A takeover's
// replay then holds no slot that the late first tick counts as missed.
func TestTickPlanner_SlowStartupReplaysNoMissedSlot(t *testing.T) {
	t.Parallel()
	tp, _ := newTestTickPlanner(&mockStateStore{state: newMockState(at(9, 0))})
	now := at(11, 59).Add(30 * time.Second)
	tp.cfg.Clock = func() time.Time { return now }
	tp.cfg.RecoveryWindow = func() time.Duration { return time.Hour }
	tp.cfg.Enqueue = enqueueOK
	tp.cfg.Periods = &slowPeriods{onAdd: func() { now = at(12, 0).Add(time.Second) }}
	dag := &ir.DAG{Name: "twice", Schedule: []ir.Schedule{mustParseSchedule(t, "30,59 11 * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	assert.Equal(t, []time.Time{at(11, 30)}, bufferedSlots(tp, dag.Name))

	runTick(t, tp, at(12, 0))
	assert.Equal(t, []schedulerstate.Period{
		{From: at(9, 0), To: at(10, 59).Add(30 * time.Second), Reason: schedulerstate.PeriodDown},
		{From: at(11, 58), To: at(12, 0), Reason: schedulerstate.PeriodDown},
	}, recordedPeriods(t, tp))
}

// slowPeriods records periods as periodRecorder does, and calls onAdd as
// each outage is written, as a write that takes time.
type slowPeriods struct {
	periodRecorder
	onAdd func()
}

func (p *slowPeriods) AddDown(ctx context.Context, down schedulerstate.Period) error {
	p.onAdd()
	return p.periodRecorder.AddDown(ctx, down)
}

// The missed-slots notice counts the slots of an outage that catch-up does
// not queue when a scheduler starts again or ticks late: the older slots of
// a window, those past its cap, those overlap_policy latest drops, and all
// of them without a window or queues.
func TestCatchupAfterOutageIsWhatCatchupQueues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		expr     string
		window   time.Duration
		latest   bool
		noQueues bool
	}{
		{name: "window", expr: "* * * * *", window: time.Hour},
		{name: "window past the cap", expr: "* * * * *", window: 24 * time.Hour},
		{name: "latest", expr: "30 * * * *", window: 3 * time.Hour, latest: true},
		{name: "no window", expr: "* * * * *"},
		{name: "queues disabled", expr: "* * * * *", window: time.Hour, noQueues: true},
	} {
		for _, late := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, late tick %t", tc.name, late), func(t *testing.T) {
				t.Parallel()
				dag := &ir.DAG{Name: "dag", CatchupWindow: tc.window, Schedule: []ir.Schedule{mustParseSchedule(t, tc.expr)}}
				if tc.latest {
					dag.OverlapPolicy = ir.OverlapPolicyLatest
				}
				stopped := at(12, 0).Add(-26 * time.Hour)
				periods := &periodRecorder{}
				var tp *TickPlanner
				if late {
					tp, _ = newTestTickPlanner(&mockStateStore{})
					tp.cfg.Clock = func() time.Time { return stopped }
					tp.cfg.QueuesEnabled = !tc.noQueues
					require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
					tp.Advance(stopped)
					tp.cfg.Periods = periods
					tp.resume(t.Context(), at(12, 0))
				} else {
					tp, _ = newTestTickPlanner(&mockStateStore{state: newMockState(stopped)})
					tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
					tp.cfg.QueuesEnabled = !tc.noQueues
					tp.cfg.Periods = periods
					require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
				}
				down, _, err := periods.Ended(t.Context(), "")
				require.NoError(t, err)
				require.Equal(t, []schedulerstate.Period{{From: stopped, To: at(12, 0), Reason: schedulerstate.PeriodDown}}, down)

				// The outage's slots that catch-up did not queue.
				inside, more := ir.ScheduleFiresBetween(dag.Schedule, "", time.UTC, stopped.Add(time.Second), at(12, 0).Add(-time.Second), 10000)
				require.False(t, more)
				var counted []time.Time
				for _, slot := range inside {
					if !slices.ContainsFunc(bufferedSlots(tp, dag.Name), slot.Equal) {
						counted = append(counted, slot)
					}
				}
				require.NotEmpty(t, counted)

				sched := backfill.SlotSchedule{Schedules: dag.Schedule, Location: time.UTC}
				missed := backfill.FindMissed(dag, sched, down, CatchupAfterOutage(dag, !tc.noQueues), stopped, at(13, 0))
				assert.Equal(t, len(counted), missed.Slots)
				assert.Equal(t, counted[0], missed.First)
				assert.Equal(t, counted[len(counted)-1], missed.Last)
			})
		}
	}
}

// The saved catch-up slots are written again only when a restart from them
// would replay other slots than the queues hold: not as catch-up runs a
// queue, nor when a suspension settles it, while another queue holds
// slots. Once the last queue runs out, they are written once, empty.
func TestTickPlanner_CatchupWrites(t *testing.T) {
	t.Parallel()
	apply := func(t *testing.T, tp *TickPlanner, change DAGChangeType, dag *ir.DAG) {
		t.Helper()
		tp.entryMu.Lock()
		tp.applyEvent(t.Context(), DAGChangeEvent{Type: change, DAGEntry: testDAGEntries(dag)[0]})
		tp.entryMu.Unlock()
		tp.Flush(t.Context())
	}
	for _, tc := range []struct {
		name   string
		act    func(t *testing.T, tp *TickPlanner, store *mockStateStore, dag *ir.DAG)
		writes int
	}{
		{name: "drain one queue", act: func(t *testing.T, tp *TickPlanner, _ *mockStateStore, dag *ir.DAG) {
			runTick(t, tp, at(12, 1))
			runTick(t, tp, at(12, 2))
			require.Empty(t, bufferedSlots(tp, "batch"))
			require.NotEmpty(t, bufferedSlots(tp, dag.Name))
		}},
		{name: "drain the last queue", writes: 1, act: func(t *testing.T, tp *TickPlanner, store *mockStateStore, dag *ir.DAG) {
			for minute := range 6 {
				runTick(t, tp, at(12, minute+1))
			}
			require.Empty(t, bufferedSlots(tp, dag.Name))
			assert.Empty(t, store.savedCatchup(), "a restart reads no slots")
		}},
		{name: "late tick", writes: 1, act: func(t *testing.T, tp *TickPlanner, _ *mockStateStore, _ *ir.DAG) {
			tp.resume(t.Context(), at(14, 0))
			runTick(t, tp, at(14, 0))
		}},
		{name: "edit dropping queued slots", writes: 1, act: func(t *testing.T, tp *TickPlanner, _ *mockStateStore, dag *ir.DAG) {
			edited := *dag
			edited.Schedule = []ir.Schedule{mustParseSchedule(t, "0 */2 * * *")}
			apply(t, tp, DAGChangeUpdated, &edited)
			require.Equal(t, []time.Time{at(8, 0), at(10, 0), at(12, 0)}, bufferedSlots(tp, dag.Name))
		}},
		{name: "delete", writes: 1, act: func(t *testing.T, tp *TickPlanner, _ *mockStateStore, dag *ir.DAG) {
			apply(t, tp, DAGChangeDeleted, dag)
		}},
		{name: "catch-up disabled", writes: 1, act: func(t *testing.T, tp *TickPlanner, _ *mockStateStore, dag *ir.DAG) {
			edited := *dag
			edited.CatchupWindow = 0
			apply(t, tp, DAGChangeUpdated, &edited)
		}},
		{name: "enqueue unset", writes: 1, act: func(t *testing.T, tp *TickPlanner, _ *mockStateStore, _ *ir.DAG) {
			tp.cfg.Enqueue = nil
			runTick(t, tp, at(12, 1))
		}},
		{name: "suspension", act: func(t *testing.T, tp *TickPlanner, _ *mockStateStore, dag *ir.DAG) {
			tp.cfg.IsSuspended = func(_ context.Context, id string) (bool, error) { return id == dag.SuspendFlagName(), nil }
			runTick(t, tp, at(12, 1))
			require.Empty(t, bufferedSlots(tp, dag.Name))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &mockStateStore{state: newMockState(at(6, 0))}
			tp, _ := newTestTickPlanner(store)
			tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
			tp.cfg.Enqueue = enqueueOK
			dag := newHourlyCatchupDAG(t, "hourly")
			batch := &ir.DAG{Name: "batch", CatchupWindow: 6 * time.Hour, Schedule: []ir.Schedule{mustParseSchedule(t, "0 */3 * * *")}}
			require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag, batch)))
			require.Len(t, bufferedSlots(tp, dag.Name), 6)
			require.Len(t, bufferedSlots(tp, batch.Name), 2)
			startup := store.catchupWriteCount()

			tc.act(t, tp, store, dag)
			assert.Equal(t, tc.writes, store.catchupWriteCount()-startup)
		})
	}
}

// A failed write of the catch-up slots is retried on the next flush, and
// the state is saved meanwhile.
func TestTickPlanner_RetriesFailedCatchupWrite(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{state: newMockState(at(6, 0)), catchupErr: errors.New("disk full")}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	dag := &ir.DAG{Name: "hourly", CatchupWindow: 2 * time.Hour, Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	require.Equal(t, 1, store.catchupWriteCount())
	require.Contains(t, store.lastSaved().DAGs, dag.Name, "the state is saved")

	store.catchupErr = nil
	tp.Flush(t.Context())
	assert.Equal(t, map[string][]time.Time{dag.Name: {at(11, 0), at(12, 0)}}, store.savedCatchup())
}

// A queue that changes while Flush writes the catch-up slots is saved by
// the next flush.
func TestTickPlanner_SavesCatchupChangedDuringWrite(t *testing.T) {
	t.Parallel()
	store := &mockStateStore{state: newMockState(at(9, 0))}
	tp, _ := newTestTickPlanner(store)
	tp.cfg.Clock = func() time.Time { return at(12, 0).Add(30 * time.Second) }
	etl, report, billing := newHourlyCatchupDAG(t, "etl"), newHourlyCatchupDAG(t, "report"), newHourlyCatchupDAG(t, "billing")
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(etl, report, billing)))
	deleteDAG := func(dag *ir.DAG) {
		tp.entryMu.Lock()
		defer tp.entryMu.Unlock()
		tp.applyEvent(t.Context(), DAGChangeEvent{Type: DAGChangeDeleted, DAGEntry: testDAGEntries(dag)[0]})
	}

	deleteDAG(etl)
	store.onSaveCatchup = func() {
		store.onSaveCatchup = nil
		deleteDAG(report)
	}
	tp.Flush(t.Context())
	tp.Flush(t.Context())
	assert.Equal(t, []string{billing.Name}, slices.Sorted(maps.Keys(store.savedCatchup())))
}
