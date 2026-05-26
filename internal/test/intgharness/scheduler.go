// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intgharness

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/stretchr/testify/require"
)

// SchedulerProbe observes a scheduler instance without leaking polling mechanics into tests.
type SchedulerProbe struct {
	h           Harness
	scheduler   *scheduler.Scheduler
	entryReader scheduler.EntryReader
	errCh       chan error
	err         error
	stopped     bool
}

// StartScheduler starts scheduler and returns a probe for semantic assertions.
func (h Harness) StartScheduler(ctx context.Context, schedulerInstance *scheduler.Scheduler, entryReader scheduler.EntryReader) *SchedulerProbe {
	h.t.Helper()

	probe := &SchedulerProbe{
		h:           h,
		scheduler:   schedulerInstance,
		entryReader: entryReader,
		errCh:       make(chan error, 1),
	}
	go func() {
		probe.errCh <- schedulerInstance.Start(ctx)
	}()
	return probe
}

// RequireRunningWithSchedule waits until scheduler is running and has loaded dagName with expression.
func (p *SchedulerProbe) RequireRunningWithSchedule(dagName, expression string, timeout time.Duration) {
	p.h.t.Helper()

	p.requireEventuallyNoSchedulerError(
		fmt.Sprintf("expected scheduler to run with schedule %q for DAG %q", expression, dagName),
		timeout,
		func() bool {
			return p.scheduler.IsRunning() && p.HasLoadedSchedule(dagName, expression)
		},
	)
}

// RequireLoadedSchedule waits until dagName has expression in the entry reader.
func (p *SchedulerProbe) RequireLoadedSchedule(dagName, expression string, timeout time.Duration) {
	p.h.t.Helper()

	p.requireEventuallyNoSchedulerError(
		fmt.Sprintf("expected scheduler to load schedule %q for DAG %q", expression, dagName),
		timeout,
		func() bool {
			return p.HasLoadedSchedule(dagName, expression)
		},
	)
}

// RequireEventually waits until condition is true while failing early if scheduler exits.
func (p *SchedulerProbe) RequireEventually(label string, timeout time.Duration, condition func() bool) {
	p.h.t.Helper()
	p.requireEventuallyNoSchedulerError(label, timeout, condition)
}

// HasLoadedSchedule reports whether the scheduler entry reader has dagName with expression.
func (p *SchedulerProbe) HasLoadedSchedule(dagName, expression string) bool {
	for _, loaded := range p.entryReader.DAGs() {
		if loaded.Name != dagName {
			continue
		}
		for _, schedule := range loaded.Schedule {
			if schedule.Expression == expression {
				return true
			}
		}
	}
	return false
}

// Stop stops scheduler and waits for its goroutine to exit.
func (p *SchedulerProbe) Stop(ctx context.Context, cancel context.CancelFunc, timeout time.Duration) {
	p.h.t.Helper()

	stopDone := make(chan struct{})
	go func() {
		p.scheduler.Stop(ctx)
		close(stopDone)
	}()

	if cancel != nil {
		cancel()
	}

	select {
	case <-stopDone:
	case <-time.After(p.h.Timeout(timeout)):
		p.h.t.Fatal("scheduler stop did not return within timeout")
	}

	if p.stopped {
		require.True(p.h.t, acceptableSchedulerStopErr(p.err), "unexpected scheduler shutdown error: %v", p.err)
		return
	}

	select {
	case err := <-p.errCh:
		p.stopped = true
		p.err = err
		require.True(p.h.t, acceptableSchedulerStopErr(err), "unexpected scheduler shutdown error: %v", err)
	case <-time.After(p.h.Timeout(timeout)):
		p.h.t.Fatal("scheduler did not stop within timeout")
	}
}

func (p *SchedulerProbe) requireEventuallyNoSchedulerError(label string, timeout time.Duration, condition func() bool) {
	p.h.Wait.EventuallyEvery(label, timeout, defaultPollInterval, func() bool {
		if err := p.pollErr(); err != nil {
			return true
		}
		return condition()
	})
	require.NoError(p.h.t, p.err)
}

func (p *SchedulerProbe) pollErr() error {
	if p.stopped {
		return p.err
	}

	select {
	case err := <-p.errCh:
		p.stopped = true
		if err == nil {
			err = errors.New("scheduler exited unexpectedly before test completed")
		}
		p.err = err
	default:
	}
	return p.err
}

func acceptableSchedulerStopErr(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
