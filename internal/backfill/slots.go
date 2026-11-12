// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package backfill plans and tracks runs of a DAG's past schedule slots.
package backfill

import (
	"context"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// SlotStatus is what the history of a schedule slot says about it.
type SlotStatus int

const (
	// SlotMissing is a slot that no run counts for.
	SlotMissing SlotStatus = iota
	// SlotSucceeded is a slot whose latest run succeeded, or that the
	// scheduler skipped under skip_if_successful.
	SlotSucceeded
	// SlotFailed is a slot whose latest run did not succeed.
	SlotFailed
	// SlotActive is a slot with a queued, running, or waiting run.
	SlotActive
)

// Slot is the state of one schedule slot of a DAG.
type Slot struct {
	At     time.Time
	Status SlotStatus
	// RunID names the run the status comes from. It is empty for a missing
	// slot and for one the scheduler skipped.
	RunID string
	// CoveredAt is the scheduled time of that run when it fired under an
	// earlier schedule of the same cadence, or zero.
	CoveredAt time.Time
	// OtherCadence reports a missing slot whose nearest run fired under a
	// schedule of another cadence, which covers no slot.
	OtherCadence bool
}

// SlotSchedule is how a DAG's slots fire: its schedules, with calendars
// bound, and the profile and server zone that select and place them.
type SlotSchedule struct {
	Schedules []ir.Schedule
	Profile   string
	Location  *time.Location
}

// History is what Dagu knows about a DAG's slots: the slot index's events
// and the runs still in its run history.
type History struct {
	Events []persis.SlotEvent
	// Runs holds the status of each run still in history, by run ID.
	Runs map[string]*ir.DAGRunStatus
}

// HistoryRange returns the range of slot events that SlotStates needs for
// the slots in [from, to]: a run under an earlier schedule can cover a slot
// from anywhere between the fires around it.
func HistoryRange(sched SlotSchedule, from, to time.Time) (time.Time, time.Time) {
	start := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, from).Start
	if start.IsZero() {
		start = from
	}
	end := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, to).Next
	if end.IsZero() {
		end = to
	}
	return start, end
}

// SlotStates returns the state of each slot from its history.
//
// A run counts for a slot when its own status says it ran: while the run
// exists, its status wins over every event about it; once removed, its final
// status counts unless it was dropped. A slot takes the status of its latest
// counting run, or is active while any of them is. A run of the scheduler,
// catchup, or a backfill under an earlier schedule covers the current slot
// nearest to it, ties going to the later slot, when both schedules have the
// same cadence. A manual run counts only for its own slot.
func SlotStates(slots []time.Time, sched SlotSchedule, history History) []Slot {
	runs, skippedSucceeded := collectRuns(history.Events)
	bySlot := make(map[time.Time][]*slotRun)
	otherCadence := make(map[time.Time]bool)
	for _, run := range runs {
		status, ok := run.countedStatus(history.Runs)
		if !ok {
			continue
		}
		run.status = status
		if ir.FiringScheduleExpression(sched.Schedules, sched.Profile, sched.Location, run.slot) != "" || !run.coversNearest() {
			bySlot[run.slot] = append(bySlot[run.slot], run)
			continue
		}
		nearest := nearestFire(sched, run.slot)
		if nearest.IsZero() {
			continue
		}
		current := ir.FiringScheduleExpression(sched.Schedules, sched.Profile, sched.Location, nearest)
		if !ir.SameCadence(run.schedule, current) {
			otherCadence[nearest] = true
			continue
		}
		bySlot[nearest] = append(bySlot[nearest], run)
	}

	states := make([]Slot, 0, len(slots))
	for _, at := range slots {
		key := at.UTC()
		state := Slot{At: at}
		counted := bySlot[key]
		if latest := latestRun(counted); latest != nil {
			state.RunID = latest.id
			state.Status = SlotFailed
			if latest.status.IsSuccess() {
				state.Status = SlotSucceeded
			}
			if !latest.slot.Equal(key) {
				state.CoveredAt = latest.slot
			}
			if active := slices.IndexFunc(counted, func(r *slotRun) bool { return r.status.IsActive() }); active >= 0 {
				state.Status = SlotActive
				state.RunID = counted[active].id
				state.CoveredAt = time.Time{}
			}
		} else if skippedSucceeded[key] {
			state.Status = SlotSucceeded
		} else {
			state.OtherCadence = otherCadence[key]
		}
		states = append(states, state)
	}
	return states
}

// slotRun is one run the slot index knows of.
type slotRun struct {
	id       string
	slot     time.Time
	trigger  ir.TriggerType
	schedule string
	// recordedAt orders a slot's runs, latest last.
	recordedAt time.Time
	final      *ir.Status
	dropped    bool
	status     ir.Status
}

