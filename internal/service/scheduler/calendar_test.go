// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func calendarSchedule(t *testing.T, expr string, cal *ir.Calendar) ir.Schedule {
	t.Helper()
	schedule, err := ir.ParseScheduleValue(map[string]any{"expression": expr, "calendar": cal.Name},
		ir.ScheduleParseOptions{AllowCalendar: true})
	require.NoError(t, err)
	require.NoError(t, schedule.BindCalendar(cal))
	return schedule
}

func everyDayCalendar(holidays ...ir.CivilDate) *ir.Calendar {
	cal := &ir.Calendar{Name: "ops", Holidays: map[ir.CivilDate]string{}}
	for day := range cal.Weekdays {
		cal.Weekdays[day] = true
	}
	for _, holiday := range holidays {
		cal.Holidays[holiday] = ""
	}
	return cal
}

// A Saturday schedule on the weekdays calendar never fires. Its search
// returns the zero time, which must not count as due or as a missed slot.
func TestCalendarScheduleThatNeverFires(t *testing.T) {
	weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
	schedule := calendarSchedule(t, "0 9 * * 6", weekdays)
	now := time.Date(2026, 1, 3, 9, 30, 0, 0, time.UTC)

	_, due := scheduleDueAt(schedule, now)
	require.False(t, due)
	require.Empty(t, ComputeMissedIntervals([]ir.Schedule{schedule}, now.Add(-48*time.Hour), now))

	planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(now.Add(-2 * time.Hour))})
	planner.cfg.Clock = func() time.Time { return now }
	planner.cfg.Location = time.UTC
	dag := &ir.DAG{Name: "never", CatchupWindow: time.Hour, OverlapPolicy: ir.OverlapPolicyAll,
		Schedule: []ir.Schedule{schedule}}
	require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
	require.Empty(t, planner.Plan(t.Context(), now))
}

// Catch-up slots queued before a holiday was added to the calendar must not
// run on that holiday.
func TestCalendarUpdateDropsPendingHolidaySlots(t *testing.T) {
	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	now := base
	planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(base.Add(-2 * time.Minute))})
	planner.cfg.Clock = func() time.Time { return now }
	planner.cfg.Location = time.UTC
	planner.cfg.Enqueue = func(context.Context, DAGEntry, string, ir.TriggerType, time.Time) error { return nil }
	dag := &ir.DAG{Name: "calendar", CatchupWindow: time.Hour, OverlapPolicy: ir.OverlapPolicyAll,
		Schedule: []ir.Schedule{calendarSchedule(t, "* * * * *", everyDayCalendar())}}
	require.NoError(t, planner.Init(t.Context(), testDAGEntries(dag)))
	now = base.Add(time.Minute)
	planner.Advance(now)

	updated := *dag
	updated.Schedule = []ir.Schedule{
		calendarSchedule(t, "* * * * *", everyDayCalendar(ir.DateOf(base))),
	}
	planner.entryMu.Lock()
	planner.handleEvent(t.Context(), DAGChangeEvent{Type: DAGChangeUpdated,
		DAGEntry: DAGEntry{DAG: &updated, DefinitionID: "calendar"}})
	planner.entryMu.Unlock()

	now = now.Add(time.Minute)
	require.Empty(t, planner.Plan(t.Context(), now))
}
