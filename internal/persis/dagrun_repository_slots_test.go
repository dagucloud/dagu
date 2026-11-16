// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
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
	// created is when the next run is created; each run is created a
	// second after the one before, so their order is fixed.
	created *time.Time
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
	return slotFixture{repository: repository, index: index, dag: dag, created: new(time.Now())}
}

// writeRun creates a run and writes each status to it in turn.
func (f slotFixture) writeRun(t *testing.T, runID string, options persis.DAGRunCreateAttemptOptions, statuses ...ir.DAGRunStatus) {
	t.Helper()
	ctx := context.Background()
	created := time.Now()
	if f.created != nil {
		created = *f.created
		*f.created = created.Add(time.Second)
	}
	attempt, err := f.repository.CreateAttempt(ctx, f.dag, created, runID, options)
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

// A run cancelled while queued never ran its slot, so retention drops it
// rather than keep its status.
func TestSlotIndexDropsNeverStartedRunOnRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSlotFixture(t)
	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Queued, slotOct1, ir.TriggerTypeScheduler),
		scheduledStatus(ir.Aborted, slotOct1, ir.TriggerTypeScheduler))

	cutoff := time.Now().Add(time.Hour)
	_, err := f.repository.RemoveOldDAGRuns(ctx, f.dag.Name, 0, persis.DAGRunRetentionOptions{OlderThan: &cutoff})
	require.NoError(t, err)

	events := f.events(t)
	require.Len(t, events, 2)
	assert.Equal(t, persis.SlotEventDropped, events[1].Kind)
}

// A run that ran and failed keeps its status through retention when a retry
// of it was cancelled before it started.
func TestSlotIndexKeepsRunWhoseRetryNeverStarted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSlotFixture(t)
	ran := scheduledStatus(ir.Failed, slotOct1, ir.TriggerTypeScheduler)
	ran.StartedAt = "2026-10-01T02:00:00Z"
	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		ran,
		scheduledStatus(ir.Queued, slotOct1, ir.TriggerTypeRetry),
		scheduledStatus(ir.Aborted, slotOct1, ir.TriggerTypeRetry))

	cutoff := time.Now().Add(time.Hour)
	_, err := f.repository.RemoveOldDAGRuns(ctx, f.dag.Name, 0, persis.DAGRunRetentionOptions{OlderThan: &cutoff})
	require.NoError(t, err)

	events := f.events(t)
	require.Len(t, events, 2)
	assert.Equal(t, persis.SlotEventFinal, events[1].Kind)
	assert.Equal(t, ir.Aborted, events[1].Status)
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
		events, err := repository.SlotEvents(ctx, dag, slotOct1.AddDate(0, 0, -1), slotOct2.AddDate(0, 0, 1))
		require.NoError(t, err)
		require.Len(t, events, 2, "each run is recorded once")
		assert.Equal(t, "run-1", events[0].RunID)
		assert.Equal(t, "0 2 * * *", events[0].Schedule)
		assert.Equal(t, "run-2", events[1].RunID)
	}
}

