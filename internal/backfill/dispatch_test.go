// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dispatchFixture runs a dispatcher whose queue is the test: queued runs
// stay queued until finish settles them.
type dispatchFixture struct {
	t          *testing.T
	ctx        context.Context
	repository *persis.DAGRunRepository
	store      *filebackfill.Store
	dispatcher *backfill.Dispatcher
	service    *backfill.Service
	dag        *ir.DAG
	sched      backfill.SlotSchedule
	queued     []string
	// clock is the time the dispatcher and the retries see.
	clock time.Time
}

func newDispatchFixture(t *testing.T) *dispatchFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	f := &dispatchFixture{t: t, ctx: ctx, clock: at(20, 0)}
	f.store = filebackfill.New(filepath.Join(dir, "backfills"))
	now := func() time.Time { return f.clock }
	f.repository = testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: slotindex.New(filepath.Join(dir, "slot-index")),
		Location:  time.UTC,
		OnRetry: func(ctx context.Context, status *ir.DAGRunStatus) error {
			return backfill.FollowRetry(ctx, f.store, status, now())
		},
	})
	dag, err := spec.LoadYAML(ctx, []byte("name: etl\nschedule: \"0 2 * * *\"\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	f.dag = dag
	f.sched = backfill.SlotSchedule{Schedules: dag.Schedule, Location: time.UTC}
	f.service = &backfill.Service{Store: f.store, History: f.repository, Now: now}
	f.dispatcher = &backfill.Dispatcher{
		Store:   f.store,
		History: f.repository,
		ReadRun: func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
			attempt, err := f.repository.FindAttempt(ctx, ref)
			if err != nil {
				return nil, err
			}
			return attempt.ReadStatus(ctx)
		},
		Target: func(context.Context, *backfill.Record) (backfill.Target, error) {
			return backfill.Target{DAG: f.dag, Schedule: f.sched}, nil
		},
		Enqueue: func(_ context.Context, record *backfill.Record, _ backfill.Target, slot backfill.RecordSlot, runID string) error {
			if err := f.write(runID, slot.At, ir.Queued, ir.TriggerTypeBackfill, record.ID); err != nil {
				return err
			}
			f.queued = append(f.queued, runID)
			return nil
		},
		Now: now,
	}
	return f
}

func (f *dispatchFixture) write(runID string, slot time.Time, status ir.Status, trigger ir.TriggerType, backfillID string) error {
	attempt, err := f.repository.CreateAttempt(f.ctx, f.dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{})
	if err != nil {
		return err
	}
	if err := attempt.Open(f.ctx); err != nil {
		return err
	}
	if err := attempt.Write(f.ctx, ir.DAGRunStatus{
		Name: f.dag.Name, DAGRunID: runID, AttemptID: attempt.ID(), Status: status,
		ScheduleTime: slot.Format(time.RFC3339), TriggerType: trigger, BackfillID: backfillID,
		StartedAt: "2026-11-01T00:00:00Z",
	}); err != nil {
		return err
	}
	return attempt.Close(f.ctx)
}

// finish settles a queued run as the queue would.
func (f *dispatchFixture) finish(runID string, status ir.Status) {
	f.t.Helper()
	attempt, err := f.repository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, runID))
	require.NoError(f.t, err)
	current, err := attempt.ReadStatusUncached(f.ctx)
	require.NoError(f.t, err)
	require.NoError(f.t, attempt.Open(f.ctx))
	current.Status, current.FinishedAt = status, f.clock.Format(time.RFC3339)
	require.NoError(f.t, attempt.Write(f.ctx, *current))
	require.NoError(f.t, attempt.Close(f.ctx))
}

// finishRetrying fails a queued run that the DAG's retry policy retries up
// to limit times, after retries of them, as a run created at created.
func (f *dispatchFixture) finishRetrying(runID string, retries, limit int, created time.Time) {
	f.t.Helper()
	attempt, err := f.repository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, runID))
	require.NoError(f.t, err)
	current, err := attempt.ReadStatusUncached(f.ctx)
	require.NoError(f.t, err)
	require.NoError(f.t, attempt.Open(f.ctx))
	current.Status, current.FinishedAt = ir.Failed, f.clock.Format(time.RFC3339)
	current.AutoRetryCount, current.AutoRetryLimit = retries, limit
	current.CreatedAt = created.UnixMilli()
	require.NoError(f.t, attempt.Write(f.ctx, *current))
	require.NoError(f.t, attempt.Close(f.ctx))
}

