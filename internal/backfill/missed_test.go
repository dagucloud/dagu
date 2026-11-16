// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
)

func periodOf(reason schedulerstate.PeriodReason, from, to time.Time) schedulerstate.Period {
	return schedulerstate.Period{From: from, To: to, Reason: reason}
}

func reasons(reasons ...schedulerstate.PeriodReason) []schedulerstate.PeriodReason {
	return reasons
}

// longAgo is a time the DAG was first seen before every period.
var longAgo = at(1, 0)

// missed counts the slots of a daily DAG at 02:00 in periods, as on the 20th.
func missed(t *testing.T, dag *ir.DAG, firstSeen time.Time, periods ...schedulerstate.Period) backfill.Missed {
	t.Helper()
	return backfill.FindMissed(dag, dailyAt(t, "0 2 * * *"), periods, backfill.Catchup{}, firstSeen, at(20, 0))
}

// The slots that came due while the DAG was suspended, the scheduler paused,
// or no scheduler ran are missed, whether or not some ran later, with why,
// and until when the newest period that holds one lasted. The tick at a period's
// end runs its own slot.
func TestFindMissedCountsPeriods(t *testing.T) {
	t.Parallel()
	missed := missed(t, &ir.DAG{}, longAgo,
		periodOf(schedulerstate.PeriodSuspended, at(3, 0), at(5, 12)),
		periodOf(schedulerstate.PeriodPaused, at(8, 0), at(9, 0)),
		periodOf(schedulerstate.PeriodDown, at(12, 1), at(13, 3)),
		periodOf(schedulerstate.PeriodPaused, at(15, 3), at(15, 4)),
		periodOf(schedulerstate.PeriodDown, at(16, 0), at(16, 2)),
	)

	assert.Equal(t, backfill.Missed{
		Slots: 6, First: at(3, 2), Last: at(13, 2),
		Reasons: reasons(schedulerstate.PeriodSuspended, schedulerstate.PeriodPaused, schedulerstate.PeriodDown),
		Until:   at(13, 3),
	}, missed)
}

// A slot older than the DAG's slot retention is not missed: its history is
// gone.
func TestFindMissedLeavesOutExpiredSlots(t *testing.T) {
	t.Parallel()
	// Five days of retention keep the slots after 00:00 on the 15th.
	missed := missed(t, &ir.DAG{SlotRetentionDays: 5}, longAgo, periodOf(schedulerstate.PeriodSuspended, at(12, 0), at(18, 0)))

	assert.Equal(t, 3, missed.Slots)
	assert.Equal(t, at(15, 2), missed.First)
}

// The scheduler's outages and pauses hold for a DAG only from when the
// scheduler first saw it, so a DAG created after one misses none of its
// slots. Its own suspensions hold whenever they were. Without that time,
// only its suspensions count.
func TestFindMissedLeavesOutPeriodsBeforeTheDAG(t *testing.T) {
	t.Parallel()
	outage := periodOf(schedulerstate.PeriodDown, at(3, 0), at(6, 0))
	pause := periodOf(schedulerstate.PeriodPaused, at(8, 0), at(11, 0))
	suspension := periodOf(schedulerstate.PeriodSuspended, at(13, 0), at(14, 0))

	assert.Equal(t, 3, missed(t, &ir.DAG{}, longAgo, outage).Slots, "a DAG seen before the outage")
	assert.Equal(t, backfill.Missed{}, missed(t, &ir.DAG{}, at(6, 0), outage), "a DAG first seen as the outage ended")

	partly := missed(t, &ir.DAG{}, at(9, 0), outage, pause)
	assert.Equal(t, 2, partly.Slots, "a DAG first seen during the pause")
	assert.Equal(t, at(9, 2), partly.First)

	assert.Equal(t, backfill.Missed{
		Slots: 1, First: at(13, 2), Last: at(13, 2), Reasons: reasons(schedulerstate.PeriodSuspended), Until: at(14, 0),
	}, missed(t, &ir.DAG{}, time.Time{}, outage, pause, suspension), "a DAG the scheduler never saw")
}