// Reading each run's snapshot through the listing adds the same events as
// looking each run up by its ID, on the first read and on a later one. The
// history has runs under an earlier schedule, a retried run, a day with a
// run index, runs already recorded or removed, and runs that belong to no
// slot of their own.
func TestSlotImportMatchesLookupImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	runsDir, ephemeralDir := filepath.Join(dir, "dag-runs"), filepath.Join(dir, "ephemeral")
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC) }
	loadDAG := func(yaml string) *ir.DAG {
		dag, err := spec.LoadYAML(ctx, []byte(yaml))
		require.NoError(t, err)
		return dag
	}
	repository := func(index persis.SlotIndex) *persis.DAGRunRepository {
		return testutil.NewFileDAGRunRepositoryWithEphemeral(runsDir, ephemeralDir, persis.DAGRunRepositoryOptions{
			SlotIndex: index,
			Location:  time.UTC,
			Now:       func() time.Time { return now },
		})
	}

	earlierDAG := loadDAG(slotDAGYAML)
	currentDAG := loadDAG("name: daily\nslot_retention_days: 30\nschedule:\n  - \"0 3 * * *\"\n" +
		"  - expression: \"0 4 * * *\"\n    profile: prod\nsteps:\n  - run: echo hi\n")
	clock := at(1, 5, 0)
	unindexed := repository(nil)
	earlier := slotFixture{repository: unindexed, dag: earlierDAG, created: &clock}
	current := slotFixture{repository: unindexed, dag: currentDAG, created: &clock}
	withProfile := func(status ir.DAGRunStatus) ir.DAGRunStatus {
		status.ProfileName = "prod"
		return status
	}

	// A day with enough finished runs to have a run index, which lists runs
	// without reading their statuses.
	for i := range 10 {
		earlier.writeRun(t, fmt.Sprintf("busy-%02d", i), persis.DAGRunCreateAttemptOptions{},
			scheduledStatus(ir.Succeeded, at(1, 2, 0), ir.TriggerTypeManual))
	}
	current.writeRun(t, "busy-profile", persis.DAGRunCreateAttemptOptions{},
		withProfile(scheduledStatus(ir.Succeeded, at(1, 4, 0), ir.TriggerTypeScheduler)))

	clock = at(2, 5, 0)
	earlier.writeRun(t, "earlier", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(2, 2, 0), ir.TriggerTypeScheduler))
	current.writeRun(t, "current", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(2, 3, 0), ir.TriggerTypeScheduler))
	current.writeRun(t, "profile", persis.DAGRunCreateAttemptOptions{},
		withProfile(scheduledStatus(ir.Succeeded, at(2, 4, 0), ir.TriggerTypeScheduler)))
	current.writeRun(t, "off-schedule", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(2, 2, 30), ir.TriggerTypeManual))
	// The retry's snapshot, under the current schedule, is the one read.
	earlier.writeRun(t, "retried", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Failed, at(2, 2, 0), ir.TriggerTypeScheduler))
	current.writeRun(t, "retried", persis.DAGRunCreateAttemptOptions{Retry: true},
		scheduledStatus(ir.Succeeded, at(2, 2, 0), ir.TriggerTypeRetry))

	clock = at(4, 5, 0)
	current.writeRun(t, "unscheduled", persis.DAGRunCreateAttemptOptions{}, ir.DAGRunStatus{Status: ir.Succeeded})
	current.writeRun(t, "enqueued", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(2, 3, 0), ir.TriggerTypeSubDAG))
	parent := ir.NewDAGRunRef(currentDAG.Name, "parent")
	current.writeRun(t, "parent", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(2, 3, 0), ir.TriggerTypeManual))
	child := scheduledStatus(ir.Succeeded, at(2, 3, 0), ir.TriggerTypeSubDAG)
	child.Root, child.Parent = parent, parent
	current.writeRun(t, "child", persis.DAGRunCreateAttemptOptions{RootDAGRun: parent}, child)
	rooted := scheduledStatus(ir.Succeeded, at(2, 3, 0), ir.TriggerTypeManual)
	rooted.Root = parent
	current.writeRun(t, "rooted", persis.DAGRunCreateAttemptOptions{}, rooted)
	current.writeRun(t, "ephemeral", persis.DAGRunCreateAttemptOptions{Ephemeral: true},
		scheduledStatus(ir.Succeeded, at(2, 3, 0), ir.TriggerTypeManual))
	current.writeRun(t, "removed", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(2, 3, 0), ir.TriggerTypeManual))
	require.NoError(t, unindexed.RemoveDAGRun(ctx, ir.NewDAGRunRef(currentDAG.Name, "removed"), persis.DAGRunRemoveOptions{}))
	current.writeRun(t, "expired", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC), ir.TriggerTypeScheduler))

	// Runs written once the slot index exists.
	seedDir := filepath.Join(dir, "slot-index")
	indexed := repository(slotindex.New(seedDir))
	clock = at(3, 5, 0)
	slotFixture{repository: indexed, dag: currentDAG, created: &clock}.writeRun(t, "recorded", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(3, 3, 0), ir.TriggerTypeScheduler))
	earlier.writeRun(t, "deleted", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(3, 2, 0), ir.TriggerTypeScheduler))
	require.NoError(t, indexed.RemoveDAGRun(ctx, ir.NewDAGRunRef(currentDAG.Name, "deleted"), persis.DAGRunRemoveOptions{}))
	// A removal that recorded the drop and then failed leaves the run.
	earlier.writeRun(t, "drop-recorded", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(3, 2, 0), ir.TriggerTypeScheduler))
	require.NoError(t, slotindex.New(seedDir).Record(ctx, persis.SlotEvent{
		Kind: persis.SlotEventDropped, DAGName: currentDAG.Name, Slot: at(3, 2, 0), RunID: "drop-recorded",
	}))

	byLookup, byListing := filepath.Join(dir, "by-lookup"), filepath.Join(dir, "by-listing")
	require.NoError(t, os.CopyFS(byLookup, os.DirFS(seedDir)))
	require.NoError(t, os.CopyFS(byListing, os.DirFS(seedDir)))
	lookupIndex, listingIndex := slotindex.New(byLookup), slotindex.New(byListing)
	lookup, listing := repository(lookupIndex), repository(listingIndex)
	from, to := at(1, 0, 0).AddDate(0, -2, 0), now
	importBoth := func() []persis.SlotEvent {
		t.Helper()
		require.NoError(t, lookup.ImportSlotsByLookup(ctx, currentDAG))
		_, err := listing.SlotEvents(ctx, currentDAG, from, to)
		require.NoError(t, err)
		want, err := lookupIndex.Events(ctx, currentDAG.Name, from, to)
		require.NoError(t, err)
		got, err := listingIndex.Events(ctx, currentDAG.Name, from, to)
		require.NoError(t, err)
		assert.Equal(t, withoutRecordedAt(want), withoutRecordedAt(got))
		return got
	}

	schedules := map[string]string{}
	for _, event := range importBoth() {
		if event.Kind == persis.SlotEventRun {
			schedules[event.RunID] = event.Schedule
		}
	}
	assert.ElementsMatch(t, []string{
		"busy-00", "busy-01", "busy-02", "busy-03", "busy-04", "busy-05", "busy-06", "busy-07", "busy-08", "busy-09",
		"busy-profile", "earlier", "current", "profile", "off-schedule", "retried", "parent", "recorded",
	}, slices.Collect(maps.Keys(schedules)))
	assert.Equal(t, "0 2 * * *", schedules["earlier"])
	assert.Equal(t, "0 3 * * *", schedules["current"])
	assert.Equal(t, "0 4 * * *", schedules["profile"])
	assert.Empty(t, schedules["retried"])

	// A later read adds a run whose recording failed.
	clock = now.Add(-30 * time.Minute)
	earlier.writeRun(t, "late", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, at(20, 2, 0), ir.TriggerTypeScheduler))
	assert.True(t, slices.ContainsFunc(importBoth(), func(event persis.SlotEvent) bool { return event.RunID == "late" }))
}