// retry queues an ended run again, as a queued retry does.
func (f *dispatchFixture) retry(runID string) {
	f.t.Helper()
	ref := ir.NewDAGRunRef(f.dag.Name, runID)
	attempt, err := f.repository.FindAttempt(f.ctx, ref)
	require.NoError(f.t, err)
	current, err := attempt.ReadStatusUncached(f.ctx)
	require.NoError(f.t, err)
	_, swapped, err := f.repository.CompareAndSwapLatestAttemptStatus(f.ctx, ref, current.AttemptID, current.Status,
		func(latest *ir.DAGRunStatus) error {
			latest.Status = ir.Queued
			return nil
		}, persis.DAGRunCompareAndSwapOptions{})
	require.NoError(f.t, err)
	require.True(f.t, swapped)
}

// retryAttempt starts a new attempt of an ended run, as a direct retry does.
func (f *dispatchFixture) retryAttempt(runID string) {
	f.t.Helper()
	attempt, err := f.repository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, runID))
	require.NoError(f.t, err)
	current, err := attempt.ReadStatusUncached(f.ctx)
	require.NoError(f.t, err)
	retry, err := f.repository.CreateAttempt(f.ctx, f.dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{Retry: true})
	require.NoError(f.t, err)
	require.NoError(f.t, retry.Open(f.ctx))
	current.AttemptID, current.Status = retry.ID(), ir.Running
	require.NoError(f.t, retry.Write(f.ctx, *current))
	require.NoError(f.t, retry.Close(f.ctx))
}

func (f *dispatchFixture) submit(options backfill.Options) *backfill.Record {
	f.t.Helper()
	if options.MaxConcurrency == 0 {
		options.MaxConcurrency = 1
	}
	if options.OnFailure == "" {
		options.OnFailure = backfill.OnFailureStop
	}
	options.From, options.To, options.Rerun = at(1, 0), at(3, 23), backfill.RerunNone
	result, err := f.service.Submit(f.ctx, backfill.SubmitRequest{
		Plan:    backfill.PlanRequest{DAG: f.dag, Schedule: f.sched, From: options.From, To: options.To, Rerun: options.Rerun},
		Options: options,
	})
	require.NoError(f.t, err)
	require.NotNil(f.t, result.Record)
	return result.Record
}

func (f *dispatchFixture) tick() {
	f.t.Helper()
	require.NoError(f.t, f.dispatcher.Tick(f.ctx))
}

func (f *dispatchFixture) record(id string) *backfill.Record {
	f.t.Helper()
	record, err := f.store.Get(f.ctx, id)
	require.NoError(f.t, err)
	return record
}

// With a concurrency of one, each slot is queued only after the one before
// it finishes, and the backfill succeeds after the last.
func TestDispatchRunsSlotsInOrder(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})

	for i, slot := range []time.Time{at(1, 2), at(2, 2), at(3, 2)} {
		f.tick()
		f.tick()
		require.Len(t, f.queued, i+1, "one run at a time")
		assert.Equal(t, backfill.RunID(record, slot), f.queued[i])
		f.finish(f.queued[i], ir.Succeeded)
	}
	assert.True(t, f.record(record.ID).Open(), "the last run has just finished")
	f.tick()
	assert.Equal(t, backfill.StatusSucceeded, f.record(record.ID).Status)
}

func TestDispatchHonorsConcurrency(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	f.submit(backfill.Options{MaxConcurrency: 2})

	f.tick()
	require.Len(t, f.queued, 2)
	f.finish(f.queued[0], ir.Succeeded)
	f.tick()
	assert.Len(t, f.queued, 3)
}

