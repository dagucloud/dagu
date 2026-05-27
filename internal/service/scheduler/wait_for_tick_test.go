// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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

	sig <- syscall.SIGTERM
	require.False(t, sc.waitForTick(context.Background(), sig, timer))

	select {
	case <-sc.quit:
	default:
		require.FailNow(t, "expected scheduler quit channel to close on signal")
	}
}

func TestRunTickSafelyRecoversTickPanic(t *testing.T) {
	t.Parallel()

	sc := &Scheduler{}

	require.NotPanics(t, func() {
		sc.runTickSafely(context.Background(), time.Now())
	})
}

func TestCronLoopRecoversTickPanicAndKeepsRunning(t *testing.T) {
	t.Parallel()

	sc := &Scheduler{
		quit: make(chan any),
		clock: func() time.Time {
			return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		},
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

	requireCronLoopRunning(t, sc, done, panicCh)

	select {
	case r := <-panicCh:
		require.Failf(t, "cronLoop panic escaped", "%v", r)
	case <-done:
		require.FailNow(t, "cronLoop exited after tick panic")
	case <-time.After(100 * time.Millisecond):
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