// An import that adds the runs in batches, one of which splits a day, adds
// the events in the same order as one that adds them at once.
func TestSlotImportInBatchesMatchesLookupImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "dag-runs")
	dag, err := spec.LoadYAML(ctx, []byte(minutelyDAGYAML))
	require.NoError(t, err)
	start := slotOct1.Truncate(24 * time.Hour).Add(12 * time.Hour)
	end := start.Add((persis.SlotImportBatch + 60) * time.Minute)
	seedMinutelyRuns(t, runsDir, dag, start, end)

	lookupIndex := slotindex.New(filepath.Join(dir, "by-lookup"))
	lookup := testutil.NewFileDAGRunRepository(runsDir, persis.DAGRunRepositoryOptions{SlotIndex: lookupIndex, Location: time.UTC})
	require.NoError(t, lookup.ImportSlotsByLookup(ctx, dag))
	want, err := lookupIndex.Events(ctx, dag.Name, start, end)
	require.NoError(t, err)

	listing := testutil.NewFileDAGRunRepository(runsDir, persis.DAGRunRepositoryOptions{
		SlotIndex: slotindex.New(filepath.Join(dir, "by-listing")),
		Location:  time.UTC,
	})
	got, err := listing.SlotEvents(ctx, dag, start, end)
	require.NoError(t, err)
	assert.Equal(t, withoutRecordedAt(want), withoutRecordedAt(got))
}

