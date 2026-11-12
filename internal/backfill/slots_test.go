// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func at(day, hour int) time.Time {
	return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC)
}

func dailyAt(t *testing.T, expr string) backfill.SlotSchedule {
	t.Helper()
	schedule, err := ir.NewCronSchedule(expr)
	require.NoError(t, err)
	return backfill.SlotSchedule{Schedules: []ir.Schedule{schedule}, Location: time.UTC}
}

// eventLog builds slot events in the order they were recorded.
type eventLog struct {
	events []persis.SlotEvent
}

func (l *eventLog) add(event persis.SlotEvent) *eventLog {
	event.DAGName = "etl"
	event.RecordedAt = time.Date(2026, 11, 1, 0, 0, len(l.events), 0, time.UTC)
	l.events = append(l.events, event)
	return l
}

func (l *eventLog) run(id string, slot time.Time, trigger ir.TriggerType, schedule string) *eventLog {
	return l.add(persis.SlotEvent{Kind: persis.SlotEventRun, Slot: slot, RunID: id, TriggerType: trigger, Schedule: schedule})
}

func (l *eventLog) final(id string, slot time.Time, status ir.Status) *eventLog {
	return l.add(persis.SlotEvent{Kind: persis.SlotEventFinal, Slot: slot, RunID: id, Status: status})
}

func (l *eventLog) dropped(id string, slot time.Time) *eventLog {
	return l.add(persis.SlotEvent{Kind: persis.SlotEventDropped, Slot: slot, RunID: id})
}

func (l *eventLog) skipped(slot time.Time, reason string) *eventLog {
	return l.add(persis.SlotEvent{Kind: persis.SlotEventSkipped, Slot: slot, SkipReason: reason})
}

func live(statuses ...*ir.DAGRunStatus) map[string]*ir.DAGRunStatus {
	runs := make(map[string]*ir.DAGRunStatus, len(statuses))
	for _, status := range statuses {
		runs[status.DAGRunID] = status
	}
	return runs
}

func started(id string, status ir.Status) *ir.DAGRunStatus {
	return &ir.DAGRunStatus{DAGRunID: id, Status: status, StartedAt: "2026-11-01T00:00:00Z"}
}

const scheduler = ir.TriggerTypeScheduler

