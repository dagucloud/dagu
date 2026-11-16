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
)

func spanOf(reason schedulerstate.SpanReason, from, to time.Time) schedulerstate.Span {
	return schedulerstate.Span{From: from, To: to, Reason: reason}
}

func reasons(reasons ...schedulerstate.SpanReason) []schedulerstate.SpanReason {
	return reasons
}

// longAgo is a time the DAG was first seen before every span.
var longAgo = at(1, 0)

// missed counts the slots of a daily DAG at 02:00 in spans, as on the 20th.
func missed(t *testing.T, dag *ir.DAG, firstSeen time.Time, spans ...schedulerstate.Span) backfill.Missed {
	t.Helper()
	return backfill.FindMissed(dag, dailyAt(t, "0 2 * * *"), spans, firstSeen, at(20, 0))
}

// The slots that came due while the DAG was suspended, the scheduler paused,
// or no scheduler ran are missed, whether or not some ran later, with why,
// and until when the newest span that holds one lasted. The tick at a span's
// end runs its own slot.
func TestFindMissedCountsSpans(t *testing.T) {
	t.Parallel()
	missed := missed(t, &ir.DAG{}, longAgo,
		spanOf(schedulerstate.SpanSuspended, at(3, 0), at(5, 12)),
		spanOf(schedulerstate.SpanPaused, at(8, 0), at(9, 0)),
		spanOf(schedulerstate.SpanDown, at(12, 1), at(13, 3)),
		spanOf(schedulerstate.SpanPaused, at(15, 3), at(15, 4)),
		spanOf(schedulerstate.SpanDown, at(16, 0), at(16, 2)),
	)

	assert.Equal(t, backfill.Missed{
		Slots: 6, First: at(3, 2), Last: at(13, 2),
		Reasons: reasons(schedulerstate.SpanSuspended, schedulerstate.SpanPaused, schedulerstate.SpanDown),
		Until:   at(13, 3),
	}, missed)
}

// A slot older than the DAG's slot retention is not missed: its history is
// gone.
func TestFindMissedLeavesOutExpiredSlots(t *testing.T) {
	t.Parallel()
	// Five days of retention keep the slots after 00:00 on the 15th.
	missed := missed(t, &ir.DAG{SlotRetentionDays: 5}, longAgo, spanOf(schedulerstate.SpanSuspended, at(12, 0), at(18, 0)))

	assert.Equal(t, 3, missed.Slots)
	assert.Equal(t, at(15, 2), missed.First)
}

// The scheduler's outages and pauses hold for a DAG only from when the
// scheduler first saw it, so a DAG created after one misses none of its
// slots. Its own suspensions hold whenever they were. Without that time,
// only its suspensions count.
func TestFindMissedLeavesOutSpansBeforeTheDAG(t *testing.T) {
	t.Parallel()
	outage := spanOf(schedulerstate.SpanDown, at(3, 0), at(6, 0))
	pause := spanOf(schedulerstate.SpanPaused, at(8, 0), at(11, 0))
	suspension := spanOf(schedulerstate.SpanSuspended, at(13, 0), at(14, 0))

	assert.Equal(t, 3, missed(t, &ir.DAG{}, longAgo, outage).Slots, "a DAG seen before the outage")
	assert.Equal(t, backfill.Missed{}, missed(t, &ir.DAG{}, at(6, 0), outage), "a DAG first seen as the outage ended")

	partly := missed(t, &ir.DAG{}, at(9, 0), outage, pause)
	assert.Equal(t, 2, partly.Slots, "a DAG first seen during the pause")
	assert.Equal(t, at(9, 2), partly.First)

	assert.Equal(t, backfill.Missed{
		Slots: 1, First: at(13, 2), Last: at(13, 2), Reasons: reasons(schedulerstate.SpanSuspended), Until: at(14, 0),
	}, missed(t, &ir.DAG{}, time.Time{}, outage, pause, suspension), "a DAG the scheduler never saw")
}

// At most 10,000 slots are looked at, the newest, so the slots of a newer
// span count however many an older span holds.
func TestFindMissedCapsSlotsAtTheNewest(t *testing.T) {
	t.Parallel()
	resumed := at(12, 0).Add(10 * time.Minute)

	missed := backfill.FindMissed(&ir.DAG{}, dailyAt(t, "* * * * *"), []schedulerstate.Span{
		spanOf(schedulerstate.SpanDown, at(2, 0), at(10, 0)),
		spanOf(schedulerstate.SpanPaused, at(12, 0), resumed),
	}, longAgo, at(20, 0))

	// The pause's 9 slots and the newest 9,991 of the outage's 11,519.
	assert.Equal(t, backfill.Missed{
		Slots: backfill.MaxMissedSlots, First: at(10, 0).Add(-9991 * time.Minute), Last: at(12, 0).Add(9 * time.Minute),
		Reasons: reasons(schedulerstate.SpanDown, schedulerstate.SpanPaused), Until: resumed,
	}, missed)
}