func withoutRecordedAt(events []persis.SlotEvent) []persis.SlotEvent {
	for i := range events {
		events[i].RecordedAt = time.Time{}
	}
	return events
}

// A read whose context ends while it reads the snapshots of the runs it adds
// records none of them, rather than record them without their schedules, so
// the next read adds them whole.
func TestSlotEventsCancelledImportRecordsNothingItCouldNotRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "dag-runs")
	dag, err := spec.LoadYAML(context.Background(), []byte(slotDAGYAML))
	require.NoError(t, err)
	old := slotFixture{repository: testutil.NewFileDAGRunRepository(runsDir, persis.DAGRunRepositoryOptions{}), dag: dag}
	old.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{}, scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler))
	old.writeRun(t, "run-2", persis.DAGRunCreateAttemptOptions{}, scheduledStatus(ir.Succeeded, slotOct2, ir.TriggerTypeScheduler))

	ctx, cancel := context.WithCancel(context.Background())
	index := &cancellingSlotIndex{SlotIndex: slotindex.New(filepath.Join(dir, "slot-index")), cancel: cancel}
	repository := testutil.NewFileDAGRunRepository(runsDir, persis.DAGRunRepositoryOptions{SlotIndex: index, Location: time.UTC})
	_, err = repository.SlotEvents(ctx, dag, slotOct1, slotOct2)
	require.ErrorIs(t, err, context.Canceled)

	events, err := repository.SlotEvents(context.Background(), dag, slotOct1, slotOct2)
	require.NoError(t, err)
	require.Len(t, events, 2)
	for _, event := range events {
		assert.Equal(t, "0 2 * * *", event.Schedule, event.RunID)
	}
}

// A read whose context ends after it added some runs keeps them, and the
// next read adds the rest.
func TestSlotEventsKeepsRunsACancelledImportAdded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "dag-runs")
	dag, err := spec.LoadYAML(context.Background(), []byte(minutelyDAGYAML))
	require.NoError(t, err)
	// More runs than an import adds at a time.
	start := slotOct1.Truncate(24 * time.Hour)
	end := start.Add((persis.SlotImportBatch + 60) * time.Minute)
	runs := seedMinutelyRuns(t, runsDir, dag, start, end)

	ctx, cancel := context.WithCancel(context.Background())
	store := slotindex.New(filepath.Join(dir, "slot-index"))
	index := &cancellingSlotIndex{SlotIndex: store, cancel: cancel, onImport: true}
	repository := testutil.NewFileDAGRunRepository(runsDir, persis.DAGRunRepositoryOptions{SlotIndex: index, Location: time.UTC})
	_, err = repository.SlotEvents(ctx, dag, start, end)
	require.ErrorIs(t, err, context.Canceled)

	kept, err := store.Events(context.Background(), dag.Name, start, end)
	require.NoError(t, err)
	assert.NotEmpty(t, kept)
	assert.Less(t, len(kept), runs)
	through, err := store.ImportedThrough(context.Background(), dag.Name)
	require.NoError(t, err)
	assert.True(t, through.IsZero(), "the history is not added through any time yet")

	events, err := repository.SlotEvents(context.Background(), dag, start, end)
	require.NoError(t, err)
	require.Len(t, events, runs)
	seen := make(map[string]bool, runs)
	for _, event := range events {
		assert.False(t, seen[event.RunID], "%s is recorded once", event.RunID)
		seen[event.RunID] = true
		assert.Equal(t, "* * * * *", event.Schedule)
	}
}

// cancellingSlotIndex ends a read's context once the read has looked up the
// runs the index holds, before it reads their snapshots, or, with onImport,
// once it has added runs to the index.
type cancellingSlotIndex struct {
	persis.SlotIndex
	cancel   context.CancelFunc
	onImport bool
}

func (c *cancellingSlotIndex) Events(ctx context.Context, dagName string, from, to time.Time) ([]persis.SlotEvent, error) {
	if !c.onImport {
		c.cancel()
	}
	return c.SlotIndex.Events(ctx, dagName, from, to)
}

