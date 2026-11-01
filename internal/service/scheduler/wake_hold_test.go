// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/require"
)

// memPauseStore is a pause store in memory, written as the API and the
// scheduler would.
type memPauseStore struct {
	mu    sync.Mutex
	pause schedulerstate.Pause
}

func (s *memPauseStore) IsPaused(ctx context.Context) (bool, error) {
	pause, err := s.Get(ctx)
	return pause.Paused, err
}

func (s *memPauseStore) Get(context.Context) (schedulerstate.Pause, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pause, nil
}

func (s *memPauseStore) Set(_ context.Context, paused bool, actor, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pause = schedulerstate.Pause{Paused: paused}
	if paused {
		s.pause.PausedAt, s.pause.PausedBy, s.pause.Reason = time.Now(), actor, reason
	}
	return nil
}

func (s *memPauseStore) SetHold(_ context.Context, hold bool, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case hold && s.pause.Paused:
		return false, nil
	case hold:
		s.pause = schedulerstate.Pause{Paused: true, Hold: true, PausedAt: time.Now(), PausedBy: schedulerstate.PausedByScheduler, Reason: reason}
	case !s.pause.WakeHold():
		return false, nil
	default:
		s.pause = schedulerstate.Pause{}
	}
	return true, nil
}

func (s *memPauseStore) state() schedulerstate.Pause {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pause
}

// wakeLoop runs a cron loop whose clock jumps from noon to five past after
// its first tick, as a host asleep for five minutes would see; the DAG has no
// catch-up window and fires at the minutes given. It returns the runs
// dispatched and the clock to move.
func wakeLoop(t *testing.T, limit time.Duration, pause *memPauseStore, minutes string) (chan PlannedRun, func(time.Time)) {
	t.Helper()
	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	wake := base.Add(5 * time.Minute)
	var now atomic.Int64
	now.Store(base.UnixNano())
	clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
	store := &mockStateStore{state: newMockState(base)}
	planner, _ := newTestTickPlanner(store)
	planner.cfg.Clock = clock
	planner.cfg.Location = time.UTC
	dag := &ir.DAG{Name: "wake", OverlapPolicy: ir.OverlapPolicyAll,
		Schedule: []ir.Schedule{mustParseSchedule(t, minutes+" * * * *")}}
	require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
	runs := make(chan PlannedRun, 10)
	record := func(_ context.Context, entry DAGEntry, id string, trigger ir.TriggerType, scheduled time.Time) error {
		runs <- PlannedRun{DAGEntry: entry, RunID: id, TriggerType: trigger, ScheduledTime: scheduled}
		return nil
	}
	planner.cfg.Dispatch = record
	planner.cfg.Enqueue = record
	store.onSave = func(state *schedulerstate.State) {
		if state.LastTick.Equal(base) {
			now.Store(wake.UnixNano())
		}
	}
	sc := &Scheduler{planner: planner, clock: clock, quit: make(chan any),
		wakeHold: wakeHold{limit: limit, store: pause, poll: 10 * time.Millisecond}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); sc.cronLoop(ctx, make(chan os.Signal)) }()
	t.Cleanup(func() { cancel(); <-done })
	return runs, func(at time.Time) { now.Store(at.UnixNano()) }
}