// A failed slot stops the backfill by default: the slots it did not reach
// are not run.
func TestDispatchStopsOnFailure(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})

	f.tick()
	f.finish(f.queued[0], ir.Failed)
	f.tick()

	assert.Len(t, f.queued, 1)
	assert.Equal(t, backfill.StatusFailed, f.record(record.ID).Status)
	progress, err := f.store.Progress(f.ctx, record.ID)
	require.NoError(t, err)
	var notRun int
	for _, event := range progress {
		if event.Kind == backfill.ProgressNotRun {
			notRun++
		}
	}
	assert.Equal(t, 2, notRun)
}

func TestDispatchContinuesOnFailure(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{OnFailure: backfill.OnFailureContinue})

	for range 3 {
		f.tick()
		f.finish(f.queued[len(f.queued)-1], ir.Failed)
	}
	f.tick()
	assert.Len(t, f.queued, 3)
	assert.Equal(t, backfill.StatusFailed, f.record(record.ID).Status)
}

// A slot that gained a successful run after planning is skipped at its turn.
func TestDispatchSkipsSlotThatGainedARun(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	require.NoError(t, f.write("manual", at(2, 2), ir.Succeeded, ir.TriggerTypeManual, ""))

	f.tick()
	f.finish(f.queued[0], ir.Succeeded)
	f.tick()

	require.Len(t, f.queued, 2)
	assert.Equal(t, backfill.RunID(record, at(3, 2)), f.queued[1], "the slot that gained a run is skipped")
}

// A backfill whose DAG was renamed or deleted ends cancelled, with the slots
// it did not reach cancelled.
func TestDispatchCancelsWhenTheDAGIsGone(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	f.dispatcher.DAGExists = func(context.Context, *backfill.Record) (bool, error) { return false, nil }
	f.finish(f.queued[0], ir.Succeeded)
	f.tick()

	assert.Len(t, f.queued, 1)
	assert.Equal(t, backfill.StatusCancelled, f.record(record.ID).Status)
	report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
	require.NoError(t, err)
	assert.Equal(t, 2, report.Counts[backfill.SlotStateCancelled])
}

// A run queued just before a crash is found again instead of queued twice.
func TestDispatchFindsARunQueuedBeforeACrash(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	require.NoError(t, f.write(backfill.RunID(record, at(1, 2)), at(1, 2), ir.Queued, ir.TriggerTypeBackfill, record.ID))
	enqueue := f.dispatcher.Enqueue
	f.dispatcher.Enqueue = func(ctx context.Context, r *backfill.Record, target backfill.Target, slot backfill.RecordSlot, runID string) error {
		if runID == backfill.RunID(record, at(1, 2)) {
			return errors.Join(errors.New("create attempt"), dagrun.ErrDAGRunAlreadyExists)
		}
		return enqueue(ctx, r, target, slot, runID)
	}

	f.tick()
	assert.Empty(t, f.queued, "the run found counts as active")
	f.finish(backfill.RunID(record, at(1, 2)), ir.Succeeded)
	f.tick()
	assert.Equal(t, []string{backfill.RunID(record, at(2, 2))}, f.queued)
}

// A run that history retention removed keeps its final status for the
// backfill, so a slot that succeeded does not stop the backfill.
func TestDispatchReadsRetainedStatusOfARemovedRun(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	f.finish(f.queued[0], ir.Succeeded)

	cutoff := time.Now().Add(time.Hour)
	removed, err := f.repository.RemoveOldDAGRuns(f.ctx, f.dag.Name, 0, persis.DAGRunRetentionOptions{OlderThan: &cutoff})
	require.NoError(t, err)
	require.Equal(t, []string{f.queued[0]}, removed)

	f.tick()
	assert.True(t, f.record(record.ID).Open(), "the backfill goes on")
	assert.Len(t, f.queued, 2, "the next slot is queued")
}

