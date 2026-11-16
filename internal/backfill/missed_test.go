// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func spanOf(reason schedulerstate.SpanReason, from, to time.Time) schedulerstate.Span {
	return schedulerstate.Span{From: from, To: to, Reason: reason}
}

// missed finds the slots the fixture's daily DAG missed in spans.
func (f *dispatchFixture) missed(spans ...schedulerstate.Span) backfill.Missed {
	f.t.Helper()
	missed, err := backfill.FindMissed(f.ctx, f.repository, f.dag, f.sched, spans, f.clock)
	require.NoError(f.t, err)
	return missed
}

func reasons(reasons ...schedulerstate.SpanReason) []schedulerstate.SpanReason {
	return reasons
}

// dagAt returns the fixture's DAG under another schedule.
func (f *dispatchFixture) dagAt(schedule string) *ir.DAG {
	f.t.Helper()
	dag, err := spec.LoadYAML(f.ctx, fmt.Appendf(nil, "name: etl\nschedule: %q\nsteps:\n  - run: echo hi\n", schedule))
	require.NoError(f.t, err)
	return dag
}

// writeCreated writes a scheduler run created at created.
func (f *dispatchFixture) writeCreated(runID string, slot time.Time, status ir.Status, created time.Time) {
	f.t.Helper()
	f.writeStatus(created, ir.DAGRunStatus{
		DAGRunID: runID, Status: status, ScheduleTime: slot.Format(time.RFC3339),
		TriggerType: ir.TriggerTypeScheduler, StartedAt: "2026-11-01T00:00:00Z",
	})
}

// writeUnstarted writes a scheduler run aborted before it started, as the
// queue leaves one it drops for a suspended DAG.
func (f *dispatchFixture) writeUnstarted(runID string, slot time.Time) {
	f.t.Helper()
	f.writeStatus(time.Now(), ir.DAGRunStatus{
		DAGRunID: runID, Status: ir.Aborted, ScheduleTime: slot.Format(time.RFC3339),
		TriggerType: ir.TriggerTypeScheduler,
	})
}

func (f *dispatchFixture) writeStatus(created time.Time, status ir.DAGRunStatus) {
	f.t.Helper()
	attempt, err := f.repository.CreateAttempt(f.ctx, f.dag, created, status.DAGRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, attempt.Open(f.ctx))
	status.Name, status.AttemptID = f.dag.Name, attempt.ID()
	require.NoError(f.t, attempt.Write(f.ctx, status))
	require.NoError(f.t, attempt.Close(f.ctx))
}