func (c *cancellingSlotIndex) Import(ctx context.Context, dagName string, events []persis.SlotEvent, through time.Time) error {
	err := c.SlotIndex.Import(ctx, dagName, events, through)
	if c.onImport {
		c.cancel()
	}
	return err
}

const minutelyDAGYAML = "name: minutely\nschedule: \"* * * * *\"\nsteps:\n  - run: echo hi\n"

// seedMinutelyRuns writes a finished scheduler run for each minute in
// [start, end) and returns how many it wrote. It writes the files directly,
// since creating a run through the store looks its ID up in the whole
// history.
func seedMinutelyRuns(tb testing.TB, runsDir string, dag *ir.DAG, start, end time.Time) int {
	tb.Helper()
	ctx := tb.Context()
	snapshot, err := json.Marshal(dag)
	require.NoError(tb, err)
	root := filedagrun.NewDataRoot(runsDir, dag.Name)
	runs := 0
	for slot := start; slot.Before(end); slot = slot.Add(time.Minute) {
		created := persis.NewUTC(slot)
		id := "run-" + slot.Format("20060102-1504")
		run, err := root.CreateDAGRun(created, id)
		require.NoError(tb, err)
		attempt, err := run.CreateAttempt(ctx, created, nil, "attempt")
		require.NoError(tb, err)
		status, err := json.Marshal(ir.DAGRunStatus{
			Name:         dag.Name,
			DAGRunID:     id,
			AttemptID:    attempt.ID(),
			Status:       ir.Succeeded,
			TriggerType:  ir.TriggerTypeScheduler,
			ScheduleTime: slot.Format(time.RFC3339),
			StartedAt:    slot.Format(time.RFC3339),
			FinishedAt:   slot.Add(time.Second).Format(time.RFC3339),
		})
		require.NoError(tb, err)
		require.NoError(tb, os.WriteFile(filepath.Join(attempt.Dir(), filedagrun.DAGDefinition), snapshot, 0o600))
		require.NoError(tb, os.WriteFile(filepath.Join(attempt.Dir(), filedagrun.JSONLStatusFile), append(status, '\n'), 0o600))
		runs++
	}
	return runs
}

// The first read adds only the runs whose slots are within the DAG's slot
// retention, so pruned slots stay pruned.
func TestSlotEventsImportsWithinRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dag, err := spec.LoadYAML(ctx, []byte(slotDAGYAML+"slot_retention_days: 30\n"))
	require.NoError(t, err)
	old := slotFixture{repository: testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{}), dag: dag}
	old.writeRun(t, "expired", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler))
	old.writeRun(t, "recent", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct2.AddDate(0, 0, 30), ir.TriggerTypeScheduler))

	now := slotOct2.AddDate(0, 0, 31)
	repository := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: slotindex.New(filepath.Join(dir, "slot-index")),
		Location:  time.UTC,
		Now:       func() time.Time { return now },
	})

	events, err := repository.SlotEvents(ctx, dag, slotOct1.AddDate(0, 0, -1), now)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "recent", events[0].RunID)
}

// A run whose slot event cannot be written is not removed, so its slot never
// loses track of it.
func TestSlotIndexBlocksRemovalItCannotRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	index := &flakySlotIndex{SlotIndex: slotindex.New(filepath.Join(dir, "slot-index"))}
	f := newSlotFixture(t)
	f.repository = testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: index,
		Location:  time.UTC,
	})
	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler))
	f.writeRun(t, "run-2", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Succeeded, slotOct2, ir.TriggerTypeScheduler))
	exists := func(runID string) bool {
		_, err := f.repository.FindAttempt(ctx, ir.NewDAGRunRef(f.dag.Name, runID))
		return err == nil
	}

	index.failures = 1
	err := f.repository.RemoveDAGRun(ctx, ir.NewDAGRunRef(f.dag.Name, "run-1"), persis.DAGRunRemoveOptions{})
	require.Error(t, err)
	assert.True(t, exists("run-1"))

	// Retention keeps the one run it could not record and removes the other.
	index.failures = 1
	cutoff := time.Now().Add(time.Hour)
	removed, err := f.repository.RemoveOldDAGRuns(ctx, f.dag.Name, 0, persis.DAGRunRetentionOptions{OlderThan: &cutoff})
	require.Error(t, err)
	require.Len(t, removed, 1)
	for _, runID := range []string{"run-1", "run-2"} {
		assert.Equal(t, runID != removed[0], exists(runID), runID)
	}
}

