// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
)

// cycleState tracks a run that loops. With loop_wait_sec, the steps of an
// always-on DAG do not end the run: the run waits, then runs them again, so a
// run ends only when it is stopped or the process that holds it is lost.
//
// The agent guards it with its lock.
type cycleState struct {
	// number is the cycle the run is in, counted from 1.
	number int
	// failed counts the cycles that ended unsuccessfully.
	failed int
	// waiting is true between the end of a cycle and the start of the next.
	waiting bool
	// startedAt is when the first cycle started, which is when the run started.
	startedAt time.Time
	// aborted is true when a stop request ended the loop during a wait.
	aborted bool

	// lastLogs are the log files of the cycle that just ended, and lastFailed
	// tells whether it failed. They are removed when the next cycle starts, so a
	// run that loops for months does not fill the disk with logs, except that
	// the logs of the latest failed cycle stay for someone to read.
	lastLogs       []string
	lastFailed     bool
	failedCycleLog []string
}

// loops tells whether a run of the DAG repeats its steps.
func (a *Agent) loops() bool {
	return a.dag.LoopWait > 0
}

// runCycles runs the plan once, or, for a DAG with loop_wait_sec, again and
// again in the same run: after each cycle the run waits and starts a fresh plan.
// A cycle that fails ends the run, so the restart policy starts a new one, unless
// loop_on_failure is "continue", when the loop goes on as a while-true loop does.
// The loop also ends when a stop is requested, when a cycle is aborted or waits
// for approval. A cycle that times out has failed. It returns the error of the
// last cycle.
func (a *Agent) runCycles(ctx context.Context, attempt runstate.Attempt, progressCh chan runtime.ProgressUpdate) error {
	a.lock.RLock()
	runner, plan := a.runner, a.plan
	a.lock.RUnlock()

	if !a.loops() {
		return runner.Run(ctx, plan, progressCh)
	}

	a.lock.Lock()
	a.cycles = &cycleState{number: 1, startedAt: plan.StartAt()}
	a.lock.Unlock()
	// Whatever way the loop ends, the run is not waiting for a cycle any more.
	// A run that stays marked as waiting would be stored as running, with no end
	// time, as its final status.
	defer a.stopWaiting()

	for {
		lastErr := runner.Run(ctx, plan, progressCh)
		again := a.endCycle(ctx, attempt, runner, plan)
		if !again {
			return lastErr
		}
		if !a.waitForNextCycle(ctx) {
			a.lock.Lock()
			a.cycles.aborted = true
			a.cycles.waiting = false
			a.lock.Unlock()
			return lastErr
		}
		next, err := a.setupFreshPlan()
		if err != nil {
			return errors.Join(lastErr, err)
		}
		if !a.startCycle(ctx, next) {
			return lastErr
		}
		plan = next
	}
}

// stopWaiting clears the wait between two cycles, which only the loop in
// runCycles can end by itself.
func (a *Agent) stopWaiting() {
	a.lock.Lock()
	defer a.lock.Unlock()
	if a.cycles != nil {
		a.cycles.waiting = false
	}
}

// endCycle records how a cycle ended and tells whether another one follows.
func (a *Agent) endCycle(ctx context.Context, attempt runstate.Attempt, runner *runtime.Runner, plan *runtime.Plan) bool {
	status := runner.Status(ctx, plan)
	failed := status == ir.Failed
	// A failed cycle ends the run unless the DAG goes on after failures. The
	// restart policy then starts a new run, which counts the interruption.
	continues := status == ir.Succeeded || status == ir.PartiallySucceeded ||
		(failed && a.dag.LoopOnFailure == ir.LoopOnFailureContinue)
	again := continues && ctx.Err() == nil && !a.stopRequested()

	a.lock.Lock()
	c := a.cycles
	if failed {
		c.failed++
	}
	c.lastLogs = cycleLogFiles(runner, plan)
	c.lastFailed = failed
	c.waiting = again
	number := c.number
	a.lock.Unlock()

	// The log of the run is never rotated while the run lives, so a cycle that
	// goes well leaves no line above the debug level.
	logCycle := logger.Debug
	if failed {
		logCycle = logger.Info
	}
	logCycle(ctx, "Cycle finished",
		slog.Int("cycle", number),
		tag.Status(status.String()),
		slog.Bool("next", again),
	)
	if again {
		a.recordWait(ctx, attempt)
	}
	return again
}