// The slots that came due while the DAG was suspended, the scheduler paused,
// or no scheduler ran are missed, with why, and until when the newest span
// that holds one lasted.
func TestFindMissedCountsSpans(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	require.NoError(t, f.write("before", at(2, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))

	missed := f.missed(
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

	missed := f.missed(spanOf(schedulerstate.SpanDown, at(3, 0), at(8, 0)))

	assert.Equal(t, backfill.Missed{
		Slots: 1, First: at(7, 2), Last: at(7, 2), Reasons: reasons(schedulerstate.SpanDown), Until: at(8, 0),
	}, missed)
	assert.Equal(t, backfill.Missed{}, f.missed(spanOf(schedulerstate.SpanDown, at(3, 0), at(7, 0))),
		"no slot is missed once every one ran")
}

// A DAG misses no slot before its first recorded one, as a DAG created after
// an outage did not miss the outage's slots.
func TestFindMissedStartsAtTheFirstRecordedSlot(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	outage := spanOf(schedulerstate.SpanDown, at(3, 0), at(5, 0))
	assert.Equal(t, backfill.Missed{}, f.missed(outage), "a DAG without history")

	require.NoError(t, f.write("first", at(10, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	missed := f.missed(outage, spanOf(schedulerstate.SpanPaused, at(12, 0), at(13, 0)))

	assert.Equal(t, backfill.Missed{
		Slots: 1, First: at(12, 2), Last: at(12, 2), Reasons: reasons(schedulerstate.SpanPaused), Until: at(13, 0),
	}, missed)
}

// A slot older than the DAG's slot retention is not missed: its history is
// gone.
func TestFindMissedLeavesOutExpiredSlots(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	f.dag.SlotRetentionDays = 5

	// Five days of retention keep the slots after 00:00 on the 15th, and
	// the DAG counts as on its schedule from there.
	missed := f.missed(spanOf(schedulerstate.SpanSuspended, at(12, 0), at(18, 0)))
	assert.Equal(t, 3, missed.Slots)
	assert.Equal(t, at(15, 2), missed.First)
}

// After an outage, a slot catch-up ran is not missed, and one it did not
// run is, whatever its window: catch-up holds the slots it has yet to run
// in memory, and a restart or a busy DAG loses them.
func TestFindMissedCountsSlotsCatchupDidNotRun(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	f.dag.CatchupWindow = 72 * time.Hour
	require.NoError(t, f.write("before", at(2, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	require.NoError(t, f.write("catchup-7", at(7, 2), ir.Succeeded, ir.TriggerTypeCatchUp, ""))
	require.NoError(t, f.write("catchup-8", at(8, 2), ir.Failed, ir.TriggerTypeCatchUp, ""))

	missed := f.missed(spanOf(schedulerstate.SpanDown, at(3, 0), at(10, 0)))

	assert.Equal(t, backfill.Missed{
		Slots: 5, First: at(3, 2), Last: at(9, 2), Reasons: reasons(schedulerstate.SpanDown), Until: at(10, 0),
	}, missed)
}

// FindMissed reads the status of a run only near the slots it looks at, yet
// leaves missing the same slots as the whole run history does: a run that
// never started, or that a dequeue or an operator removed, counts for none,
// and one that retention removed counts with its final status.
func TestFindMissedAgreesWithRunHistory(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	ref := func(id string) ir.DAGRunRef { return ir.NewDAGRunRef(f.dag.Name, id) }
	f.writeCreated("retired", at(9, 2), ir.Succeeded, time.Now().AddDate(0, 0, -10))
	require.NoError(t, f.write("before", at(2, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	removed, err := f.repository.RemoveOldDAGRuns(f.ctx, f.dag.Name, 0, persis.DAGRunRetentionOptions{RetentionRuns: new(1)})
	require.NoError(t, err)
	require.Equal(t, []string{"retired"}, removed)
	require.NoError(t, f.write("manual", at(3, 2), ir.Succeeded, ir.TriggerTypeManual, ""))
	require.NoError(t, f.write("failed", at(4, 2), ir.Failed, ir.TriggerTypeBackfill, "bf1"))
	require.NoError(t, f.write("running", at(5, 2), ir.Running, ir.TriggerTypeCatchUp, ""))
	require.NoError(t, f.write("queued", at(6, 2), ir.Queued, ir.TriggerTypeBackfill, "bf1"))
	require.NoError(t, f.write("dequeued", at(7, 2), ir.Queued, ir.TriggerTypeBackfill, "bf1"))
	require.NoError(t, queue.AbortQueuedDAGRun(f.ctx, f.repository, ref("dequeued")))
	f.writeUnstarted("dropped", at(8, 2))
	require.NoError(t, f.write("deleted", at(10, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	require.NoError(t, f.repository.RemoveDAGRun(f.ctx, ref("deleted"), persis.DAGRunRemoveOptions{}))
	// A run of an earlier schedule of the same cadence covers the slot
	// nearest to it; one of another cadence covers none.
	require.NoError(t, f.writeRun(f.dagAt("0 1 * * *"), "earlier", at(11, 1), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	require.NoError(t, f.writeRun(f.dagAt("0 * * * *"), "hourly", at(12, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	require.NoError(t, f.repository.RecordSkippedSlot(f.ctx, "etl", at(13, 2), "0 2 * * *", "running"))
	require.NoError(t, f.write("requeued", at(14, 2), ir.Queued, ir.TriggerTypeScheduler, ""))
	require.NoError(t, queue.AbortQueuedDAGRun(f.ctx, f.repository, ref("requeued")))
	require.NoError(t, f.write("rerun", at(14, 2), ir.Succeeded, ir.TriggerTypeManual, ""))

	span := spanOf(schedulerstate.SpanSuspended, at(3, 0), at(19, 0))
	missing := []time.Time{at(7, 2), at(8, 2), at(10, 2), at(12, 2), at(15, 2), at(16, 2), at(17, 2), at(18, 2)}
	var fires []time.Time
	for day := 3; day <= 18; day++ {
		fires = append(fires, at(day, 2))
	}
	history, err := backfill.LoadHistory(f.ctx, f.repository, f.dag, f.sched, fires[0], fires[len(fires)-1])
	require.NoError(t, err)
	var byHistory []time.Time
	for _, state := range backfill.SlotStates(fires, f.sched, history) {
		// The skipped slot has no run either; its skip leaves it out.
		if state.Status == backfill.SlotMissing && !state.At.Equal(at(13, 2)) {
			byHistory = append(byHistory, state.At)
		}
	}
	require.Equal(t, missing, byHistory, "the whole run history")

	assert.Equal(t, backfill.Missed{
		Slots: len(missing), First: missing[0], Last: missing[len(missing)-1],
		Reasons: reasons(schedulerstate.SpanSuspended), Until: at(19, 0),
	}, f.missed(span))
}

// countingHistory is a run history whose runs were created as the slot index
// recorded them. It counts the statuses it lists.
type countingHistory struct {
	events   []persis.SlotEvent
	statuses []*ir.DAGRunStatus
	listed   int
}

// run records a run of a slot created at created.
func (h *countingHistory) run(id string, slot, created time.Time, trigger ir.TriggerType) {
	h.events = append(h.events, persis.SlotEvent{
		Kind: persis.SlotEventRun, DAGName: "etl", Slot: slot, RunID: id,
		TriggerType: trigger, Schedule: "0 2 * * *", RecordedAt: created,
	})
	status := started(id, ir.Succeeded)
	status.CreatedAt = created.UnixMilli()
	h.statuses = append(h.statuses, status)
}

func (h *countingHistory) SlotEvents(_ context.Context, _ *ir.DAG, from, to time.Time) ([]persis.SlotEvent, error) {
	var events []persis.SlotEvent
	for _, event := range h.events {
		if !event.Slot.Before(from) && !event.Slot.After(to) {
			events = append(events, event)
		}
	}
	return events, nil
}

// ListStatuses lists the runs created in the range, as run history is kept
// by creation time.
func (h *countingHistory) ListStatuses(_ context.Context, options persis.DAGRunListOptions) ([]*ir.DAGRunStatus, error) {
	var statuses []*ir.DAGRunStatus
	for _, status := range h.statuses {
		created := time.UnixMilli(status.CreatedAt)
		if (options.From.IsZero() || !created.Before(options.From.Time)) && (options.To.IsZero() || !created.After(options.To.Time)) {
			statuses = append(statuses, status)
		}
	}
	h.listed += len(statuses)
	return statuses, nil
}

// The statuses FindMissed reads are those of the runs created near the runs
// of the slots it looks at, however many runs the DAG has besides.
func TestFindMissedReadsOnlyRunsNearItsSlots(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	history := &countingHistory{}
	// The scheduler ran each day outside the spans, and a backfill ran the
	// 6th on the 25th.
	for day := 1; day <= 27; day++ {
		if (day < 5 || day > 7) && (day < 20 || day > 22) {
			history.run(fmt.Sprintf("daily-%d", day), at(day, 2), at(day, 2), ir.TriggerTypeScheduler)
		}
	}
	history.run("backfill", at(6, 2), at(25, 12), ir.TriggerTypeBackfill)

	missed, err := backfill.FindMissed(f.ctx, history, f.dag, f.sched, []schedulerstate.Span{
		spanOf(schedulerstate.SpanSuspended, at(5, 0), at(8, 0)),
		spanOf(schedulerstate.SpanSuspended, at(20, 0), at(23, 0)),
	}, at(28, 0))
	require.NoError(t, err)

	assert.Equal(t, 5, missed.Slots)
	assert.Equal(t, 1, history.listed, "of %d runs", len(history.statuses))
}

// At most 10,000 slots are looked at, the newest, so the slots of a newer
// span count however many an older span holds.
func TestFindMissedCapsSlotsAtTheNewest(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	require.NoError(t, f.write("before", at(2, 0), ir.Succeeded, ir.TriggerTypeScheduler, ""))
	resumed := at(12, 0).Add(10 * time.Minute)

	missed, err := backfill.FindMissed(f.ctx, f.repository, f.dag, dailyAt(t, "* * * * *"), []schedulerstate.Span{
		spanOf(schedulerstate.SpanDown, at(2, 0), at(10, 0)),
		spanOf(schedulerstate.SpanPaused, at(12, 0), resumed),
	}, f.clock)
	require.NoError(t, err)

	// The pause's 9 slots and the newest 9,991 of the outage's 11,519.
	assert.Equal(t, backfill.Missed{
		Slots: backfill.MaxMissedSlots, First: at(10, 0).Add(-9991 * time.Minute), Last: at(12, 0).Add(9 * time.Minute),
		Reasons: reasons(schedulerstate.SpanDown, schedulerstate.SpanPaused), Until: resumed,
	}, missed)
}

// Of a span that holds more slots than are looked at, the DAG misses those
// looked at when its first recorded slot is among those left out.
func TestFindMissedFindsTheFirstRecordedSlotPastTheCap(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	outage := []schedulerstate.Span{spanOf(schedulerstate.SpanDown, at(2, 0), at(10, 0))}
	slots := func() int {
		t.Helper()
		missed, err := backfill.FindMissed(f.ctx, f.repository, f.dag, dailyAt(t, "* * * * *"), outage, f.clock)
		require.NoError(t, err)
		return missed.Slots
	}
	assert.Zero(t, slots(), "a DAG without history")

	require.NoError(t, f.write("manual", at(3, 0), ir.Succeeded, ir.TriggerTypeManual, ""))
	assert.Equal(t, backfill.MaxMissedSlots, slots())
}
