// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
	"github.com/dagucloud/dagu/v2/internal/proc"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBackfillScheduleParams(t *testing.T) {
	f := newQueueFixture(t)
	dag, err := spec.LoadYAML(f.ctx, []byte(`
name: scheduled-report
params: {mode: default}
schedule:
  - expression: "0 5 * * *"
    params: {mode: y}
  - expression: "0 6 * * *"
    params: {mode: z}
  - expression: "0 5,6 * * *"
    params: {mode: overlap}
  - expression: "0 7 * * *"
steps:
  - run: echo report
`))
	require.NoError(t, err)

	cfg := &config.Config{}
	cfg.Paths.LogDir = t.TempDir()
	s := &Scheduler{config: cfg, dagRunRepository: f.dagRunRepository, queueStore: f.queueStore}
	target := backfill.Target{DAG: dag, Schedule: backfill.SlotSchedule{Schedules: dag.Schedule, Location: time.UTC}}
	for _, tc := range []struct {
		runID    string
		hour     int
		explicit string
		want     string
	}{
		{runID: "first", hour: 5, want: "mode=y"},
		{runID: "second", hour: 6, want: "mode=z"},
		{runID: "explicit", hour: 6, explicit: "mode=manual", want: "mode=manual"},
		{runID: "defaults", hour: 7, want: "mode=default"},
	} {
		record := &backfill.Record{DAGName: dag.Name, ID: "bf123456"}
		record.Options.Params = tc.explicit
		slot := backfill.RecordSlot{At: time.Date(2026, 11, 12, tc.hour, 0, 0, 0, time.UTC)}
		require.NoError(t, s.enqueueBackfillRun(f.ctx, record, target, slot, tc.runID))
		attempt, err := f.dagRunRepository.FindAttempt(f.ctx, ir.NewDAGRunRef(dag.Name, tc.runID))
		require.NoError(t, err)
		status, err := attempt.ReadStatus(f.ctx)
		require.NoError(t, err)
		require.Contains(t, status.ParamsList, tc.want)
	}
}

// writeQueuedRun writes a queued run of the fixture's DAG, a backfill run
// when backfillID is set.
func writeQueuedRun(t *testing.T, f *queueFixture, runID, backfillID string) ir.DAGRunRef {
	t.Helper()
	run, err := f.dagRunRepository.CreateAttempt(f.ctx, f.dag, time.Now(), runID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, run.Open(f.ctx))
	st := ir.InitialStatus(f.dag)
	st.Status, st.DAGRunID, st.AttemptID, st.BackfillID = ir.Queued, runID, run.ID(), backfillID
	require.NoError(t, run.Write(f.ctx, st))
	require.NoError(t, run.Close(f.ctx))
	return ir.NewDAGRunRef(f.dag.Name, runID)
}

// A queued backfill run does not keep a DAG busy for its live schedule; a
// queued live run does.
func TestLiveQueuedCheckerIgnoresBackfillRuns(t *testing.T) {
	f := newQueueFixture(t).withDAG("live-queued-dag", 1)
	check := liveQueuedChecker(f.queueStore, liveRunChecker(f.dagRunRepository))

	backfillRun := writeQueuedRun(t, f, "backfill-run", "bf123456")
	require.NoError(t, f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityLow, backfillRun))
	busy, err := check(f.ctx, f.dag)
	require.NoError(t, err)
	assert.False(t, busy)

	liveRun := writeQueuedRun(t, f, "live-run", "")
	require.NoError(t, f.queueStore.Enqueue(f.ctx, f.dag.Name, queuedomain.QueuePriorityHigh, liveRun))
	busy, err = check(f.ctx, f.dag)
	require.NoError(t, err)
	assert.True(t, busy)
}

