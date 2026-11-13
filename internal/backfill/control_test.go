// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// control withdraws a queued run as a dequeue does, which removes it, and
// records the runs it stops.
func (f *dispatchFixture) control(stopped *[]string) backfill.RunControl {
	return backfill.RunControl{
		ReadRun: f.dispatcher.ReadRun,
		Withdraw: func(ctx context.Context, ref ir.DAGRunRef) error {
			return f.repository.RemoveDAGRun(ctx, ref, persis.DAGRunRemoveOptions{})
		},
		Stop: func(_ context.Context, ref ir.DAGRunRef) error {
			*stopped = append(*stopped, ref.ID)
			return nil
		},
	}
}

func (f *dispatchFixture) states(id string) []backfill.SlotState {
	f.t.Helper()
	report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(id))
	require.NoError(f.t, err)
	var states []backfill.SlotState
	for _, slot := range report.Slots {
		states = append(states, slot.State)
	}
	return states
}

// Cancel withdraws the queued runs, so their slots count as missing again,
// lets a running one finish, and cancels the slots not reached.
func TestCancelWithdrawsQueuedRuns(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{MaxConcurrency: 2})
	f.tick()
	f.finish(f.queued[0], ir.Running)

	var stopped []string
	cancelled, err := f.service.Cancel(f.ctx, record.ID, f.control(&stopped), false)
	require.NoError(t, err)

	assert.Equal(t, backfill.StatusCancelled, cancelled.Status)
	assert.Empty(t, stopped)
	assert.Equal(t, []backfill.SlotState{backfill.SlotStateRunning, backfill.SlotStateCancelled, backfill.SlotStateCancelled}, f.states(record.ID))
	history, err := backfill.LoadHistory(f.ctx, f.repository, f.dag, f.sched, at(2, 2), at(2, 2))
	require.NoError(t, err)
	assert.Equal(t, backfill.SlotMissing, backfill.SlotStates([]time.Time{at(2, 2)}, f.sched, history)[0].Status)

	f.tick()
	assert.Len(t, f.queued, 2, "a cancelled backfill queues nothing more")
	_, err = f.service.Cancel(f.ctx, record.ID, f.control(&stopped), false)
	require.ErrorIs(t, err, backfill.ErrEnded)
}

func TestCancelStopsRunningRuns(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	f.finish(f.queued[0], ir.Running)

	var stopped []string
	_, err := f.service.Cancel(f.ctx, record.ID, f.control(&stopped), true)
	require.NoError(t, err)
	assert.Equal(t, []string{f.queued[0]}, stopped)
}

// Resume after a failure runs the failed slot and those not reached again,
// with new runs.
func TestResumeRunsSlotsThatDidNotSucceed(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	f.finish(f.queued[0], ir.Failed)
	f.tick()
	require.Equal(t, backfill.StatusFailed, f.record(record.ID).Status)

	req := backfill.ResumeRequest{ID: record.ID, DAG: f.dag, Schedule: f.sched, DryRun: true}
	var stopped []string
	preview, err := f.service.Resume(f.ctx, req, f.control(&stopped))
	require.NoError(t, err)
	assert.Equal(t, []backfill.Action{backfill.ActionRun, backfill.ActionRun, backfill.ActionRun}, actions(preview.Plan))

	req.DryRun = false
	resumed, err := f.service.Resume(f.ctx, req, f.control(&stopped))
	require.NoError(t, err)
	assert.Equal(t, preview.Plan.Revision, resumed.Plan.Revision, "the dry run shows the resume")
	require.NotNil(t, resumed.Record)
	assert.True(t, resumed.Record.Open())
	assert.Equal(t, 1, resumed.Record.Generation)

	f.tick()
	require.Len(t, f.queued, 2)
	assert.Equal(t, backfill.RunID(resumed.Record, at(1, 2)), f.queued[1], "the slot gets a new run")
	assert.NotEqual(t, f.queued[0], f.queued[1])
}