// statusCompactor is implemented by an attempt that can rewrite its status file
// to the latest status.
type statusCompactor interface {
	Compact(ctx context.Context) error
}

// recordWait stores that the run is waiting for its next cycle, so that a
// reader of the stored status sees a run that goes on and not one that ended,
// then compacts the status file.
func (a *Agent) recordWait(ctx context.Context, attempt runstate.Attempt) {
	if _, err := a.recordCurrentStatus(ctx, attempt); err != nil {
		logger.Warn(ctx, "Failed to store the status of the wait between cycles", tag.Error(err))
		return
	}
	if compactor, ok := attempt.(statusCompactor); ok {
		// A status write from another goroutine must not interleave with the
		// rewrite of the file.
		a.statusWriteMu.Lock()
		err := compactor.Compact(ctx)
		a.statusWriteMu.Unlock()
		if err != nil {
			logger.Warn(ctx, "Failed to compact the status file between cycles", tag.Error(err))
		}
	}
}

// waitForNextCycle waits loop_wait_sec, and reports false when a stop request
// or the end of the context cuts the wait short.
func (a *Agent) waitForNextCycle(ctx context.Context) bool {
	timer := time.NewTimer(a.dag.LoopWait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return !a.stopRequested()
	case <-a.stopped:
		return false
	case <-ctx.Done():
		return false
	}
}

// startCycle makes next the plan of the run and removes the logs of the cycle
// before it, except those of the latest failed cycle. It reports false, and
// starts nothing, when a stop was requested since the wait ended. The check and
// the swap of the plan happen under the lock that a stop request takes, so a
// stop either comes before the check or reaches the new plan.
func (a *Agent) startCycle(ctx context.Context, next *runtime.Plan) bool {
	a.lock.Lock()
	c := a.cycles
	if a.stopRequested() {
		c.aborted = true
		c.waiting = false
		a.lock.Unlock()
		return false
	}
	a.plan = next
	c.number++
	c.waiting = false
	var remove []string
	if c.lastFailed {
		remove, c.failedCycleLog = c.failedCycleLog, c.lastLogs
	} else {
		remove = c.lastLogs
	}
	c.lastLogs = nil
	number := c.number
	a.lock.Unlock()

	for _, file := range remove {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			logger.Debug(ctx, "Failed to remove the log of an earlier cycle",
				slog.String("file", file), tag.Error(err))
		}
	}
	logger.Debug(ctx, "Cycle started", slog.Int("cycle", number))
	return true
}

// stopRequested tells whether a stop was requested.
func (a *Agent) stopRequested() bool {
	select {
	case <-a.stopped:
		return true
	default:
		return false
	}
}

// cycleLogFiles lists the step and handler logs of a cycle.
func cycleLogFiles(runner *runtime.Runner, plan *runtime.Plan) []string {
	var files []string
	add := func(data runtime.NodeData) {
		for _, file := range []string{data.State.Stdout, data.State.Stderr} {
			if file != "" {
				files = append(files, file)
			}
		}
	}
	for _, data := range plan.NodeData() {
		add(data)
	}
	for _, handler := range []ir.HandlerType{
		ir.HandlerOnInit, ir.HandlerOnSuccess, ir.HandlerOnFailure,
		ir.HandlerOnAbort, ir.HandlerOnWait, ir.HandlerOnExit,
	} {
		if node := runner.HandlerNode(handler); node != nil {
			add(node.NodeData())
		}
	}
	return files
}

// cycleStatusOptions adjusts the status of a run that loops, and returns the
// options that carry its cycle counters. The caller holds the agent's lock.
//
// Between two cycles the plan of the last cycle is finished, but the run is
// not: it reports as running with no end. A run starts when its first cycle
// does. A loop that a stop request ended during a wait reports as aborted, so
// that unless-stopped sees the stop.
func (a *Agent) cycleStatusOptions(status *ir.Status, finishedAt, startedAt *time.Time) []ir.StatusOption {
	c := a.cycles
	if c == nil {
		return nil
	}
	*startedAt = c.startedAt
	switch {
	case c.aborted:
		*status = ir.Aborted
	case c.waiting:
		*status = ir.Running
		*finishedAt = time.Time{}
	}
	return []ir.StatusOption{ir.WithCycles(c.number, c.failed)}
}