// Once the dispatcher has seen a run end, neither it nor a report reads the
// run again, so each read of a long backfill costs a lookup per run in
// progress only.
func TestDispatchReadsOnlyRunsInProgress(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	reads := map[string]int{}
	read := f.dispatcher.ReadRun
	f.dispatcher.ReadRun = func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
		reads[ref.ID]++
		return read(ctx, ref)
	}
	record := f.submit(backfill.Options{MaxConcurrency: 2, OnFailure: backfill.OnFailureContinue})
	f.tick()
	f.finish(f.queued[0], ir.Succeeded)
	f.finish(f.queued[1], ir.Failed)
	f.tick()
	require.Len(t, f.queued, 3)

	clear(reads)
	f.tick()
	report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{f.queued[2]: 2}, reads, "only the queued run is read")
	assert.Equal(t, map[backfill.SlotState]int{
		backfill.SlotStateSucceeded: 1,
		backfill.SlotStateFailed:    1,
		backfill.SlotStateQueued:    1,
	}, report.Counts)
}

// A retry of a backfill run that ended makes its slot active again: the
// backfill waits for the retry and counts how it ends.
func TestDispatchFollowsARetry(t *testing.T) {
	t.Parallel()
	for name, retry := range map[string]func(*dispatchFixture, string){
		"queued":      (*dispatchFixture).retry,
		"new attempt": (*dispatchFixture).retryAttempt,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newDispatchFixture(t)
			record := f.submit(backfill.Options{OnFailure: backfill.OnFailureContinue})
			f.tick()
			f.finish(f.queued[0], ir.Failed)
			f.tick()
			require.Len(t, f.queued, 2)

			retry(f, f.queued[0])
			f.finish(f.queued[1], ir.Succeeded)
			f.tick()
			assert.Len(t, f.queued, 2, "the retry holds the backfill's one place")

			f.finish(f.queued[0], ir.Succeeded)
			f.tick()
			require.Len(t, f.queued, 3)
			f.finish(f.queued[2], ir.Succeeded)
			f.tick()
			assert.Equal(t, backfill.StatusSucceeded, f.record(record.ID).Status, "the retry succeeded")
		})
	}
}

// A retry that queues a run again after the dispatcher read the run's old
// status keeps the dispatcher from recording that status as the slot's
// outcome.
func TestDispatchDropsAnOutcomeARetryOvertook(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	runID := f.queued[0]
	f.finish(runID, ir.Failed)
	read := f.dispatcher.ReadRun
	f.dispatcher.ReadRun = func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
		status, err := read(ctx, ref)
		if ref.ID == runID {
			f.retry(runID)
		}
		return status, err
	}
	f.tick()
	f.dispatcher.ReadRun = read

	report, err := backfill.Describe(f.ctx, f.store, read, f.record(record.ID))
	require.NoError(t, err)
	assert.Equal(t, backfill.SlotStateQueued, report.Slots[0].State, "the slot follows the retry")
	f.tick()
	f.finish(runID, ir.Succeeded)
	f.tick()
	assert.Len(t, f.queued, 2, "the backfill goes on once the retry succeeds")
}

// A run that a retry queued again but has not made active yet counts as
// active for a while, so the backfill does not end between the two.
func TestDispatchWaitsForARetryToStart(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{OnFailure: backfill.OnFailureContinue})
	for range 3 {
		f.tick()
		f.finish(f.queued[len(f.queued)-1], ir.Failed)
	}
	attempt, err := f.repository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, f.queued[0]))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)
	f.clock = f.clock.Add(time.Second)
	require.NoError(t, backfill.FollowRetry(f.ctx, f.store, status, f.clock))

	f.tick()
	assert.True(t, f.record(record.ID).Open(), "the retry is about to start the run")
	f.clock = f.clock.Add(2 * time.Minute)
	f.tick()
	assert.Equal(t, backfill.StatusFailed, f.record(record.ID).Status, "the retry never started")
}

