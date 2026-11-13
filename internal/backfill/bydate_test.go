// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var datedNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// resolveBy resolves a backfill by a dates entry and lists its slots.
func resolveBy(t *testing.T, dag *ir.DAG, sched backfill.SlotSchedule, req backfill.Request) (backfill.PlanRequest, backfill.SlotRange) {
	t.Helper()
	planReq, _, err := req.Resolve(dag, sched, 0, datedNow)
	require.NoError(t, err)
	rng, err := backfill.SlotsOf(planReq)
	require.NoError(t, err)
	return planReq, rng
}

func datedDAG(sched backfill.SlotSchedule, dates ...ir.DateSpec) *ir.DAG {
	return &ir.DAG{Name: "etl", Schedule: sched.Schedules, Dates: dates}
}

// --by selects the slots whose entry falls in the range, however the entry
// is formatted.
func TestSlotsByDate(t *testing.T) {
	t.Parallel()

	hourly := dailyAt(t, "0 * * * *")
	for _, format := range []string{"", ir.DateFormatUnix} {
		dag := datedDAG(hourly, ir.DateSpec{Name: "target_day", Shift: []string{"-1d"}, Format: format})
		_, rng := resolveBy(t, dag, hourly, backfill.Request{By: "target_day", From: "2026-10-01", To: "2026-10-31"})

		require.Len(t, rng.Slots, 744, "format %q", format)
		assert.Equal(t, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), rng.Slots[0])
		assert.Equal(t, time.Date(2026, 11, 1, 23, 0, 0, 0, time.UTC), rng.Slots[len(rng.Slots)-1])
	}
}

// Monday's window on a weekday schedule holds the weekend, so a range of
// one Saturday selects Monday's slot.
func TestSlotsByDateList(t *testing.T) {
	t.Parallel()

	sched := dailyAt(t, "0 2 * * *")
	weekdays, _ := ir.BuiltinCalendar(ir.CalendarWeekdays)
	sched.Schedules[0].Calendar = ir.CalendarWeekdays
	require.NoError(t, sched.Schedules[0].BindCalendar(weekdays))
	dag := datedDAG(sched, ir.DateSpec{Name: "window_days", Each: "1d"})

	_, rng := resolveBy(t, dag, sched, backfill.Request{By: "window_days", From: "2026-11-07", To: "2026-11-07"})
	assert.Equal(t, []time.Time{time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)}, rng.Slots)
}

// A range that reaches today runs to now and is clamped.
func TestSlotsByDateToToday(t *testing.T) {
	t.Parallel()

	daily := dailyAt(t, "0 2 * * *")
	dag := datedDAG(daily, ir.DateSpec{Name: "target_day", Shift: []string{"-1d"}})
	_, rng := resolveBy(t, dag, daily, backfill.Request{By: "target_day", From: "2026-11-10", To: "2026-11-20"})

	assert.True(t, rng.Clamped)
	assert.Equal(t, []time.Time{
		time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 12, 2, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC),
	}, rng.Slots)
}

func TestByRequestErrors(t *testing.T) {
	t.Parallel()

	daily := dailyAt(t, "0 2 * * *")
	dag := datedDAG(daily, ir.DateSpec{Name: "target_day", Shift: []string{"-1d"}})
	for _, tc := range []struct {
		name string
		req  backfill.Request
		want string
	}{
		{name: "unknown entry", req: backfill.Request{By: "load_day", From: "2026-10-01"}, want: `has no date "load_day"`},
		{name: "a time", req: backfill.Request{By: "target_day", From: "2026-10-01T02:00"}, want: "by takes dates"},
		{name: "to before from", req: backfill.Request{By: "target_day", From: "2026-10-02", To: "2026-10-01"}, want: "to is before from"},
		{name: "named slots", req: backfill.Request{By: "target_day", Slots: []string{"2026-10-02T02:00"}}, want: "cannot be combined with slots"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := tc.req.Resolve(dag, daily, 0, datedNow)
			require.ErrorIs(t, err, backfill.ErrInvalidRequest)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// A plan carries the dates each slot's run gets, and the range it selected
// by.
func TestPlanCarriesDates(t *testing.T) {
	t.Parallel()

	daily := dailyAt(t, "0 2 * * *")
	dag := datedDAG(daily, ir.DateSpec{Name: "target_day", Shift: []string{"-1d"}})
	req, rng := resolveBy(t, dag, daily, backfill.Request{By: "target_day", From: "2026-10-01", To: "2026-10-02"})
	states := make([]backfill.Slot, len(rng.Slots))
	for i, slot := range rng.Slots {
		states[i] = backfill.Slot{At: slot, Status: backfill.SlotMissing}
	}

	plan := backfill.MakePlan(req, rng, states, nil)
	assert.Equal(t, "target_day", plan.LoadsEntry())
	assert.Equal(t, ir.CivilDate{Year: 2026, Month: time.October, Day: 1}, plan.FromDate)
	require.Len(t, plan.Slots, 2)
	assert.Equal(t, []ir.DateValue{{Name: "target_day", Value: "2026-10-01"}}, plan.Slots[0].Dates)
	assert.Equal(t, []ir.DateValue{{Name: "target_day", Value: "2026-10-02"}}, plan.Slots[1].Dates)
}

// A list matches by its values, not by the span between them.
func TestSlotsByDateListValues(t *testing.T) {
	t.Parallel()

	weekly := dailyAt(t, "0 2 * * 1")
	dag := datedDAG(weekly, ir.DateSpec{Name: "days", Each: "2d"})
	// The slot of Monday, November 9 lists November 2, 4, 6, and 8.
	_, rng := resolveBy(t, dag, weekly, backfill.Request{By: "days", From: "2026-11-07", To: "2026-11-07"})
	assert.Empty(t, rng.Slots)

	_, rng = resolveBy(t, dag, weekly, backfill.Request{By: "days", From: "2026-11-08", To: "2026-11-08"})
	assert.Equal(t, []time.Time{time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)}, rng.Slots)
}

// A month shift clamps at a month end, so a date can move back from one fire
// to the next; every fire whose date is in the range is still selected.
func TestSlotsByDateAcrossMonthClamp(t *testing.T) {
	t.Parallel()

	hourly := dailyAt(t, "0 * * * *")
	dag := datedDAG(hourly, ir.DateSpec{Name: "due", Shift: []string{"+1mo", "+2h"}})
	_, rng := resolveBy(t, dag, hourly, backfill.Request{By: "due", From: "2026-02-28", To: "2026-02-28"})

	// January 27 22:00 to 28 21:00, then 00:00 to 21:00 of January 29, 30,
	// and 31, which clamp to February 28.
	require.Len(t, rng.Slots, 24+3*22)
	assert.Equal(t, time.Date(2026, 1, 27, 22, 0, 0, 0, time.UTC), rng.Slots[0])
	assert.Equal(t, time.Date(2026, 1, 31, 21, 0, 0, 0, time.UTC), rng.Slots[len(rng.Slots)-1])
}
