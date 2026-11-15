// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func spanOf(reason schedulerstate.SpanReason, from, to time.Time) schedulerstate.Span {
	return schedulerstate.Span{From: from, To: to, Reason: reason}
}

// missed finds the slots the fixture's daily DAG missed in spans.
func (f *dispatchFixture) missed(catchup time.Duration, spans ...schedulerstate.Span) backfill.Missed {
	f.t.Helper()
	missed, err := backfill.FindMissed(f.ctx, f.repository, f.dag, f.sched, spans, catchup, f.clock)
	require.NoError(f.t, err)
	return missed
}

func reasons(reasons ...schedulerstate.SpanReason) []schedulerstate.SpanReason {
	return reasons
}

// The slots that came due while the DAG was suspended, the scheduler paused,
// or no scheduler ran are missed, with why, and until when the newest span
// that holds one lasted.
func TestFindMissedCountsSpans(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	require.NoError(t, f.write("before", at(2, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))

	missed := f.missed(0,
		spanOf(schedulerstate.SpanSuspended, at(3, 0), at(5, 12)),
		spanOf(schedulerstate.SpanPaused, at(8, 0), at(9, 0)),
		spanOf(schedulerstate.SpanDown, at(12, 1), at(13, 3)),
		spanOf(schedulerstate.SpanPaused, at(15, 3), at(15, 4)),
	)

	assert.Equal(t, backfill.Missed{
		Slots: 6, First: at(3, 2), Last: at(13, 2),
		Reasons: reasons(schedulerstate.SpanSuspended, schedulerstate.SpanPaused, schedulerstate.SpanDown),
		Until:   at(13, 3),
	}, missed)
}

// A slot that ran by any path, whatever its run's status, or that the
// scheduler skipped while the DAG was busy, is not missed.
func TestFindMissedLeavesOutSlotsThatRan(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	require.NoError(t, f.write("before", at(2, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	require.NoError(t, f.write("manual", at(3, 2), ir.Succeeded, ir.TriggerTypeManual, ""))
	require.NoError(t, f.write("backfill", at(4, 2), ir.Failed, ir.TriggerTypeBackfill, "bf1"))
	require.NoError(t, f.write("catchup", at(5, 2), ir.Succeeded, ir.TriggerTypeCatchUp, ""))
	require.NoError(t, f.repository.RecordSkippedSlot(f.ctx, "etl", at(6, 2), "0 2 * * *", "running"))

	missed := f.missed(0, spanOf(schedulerstate.SpanDown, at(3, 0), at(8, 0)))

	assert.Equal(t, backfill.Missed{
		Slots: 1, First: at(7, 2), Last: at(7, 2), Reasons: reasons(schedulerstate.SpanDown), Until: at(8, 0),
	}, missed)
	assert.Equal(t, backfill.Missed{}, f.missed(0, spanOf(schedulerstate.SpanDown, at(3, 0), at(7, 0))),
		"no slot is missed once every one ran")
}

// A DAG misses no slot before its first recorded one, as a DAG created after
// an outage did not miss the outage's slots.
func TestFindMissedStartsAtTheFirstRecordedSlot(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	outage := spanOf(schedulerstate.SpanDown, at(3, 0), at(5, 0))
	assert.Equal(t, backfill.Missed{}, f.missed(0, outage), "a DAG without history")

	require.NoError(t, f.write("first", at(10, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	missed := f.missed(0, outage, spanOf(schedulerstate.SpanPaused, at(12, 0), at(13, 0)))

	assert.Equal(t, backfill.Missed{
		Slots: 1, First: at(12, 2), Last: at(12, 2), Reasons: reasons(schedulerstate.SpanPaused), Until: at(13, 0),
	}, missed)
}

// The slots that catch-up replays after an outage are left to it, and a
// slot older than the DAG's slot retention is not missed: its history is
// gone.
func TestFindMissedLeavesOutReplayedAndExpiredSlots(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	require.NoError(t, f.write("before", at(2, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))

	// A catch-up window of three days replays the slots after 00:00 on the
	// 7th.
	missed := f.missed(72*time.Hour, spanOf(schedulerstate.SpanDown, at(3, 0), at(10, 0)))
	assert.Equal(t, backfill.Missed{
		Slots: 4, First: at(3, 2), Last: at(6, 2), Reasons: reasons(schedulerstate.SpanDown), Until: at(10, 0),
	}, missed)

	// Five days of retention keep the slots after 00:00 on the 15th, and
	// the DAG counts as on its schedule from there.
	f.dag.SlotRetentionDays = 5
	missed = f.missed(0, spanOf(schedulerstate.SpanSuspended, at(12, 0), at(18, 0)))
	assert.Equal(t, 3, missed.Slots)
	assert.Equal(t, at(15, 2), missed.First)
}

// At most 10,000 slots are looked at, the oldest first.
func TestFindMissedCapsSlots(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	minutely := dailyAt(t, "* * * * *")
	require.NoError(t, f.write("before", at(2, 0), ir.Succeeded, ir.TriggerTypeScheduler, ""))

	missed, err := backfill.FindMissed(f.ctx, f.repository, f.dag, minutely,
		[]schedulerstate.Span{spanOf(schedulerstate.SpanDown, at(2, 0), at(10, 0))}, 0, f.clock)
	require.NoError(t, err)

	assert.Equal(t, backfill.MaxMissedSlots, missed.Slots)
	assert.Equal(t, at(2, 0).Add(time.Minute), missed.First)
	assert.Equal(t, at(2, 0).Add(backfill.MaxMissedSlots*time.Minute), missed.Last)
}