// A retry that queues a slot again while a tick runs keeps the backfill
// open, though the tick saw every slot ended.
func TestDispatchStaysOpenForARetryDuringATick(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{OnFailure: backfill.OnFailureContinue})
	f.tick()
	f.finish(f.queued[0], ir.Failed)
	for range 2 {
		f.tick()
		f.finish(f.queued[len(f.queued)-1], ir.Succeeded)
	}
	read := f.dispatcher.ReadRun
	f.dispatcher.ReadRun = func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
		if ref.ID == f.queued[2] {
			f.retry(f.queued[0])
		}
		return read(ctx, ref)
	}
	f.tick()
	f.dispatcher.ReadRun = read
	assert.True(t, f.record(record.ID).Open(), "the retried slot is active again")

	f.tick()
	assert.True(t, f.record(record.ID).Open())
	f.finish(f.queued[0], ir.Succeeded)
	f.tick()
	assert.Equal(t, backfill.StatusSucceeded, f.record(record.ID).Status)
}

// A retry of a run of a backfill that ended opens the backfill again: it
// ends as the slots end then. A cancelled backfill stays cancelled, and a
// stopped one still has the slots it did not reach.
func TestRetryReopensAnEndedBackfill(t *testing.T) {
	t.Parallel()
	t.Run("failed", func(t *testing.T) {
		t.Parallel()
		f := newDispatchFixture(t)
		record := f.submit(backfill.Options{OnFailure: backfill.OnFailureContinue})
		for i, status := range []ir.Status{ir.Failed, ir.Succeeded, ir.Succeeded} {
			f.tick()
			f.finish(f.queued[i], status)
		}
		f.tick()
		require.Equal(t, backfill.StatusFailed, f.record(record.ID).Status)

		f.retry(f.queued[0])
		assert.True(t, f.record(record.ID).Open(), "the retry opens the backfill")
		f.tick()
		assert.True(t, f.record(record.ID).Open())
		f.finish(f.queued[0], ir.Succeeded)
		f.tick()
		assert.Equal(t, backfill.StatusSucceeded, f.record(record.ID).Status, "every slot succeeded")
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		f := newDispatchFixture(t)
		record := f.submit(backfill.Options{})
		f.tick()
		f.finish(f.queued[0], ir.Failed)
		require.NoError(t, f.store.End(f.ctx, record.ID, backfill.StatusCancelled, f.clock))

		f.retry(f.queued[0])
		assert.Equal(t, backfill.StatusCancelled, f.record(record.ID).Status)
		report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
		require.NoError(t, err)
		assert.Equal(t, backfill.SlotStateQueued, report.Slots[0].State, "the slot follows its run")
	})
	t.Run("stopped", func(t *testing.T) {
		t.Parallel()
		f := newDispatchFixture(t)
		record := f.submit(backfill.Options{})
		f.tick()
		f.finish(f.queued[0], ir.Failed)
		f.tick()
		require.Equal(t, backfill.StatusFailed, f.record(record.ID).Status)

		f.retry(f.queued[0])
		f.tick()
		assert.True(t, f.record(record.ID).Open())
		f.finish(f.queued[0], ir.Succeeded)
		f.tick()
		assert.Equal(t, backfill.StatusFailed, f.record(record.ID).Status, "two slots were not run")
		assert.Len(t, f.queued, 1, "the stopped backfill queues nothing more")
	})
}

// A failed run that the DAG's retry policy will retry is not yet a failed
// slot: the backfill waits for the retry instead of stopping. Once the
// retries run out, or the retry scan's window passes, the slot has failed.
func TestDispatchWaitsForTheRetryPolicy(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		retries int
		window  time.Duration
		age     time.Duration
		waits   bool
	}{
		"retries left":         {retries: 0, window: 24 * time.Hour, waits: true},
		"retries spent":        {retries: 2, window: 24 * time.Hour},
		"no retry scan":        {retries: 0, window: 0},
		"past the scan window": {retries: 0, window: time.Hour, age: 2 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newDispatchFixture(t)
			f.dispatcher.RetryWindow = tc.window
			record := f.submit(backfill.Options{})
			f.tick()
			f.finishRetrying(f.queued[0], tc.retries, 2, f.clock.Add(-tc.age))
			f.tick()

			if tc.waits {
				assert.True(t, f.record(record.ID).Open(), "the retry may still fix the slot")
				assert.Len(t, f.queued, 1, "the failed slot holds the backfill's one place")
				return
			}
			assert.Equal(t, backfill.StatusFailed, f.record(record.ID).Status)
		})
	}
}

