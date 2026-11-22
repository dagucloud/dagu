// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

// fakeHistoryStore is the store and the recorder of a ServiceHistory in one.
type fakeHistoryStore struct {
	noDir     bool
	samples   []samples.Sample
	order     *[]string // shared with a scanner harness, when set
	newest    time.Time
	hasNewest bool
	dirCalls  int
	opens     int
	closes    int
	opts      samples.Options

	openErr, recordErr, newestErr error
}

type fakeHistoryRecorder struct{ store *fakeHistoryStore }

func (r fakeHistoryRecorder) Record(_ context.Context, s samples.Sample) error {
	if r.store.recordErr != nil {
		return r.store.recordErr
	}
	r.store.samples = append(r.store.samples, s)
	if r.store.order != nil {
		*r.store.order = append(*r.store.order, "sample:"+s.RunID)
	}
	return nil
}

func (r fakeHistoryRecorder) Flush(context.Context) error { return nil }

func (r fakeHistoryRecorder) Close(context.Context) error {
	r.store.closes++
	return nil
}

func (f *fakeHistoryStore) history(clock Clock) *ServiceHistory {
	return NewServiceHistory(ServiceHistoryConfig{
		Dir: func(string) (string, bool) {
			f.dirCalls++
			return "/history", !f.noDir
		},
		Open: func(_ context.Context, _ string, o samples.Options) (samples.Recorder, error) {
			f.opens++
			f.opts = o
			if f.openErr != nil {
				return nil, f.openErr
			}
			return fakeHistoryRecorder{f}, nil
		},
		NewestEnd: func(string) (time.Time, bool, error) {
			return f.newest, f.hasNewest, f.newestErr
		},
		Clock: clock,
	})
}

var historyT0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

// histRun makes an ended run that starts at historyT0 plus start and lasts
// for ran.
func histRun(id string, status ir.Status, start, ran time.Duration) ir.DAGRunStatus {
	const layout = "2006-01-02T15:04:05.000Z07:00"
	return ir.DAGRunStatus{
		DAGRunID:   id,
		AttemptID:  id + "-a1",
		Status:     status,
		StartedAt:  historyT0.Add(start).Format(layout),
		FinishedAt: historyT0.Add(start + ran).Format(layout),
	}
}

func runIDs(list []samples.Sample) []string {
	var ids []string
	for _, s := range list {
		ids = append(ids, s.RunID)
	}
	return ids
}

