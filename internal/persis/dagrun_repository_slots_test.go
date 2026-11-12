// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const slotDAGYAML = "name: daily\nschedule: \"0 2 * * *\"\nsteps:\n  - run: echo hi\n"

var (
	slotOct1 = time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC)
	slotOct2 = time.Date(2026, 10, 2, 2, 0, 0, 0, time.UTC)
)

type slotFixture struct {
	repository *persis.DAGRunRepository
	index      *slotindex.Store
	dag        *ir.DAG
}

func newSlotFixture(t *testing.T) slotFixture {
	t.Helper()
	dir := t.TempDir()
	index := slotindex.New(filepath.Join(dir, "slot-index"))
	repository := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: index,
		Location:  time.UTC,
	})
	dag, err := spec.LoadYAML(context.Background(), []byte(slotDAGYAML))
	require.NoError(t, err)
	return slotFixture{repository: repository, index: index, dag: dag}
}

// writeRun creates a run and writes each status to it in turn.
func (f slotFixture) writeRun(t *testing.T, runID string, options persis.DAGRunCreateAttemptOptions, statuses ...ir.DAGRunStatus) {
	t.Helper()
	ctx := context.Background()
	attempt, err := f.repository.CreateAttempt(ctx, f.dag, time.Now(), runID, options)
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	for _, status := range statuses {
		status.Name = f.dag.Name
		status.DAGRunID = runID
		status.AttemptID = attempt.ID()
		require.NoError(t, attempt.Write(ctx, status))
	}
	require.NoError(t, attempt.Close(ctx))
}

func (f slotFixture) events(t *testing.T) []persis.SlotEvent {
	t.Helper()
	events, err := f.index.Events(context.Background(), f.dag.Name, slotOct1.AddDate(0, 0, -1), slotOct2.AddDate(0, 0, 1))
	require.NoError(t, err)
	return events
}

func scheduledStatus(status ir.Status, slot time.Time, trigger ir.TriggerType) ir.DAGRunStatus {
	return ir.DAGRunStatus{Status: status, ScheduleTime: slot.Format(time.RFC3339), TriggerType: trigger}
}

func TestSlotIndexRecordsNewRunOnce(t *testing.T) {
	t.Parallel()
	f := newSlotFixture(t)

	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Queued, slotOct1, ir.TriggerTypeScheduler),
		scheduledStatus(ir.Running, slotOct1, ir.TriggerTypeScheduler),
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler),
	)

	events := f.events(t)
	require.Len(t, events, 1)
	assert.Equal(t, persis.SlotEventRun, events[0].Kind)
	assert.Equal(t, "run-1", events[0].RunID)
	assert.Equal(t, slotOct1, events[0].Slot)
	assert.Equal(t, ir.TriggerTypeScheduler, events[0].TriggerType)
	assert.Equal(t, "0 2 * * *", events[0].Schedule)
	assert.Empty(t, events[0].Status, "a run event never carries status")
}

// A schedule without its own zone fires in the server's zone.
func TestSlotIndexNamesScheduleInServerZone(t *testing.T) {
	t.Parallel()
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	dir := t.TempDir()
	index := slotindex.New(filepath.Join(dir, "slot-index"))
	f := slotFixture{
		repository: testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
			SlotIndex: index,
			Location:  tokyo,
		}),
		index: index,
	}
	f.dag, err = spec.LoadYAML(context.Background(), []byte(slotDAGYAML))
	require.NoError(t, err)

	slot := time.Date(2026, 10, 2, 2, 0, 0, 0, tokyo)
	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{}, scheduledStatus(ir.Running, slot, ir.TriggerTypeManual))

	events := f.events(t)
	require.Len(t, events, 1)
	assert.Equal(t, "0 2 * * *", events[0].Schedule)
}

// A manual run on a time the schedule does not fire names no schedule.
func TestSlotIndexRecordsOffScheduleRunWithoutSchedule(t *testing.T) {
	t.Parallel()
	f := newSlotFixture(t)

	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Running, slotOct1.Add(30*time.Minute), ir.TriggerTypeManual))

	events := f.events(t)
	require.Len(t, events, 1)
	assert.Equal(t, ir.TriggerTypeManual, events[0].TriggerType)
	assert.Empty(t, events[0].Schedule)
}

func TestSlotIndexSkipsRunsThatAreNotSlotRuns(t *testing.T) {
	t.Parallel()
	f := newSlotFixture(t)

	// A run without a scheduled time.
	f.writeRun(t, "unscheduled", persis.DAGRunCreateAttemptOptions{}, ir.DAGRunStatus{Status: ir.Running})
	// A run that dag.enqueue started for a parent; it takes the parent's slot.
	f.writeRun(t, "enqueued-child", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Queued, slotOct1, ir.TriggerTypeSubDAG))
	// A retry is the same run of its slot.
	f.writeRun(t, "retried", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Failed, slotOct2, ir.TriggerTypeScheduler))
	f.writeRun(t, "retried", persis.DAGRunCreateAttemptOptions{Retry: true},
		scheduledStatus(ir.Running, slotOct2, ir.TriggerTypeRetry))

	events := f.events(t)
	require.Len(t, events, 1)
	assert.Equal(t, "retried", events[0].RunID)
}

