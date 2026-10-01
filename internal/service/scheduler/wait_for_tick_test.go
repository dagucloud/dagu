// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/signalctx"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/require"
)

func TestCronLoopWakeGap(t *testing.T) {
	for _, mode := range []string{"catchup", "disabled", "queues-disabled"} {
		t.Run(mode, func(t *testing.T) {
			base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
			wake := base.Add(5 * time.Minute)
			var now atomic.Int64
			now.Store(base.UnixNano())
			clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
			store := &mockStateStore{state: newMockState(base)}
			planner, _ := newTestTickPlanner(store)
			planner.cfg.Clock = clock
			planner.cfg.Location = time.UTC
			planner.cfg.QueuesEnabled = mode != "queues-disabled"
			dag := &ir.DAG{Name: "wake", OverlapPolicy: ir.OverlapPolicyLatest,
				Schedule: []ir.Schedule{mustParseSchedule(t, "2,4,5 * * * *")}}
			if mode != "disabled" {
				dag.CatchupWindow = time.Hour
			}
			require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
			runs := make(chan PlannedRun, 10)
			record := func(_ context.Context, entry DAGEntry, id string, trigger ir.TriggerType, scheduled time.Time) error {
				runs <- PlannedRun{DAGEntry: entry, RunID: id, TriggerType: trigger, ScheduledTime: scheduled}
				return nil
			}
			planner.cfg.Dispatch = record
			planner.cfg.Enqueue = record
			// Jump after the first completed tick, without waiting or suspending the host.
			store.onSave = func(state *schedulerstate.State) {
				if state.LastTick.Equal(base) {
					now.Store(wake.UnixNano())
				}
			}
			sc := &Scheduler{planner: planner, clock: clock, quit: make(chan any)}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { defer close(done); sc.cronLoop(ctx, make(chan os.Signal)) }()
			t.Cleanup(func() { cancel(); <-done })
			select {
			case run := <-runs:
				require.True(t, wake.Equal(run.ScheduledTime), "unexpected scheduled time: %s", run.ScheduledTime)
				want := ir.TriggerTypeScheduler
				if mode == "catchup" {
					want = ir.TriggerTypeCatchUp
				}
				require.Equal(t, want, run.TriggerType)
			case <-time.After(time.Second):
				t.Fatal("scheduler did not process the wake gap")
			}
		})
	}
}

func TestCronLoopBackwardClock(t *testing.T) {
	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(base)})
	planner.cfg.Clock = func() time.Time { return base }
	planner.cfg.Location = time.UTC
	runs := make(chan struct{}, 10)
	planner.cfg.Dispatch = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error {
		runs <- struct{}{}
		return nil
	}
	require.NoError(t, planner.Init(t.Context(), testDAGEntries(&ir.DAG{Name: "backward",
		Schedule: []ir.Schedule{mustParseSchedule(t, "* * * * *")}})))
	var reads atomic.Int32
	clock := func() time.Time {
		if reads.Add(1) == 1 {
			return base.Add(5 * time.Minute)
		}
		return base
	}
	sc := &Scheduler{planner: planner, clock: clock, quit: make(chan any)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); sc.cronLoop(ctx, make(chan os.Signal)) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-runs:
		t.Fatal("a clock rollback dispatched a future slot")
	case <-time.After(50 * time.Millisecond):
	}
	// The planner checkpoint remains at the last real wall-clock slot.
	planner.mu.RLock()
	defer planner.mu.RUnlock()
	require.True(t, planner.watermarkState.LastTick.Equal(base))
}

func TestWaitForTickSignalStopsScheduler(t *testing.T) {
	t.Parallel()

	sc := &Scheduler{
		entryReader:    &staticEntryReader{},
		quit:           make(chan any),
		queueProcessor: NewQueueProcessor(nil, nil, nil, nil, config.Queues{}),
		planner:        &TickPlanner{},
	}

	sig := make(chan os.Signal, 1)
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	// The signal is sent on the channel, not by the OS.
	ctx := signalctx.WithOSSignalsDisabled(context.Background())
	sig <- syscall.SIGTERM
	require.False(t, sc.waitForTick(ctx, sig, timer))

	select {
	case <-sc.quit:
	default:
		require.FailNow(t, "expected scheduler quit channel to close on signal")
	}
}

func TestRunTickSafelyRecoversTickPanic(t *testing.T) {
	t.Parallel()

	sc, panicTriggered := newPanickingScheduler(t)

	require.NotPanics(t, func() {
		sc.runTickSafely(context.Background(), time.Now())
	})
	requirePanicTriggered(t, panicTriggered)
}

func TestCronLoopRecoversTickPanicAndKeepsRunning(t *testing.T) {
	t.Parallel()

	sc, panicTriggered := newPanickingScheduler(t)
	sc.quit = make(chan any)
	sc.clock = func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	}
	sig := make(chan os.Signal, 1)
	done := make(chan struct{})
	panicCh := make(chan any, 1)

	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				panicCh <- r
			}
		}()
		sc.cronLoop(context.Background(), sig)
	}()

	defer func() {
		select {
		case <-sc.quit:
		default:
			close(sc.quit)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			require.FailNow(t, "cronLoop did not stop")
		}
	}()

	requirePanicTriggered(t, panicTriggered)
	requireCronLoopRunning(t, sc, done, panicCh)

	select {
	case r := <-panicCh:
		require.Failf(t, "cronLoop panic escaped", "%v", r)
	case <-done:
		require.FailNow(t, "cronLoop exited after tick panic")
	case <-time.After(100 * time.Millisecond):
	}
}

func newPanickingScheduler(t *testing.T) (*Scheduler, <-chan struct{}) {
	t.Helper()

	panicTriggered := make(chan struct{}, 1)
	planner := NewTickPlanner(TickPlannerConfig{
		IsSuspended: func(context.Context, string) (bool, error) {
			panicTriggered <- struct{}{}
			panic("test tick panic")
		},
	})
	require.NoError(t, planner.Init(t.Context(), testDAGEntries(&ir.DAG{Name: "panic-dag"})))

	return &Scheduler{planner: planner, clock: time.Now}, panicTriggered
}

func requirePanicTriggered(t *testing.T, panicTriggered <-chan struct{}) {
	t.Helper()

	select {
	case <-panicTriggered:
	case <-time.After(time.Second):
		require.FailNow(t, "scheduler tick did not reach panic dependency")
	}
}

func requireCronLoopRunning(t *testing.T, sc *Scheduler, done <-chan struct{}, panicCh <-chan any) {
	t.Helper()

	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case r := <-panicCh:
			require.Failf(t, "cronLoop panic escaped", "%v", r)
		case <-done:
			require.FailNow(t, "cronLoop exited before reporting running")
		case <-deadline:
			require.FailNow(t, "cronLoop did not report running")
		case <-ticker.C:
			if sc.IsRunning() {
				return
			}
		}
	}
}
