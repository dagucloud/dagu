// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
)

const SchedulerStateVersion = 3

func cloneDAGWatermark(w DAGWatermark) DAGWatermark {
	cloned := DAGWatermark{
		LastScheduledTime:        w.LastScheduledTime,
		StartScheduleFingerprint: w.StartScheduleFingerprint,
		SkipSuccessResetAt:       w.SkipSuccessResetAt,
	}
	if len(w.OneOffs) > 0 {
		cloned.OneOffs = make(map[string]OneOffScheduleState, len(w.OneOffs))
		maps.Copy(cloned.OneOffs, w.OneOffs)
	}
	return cloned
}

func isZeroDAGWatermark(w DAGWatermark) bool {
	return w.LastScheduledTime.IsZero() &&
		w.StartScheduleFingerprint == "" &&
		w.SkipSuccessResetAt.IsZero() &&
		len(w.OneOffs) == 0
}

func oneOffSchedules(all []core.Schedule) []core.Schedule {
	var result []core.Schedule
	for _, schedule := range all {
		if schedule.IsOneOff() {
			result = append(result, schedule)
		}
	}
	return result
}

func reconcileOneOffState(current DAGWatermark, schedules []core.Schedule, now time.Time) (DAGWatermark, bool) {
	next := cloneDAGWatermark(current)
	active := make(map[string]struct{})
	changed := false

	for _, schedule := range oneOffSchedules(schedules) {
		fingerprint := schedule.Fingerprint()
		if fingerprint == "" {
			continue
		}
		active[fingerprint] = struct{}{}

		scheduledTime, ok := schedule.OneOffTime()
		if !ok {
			continue
		}

		if next.OneOffs == nil {
			next.OneOffs = make(map[string]OneOffScheduleState)
		}

		if existing, ok := next.OneOffs[fingerprint]; ok {
			if existing.ScheduledTime.IsZero() {
				existing.ScheduledTime = scheduledTime
				next.OneOffs[fingerprint] = existing
				changed = true
			}
			continue
		}

		status := OneOffStatusConsumed
		if !scheduledTime.Before(now) {
			status = OneOffStatusPending
		}
		next.OneOffs[fingerprint] = OneOffScheduleState{
			ScheduledTime: scheduledTime,
			Status:        status,
		}
		changed = true
	}

	for fingerprint := range next.OneOffs {
		if _, ok := active[fingerprint]; ok {
			continue
		}
		delete(next.OneOffs, fingerprint)
		changed = true
	}

	if len(next.OneOffs) == 0 {
		next.OneOffs = nil
	}

	return next, changed
}

func startScheduleFingerprint(schedules []core.Schedule, skipIfSuccessful bool) string {
	fingerprints := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		if !schedule.IsCron() {
			continue
		}
		fingerprint := schedule.Fingerprint()
		if fingerprint == "" {
			continue
		}
		fingerprints = append(fingerprints, fingerprint)
	}
	if len(fingerprints) == 0 {
		return ""
	}

	slices.Sort(fingerprints)
	return fmt.Sprintf("skip:%t|%s", skipIfSuccessful, strings.Join(fingerprints, ","))
}

func reconcileStartScheduleState(current DAGWatermark, schedules []core.Schedule, skipIfSuccessful bool, observedAt time.Time) (DAGWatermark, bool) {
	next := cloneDAGWatermark(current)
	fingerprint := startScheduleFingerprint(schedules, skipIfSuccessful)

	if next.StartScheduleFingerprint == fingerprint {
		return next, false
	}
	if fingerprint == "" {
		if next.StartScheduleFingerprint == "" && next.SkipSuccessResetAt.IsZero() {
			return next, false
		}
		next.StartScheduleFingerprint = ""
		next.SkipSuccessResetAt = time.Time{}
		return next, true
	}

	// Empty fingerprints come from pre-v3 watermark state where schedule identity
	// was not persisted, so seed the current fingerprint without forcing a reset.
	if next.StartScheduleFingerprint == "" {
		next.StartScheduleFingerprint = fingerprint
		return next, true
	}

	next.StartScheduleFingerprint = fingerprint
	next.SkipSuccessResetAt = observedAt
	return next, true
}

// NextPlannedRun projects the next scheduler-aware run time for DAG listing/sorting.
func NextPlannedRun(dag *core.DAG, now time.Time, state *SchedulerState) time.Time {
	if dag == nil {
		return time.Time{}
	}

	var dagState DAGWatermark
	if state != nil {
		dagState = state.DAGs[dag.Name]
	}

	var next time.Time
	for _, schedule := range dag.Schedule {
		var candidate time.Time
		switch {
		case schedule.IsCron():
			candidate = schedule.Next(now)
		case schedule.IsOneOff():
			fingerprint := schedule.Fingerprint()
			if oneOff, ok := dagState.OneOffs[fingerprint]; ok {
				if oneOff.Status != OneOffStatusPending {
					continue
				}
				candidate = oneOff.ScheduledTime
			} else {
				candidate = schedule.Next(now)
			}
		}

		if candidate.IsZero() {
			continue
		}
		if next.IsZero() || candidate.Before(next) {
			next = candidate
		}
	}

	return next
}