// A retry of a run whose backfill is removed while the retry waits for its
// lock goes on without the backfill.
func TestRetryOfARemovedBackfill(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	f.finish(f.queued[0], ir.Failed)
	f.tick()
	attempt, err := f.repository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, f.queued[0]))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.ctx)
	require.NoError(t, err)

	store := lockHook{Store: f.store, before: func() {
		require.NoError(t, f.store.Delete(f.ctx, record.ID))
	}}
	require.NoError(t, backfill.FollowRetry(f.ctx, store, status, f.clock))
}

// lockHook runs before each time a backfill's lock is taken.
type lockHook struct {
	backfill.Store
	before func()
}

func (h lockHook) LockRecord(ctx context.Context, id string) (func(), error) {
	h.before()
	return h.Store.LockRecord(ctx, id)
}

// failAt fails a queued run at step with err, as a run whose step failed.
func (f *dispatchFixture) failAt(runID, step, err string) {
	f.t.Helper()
	attempt, findErr := f.repository.FindAttempt(f.ctx, ir.NewDAGRunRef(f.dag.Name, runID))
	require.NoError(f.t, findErr)
	current, readErr := attempt.ReadStatusUncached(f.ctx)
	require.NoError(f.t, readErr)
	require.NoError(f.t, attempt.Open(f.ctx))
	current.Status, current.FinishedAt = ir.Failed, f.clock.Format(time.RFC3339)
	current.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "extract"}, Status: ir.NodeSucceeded},
		{Step: ir.Step{Name: step}, Status: ir.NodeFailed, Error: err},
	}
	require.NoError(f.t, attempt.Write(f.ctx, *current))
	require.NoError(f.t, attempt.Close(f.ctx))
}

// A backfill's failed slots are grouped by the step their runs failed at and
// its error, the largest group first, each with its earliest slot's run.
func TestDescribeGroupsFailures(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{OnFailure: backfill.OnFailureContinue})
	for _, fail := range []struct{ step, err string }{
		{"export", "disk full"},
		{"load", "connection refused\ndial tcp 10.0.0.1:5432"},
		{"load", "connection refused"},
	} {
		f.tick()
		f.failAt(f.queued[len(f.queued)-1], fail.step, fail.err)
	}
	f.tick()

	report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
	require.NoError(t, err)
	require.Len(t, report.Failures, 2)
	assert.Equal(t, backfill.Failure{Step: "load", Error: "connection refused", Slots: 2, At: at(2, 2), RunID: f.queued[1]}, report.Failures[0])
	assert.Equal(t, backfill.Failure{Step: "export", Error: "disk full", Slots: 1, At: at(1, 2), RunID: f.queued[0]}, report.Failures[1])
}

// A run that failed since the dispatcher's last tick is grouped by how it
// failed already, from the status the report reads.
func TestDescribeGroupsAFailureNotYetRecorded(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	f.tick()
	f.failAt(f.queued[0], "load", "connection refused")

	report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
	require.NoError(t, err)
	require.Len(t, report.Failures, 1)
	assert.Equal(t, "load", report.Failures[0].Step)
	assert.Equal(t, "connection refused", report.Failures[0].Error)
}

// The dispatcher reports a backfill once, in the tick it ends.
func TestDispatchReportsTheEnd(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	var ended []*backfill.Record
	f.dispatcher.Ended = func(_ context.Context, record *backfill.Record) {
		ended = append(ended, record)
	}
	record := f.submit(backfill.Options{})
	for range 3 {
		f.tick()
		require.Empty(t, ended, "the backfill is open while a slot is left")
		f.finish(f.queued[len(f.queued)-1], ir.Succeeded)
	}
	f.tick()
	require.Len(t, ended, 1)
	assert.Equal(t, record.ID, ended[0].ID)
	assert.Equal(t, backfill.StatusSucceeded, ended[0].Status)

	f.tick()
	assert.Len(t, ended, 1, "an ended backfill is not reported again")
}
