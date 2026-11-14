// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"time"

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
		watermark.Missed = &schedulerstate.MissedGap{From: since, To: to, Reason: reason, Slots: slots}
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
	watermark.Missed = &schedulerstate.MissedGap{From: from, To: to, Reason: schedulerstate.GapDown, Slots: slots}
	tp.watermarkState.DAGs[entry.DAG.Name] = watermark
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