func TestServiceHistory(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "svc", RestartWait: 5 * time.Second}
	now := historyT0.Add(time.Hour)
	clock := func() time.Time { return now }

	t.Run("OrderAndDedup", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{}
		h := store.history(clock)
		r1 := histRun("r1", ir.Succeeded, 0, time.Second)
		r2 := histRun("r2", ir.Succeeded, 2*time.Second, time.Second)
		r3 := histRun("r3", ir.Succeeded, 4*time.Second, time.Second)
		active := histRun("r4", ir.Running, 6*time.Second, 0)

		require.NoError(t, h.Observe(t.Context(), dag, r3, active, r1, r2, r3))
		assert.Equal(t, []string{"r1", "r2", "r3"}, runIDs(store.samples), "the oldest end first, once")

		require.NoError(t, h.Observe(t.Context(), dag, r3, r2))
		assert.Len(t, store.samples, 3, "a run that is recorded is not recorded again")

		active.Status, active.FinishedAt = ir.Succeeded, histRun("r4", ir.Succeeded, 6*time.Second, time.Second).FinishedAt
		require.NoError(t, h.Observe(t.Context(), dag, active))
		assert.Equal(t, []string{"r1", "r2", "r3", "r4"}, runIDs(store.samples), "a run records when it ends")
		assert.Equal(t, 1, store.opens)
	})

	t.Run("OpenOptions", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{}
		require.NoError(t, store.history(clock).Observe(t.Context(), dag))
		assert.True(t, store.opts.RunHistory)
		assert.Equal(t, int64(4<<20), store.opts.MaxEventBytes)
	})

	t.Run("NoDir", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{noDir: true}
		require.NoError(t, store.history(clock).Observe(t.Context(), dag, histRun("r1", ir.Succeeded, 0, time.Second)))
		assert.Zero(t, store.opens)
	})

	t.Run("NilHistory", func(t *testing.T) {
		t.Parallel()
		var h *ServiceHistory
		require.NoError(t, h.Observe(t.Context(), dag, histRun("r1", ir.Succeeded, 0, time.Second)))
		h.Forget(t.Context(), nil)
		h.Close(t.Context())
	})

	// A retry is a new attempt of the same run, and it records one more sample.
	t.Run("RetryAttempt", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{}
		h := store.history(clock)
		first := histRun("r1", ir.Failed, 0, time.Second)
		require.NoError(t, h.Observe(t.Context(), dag, first))
		retry := histRun("r1", ir.Succeeded, 10*time.Second, time.Second)
		retry.AttemptID = "r1-a2"
		require.NoError(t, h.Observe(t.Context(), dag, retry, first))
		assert.Equal(t, []string{"r1", "r1"}, runIDs(store.samples))
		assert.Equal(t, 1, store.samples[0].ExitCode)
		assert.Zero(t, store.samples[1].ExitCode)
	})

	t.Run("OverlappingRun", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{}
		h := store.history(clock)
		require.NoError(t, h.Observe(t.Context(), dag, histRun("r2", ir.Succeeded, 5*time.Second, time.Second)))
		require.NoError(t, h.Observe(t.Context(), dag, histRun("r1", ir.Succeeded, 0, 8*time.Second)))
		assert.Equal(t, []string{"r2", "r1"}, runIDs(store.samples), "an older run still records")
	})

	t.Run("Mapping", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("é", 200)
		failedStep := histRun("r", ir.Failed, 0, 1500*time.Millisecond)
		failedStep.Nodes = []*ir.Node{
			{Step: ir.Step{Name: "ok"}, Status: ir.NodeSucceeded, Error: "not this"},
			{Step: ir.Step{Name: "a"}, Status: ir.NodeFailed, Error: "first failure"},
			{Step: ir.Step{Name: "b"}, Status: ir.NodeFailed, Error: "second failure"},
		}
		runError := histRun("r", ir.Failed, 0, time.Second)
		runError.Error = "run error"
		runError.Nodes = failedStep.Nodes
		cut := histRun("r", ir.Failed, 0, time.Second)
		cut.Error = long
		notStarted := ir.DAGRunStatus{
			DAGRunID: "r", AttemptID: "a", Status: ir.Failed,
			FinishedAt: histRun("r", ir.Failed, 7*time.Second, 0).FinishedAt,
		}
		aborted := histRun("r", ir.Aborted, 0, time.Second)
		aborted.Error = "stopped"

		tests := []struct {
			name     string
			run      ir.DAGRunStatus
			time     time.Time
			duration time.Duration
			exit     int
			message  string
			gap      time.Duration
		}{
			// A gap event follows a success only: the scanner makes a run that
			// does not succeed wait longer. The bound is restart_wait_sec plus 60 s.
			{name: "Succeeded", run: histRun("r", ir.Succeeded, 0, time.Second), time: historyT0, duration: time.Second, gap: 65 * time.Second},
			{name: "Partial", run: histRun("r", ir.PartiallySucceeded, 0, time.Second), time: historyT0, duration: time.Second, gap: 65 * time.Second},
			{name: "Aborted", run: aborted, time: historyT0, duration: time.Second},
			{name: "Rejected", run: histRun("r", ir.Rejected, 0, 0), time: historyT0},
			{name: "FailedStep", run: failedStep, time: historyT0, duration: 1500 * time.Millisecond, exit: 1, message: "first failure"},
			{name: "FailedRun", run: runError, time: historyT0, duration: time.Second, exit: 1, message: "run error"},
			{name: "FailedCut", run: cut, time: historyT0, duration: time.Second, exit: 1, message: strings.Repeat("é", 128)},
			{name: "NotStarted", run: notStarted, time: historyT0.Add(7 * time.Second), exit: 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				store := &fakeHistoryStore{}
				require.NoError(t, store.history(clock).Observe(t.Context(), dag, tt.run))
				require.Len(t, store.samples, 1)
				got := store.samples[0]
				assert.True(t, tt.time.Equal(got.Time), "time = %v, want %v", got.Time, tt.time)
				assert.Equal(t, tt.duration, got.Duration)
				assert.Equal(t, tt.exit, got.ExitCode)
				assert.Equal(t, tt.message, got.Message)
				assert.Equal(t, "r", got.RunID)
				assert.Equal(t, tt.gap, got.GapAfter)
				assert.Equal(t, 15*time.Second, got.ActiveGap, "restart_wait_sec plus 10 s, whatever the status")
			})
		}
	})

	// The store holds every run that ended at or before its newest sample, so
	// only a later run records.
	t.Run("AfterNewestEnd", func(t *testing.T) {
		t.Parallel()
		runs := []ir.DAGRunStatus{
			histRun("r3", ir.Succeeded, 20*time.Second, time.Second),
			histRun("r2", ir.Succeeded, 10*time.Second, time.Second),
			histRun("r1", ir.Succeeded, 0, time.Second),
		}
		store := &fakeHistoryStore{hasNewest: true, newest: historyT0.Add(11 * time.Second)}
		require.NoError(t, store.history(clock).Observe(t.Context(), dag, runs...))
		assert.Equal(t, []string{"r3"}, runIDs(store.samples))
	})

	// A scheduler that restarts finds its runs in the store, and records none
	// of them again.
	t.Run("NoDuplicateAfterRestart", func(t *testing.T) {
		t.Parallel()
		retained := []ir.DAGRunStatus{
			histRun("r2", ir.Succeeded, 10*time.Second, time.Second),
			histRun("r1", ir.Succeeded, 0, time.Second),
		}
		store := &fakeHistoryStore{hasNewest: true, newest: historyT0.Add(11 * time.Second)}
		require.NoError(t, store.history(clock).Observe(t.Context(), dag, retained...))
		assert.Empty(t, store.samples)
	})

	t.Run("OpenErrorThenRetry", func(t *testing.T) {
		t.Parallel()
		at := now
		store := &fakeHistoryStore{openErr: errors.New("disk full")}
		h := store.history(func() time.Time { return at })
		r1 := histRun("r1", ir.Succeeded, 0, time.Second)
		r2 := histRun("r2", ir.Succeeded, 10*time.Second, time.Second)

		require.ErrorContains(t, h.Observe(t.Context(), dag, r1), "disk full")
		at = at.Add(10 * time.Second)
		require.ErrorContains(t, h.Observe(t.Context(), dag, r2, r1), "disk full")
		assert.Equal(t, 1, store.opens, "no new try before the retry delay")

		store.openErr = nil
		at = at.Add(historyOpenRetry)
		require.NoError(t, h.Observe(t.Context(), dag, r2, r1))
		assert.Equal(t, 2, store.opens)
		assert.Equal(t, []string{"r1", "r2"}, runIDs(store.samples), "the runs that the scanner still reads record")
	})

	t.Run("RecordErrorReopens", func(t *testing.T) {
		t.Parallel()
		at := now
		r1 := histRun("r1", ir.Succeeded, 0, time.Second)
		r2 := histRun("r2", ir.Succeeded, 10*time.Second, time.Second)
		store := &fakeHistoryStore{recordErr: errors.New("io error")}
		h := store.history(func() time.Time { return at })

		require.Error(t, h.Observe(t.Context(), dag, r1))
		assert.Equal(t, 1, store.closes, "a failed store closes")

		store.recordErr = nil
		at = at.Add(historyOpenRetry)
		require.NoError(t, h.Observe(t.Context(), dag, r2, r1))
		assert.Equal(t, []string{"r1", "r2"}, runIDs(store.samples))
	})

	t.Run("NewestEndError", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{newestErr: errors.New("unreadable")}
		require.ErrorContains(t, store.history(clock).Observe(t.Context(), dag, histRun("r1", ir.Succeeded, 0, time.Second)), "unreadable")
		assert.Empty(t, store.samples)
		assert.Equal(t, 1, store.closes)
	})

	t.Run("ForgetAndClose", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{}
		h := store.history(clock)
		other := &ir.DAG{Name: "other"}
		require.NoError(t, h.Observe(t.Context(), dag))
		require.NoError(t, h.Observe(t.Context(), other))
		assert.Equal(t, 2, store.opens)

		h.Forget(t.Context(), map[string]struct{}{"svc": {}})
		assert.Equal(t, 1, store.closes)
		h.Close(t.Context())
		assert.Equal(t, 2, store.closes)

		require.NoError(t, h.Observe(t.Context(), dag))
		assert.Equal(t, 3, store.opens, "a DAG that is a service again opens its store")
	})

	// The memory of a DAG does not grow with the count of runs.
	t.Run("BoundedMemory", func(t *testing.T) {
		t.Parallel()
		store := &fakeHistoryStore{}
		h := store.history(clock)
		for i := range 500 {
			run := histRun(fmt.Sprintf("r%d", i), ir.Succeeded, time.Duration(i)*time.Second, time.Millisecond)
			require.NoError(t, h.Observe(t.Context(), dag, run))
		}
		require.Len(t, store.samples, 500)
		d := h.dags["svc"]
		assert.LessOrEqual(t, len(d.seen), historySeenRuns)
		assert.LessOrEqual(t, len(d.ring), historySeenRuns)
	})
}