// A run event that fails to write is written on the attempt's next status
// write.
func TestSlotIndexRetriesFailedRunRecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	index := &flakySlotIndex{SlotIndex: slotindex.New(filepath.Join(dir, "slot-index")), failures: 1}
	f := newSlotFixture(t)
	f.index = index.SlotIndex.(*slotindex.Store)
	f.repository = testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: index,
		Location:  time.UTC,
	})

	f.writeRun(t, "run-1", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Queued, slotOct1, ir.TriggerTypeScheduler),
		scheduledStatus(ir.Running, slotOct1, ir.TriggerTypeScheduler),
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler),
	)

	events := f.events(t)
	require.Len(t, events, 1)
	assert.Equal(t, "run-1", events[0].RunID)
}

// flakySlotIndex fails its next failures records.
type flakySlotIndex struct {
	persis.SlotIndex
	failures int
}

func (f *flakySlotIndex) Record(ctx context.Context, event persis.SlotEvent) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("slot index unavailable")
	}
	return f.SlotIndex.Record(ctx, event)
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

// A run whose every recording failed is added on the next read of its DAG's
// slots, after the history was imported.
func TestSlotEventsRecoversAFailedRecording(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	store := slotindex.New(filepath.Join(dir, "slot-index"))
	index := &flakySlotIndex{SlotIndex: store}
	f := newSlotFixture(t)
	f.index = store
	f.repository = testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: index,
		Location:  time.UTC,
	})
	_, err := f.repository.SlotEvents(ctx, f.dag, slotOct1, slotOct2)
	require.NoError(t, err)

	index.failures = 3
	f.writeRun(t, "lost", persis.DAGRunCreateAttemptOptions{},
		scheduledStatus(ir.Queued, slotOct1, ir.TriggerTypeScheduler),
		scheduledStatus(ir.Running, slotOct1, ir.TriggerTypeScheduler),
		scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeScheduler),
	)
	require.Empty(t, f.events(t), "every recording failed")

	events, err := f.repository.SlotEvents(ctx, f.dag, slotOct1, slotOct2)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "lost", events[0].RunID)
}

// A schedule's business calendar is applied to an imported run, whose
// snapshot does not carry the calendar: a manual run on a day the calendar
// excludes names no schedule.
func TestSlotEventsAppliesCalendarsToImportedRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	calendars := filepath.Join(dir, "calendars")
	require.NoError(t, os.MkdirAll(calendars, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(calendars, "workdays.yaml"), []byte("weekdays: [MON, TUE, WED, THU, FRI]\n"), 0o600))
	dag, err := spec.LoadYAML(ctx, []byte("name: daily\nschedule:\n  - expression: \"0 2 * * *\"\n    calendar: workdays\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	old := slotFixture{repository: testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{}), dag: dag}
	// 2026-10-03 is a Saturday and 2026-10-05 a Monday.
	saturday := time.Date(2026, 10, 3, 2, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 10, 5, 2, 0, 0, 0, time.UTC)
	old.writeRun(t, "saturday", persis.DAGRunCreateAttemptOptions{}, scheduledStatus(ir.Succeeded, saturday, ir.TriggerTypeManual))
	old.writeRun(t, "monday", persis.DAGRunCreateAttemptOptions{}, scheduledStatus(ir.Succeeded, monday, ir.TriggerTypeManual))

	repository := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex:    slotindex.New(filepath.Join(dir, "slot-index")),
		Location:     time.UTC,
		CalendarsDir: calendars,
	})
	events, err := repository.SlotEvents(ctx, dag, saturday, monday)
	require.NoError(t, err)
	schedules := map[string]string{}
	for _, event := range events {
		schedules[event.RunID] = event.Schedule
	}
	assert.Equal(t, map[string]string{"saturday": "", "monday": "0 2 * * *"}, schedules)
}

