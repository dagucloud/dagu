// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// holdReason tells whether the DAGs held in this tick are held by an
// operator's pause or by their own suspension.
func (tp *TickPlanner) holdReason(ctx context.Context) schedulerstate.GapReason {
	if tp.cfg.IsPaused != nil {
		if paused, err := tp.cfg.IsPaused(ctx); err == nil && paused {
			return schedulerstate.GapPaused
		}
	}
	return schedulerstate.GapSuspended
}

// holdDAG notes when the scheduler first found a DAG held.
func (tp *TickPlanner) holdDAG(dagName string, reason schedulerstate.GapReason, now time.Time) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.watermarkState.DAGs[dagName].HeldSince != nil {
		return
	}
	watermark := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[dagName])
	heldSince := now
	watermark.HeldSince, watermark.HeldBy = &heldSince, reason
	tp.watermarkState.DAGs[dagName] = watermark
}

// releaseDAG ends a DAG's hold at to. The slots that came due while it was
// held, from the tick that found it held through through, make its missed
// gap.
func (tp *TickPlanner) releaseDAG(dagName string, schedules []ir.Schedule, to, through time.Time) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.watermarkState.DAGs[dagName].HeldSince == nil {
		return
	}
	watermark := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[dagName])
	since, reason := *watermark.HeldSince, watermark.HeldBy
	watermark.HeldSince, watermark.HeldBy = nil, ""
	slots := countSlots(schedules, since.Add(-time.Second).In(tp.cfg.Location), through)
	if slots > 0 {
		watermark.Missed = &schedulerstate.MissedGap{From: since, To: to, Through: through, Reason: reason, Slots: slots}
	}
	tp.watermarkState.DAGs[dagName] = watermark
}

// settleRestoredHold ends at startup the hold of a DAG that is no longer
// held. Its hold lasted at least to the last tick, and catch-up and the
// outage's gap take the slots after it.
func (tp *TickPlanner) settleRestoredHold(ctx context.Context, entry DAGEntry, active activeDAGSchedules, lastTick time.Time) {
	tp.mu.RLock()
	held := tp.watermarkState.DAGs[entry.DAG.Name].HeldSince != nil
	tp.mu.RUnlock()
	if !held || lastTick.IsZero() {
		return
	}
	suspended, err := isSuspendedDAG(ctx, tp.cfg.IsSuspended, nil, entry.DAG, entry.DefinitionID)
	if err != nil || suspended {
		return
	}
	tp.releaseDAG(entry.DAG.Name, active.start, lastTick, lastTick)
}

// recordDownGap records as the gap from to to the slots of a DAG that is
// not caught up which came due after from, the last tick, and by through,
// while no tick ran.
func (tp *TickPlanner) recordDownGap(ctx context.Context, entry DAGEntry, active activeDAGSchedules, from, to, through time.Time) {
	if tp.cfg.QueuesEnabled && entry.DAG.CatchupWindow > 0 {
		return
	}
	tp.recordGap(ctx, entry, active.start, from, to, through)
}

// recordClampedGap records as missed the slots a DAG that is caught up, or
// recovered after a failover, left behind: those after its last tick or
// slot and by replayFrom, where its replay starts.
func (tp *TickPlanner) recordClampedGap(ctx context.Context, entry DAGEntry, active activeDAGSchedules, lastTick, lastScheduled, replayFrom time.Time) {
	if !tp.cfg.QueuesEnabled {
		return
	}
	from := lastTick
	if lastScheduled.After(from) {
		from = lastScheduled
	}
	if !replayFrom.After(from) {
		return
	}
	// The catch-up replays the slots after replayFrom only.
	tp.recordGap(ctx, entry, active.start, from, replayFrom, replayFrom)
}

// recordGap records as the gap from to to the slots after from and by
// through that passed while no scheduler ran. A suspended DAG records none:
// its hold makes its gap.
func (tp *TickPlanner) recordGap(ctx context.Context, entry DAGEntry, schedules []ir.Schedule, from, to, through time.Time) {
	if from.IsZero() || !to.After(from) {
		return
	}
	slots := countSlots(schedules, from.In(tp.cfg.Location), through)
	if slots == 0 {
		return
	}
	suspended, err := isSuspendedDAG(ctx, tp.cfg.IsSuspended, nil, entry.DAG, entry.DefinitionID)
	if err != nil || suspended {
		return
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	watermark := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[entry.DAG.Name])
	watermark.Missed = &schedulerstate.MissedGap{From: from, To: to, Through: through, Reason: schedulerstate.GapDown, Slots: slots}
	tp.watermarkState.DAGs[entry.DAG.Name] = watermark
}