// A real store: the samples, the events and the restart of the scheduler.
func TestServiceHistoryStore(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "dag", "run-history")
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	dag := &ir.DAG{Name: "svc", RestartWait: 5 * time.Second}

	run := func(id string, status ir.Status, start, ran time.Duration, runErr string) ir.DAGRunStatus {
		const layout = "2006-01-02T15:04:05.000Z07:00"
		return ir.DAGRunStatus{
			DAGRunID: id, AttemptID: id + "-a1", Status: status, Error: runErr,
			StartedAt:  base.Add(start).Format(layout),
			FinishedAt: base.Add(start + ran).Format(layout),
		}
	}
	r1 := run("r1", ir.Succeeded, 0, time.Second, "")
	r2 := run("r2", ir.Failed, 10*time.Second, time.Second, "boom")
	r3 := run("r3", ir.Failed, 20*time.Second, time.Second, "boom")
	r4 := run("r4", ir.Succeeded, 30*time.Second, time.Second, "")
	// r5 starts 200 s after the end of r4: more than restart_wait_sec plus 60 s.
	r5 := run("r5", ir.Succeeded, 231*time.Second, time.Second, "")
	r6 := run("r6", ir.Succeeded, 240*time.Second, time.Second, "")

	var retained []ir.DAGRunStatus // newest first
	newHistory := func() *ServiceHistory {
		return NewServiceHistory(ServiceHistoryConfig{
			Dir:       func(string) (string, bool) { return dir, true },
			Open:      samples.OpenRecorder,
			NewestEnd: samples.NewestEnd,
		})
	}
	// observe ends a run as the scanner sees it: the latest run, then the recent
	// runs.
	observe := func(h *ServiceHistory, r ir.DAGRunStatus) {
		t.Helper()
		retained = append([]ir.DAGRunStatus{r}, retained...)
		require.NoError(t, h.Observe(ctx, dag, retained[:min(11, len(retained))]...))
	}
	availability := func() samples.Availability {
		t.Helper()
		a, err := samples.OpenReader(dir).Availability(ctx, time.Now())
		require.NoError(t, err)
		return a
	}

	h := newHistory()
	for _, r := range []ir.DAGRunStatus{r1, r2, r3, r4, r5} {
		observe(h, r)
	}
	// A reader sees each sample at once, with no flush.
	a := availability()
	assert.Equal(t, uint64(5), a.TotalCount)
	require.NotEmpty(t, a.Windows)
	assert.Equal(t, uint64(5), a.Windows[0].Count)
	assert.Equal(t, uint64(2), a.Windows[0].Failures)

	page, err := samples.OpenReader(dir).Events(ctx, samples.EventQuery{
		From: base.Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 10,
	})
	require.NoError(t, err)
	var kinds, ids []string
	for _, e := range page.Events {
		kinds, ids = append(kinds, string(e.Kind)), append(ids, e.RunID)
	}
	assert.Equal(t, []string{"gap", "recovered", "failed"}, kinds, "newest first, no started event")
	assert.Equal(t, []string{"r5", "r4", "r2"}, ids)
	assert.Equal(t, 200*time.Second, page.Events[0].Gap)
	assert.True(t, page.Events[0].Time.Equal(base.Add(31*time.Second)), "the gap starts at the end of r4")

	end, ok, err := samples.NewestEnd(dir)
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, end.Equal(base.Add(232*time.Second)), "the end of r5, to the millisecond: %v", end)

	// A scheduler that restarts records no run twice, and records the run that
	// ended while it was stopped.
	h.Close(ctx)
	h = newHistory()
	require.NoError(t, h.Observe(ctx, dag, retained[:min(11, len(retained))]...))
	assert.Equal(t, uint64(5), availability().TotalCount, "the runs of the store are not recorded again")
	observe(h, r6)
	assert.Equal(t, uint64(6), availability().TotalCount)
	h.Close(ctx)

	h = newHistory()
	require.NoError(t, h.Observe(ctx, dag, retained[:min(11, len(retained))]...))
	assert.Equal(t, uint64(6), availability().TotalCount)

	// A late start after a failed run and a restart of the scheduler is part of
	// the outage, not a gap: the store keeps the bound of the failed run.
	observe(h, run("r7", ir.Failed, 250*time.Second, time.Second, "boom"))
	h.Close(ctx)
	h = newHistory()
	observe(h, run("r8", ir.Succeeded, 600*time.Second, time.Second, ""))
	h.Close(ctx)
	page, err = samples.OpenReader(dir).Events(ctx, samples.EventQuery{
		From: base.Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 10,
	})
	require.NoError(t, err)
	kinds, ids = nil, nil
	for _, e := range page.Events {
		kinds, ids = append(kinds, string(e.Kind)), append(ids, e.RunID)
	}
	assert.Equal(t, []string{"recovered", "failed", "gap", "recovered", "failed"}, kinds)
	assert.Equal(t, []string{"r8", "r7", "r5", "r4", "r2"}, ids)
}