func TestLiveRunningCheckerIgnoresBackfillRuns(t *testing.T) {
	f := newQueueFixture(t).withDAG("live-running-dag", 1)
	backfillRun := writeQueuedRun(t, f, "backfill-run", "bf123456")
	liveRun := writeQueuedRun(t, f, "live-run", "")
	entry := func(ref ir.DAGRunRef) proc.ProcEntry {
		return proc.ProcEntry{GroupName: f.dag.ProcGroup(), Fresh: true, Meta: proc.ProcMeta{Name: ref.Name, DAGRunID: ref.ID}}
	}
	elsewhere := entry(liveRun)
	elsewhere.GroupName = "another-queue"

	for _, tc := range []struct {
		name    string
		entries []proc.ProcEntry
		want    bool
	}{
		{"backfill run only", []proc.ProcEntry{entry(backfillRun)}, false},
		{"live run too", []proc.ProcEntry{entry(backfillRun), entry(liveRun)}, true},
		// The count is of the DAG's queue, so a run in another queue does
		// not make it busy.
		{"live run in another queue", []proc.ProcEntry{entry(backfillRun), elsewhere}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			procRepository := &mockProcRepository{}
			procRepository.On("CountAliveByDAGName", mock.Anything, f.dag.ProcGroup(), f.dag.Name).Return(len(tc.entries), nil)
			procRepository.On("ListAllEntries", mock.Anything).Return(tc.entries, nil)

			busy, err := liveRunningChecker(procRepository, liveRunChecker(f.dagRunRepository))(f.ctx, f.dag)
			require.NoError(t, err)
			assert.Equal(t, tc.want, busy)
		})
	}
}

// fixedProfile resolves every DAG's default profile to one name.
type fixedProfile string

func (p fixedProfile) ResolveProfile(context.Context, string, string) (string, error) {
	return string(p), nil
}

// Catchup leaves a slot to a backfill that holds it under the DAG's default
// profile, whose schedule is the only one that fires.
func TestGuardCatchupSlotUsesTheDefaultProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	schedule, err := ir.NewCronSchedule("0 2 * * *")
	require.NoError(t, err)
	schedule.Profile = "prod"
	dag := &ir.DAG{Name: "etl", Schedule: []ir.Schedule{schedule}}
	repository := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: slotindex.New(filepath.Join(dir, "slot-index")),
		Location:  time.UTC,
	})
	cfg := &config.Config{}
	cfg.Core.Location = time.UTC
	s := &Scheduler{
		dagExecutor:      &DAGExecutor{profileResolver: fixedProfile("prod")},
		backfills:        &backfill.Dispatcher{Store: filebackfill.New(filepath.Join(dir, "backfills"))},
		dagRunRepository: repository,
		config:           cfg,
	}
	slot := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	attempt, err := repository.CreateAttempt(ctx, dag, time.Now(), "backfill-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name: dag.Name, DAGRunID: "backfill-run", AttemptID: attempt.ID(), Status: ir.Queued,
		ScheduleTime: slot.Format(time.RFC3339), TriggerType: ir.TriggerTypeBackfill, BackfillID: "bf123456", ProfileName: "prod",
	}))
	require.NoError(t, attempt.Close(ctx))

	enqueued := false
	held, err := s.guardCatchupSlot(ctx, DAGEntry{DAG: dag, DefinitionID: "etl"}, slot, func() error {
		enqueued = true
		return nil
	})
	require.NoError(t, err)
	assert.True(t, held)
	assert.False(t, enqueued)

	held, err = s.guardCatchupSlot(ctx, DAGEntry{DAG: dag, DefinitionID: "etl"}, slot.AddDate(0, 0, 1), func() error {
		enqueued = true
		return nil
	})
	require.NoError(t, err)
	assert.False(t, held)
	assert.True(t, enqueued, "a slot no backfill holds is queued")
}

// A backfill's DAG is gone once its file is deleted or defines another DAG.
func TestBackfillDAGExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.DAGsDir = dir
	s := &Scheduler{config: cfg}
	record := &backfill.Record{DAGName: "etl", DAGFile: filepath.Join(dir, "etl.yaml")}
	write := func(name string) {
		t.Helper()
		require.NoError(t, os.WriteFile(record.DAGFile, []byte("name: "+name+"\nsteps:\n  - run: echo hi\n"), 0o600))
	}

	write("etl")
	exists, err := s.backfillDAGExists(ctx, record)
	require.NoError(t, err)
	assert.True(t, exists)

	write("renamed")
	exists, err = s.backfillDAGExists(ctx, record)
	require.NoError(t, err)
	assert.False(t, exists, "the file defines another DAG")

	require.NoError(t, os.Remove(record.DAGFile))
	exists, err = s.backfillDAGExists(ctx, record)
	require.NoError(t, err)
	assert.False(t, exists, "the file is gone")
}