func TestSlotStates(t *testing.T) {
	t.Parallel()

	slot := at(5, 2)
	tests := []struct {
		name     string
		schedule string
		events   *eventLog
		runs     map[string]*ir.DAGRunStatus
		want     backfill.Slot
	}{
		{
			name:   "no run",
			events: &eventLog{},
			want:   backfill.Slot{Status: backfill.SlotMissing},
		},
		{
			name:   "run in history",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *"),
			runs:   live(started("a", ir.Succeeded)),
			want:   backfill.Slot{Status: backfill.SlotSucceeded, RunID: "a"},
		},
		{
			name:   "partially succeeded counts as succeeded",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *"),
			runs:   live(started("a", ir.PartiallySucceeded)),
			want:   backfill.Slot{Status: backfill.SlotSucceeded, RunID: "a"},
		},
		{
			name:   "latest run decides",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *").run("b", slot, ir.TriggerTypeManual, "0 2 * * *"),
			runs:   live(started("a", ir.Succeeded), started("b", ir.Failed)),
			want:   backfill.Slot{Status: backfill.SlotFailed, RunID: "b"},
		},
		{
			name:   "an active run makes the slot active",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *").run("b", slot, ir.TriggerTypeManual, "0 2 * * *"),
			runs:   live(started("a", ir.Running), started("b", ir.Failed)),
			want:   backfill.Slot{Status: backfill.SlotActive, RunID: "a"},
		},
		{
			name:   "retention keeps the final status",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *").final("a", slot, ir.Succeeded),
			want:   backfill.Slot{Status: backfill.SlotSucceeded, RunID: "a"},
		},
		{
			name:   "a dropped run counts for nothing",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *").dropped("a", slot),
			want:   backfill.Slot{Status: backfill.SlotMissing},
		},
		{
			name:   "a run removed without a record counts for nothing",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *"),
			want:   backfill.Slot{Status: backfill.SlotMissing},
		},
		{
			// A drop whose removal then failed leaves the run.
			name:   "a run still in history wins over its drop",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *").dropped("a", slot),
			runs:   live(started("a", ir.Succeeded)),
			want:   backfill.Slot{Status: backfill.SlotSucceeded, RunID: "a"},
		},
		{
			name:   "a run cancelled while queued counts for nothing",
			events: (&eventLog{}).run("a", slot, scheduler, "0 2 * * *"),
			runs:   live(&ir.DAGRunStatus{DAGRunID: "a", Status: ir.Aborted}),
			want:   backfill.Slot{Status: backfill.SlotMissing},
		},
		{
			name:   "skip_if_successful counts as succeeded",
			events: (&eventLog{}).skipped(slot, "succeeded"),
			want:   backfill.Slot{Status: backfill.SlotSucceeded},
		},
		{
			name:   "a slot skipped while busy is missing",
			events: (&eventLog{}).skipped(slot, "running"),
			want:   backfill.Slot{Status: backfill.SlotMissing},
		},
		{
			// After a move from 02:00 to 01:00, the 02:00 run of October 5
			// covers the 01:00 slot of October 5.
			name:     "a run under an earlier schedule covers the nearest slot",
			schedule: "0 1 * * *",
			events:   (&eventLog{}).run("a", slot, scheduler, "0 2 * * *"),
			runs:     live(started("a", ir.Succeeded)),
			want:     backfill.Slot{At: at(5, 1), Status: backfill.SlotSucceeded, RunID: "a", CoveredAt: slot},
		},
		{
			name:     "a manual run covers only its own slot",
			schedule: "0 1 * * *",
			events:   (&eventLog{}).run("a", slot, ir.TriggerTypeManual, "0 2 * * *"),
			runs:     live(started("a", ir.Succeeded)),
			want:     backfill.Slot{At: at(5, 1), Status: backfill.SlotMissing},
		},
		{
			name:     "a run of another cadence covers nothing",
			schedule: "0 1 * * *",
			events:   (&eventLog{}).run("a", slot, scheduler, "0 2 * * 1-5"),
			runs:     live(started("a", ir.Succeeded)),
			want:     backfill.Slot{At: at(5, 1), Status: backfill.SlotMissing, OtherCadence: true},
		},
		{
			// 13:00 is as far from 01:00 on October 5 as from 01:00 on
			// October 6.
			name:     "a tie goes to the later slot",
			schedule: "0 1 * * *",
			events:   (&eventLog{}).run("a", at(5, 13), scheduler, "0 13 * * *"),
			runs:     live(started("a", ir.Succeeded)),
			want:     backfill.Slot{At: at(6, 1), Status: backfill.SlotSucceeded, RunID: "a", CoveredAt: at(5, 13)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expr := tc.schedule
			if expr == "" {
				expr = "0 2 * * *"
			}
			want := tc.want
			if want.At.IsZero() {
				want.At = slot
			}
			states := backfill.SlotStates([]time.Time{want.At}, dailyAt(t, expr), backfill.History{
				Events: tc.events.events,
				Runs:   tc.runs,
			})
			require.Len(t, states, 1)
			got := states[0]
			assert.Equal(t, got.RunID != "", !got.RecordedAt.IsZero(), "a slot with a run knows when it was recorded")
			got.RecordedAt = time.Time{}
			assert.Equal(t, want, got)
		})
	}
}

func TestHistoryRange(t *testing.T) {
	t.Parallel()

	from, to := backfill.HistoryRange(dailyAt(t, "0 2 * * *"), at(5, 2), at(7, 2))
	assert.Equal(t, at(4, 2), from)
	assert.Equal(t, at(8, 2), to)
}

// Catchup leaves a slot to a backfill whose latest run of it is active or
// succeeded. A manual run does not hold the slot.
func TestHeldByBackfill(t *testing.T) {
	t.Parallel()

	slot := at(5, 2)
	sched := dailyAt(t, "0 2 * * *")
	backfillRun := func(status ir.Status) backfill.History {
		return backfill.History{
			Events: (&eventLog{}).run("b", slot, ir.TriggerTypeBackfill, "0 2 * * *").events,
			Runs:   live(started("b", status)),
		}
	}
	assert.True(t, backfill.HeldByBackfill(slot, sched, backfillRun(ir.Succeeded)))
	assert.True(t, backfill.HeldByBackfill(slot, sched, backfillRun(ir.Queued)))
	assert.False(t, backfill.HeldByBackfill(slot, sched, backfillRun(ir.Failed)))
	manual := backfill.History{
		Events: (&eventLog{}).run("m", slot, ir.TriggerTypeManual, "0 2 * * *").events,
		Runs:   live(started("m", ir.Succeeded)),
	}
	assert.False(t, backfill.HeldByBackfill(slot, sched, manual))
}