// coverGap takes from a DAG's missed gap the slots a backfill of a range
// runs, from the gap's first slot up to the first one it does not run. The
// gap then starts after them, or is gone when the backfill runs them all. A
// backfill that names its slots or selects them by a date leaves the gap as
// it is, since it may skip slots between. It reports whether the gap
// changed.
func (tp *TickPlanner) coverGap(ctx context.Context, record *backfill.Record) bool {
	if len(record.Slots) == 0 || len(record.Options.Slots) > 0 || record.Options.By != "" {
		return false
	}
	tp.mu.RLock()
	missed := tp.watermarkState.DAGs[record.DAGName].Missed
	tp.mu.RUnlock()
	if missed == nil {
		return false
	}
	gap := *missed
	start, end := gapStart(gap), gapEnd(gap)
	if !record.Slots[len(record.Slots)-1].At.After(start) || record.Slots[0].At.After(end) {
		return false
	}
	schedules, ok := tp.startSchedules(ctx, record.DAGName)
	if !ok {
		return false
	}
	last := lastCovered(schedules, start.In(tp.cfg.Location), end, record.Slots)
	if last.IsZero() {
		return false
	}
	gap.From = last
	if gap.Reason != schedulerstate.GapDown {
		gap.From = last.Add(time.Second)
	}
	// The slots left are counted under the schedules as they are now, which
	// may differ from those the gap was counted under.
	remaining := countSlots(schedules, gapStart(gap).In(tp.cfg.Location), end)

	tp.mu.Lock()
	defer tp.mu.Unlock()
	watermark := schedulerstate.CloneDAGWatermark(tp.watermarkState.DAGs[record.DAGName])
	if watermark.Missed == nil || !sameGap(*watermark.Missed, *missed) {
		return false
	}
	watermark.Missed = nil
	if remaining > 0 {
		gap.Slots = remaining
		watermark.Missed = &gap
	}
	tp.watermarkState.DAGs[record.DAGName] = watermark
	return true
}

// lastCovered walks the fire times of the schedules after from and by to
// while the backfill's slots, oldest first, hold each, and returns the last
// they hold, or zero when they skip the first.
func lastCovered(schedules []ir.Schedule, from, to time.Time, slots []backfill.RecordSlot) time.Time {
	var last time.Time
	i := 0
	for at := nextFire(schedules, from); !at.IsZero() && !at.After(to); at = nextFire(schedules, at) {
		for i < len(slots) && slots[i].At.Before(at) {
			i++
		}
		if i == len(slots) || !slots[i].At.Equal(at) {
			break
		}
		last = at
	}
	return last
}

// nextFire returns the earliest fire time of the cron schedules after from,
// or zero when none fires again.
func nextFire(schedules []ir.Schedule, from time.Time) time.Time {
	var next time.Time
	for _, schedule := range schedules {
		if schedule.Parsed == nil {
			continue
		}
		if at := schedule.Parsed.Next(from).Round(0); !at.IsZero() && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	return next
}

// gapStart is the instant a gap's slots come after. A hold's gap holds the
// slot of the tick that found the DAG held, while an outage's starts after
// the last tick, whose slots ran.
func gapStart(gap schedulerstate.MissedGap) time.Time {
	if gap.Reason == schedulerstate.GapDown {
		return gap.From
	}
	return gap.From.Add(-time.Second)
}

// gapEnd is the last instant whose slots a gap counts.
func gapEnd(gap schedulerstate.MissedGap) time.Time {
	if gap.Through.IsZero() {
		return gap.To
	}
	return gap.Through
}

func sameGap(a, b schedulerstate.MissedGap) bool {
	return a.From.Equal(b.From) && a.To.Equal(b.To) && a.Through.Equal(b.Through) &&
		a.Reason == b.Reason && a.Slots == b.Slots
}

// startSchedules returns the start schedules of a DAG the planner tracks.
func (tp *TickPlanner) startSchedules(ctx context.Context, dagName string) ([]ir.Schedule, bool) {
	tp.entryMu.Lock()
	entry, ok := tp.entries[dagName]
	var dagEntry DAGEntry
	if ok {
		dagEntry = entry.DAGEntry
	}
	tp.entryMu.Unlock()
	if !ok {
		return nil, false
	}
	active, ok := tp.activeDAGSchedules(ctx, dagEntry)
	return active.start, ok
}

// countSlots counts the fire times of the cron schedules after from and by
// to, up to schedulerstate.MaxGapSlots, without collecting them.
func countSlots(schedules []ir.Schedule, from, to time.Time) int {
	seen := make(map[time.Time]struct{})
	for _, schedule := range schedules {
		if schedule.Parsed == nil {
			continue
		}
		for t := schedule.Parsed.Next(from).Round(0); !t.IsZero() && !t.After(to); {
			if len(seen) == schedulerstate.MaxGapSlots {
				return len(seen)
			}
			seen[t.UTC()] = struct{}{}
			next := schedule.Parsed.Next(t).Round(0)
			if !next.After(t) {
				break
			}
			t = next
		}
	}
	return len(seen)
}