// collectRuns folds the events into one entry per run, and finds the slots
// the scheduler skipped under skip_if_successful.
func collectRuns(events []persis.SlotEvent) ([]*slotRun, map[time.Time]bool) {
	byID := make(map[string]*slotRun)
	var runs []*slotRun
	skippedSucceeded := make(map[time.Time]bool)
	for _, event := range events {
		if event.Kind == persis.SlotEventSkipped {
			if event.SkipReason == skipReasonSucceeded {
				skippedSucceeded[event.Slot.UTC()] = true
			}
			continue
		}
		if event.RunID == "" {
			continue
		}
		run := byID[event.RunID]
		if run == nil {
			run = &slotRun{id: event.RunID, slot: event.Slot.UTC(), recordedAt: event.RecordedAt}
			byID[event.RunID] = run
			runs = append(runs, run)
		}
		if run.trigger == ir.TriggerTypeUnknown {
			run.trigger = event.TriggerType
		}
		if run.schedule == "" {
			run.schedule = event.Schedule
		}
		switch event.Kind {
		case persis.SlotEventFinal:
			status := event.Status
			run.final = &status
		case persis.SlotEventDropped:
			run.dropped = true
		case persis.SlotEventRun, persis.SlotEventSkipped:
		}
	}
	return runs, skippedSucceeded
}

// skipReasonSucceeded is the reason the scheduler records for a slot that
// skip_if_successful held back.
const skipReasonSucceeded = "succeeded"

// countedStatus returns the status a run counts with, or false when it
// counts for no slot.
func (r *slotRun) countedStatus(live map[string]*ir.DAGRunStatus) (ir.Status, bool) {
	if status, ok := live[r.id]; ok {
		if status.NeverStarted() {
			return 0, false
		}
		return status.Status, true
	}
	if r.dropped || r.final == nil {
		return 0, false
	}
	return *r.final, true
}

// coversNearest reports whether the run can stand for the slot nearest to
// it, which only a run its schedule fired can.
func (r *slotRun) coversNearest() bool {
	switch r.trigger {
	case ir.TriggerTypeScheduler, ir.TriggerTypeCatchUp, ir.TriggerTypeBackfill:
		return r.schedule != ""
	case ir.TriggerTypeUnknown, ir.TriggerTypeManual, ir.TriggerTypeWebhook, ir.TriggerTypeSubDAG,
		ir.TriggerTypeRetry, ir.TriggerTypeFile, ir.TriggerTypeRestart:
		return false
	}
	return false
}

// nearestFire returns the current fire nearest to t, the later one on a tie,
// in UTC.
func nearestFire(sched SlotSchedule, t time.Time) time.Time {
	window := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, t)
	switch {
	case window.Start.IsZero():
		return window.Next.UTC()
	case window.Next.IsZero():
		return window.Start.UTC()
	case window.Next.Sub(t) <= t.Sub(window.Start):
		return window.Next.UTC()
	default:
		return window.Start.UTC()
	}
}

func latestRun(runs []*slotRun) *slotRun {
	var latest *slotRun
	for _, run := range runs {
		if latest == nil || !run.recordedAt.Before(latest.recordedAt) {
			latest = run
		}
	}
	return latest
}

// HistoryReader reads what a DAG's slot history is made of.
type HistoryReader interface {
	SlotEvents(ctx context.Context, dag *ir.DAG, from, to time.Time) ([]persis.SlotEvent, error)
	ListStatuses(ctx context.Context, options persis.DAGRunListOptions) ([]*ir.DAGRunStatus, error)
}

var _ HistoryReader = (*persis.DAGRunRepository)(nil)

// LoadHistory reads the history SlotStates needs for the slots of dag in
// [from, to].
func LoadHistory(ctx context.Context, reader HistoryReader, dag *ir.DAG, sched SlotSchedule, from, to time.Time) (History, error) {
	start, end := HistoryRange(sched, from, to)
	events, err := reader.SlotEvents(ctx, dag, start, end)
	if err != nil {
		return History{}, err
	}
	referenced := make(map[string]bool, len(events))
	for _, event := range events {
		if event.RunID != "" {
			referenced[event.RunID] = true
		}
	}
	history := History{Events: events, Runs: make(map[string]*ir.DAGRunStatus, len(referenced))}
	if len(referenced) == 0 {
		return history, nil
	}
	statuses, err := reader.ListStatuses(ctx, persis.DAGRunListOptions{
		ExactName:  dag.Name,
		AllHistory: true,
		Unbounded:  true,
	})
	if err != nil {
		return History{}, err
	}
	for _, status := range statuses {
		if referenced[status.DAGRunID] {
			history.Runs[status.DAGRunID] = status
		}
	}
	return history, nil
}