// A backfill of old slots does not decide how a DAG's live state reads: its
// newest run is its newest run that is not a backfill run, unless it has
// only backfill runs.
func TestLatestRunSkipsBackfillRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSlotFixture(t)
	backfillRun := func(runID string, slot time.Time) {
		t.Helper()
		status := scheduledStatus(ir.Failed, slot, ir.TriggerTypeBackfill)
		status.BackfillID = "bf123456"
		status.Labels = []string{"team=data"}
		f.writeRun(t, runID, persis.DAGRunCreateAttemptOptions{}, status)
	}
	latestID := func() string {
		t.Helper()
		attempt, err := f.repository.LatestAttempt(ctx, f.dag.Name, persis.DAGRunLatestAttemptOptions{AllHistory: true})
		require.NoError(t, err)
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		return status.DAGRunID
	}
	listedID := func() string {
		t.Helper()
		statuses, err := f.repository.LatestStatuses(ctx, persis.DAGRunLatestStatusesQuery{})
		require.NoError(t, err)
		require.Len(t, statuses, 1)
		return statuses[0].DAGRunID
	}

	backfillRun("backfill-1", slotOct1)
	assert.Equal(t, "backfill-1", latestID(), "a DAG with only backfill runs shows the latest")
	assert.Equal(t, "backfill-1", listedID())

	f.writeRun(t, "live", persis.DAGRunCreateAttemptOptions{}, scheduledStatus(ir.Succeeded, slotOct2, ir.TriggerTypeScheduler))
	backfillRun("backfill-2", slotOct1)

	assert.Equal(t, "live", latestID())
	assert.Equal(t, "live", listedID())

	// The live run lacks the label asked for, so the DAG keeps the backfill
	// run that has it.
	statuses, err := f.repository.LatestStatuses(ctx, persis.DAGRunLatestStatusesQuery{Labels: []string{"team=data"}})
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "backfill-2", statuses[0].DAGRunID)
}

// A DAG's history page leaves out backfill runs, so a long backfill does not
// push the live runs out of it, unless the DAG has no other runs; then the
// pages that follow list them too.
func TestRecentStatusesPageLeavesOutBackfillRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backfillRun := func(f slotFixture, runID string) {
		t.Helper()
		status := scheduledStatus(ir.Succeeded, slotOct1, ir.TriggerTypeBackfill)
		status.BackfillID = "bf123456"
		f.writeRun(t, runID, persis.DAGRunCreateAttemptOptions{}, status)
	}
	ids := func(page persis.DAGRunStatusPage) []string {
		var out []string
		for _, status := range page.Items {
			out = append(out, status.DAGRunID)
		}
		return out
	}

	only := newSlotFixture(t)
	backfillRun(only, "backfill-1")
	backfillRun(only, "backfill-2")
	first, err := only.repository.RecentStatusesPage(ctx, only.dag.Name, persis.RecentStatusesPageOptions{Limit: 1, ExcludeBackfill: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"backfill-2"}, ids(first))
	require.NotEmpty(t, first.NextCursor)
	second, err := only.repository.RecentStatusesPage(ctx, only.dag.Name, persis.RecentStatusesPageOptions{Limit: 1, Cursor: first.NextCursor, ExcludeBackfill: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"backfill-1"}, ids(second))

	mixed := newSlotFixture(t)
	mixed.writeRun(t, "live", persis.DAGRunCreateAttemptOptions{}, scheduledStatus(ir.Succeeded, slotOct2, ir.TriggerTypeScheduler))
	backfillRun(mixed, "backfill-1")
	page, err := mixed.repository.RecentStatusesPage(ctx, mixed.dag.Name, persis.RecentStatusesPageOptions{Limit: 10, ExcludeBackfill: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"live"}, ids(page))
	page, err = mixed.repository.RecentStatusesPage(ctx, mixed.dag.Name, persis.RecentStatusesPageOptions{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, []string{"backfill-1", "live"}, ids(page))
}