// At most 10,000 slots are looked at, the newest, so the slots of a newer
// period count however many an older period holds.
func TestFindMissedCapsSlotsAtTheNewest(t *testing.T) {
	t.Parallel()
	resumed := at(12, 0).Add(10 * time.Minute)

	missed := backfill.FindMissed(&ir.DAG{}, dailyAt(t, "* * * * *"), []schedulerstate.Period{
		periodOf(schedulerstate.PeriodDown, at(2, 0), at(10, 0)),
		periodOf(schedulerstate.PeriodPaused, at(12, 0), resumed),
	}, backfill.Catchup{}, longAgo, at(20, 0))

	// The pause's 9 slots and the newest 9,991 of the outage's 11,519.
	assert.Equal(t, backfill.Missed{
		Slots: backfill.MaxMissedSlots, First: at(10, 0).Add(-9991 * time.Minute), Last: at(12, 0).Add(9 * time.Minute),
		Reasons: reasons(schedulerstate.PeriodDown, schedulerstate.PeriodPaused), Until: resumed,
	}, missed)
}

// When the scheduler runs again after an outage, catch-up replays the newest
// slots within the DAG's catchup_window, at most Max of them, so only the
// others are missed. Without a window, every slot of the outage is.
func TestFindMissedLeavesOutReplayedSlots(t *testing.T) {
	t.Parallel()
	down := func(sched backfill.SlotSchedule, catchup backfill.Catchup, from, to time.Time) backfill.Missed {
		outage := []schedulerstate.Period{periodOf(schedulerstate.PeriodDown, from, to)}
		return backfill.FindMissed(&ir.DAG{}, sched, outage, catchup, longAgo, at(20, 0))
	}
	minutely, hourly := dailyAt(t, "* * * * *"), dailyAt(t, "0 * * * *")
	restart := at(12, 0)
	hour := backfill.Catchup{Window: time.Hour, Max: 1000}

	assert.Equal(t, backfill.Missed{}, down(minutely, hour, restart.Add(-3*time.Minute), restart), "a restart within the window")

	outage := down(minutely, hour, restart.Add(-2*time.Hour), restart)
	assert.Equal(t, 60, outage.Slots, "the slots before the window")
	assert.Equal(t, restart.Add(-2*time.Hour+time.Minute), outage.First)
	assert.Equal(t, restart.Add(-time.Hour), outage.Last)
	assert.Equal(t, 119, down(minutely, backfill.Catchup{}, restart.Add(-2*time.Hour), restart).Slots, "without a window")

	// A day's window holds 1,440 slots, the restart's own among them, and
	// catch-up replays the newest 1,000.
	day := backfill.Catchup{Window: 24 * time.Hour, Max: 1000}
	assert.Equal(t, 1439-999, down(minutely, day, restart.Add(-24*time.Hour), restart).Slots, "the slots past the cap")

	// From 08:30 to 12:30, the window replays 10:00 to 12:00, or, with
	// overlap_policy latest, 12:00 only.
	from, to := restart.Add(-210*time.Minute), restart.Add(30*time.Minute)
	assert.Equal(t, 1, down(hourly, backfill.Catchup{Window: 3 * time.Hour, Max: 1000}, from, to).Slots)
	assert.Equal(t, 3, down(hourly, backfill.Catchup{Window: 3 * time.Hour, Max: 1}, from, to).Slots, "the slots latest drops")

	// Catch-up replays no suspension's slots.
	suspended := backfill.FindMissed(&ir.DAG{}, minutely, []schedulerstate.Period{
		periodOf(schedulerstate.PeriodSuspended, restart.Add(-3*time.Minute), restart),
	}, hour, longAgo, at(20, 0))
	assert.Equal(t, 2, suspended.Slots)
}

// countingSchedule counts the evaluations of a cron schedule.
type countingSchedule struct {
	cron.Schedule
	calls *int
}