// Resume on an open backfill withdraws its queued runs and continues with the
// new concurrency.
func TestResumeAnOpenBackfill(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{MaxConcurrency: 2})
	f.tick()
	require.Len(t, f.queued, 2)

	var stopped []string
	resumed, err := f.service.Resume(f.ctx, backfill.ResumeRequest{
		ID: record.ID, DAG: f.dag, Schedule: f.sched, MaxConcurrency: 3,
	}, f.control(&stopped))
	require.NoError(t, err)
	require.NotNil(t, resumed.Record)
	assert.Equal(t, 3, resumed.Record.Options.MaxConcurrency)
	assert.Len(t, resumed.Record.Slots, 3)

	f.tick()
	assert.Len(t, f.queued, 5, "every slot gets a new run at once")
}

// A resume keeps following a run that is still active: it counts against the
// concurrency and decides how its slot ends.
func TestResumeKeepsActiveRuns(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{MaxConcurrency: 2})
	f.tick()
	require.Len(t, f.queued, 2)
	f.finish(f.queued[0], ir.Running)

	var stopped []string
	resumed, err := f.service.Resume(f.ctx, backfill.ResumeRequest{ID: record.ID, DAG: f.dag, Schedule: f.sched}, f.control(&stopped))
	require.NoError(t, err)
	require.NotNil(t, resumed.Record)
	assert.Len(t, resumed.Record.Slots, 3)
	assert.Equal(t, []backfill.SlotState{backfill.SlotStateRunning, backfill.SlotStatePending, backfill.SlotStatePending}, f.states(record.ID))

	f.tick()
	assert.Len(t, f.queued, 3, "the running run takes one place of two")
	f.finish(f.queued[0], ir.Failed)
	assert.Equal(t, backfill.SlotStateFailed, f.states(record.ID)[0])
}

// After a resume, a slot that gained a successful run since is skipped at
// its turn, whatever --rerun the backfill was submitted with.
func TestResumedBackfillSkipsSlotThatSucceededSince(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	result, err := f.service.Submit(f.ctx, backfill.SubmitRequest{
		Plan:    backfill.PlanRequest{DAG: f.dag, Schedule: f.sched, From: at(1, 0), To: at(3, 23), Rerun: backfill.RerunAll},
		Options: backfill.Options{From: at(1, 0), To: at(3, 23), Rerun: backfill.RerunAll, MaxConcurrency: 1, OnFailure: backfill.OnFailureStop},
	})
	require.NoError(t, err)
	record := result.Record
	f.tick()
	f.finish(f.queued[0], ir.Failed)
	f.tick()
	require.Equal(t, backfill.StatusFailed, f.record(record.ID).Status)

	var stopped []string
	_, err = f.service.Resume(f.ctx, backfill.ResumeRequest{ID: record.ID, DAG: f.dag, Schedule: f.sched}, f.control(&stopped))
	require.NoError(t, err)
	require.NoError(t, f.write("live", at(2, 2), ir.Succeeded, ir.TriggerTypeScheduler, ""))

	f.tick()
	f.finish(f.queued[1], ir.Succeeded)
	f.tick()
	assert.Equal(t, backfill.SlotStateSkipped, f.states(record.ID)[1])
}

// A cancel that fails part way records the runs it withdrew, so they are not
// taken for failed runs.
func TestCancelRecordsRunsWithdrawnBeforeAFailure(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{MaxConcurrency: 3})
	f.tick()
	require.Len(t, f.queued, 3)

	var stopped []string
	control := f.control(&stopped)
	withdraw := control.Withdraw
	control.Withdraw = func(ctx context.Context, ref ir.DAGRunRef) error {
		if ref.ID == f.queued[1] {
			return errors.New("queue unavailable")
		}
		return withdraw(ctx, ref)
	}
	_, err := f.service.Cancel(f.ctx, record.ID, control, false)
	require.ErrorContains(t, err, "queue unavailable")
	assert.Equal(t, backfill.SlotStateCancelled, f.states(record.ID)[0])
	assert.True(t, f.record(record.ID).Open(), "the cancel did not finish")
}