func TestSlotIndexKeepsFinalStatusThroughRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSlotFixture(t)
	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler))

	cutoff := time.Now().Add(time.Hour)
	removed, err := f.repository.RemoveOldDAGRuns(ctx, f.dag.Name, 0, persis.DAGRunRetentionOptions{OlderThan: &cutoff})
	require.NoError(t, err)
	require.Equal(t, []string{"run-1"}, removed)

	events := f.events(t)
	require.Len(t, events, 2)
	assert.Equal(t, persis.SlotEventFinal, events[1].Kind)
	assert.Equal(t, "run-1", events[1].RunID)
	assert.Equal(t, ir.Succeeded, events[1].Status)
}

func TestSlotIndexDropsRunsAnOperatorDeletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSlotFixture(t)
	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler))
	f.writeRun(t, "run-2", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Failed, slotOct2, ir.TriggerTypeScheduler))

	require.NoError(t, f.repository.RemoveDAGRun(ctx, ir.NewDAGRunRef(f.dag.Name, "run-1"), persis.DAGRunRemoveOptions{}))
	cutoff := time.Now().Add(time.Hour)
	_, err := f.repository.RemoveOldDAGRuns(ctx, f.dag.Name, 0, persis.DAGRunRetentionOptions{OlderThan: &cutoff, DropSlots: true})
	require.NoError(t, err)

	var dropped []string
	for _, event := range f.events(t) {
		assert.NotEqual(t, persis.SlotEventFinal, event.Kind)
		if event.Kind == persis.SlotEventDropped {
			dropped = append(dropped, event.RunID)
		}
	}
	assert.ElementsMatch(t, []string{"run-1", "run-2"}, dropped)
}

// A run refused for being active stays counted for its slot.
func TestSlotIndexKeepsActiveRunRefusedRemoval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSlotFixture(t)
	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Queued, slotOct1, ir.TriggerTypeScheduler))

	err := f.repository.RemoveDAGRun(ctx, ir.NewDAGRunRef(f.dag.Name, "run-1"), persis.DAGRunRemoveOptions{RejectActive: true})
	require.Error(t, err)

	events := f.events(t)
	require.Len(t, events, 1)
	assert.Equal(t, persis.SlotEventRun, events[0].Kind)
}

// Runs written before the index existed are added the first time a DAG's
// slots are read, each with the schedule of its own snapshot. A run the index
// already recorded is not added again.
func TestSlotEventsImportsExistingHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	unindexed := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{})
	dag, err := spec.LoadYAML(ctx, []byte(slotDAGYAML))
	require.NoError(t, err)
	old := slotFixture{repository: unindexed, dag: dag}
	old.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler))
	old.writeRun(t, "manual", persis.DAGRunCreateAttemptOptions{}, ir.DAGRunStatus{Status: ir.Succeeded})

	index := slotindex.New(filepath.Join(dir, "slot-index"))
	repository := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: index,
		Location:  time.UTC,
	})
	indexed := slotFixture{repository: repository, dag: dag}
	indexed.writeRun(t, "run-2", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct2, ir.TriggerTypeScheduler))

	for range 2 {
		events, err := repository.SlotEvents(ctx, dag.Name, slotOct1.AddDate(0, 0, -1), slotOct2.AddDate(0, 0, 1))
		require.NoError(t, err)
		require.Len(t, events, 2, "each run is recorded once")
		assert.Equal(t, "run-1", events[0].RunID)
		assert.Equal(t, "0 2 * * *", events[0].Schedule)
		assert.Equal(t, "run-2", events[1].RunID)
	}
}

func TestSlotIndexPrunesByRetentionDays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	index := slotindex.New(filepath.Join(dir, "slot-index"))
	repository := persis.NewDAGRunRepository(nil, nil, persis.DAGRunRepositoryOptions{
		SlotIndex: index,
		Now:       func() time.Time { return now },
	})
	for _, slot := range []time.Time{slotOct1, slotOct2} {
		require.NoError(t, repository.RecordSkippedSlot(ctx, "daily", slot, "0 2 * * *", "succeeded"))
	}

	require.NoError(t, repository.PruneSlots(ctx, "daily", 30))

	events, err := index.Events(ctx, "daily", slotOct1.AddDate(0, 0, -1), now)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, slotOct2, events[0].Slot)
	assert.Equal(t, persis.SlotEventSkipped, events[0].Kind)
	assert.Equal(t, "succeeded", events[0].SkipReason)
}
