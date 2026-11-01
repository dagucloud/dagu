// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// Limits on the schedules one report lists; schedules_total counts them all.
const (
	maxSchedules      = 500
	maxSchedulesBytes = 128 << 10
	// unticked is how long after a DAG's next run passes without a tick
	// before the slot counts as missed.
	unticked = time.Minute
)

// Scheduler is what the scheduler keeps, for reports. A nil store leaves
// out what it holds.
type Scheduler struct {
	// State holds each DAG's next run and latest start slot, and the last
	// tick.
	State schedulerstate.Store
	// Pause says whether the scheduler is paused.
	Pause schedulerstate.PauseStore
}

// schedulerHealth is the scheduler's state in a report's health.
type schedulerHealth struct {
	LastTick string `json:"last_tick,omitempty"`
	Paused   bool   `json:"paused"`
}

// schedule is a scheduled DAG in a report: its next run, and the latest
// slot that came due with what came of it.
type schedule struct {
	Workflow    string `json:"workflow"`
	NextRun     string `json:"next_run"`
	LastSlot    string `json:"last_slot,omitempty"`
	LastOutcome string `json:"last_outcome,omitempty"`
}

// load reads the scheduler's state once for a report; false when there is
// none to read or it cannot be read.
func (s Scheduler) load(ctx context.Context) (*schedulerstate.State, bool) {
	if s.State == nil {
		return nil, false
	}
	state, err := s.State.Load(ctx)
	if err != nil {
		logger.Debug(ctx, "Failed to read the scheduler state for the server report", tag.Error(err))
		return nil, false
	}
	return state, true
}

// health is the scheduler's state as read, or nil when nothing could be
// read of it.
func (s Scheduler) health(ctx context.Context, state *schedulerstate.State, read bool) *schedulerHealth {
	if (s.State == nil && s.Pause == nil) || (s.State != nil && !read) {
		return nil
	}
	var out schedulerHealth
	if state != nil && !state.LastTick.IsZero() {
		out.LastTick = formatTime(state.LastTick)
	}
	if s.Pause != nil {
		paused, err := s.Pause.IsPaused(ctx)
		if err != nil {
			logger.Debug(ctx, "Failed to read the scheduler pause for the server report", tag.Error(err))
			return nil
		}
		out.Paused = paused
	}
	return &out
}

// schedulesOf lists the DAGs the scheduler will start as of now, those whose
// latest slot did not settle first, then the latest slots first, up to the
// limits, and counts them all. A next run that passed with no tick since is
// taken as a missed slot.
func schedulesOf(state *schedulerstate.State, now time.Time) ([]schedule, int) {
	type entry struct {
		name string
		next time.Time
		slot *schedulerstate.ScheduleSlot
	}
	var entries []entry
	for name, dag := range state.DAGs {
		if dag.NextRun == nil {
			continue
		}
		next, slot := *dag.NextRun, dag.LastSlot
		if now.Sub(next) > unticked && state.LastTick.Before(next) && (slot == nil || slot.At.Before(next)) {
			slot = &schedulerstate.ScheduleSlot{At: next, Outcome: schedulerstate.SlotMissed}
		}
		entries = append(entries, entry{name: name, next: next, slot: slot})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if unsettled := cmp.Compare(settledRank(a.slot), settledRank(b.slot)); unsettled != 0 {
			return unsettled
		}
		if latest := slotTime(b.slot).Compare(slotTime(a.slot)); latest != 0 {
			return latest
		}
		return cmp.Compare(a.name, b.name)
	})

	out := make([]schedule, 0, min(len(entries), maxSchedules))
	size := 0
	for _, e := range entries {
		if len(out) == maxSchedules {
			break
		}
		s := schedule{Workflow: e.name, NextRun: formatTime(e.next)}
		if e.slot != nil {
			s.LastSlot, s.LastOutcome = formatTime(e.slot.At), string(e.slot.Outcome)
		}
		encoded, err := json.Marshal(s)
		if err != nil || size+len(encoded)+1 > maxSchedulesBytes {
			break
		}
		size += len(encoded) + 1
		out = append(out, s)
	}
	return out, len(entries)
}

// settledRank orders a slot that did not settle before any other.
func settledRank(slot *schedulerstate.ScheduleSlot) int {
	if slot != nil && !slot.Outcome.Settled() {
		return 0
	}
	return 1
}

func slotTime(slot *schedulerstate.ScheduleSlot) time.Time {
	if slot == nil {
		return time.Time{}
	}
	return slot.At
}
