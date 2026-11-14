// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// A host back from sleep runs the scheduler's first tick at once, for the
// minute under way. That tick fires the slot of that minute and any one-off
// schedule whose time passed, before whatever manages the DAGs can bring them
// up to date; a schedule moved to another host meanwhile would fire here too.
//
// With scheduler.wake_hold set, a tick that comes more than wakeGap late
// holds the scheduler instead: it pauses itself, a pause the store marks as
// its hold (PauseStore.SetHold), and runs no tick until the pause is cleared
// through the API or the hold's duration passes. The ticks it held then run,
// the wake's minute first, each dispatched before the next so a run is seen
// by the tick after it. A wake hold is not an operator's pause: queued runs
// go on, and nothing is dequeued or discarded (suspension.go).
const (
	// wakeGap is how late a tick must come before the time in between counts
	// as the host asleep: a whole minute, so a slow tick is not taken for a
	// wake.
	wakeGap = time.Minute
	// wakeHoldPoll is how often a holding scheduler looks for its release.
	wakeHoldPoll = time.Second
	// wakeHoldReason is shown wherever the pause is.
	wakeHoldReason = "The scheduler is holding its schedules after the host woke from sleep, until its definitions have been refreshed or the hold's duration passes."
)

// wakeHold is the scheduler's hold after a wake, and its state.
type wakeHold struct {
	// limit is how long a hold may last; zero means the scheduler never holds.
	limit time.Duration
	store schedulerstate.PauseStore
	// poll is how often a holding scheduler looks for its release.
	poll time.Duration
	// since is when the current hold began; zero while none is held.
	since time.Time
}

// tickOutcome is what the cron loop does with an overdue tick.
type tickOutcome int

const (
	// tickNow runs the tick as usual.
	tickNow tickOutcome = iota
	// tickLater holds the tick; the loop asks again after the wait given.
	tickLater
	// tickHeld ends a hold: the ticks since the wake run, oldest first.
	tickHeld
)

func newWakeHold(limit time.Duration, store schedulerstate.PauseStore) wakeHold {
	return wakeHold{limit: limit, store: store, poll: wakeHoldPoll}
}

func (h *wakeHold) enabled() bool {
	return h.limit > 0 && h.store != nil
}

// check decides what the loop does with the tick due at tickTime, now that
// it is now. While a hold lasts it answers tickLater with how long to wait;
// when one ends it answers tickHeld, with when the hold began.
func (h *wakeHold) check(ctx context.Context, tickTime, now time.Time) (tickOutcome, time.Duration, time.Time) {
	if !h.since.IsZero() {
		if wait, holding := h.holding(ctx, now); holding {
			return tickLater, wait, time.Time{}
		}
		since := h.since
		h.since = time.Time{}
		return tickHeld, 0, since
	}
	if h.begin(ctx, tickTime, now) {
		return tickLater, h.poll, time.Time{}
	}
	return tickNow, 0, time.Time{}
}

// begin starts a hold when the tick comes more than wakeGap late and no pause
// is in force; an operator's pause governs on its own.
func (h *wakeHold) begin(ctx context.Context, tickTime, now time.Time) bool {
	if !h.enabled() || now.Sub(tickTime) <= wakeGap {
		return false
	}
	held, err := h.store.SetHold(ctx, true, wakeHoldReason)
	if err != nil {
		logger.Warn(ctx, "Could not hold the scheduler after the wake", tag.Error(err))
		return false
	}
	if !held {
		return false
	}
	h.since = now
	logger.Info(ctx, "Holding schedules after the host woke",
		slog.Time("tick_time", tickTime),
		slog.Duration("late", now.Sub(tickTime)),
		slog.Duration("limit", h.limit),
	)
	return true
}

// holding reports whether the hold goes on, and how long to wait before
// asking again. It ends when the pause is cleared or replaced through the
// API, or when its duration passes, which clears the pause.
func (h *wakeHold) holding(ctx context.Context, now time.Time) (time.Duration, bool) {
	pause, err := h.store.Get(ctx)
	if err != nil {
		logger.Warn(ctx, "Could not read the scheduler pause state while holding", tag.Error(err))
	} else if !pause.WakeHold() {
		logger.Info(ctx, "Wake hold released", slog.Duration("held", now.Sub(h.since)))
		return 0, false
	}
	if remaining := h.since.Add(h.limit).Sub(now); remaining > 0 {
		return min(h.poll, remaining), true
	}
	if _, err := h.store.SetHold(ctx, false, ""); err != nil {
		logger.Error(ctx, "Could not clear the wake hold", tag.Error(err))
	}
	logger.Info(ctx, "Wake hold ended after its duration", slog.Duration("limit", h.limit))
	return 0, false
}

// clearStale lifts a hold a previous scheduler process left behind, so a
// restart never starts out paused by nobody.
func (h *wakeHold) clearStale(ctx context.Context) {
	if h.store == nil {
		return
	}
	lifted, err := h.store.SetHold(ctx, false, "")
	if err != nil {
		logger.Warn(ctx, "Could not clear a stale wake hold", tag.Error(err))
	} else if lifted {
		logger.Info(ctx, "Cleared a wake hold a previous scheduler left behind")
	}
}