func awaitHold(t *testing.T, pause *memPauseStore) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !pause.state().WakeHold() {
		if time.Now().After(deadline) {
			t.Fatalf("the scheduler did not hold after the wake: %+v", pause.state())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func nextRun(t *testing.T, runs chan PlannedRun) PlannedRun {
	t.Helper()
	select {
	case run := <-runs:
		return run
	case <-time.After(2 * time.Second):
		t.Fatal("no run came")
		return PlannedRun{}
	}
}

func noRun(t *testing.T, runs chan PlannedRun, within time.Duration) {
	t.Helper()
	select {
	case run := <-runs:
		t.Fatalf("a run came while the scheduler held: %s", run.ScheduledTime)
	case <-time.After(within):
	}
}

func TestWakeHoldKeepsTheTicksAfterAWakeUntilReleased(t *testing.T) {
	pause := &memPauseStore{}
	runs, setClock := wakeLoop(t, 10*time.Minute, pause, "2,4,5,6")
	awaitHold(t, pause)
	state := pause.state()
	require.Equal(t, schedulerstate.PausedByScheduler, state.PausedBy)
	require.Equal(t, wakeHoldReason, state.Reason)
	// The slot of the minute the host woke in waits, as every one-off would.
	noRun(t, runs, 200*time.Millisecond)

	// A minute on, the pause is cleared through the API: the held minutes
	// run, oldest first, and the loop goes on from the minute under way.
	wake := time.Date(2026, 1, 2, 12, 5, 0, 0, time.UTC)
	setClock(wake.Add(time.Minute + 10*time.Second))
	require.NoError(t, pause.Set(t.Context(), false, "", ""))
	for _, want := range []time.Time{wake, wake.Add(time.Minute)} {
		run := nextRun(t, runs)
		require.True(t, want.Equal(run.ScheduledTime), "unexpected scheduled time: %s", run.ScheduledTime)
		require.Equal(t, ir.TriggerTypeScheduler, run.TriggerType)
	}
	noRun(t, runs, 100*time.Millisecond)
	require.False(t, pause.state().Paused)
}

func TestWakeHoldEndsAfterItsDuration(t *testing.T) {
	pause := &memPauseStore{}
	runs, setClock := wakeLoop(t, 2*time.Minute, pause, "2,4,5,6")
	awaitHold(t, pause)
	noRun(t, runs, 100*time.Millisecond)
	// Nobody comes to release it; past its duration the scheduler lets
	// itself go and lifts the hold it placed.
	wake := time.Date(2026, 1, 2, 12, 5, 0, 0, time.UTC)
	setClock(wake.Add(2*time.Minute + 30*time.Second))
	for _, want := range []time.Time{wake, wake.Add(time.Minute)} {
		run := nextRun(t, runs)
		require.True(t, want.Equal(run.ScheduledTime), "unexpected scheduled time: %s", run.ScheduledTime)
	}
	require.False(t, pause.state().Paused)
}

func TestWakeHoldDefersToAnOperatorsPause(t *testing.T) {
	pause := &memPauseStore{}
	require.NoError(t, pause.Set(t.Context(), true, "alice", "maintenance"))
	runs, _ := wakeLoop(t, 10*time.Minute, pause, "2,4,5,6")
	// The tick runs as usual; the operator's pause, not a hold, governs what
	// it dispatches (the test planner's guards ignore the pause).
	run := nextRun(t, runs)
	require.True(t, time.Date(2026, 1, 2, 12, 5, 0, 0, time.UTC).Equal(run.ScheduledTime))
	state := pause.state()
	require.Equal(t, "alice", state.PausedBy)
	require.Equal(t, "maintenance", state.Reason)
	require.False(t, state.Hold)
}

func TestWakeHoldIsOffByDefault(t *testing.T) {
	pause := &memPauseStore{}
	runs, _ := wakeLoop(t, 0, pause, "2,4,5,6")
	run := nextRun(t, runs)
	require.True(t, time.Date(2026, 1, 2, 12, 5, 0, 0, time.UTC).Equal(run.ScheduledTime))
	require.False(t, pause.state().Paused)
}

func TestASuspensionCheckerLetsAWakeHoldThrough(t *testing.T) {
	pause := &memPauseStore{}
	perDAG := func(context.Context, string) (bool, error) { return false, nil }
	check := newSuspensionChecker(pause, perDAG)

	held, err := pause.SetHold(t.Context(), true, wakeHoldReason)
	require.NoError(t, err)
	require.True(t, held)
	suspended, err := check(t.Context(), "dag")
	require.NoError(t, err)
	require.False(t, suspended, "a wake hold keeps the scheduler's ticks back, not the queue")

	require.NoError(t, pause.Set(t.Context(), true, "alice", "maintenance"))
	suspended, err = check(t.Context(), "dag")
	require.NoError(t, err)
	require.True(t, suspended, "an operator's pause suspends every DAG")

	// A user who happens to bear the scheduler's name pauses like any other.
	require.NoError(t, pause.Set(t.Context(), true, schedulerstate.PausedByScheduler, "maintenance"))
	suspended, err = check(t.Context(), "dag")
	require.NoError(t, err)
	require.True(t, suspended, "only the store marks a hold")
}

func TestAStaleWakeHoldIsClearedAtStart(t *testing.T) {
	pause := &memPauseStore{}
	hold := newWakeHold(time.Minute, pause)
	held, err := pause.SetHold(t.Context(), true, wakeHoldReason)
	require.NoError(t, err)
	require.True(t, held)
	hold.clearStale(t.Context())
	require.False(t, pause.state().Paused)

	require.NoError(t, pause.Set(t.Context(), true, "alice", "maintenance"))
	hold.clearStale(t.Context())
	require.Equal(t, "alice", pause.state().PausedBy, "an operator's pause stays")
}