func (s countingSchedule) Next(t time.Time) time.Time {
	*s.calls++
	return s.Schedule.Next(t)
}

// An outage is clamped to the slots catch-up does not queue only when its
// slots are looked at, so older outages cost nothing once the newest fill
// the cap.
func TestFindMissedClampsOnlyTheOutagesLookedAt(t *testing.T) {
	t.Parallel()
	sched := dailyAt(t, "* * * * *")
	var calls int
	sched.Schedules[0].Parsed = countingSchedule{Schedule: sched.Schedules[0].Parsed, calls: &calls}
	catchup := backfill.Catchup{Window: 24 * time.Hour, Max: 1000}

	// A week-long outage, whose slots past catch-up's 1,000 fill the cap,
	// after 99 day-long outages, each with 1,000 slots catch-up queues.
	restart := at(20, 0)
	newest := restart.Add(-8 * 24 * time.Hour)
	outages := []schedulerstate.Period{periodOf(schedulerstate.PeriodDown, newest, restart)}
	for i := 1; i < 100; i++ {
		from := newest.Add(-time.Duration(2*i) * 24 * time.Hour)
		outages = append(outages, periodOf(schedulerstate.PeriodDown, from, from.Add(24*time.Hour)))
	}

	missed := backfill.FindMissed(&ir.DAG{}, sched, outages, catchup, restart.AddDate(-1, 0, 0), restart)

	assert.Equal(t, backfill.Missed{
		Slots: backfill.MaxMissedSlots, First: restart.Add(-(999 + backfill.MaxMissedSlots) * time.Minute), Last: restart.Add(-1000 * time.Minute),
		Reasons: reasons(schedulerstate.PeriodDown), Until: restart,
	}, missed)
	// A few evaluations per fire time read: the slots counted and the newest
	// outage's replay.
	assert.Less(t, calls, 4*(backfill.MaxMissedSlots+catchup.Max), "evaluations")
}

// The reasons are those of the periods that hold a counted slot, in the order
// of the slots, or of the periods for a slot several hold, and until when the
// newest of them lasted. An outage whose slots catch-up queues holds none,
// whether inside a suspension or past its end.
func TestFindMissedNamesThePeriodsHoldingSlots(t *testing.T) {
	t.Parallel()
	hourly := dailyAt(t, "0 * * * *")
	find := func(catchup backfill.Catchup, periods ...schedulerstate.Period) backfill.Missed {
		return backfill.FindMissed(&ir.DAG{}, hourly, periods, catchup, longAgo, at(20, 0))
	}
	suspension := periodOf(schedulerstate.PeriodSuspended, at(10, 0), at(11, 6))
	inside := periodOf(schedulerstate.PeriodDown, at(11, 0), at(11, 3))
	past := periodOf(schedulerstate.PeriodDown, at(11, 4).Add(30*time.Minute), at(11, 8))

	assert.Equal(t, backfill.Missed{
		Slots: 29, First: at(10, 1), Last: at(11, 5),
		Reasons: reasons(schedulerstate.PeriodSuspended), Until: suspension.To,
	}, find(backfill.Catchup{Window: 6 * time.Hour, Max: 1000}, suspension, inside, past), "the outages' slots replayed")
	assert.Equal(t, backfill.Missed{
		Slots: 31, First: at(10, 1), Last: at(11, 7),
		Reasons: reasons(schedulerstate.PeriodSuspended, schedulerstate.PeriodDown), Until: past.To,
	}, find(backfill.Catchup{}, past, inside, suspension), "without catch-up")

	pause := periodOf(schedulerstate.PeriodPaused, at(11, 0), at(11, 2))
	assert.Equal(t, reasons(schedulerstate.PeriodDown, schedulerstate.PeriodPaused), find(backfill.Catchup{}, inside, pause).Reasons)
	assert.Equal(t, reasons(schedulerstate.PeriodPaused, schedulerstate.PeriodDown), find(backfill.Catchup{}, pause, inside).Reasons)
}
