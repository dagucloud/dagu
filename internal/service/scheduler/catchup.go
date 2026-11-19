// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"slices"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ComputeReplayFrom computes the earliest timestamp worth replaying for a DAG.
//
//	replayFrom = max(
//	    now - catchupWindow,
//	    lastTick,
//	    lastScheduledTime,
//	)
func ComputeReplayFrom(catchupWindow time.Duration, lastTick, lastScheduledTime, now time.Time) time.Time {
	earliest := now.Add(-catchupWindow)
	if lastTick.After(earliest) {
		earliest = lastTick
	}
	if lastScheduledTime.After(earliest) {
		earliest = lastScheduledTime
	}
	return earliest
}

// MaxMissedRuns is the maximum number of missed intervals that will be
// replayed per DAG. Prevents memory explosion for large catchup windows
// with high-frequency schedules (e.g., 30-day window + per-minute cron).
const MaxMissedRuns = 1000

type missedScheduleInterval struct {
	Schedule      ir.Schedule
	ScheduledTime time.Time
}

// CatchupAfterOutage returns what catch-up queues of a DAG's slots that came
// due while no scheduler ran, when a scheduler starts again or ticks late:
// with queues enabled, the newest MaxMissedRuns within the DAG's catchup
// window, or the newest one under overlap_policy latest.
//
// Catch-up then runs one queued slot per tick while the DAG is idle, and
// may never run some: a restart queues again only the saved slots within
// the window before it, and a pause or a suspension drops the queue. No
// later period holds such a slot, so it is not counted as missed.
func CatchupAfterOutage(dag *ir.DAG, queuesEnabled bool) backfill.Catchup {
	if !queuesEnabled || dag.CatchupWindow <= 0 {
		return backfill.Catchup{}
	}
	if dag.OverlapPolicy == ir.OverlapPolicyLatest {
		return backfill.Catchup{Window: dag.CatchupWindow, Max: 1}
	}
	return backfill.Catchup{Window: dag.CatchupWindow, Max: MaxMissedRuns}
}

// savedCatchupIntervals returns the saved catch-up slots after earliest and by
// latest that a cron schedule still fires, each with the first such schedule.
func savedCatchupIntervals(schedules []ir.Schedule, slots []time.Time, earliest, latest time.Time, loc *time.Location) []missedScheduleInterval {
	var intervals []missedScheduleInterval
	for _, slot := range slots {
		if !slot.After(earliest) || slot.After(latest) {
			continue
		}
		slot = slot.In(loc)
		if schedule, ok := firstCronFiring(schedules, slot); ok {
			intervals = append(intervals, missedScheduleInterval{Schedule: schedule, ScheduledTime: slot})
		}
	}
	return intervals
}

// firstCronFiring returns the first cron schedule that fires at slot.
func firstCronFiring(schedules []ir.Schedule, slot time.Time) (ir.Schedule, bool) {
	for _, schedule := range schedules {
		if schedule.IsCron() && scheduleMatchesFireTime(schedule, slot) {
			return schedule, true
		}
	}
	return ir.Schedule{}, false
}

// mergeIntervals returns the slots of a and b once each, oldest first, keeping
// the latest MaxMissedRuns.
func mergeIntervals(a, b []missedScheduleInterval) []missedScheduleInterval {
	merged := slices.Concat(a, b)
	slices.SortStableFunc(merged, func(x, y missedScheduleInterval) int {
		return x.ScheduledTime.Compare(y.ScheduledTime)
	})
	merged = slices.CompactFunc(merged, func(x, y missedScheduleInterval) bool {
		return x.ScheduledTime.Equal(y.ScheduledTime)
	})
	if len(merged) > MaxMissedRuns {
		merged = merged[len(merged)-MaxMissedRuns:]
	}
	return merged
}

// ComputeMissedIntervals iterates each schedule's cron expression from
// replayFrom to replayTo, collects all missed ticks, merges and sorts
// chronologically. Duplicates across schedules are removed.
// If the total exceeds MaxMissedRuns, only the most recent runs are kept.
func ComputeMissedIntervals(schedules []ir.Schedule, replayFrom, replayTo time.Time) []time.Time {
	intervals := computeMissedScheduleIntervals(schedules, replayFrom, replayTo)
	result := make([]time.Time, 0, len(intervals))
	for _, interval := range intervals {
		result = append(result, interval.ScheduledTime)
	}
	return result
}

func computeMissedScheduleIntervals(schedules []ir.Schedule, replayFrom, replayTo time.Time) []missedScheduleInterval {
	seen := make(map[time.Time]struct{})
	var result []missedScheduleInterval

	for _, sched := range schedules {
		if sched.Parsed == nil {
			continue
		}
		// Exclusive start: replayFrom was already dispatched. A zero time
		// means the schedule never fires again.
		t := sched.Parsed.Next(replayFrom).Round(0)
		for !t.IsZero() && !t.After(replayTo) {
			if _, dup := seen[t.UTC()]; !dup {
				seen[t.UTC()] = struct{}{}
				result = append(result, missedScheduleInterval{
					Schedule:      sched,
					ScheduledTime: t,
				})
			}
			next := sched.Parsed.Next(t).Round(0)
			if !next.After(t) {
				break // defensive: prevent infinite loop on degenerate schedule
			}
			t = next
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ScheduledTime.Before(result[j].ScheduledTime)
	})

	// Cap to most recent runs to prevent memory explosion.
	if len(result) > MaxMissedRuns {
		result = result[len(result)-MaxMissedRuns:]
	}

	return result
}
