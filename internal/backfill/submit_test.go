// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filebackfill "github.com/dagucloud/dagu/v2/internal/persis/file/backfill"
	"github.com/dagucloud/dagu/v2/internal/persis/file/slotindex"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type submitFixture struct {
	service *backfill.Service
	store   *filebackfill.Store
	request func(from, to time.Time, rerun backfill.Rerun) backfill.SubmitRequest
}

func newSubmitFixture(t *testing.T) submitFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	repository := testutil.NewFileDAGRunRepository(filepath.Join(dir, "dag-runs"), persis.DAGRunRepositoryOptions{
		SlotIndex: slotindex.New(filepath.Join(dir, "slot-index")),
		Location:  time.UTC,
	})
	dag, err := spec.LoadYAML(ctx, []byte("name: etl\nschedule: \"0 2 * * *\"\nsteps:\n  - run: echo hi\n"))
	require.NoError(t, err)
	store := filebackfill.New(filepath.Join(dir, "backfills"))
	now := at(20, 0)
	sched := backfill.SlotSchedule{Schedules: dag.Schedule, Location: time.UTC}
	return submitFixture{
		service: &backfill.Service{Store: store, History: repository, Now: func() time.Time { return now }},
		store:   store,
		request: func(from, to time.Time, rerun backfill.Rerun) backfill.SubmitRequest {
			return backfill.SubmitRequest{
				Plan:    backfill.PlanRequest{DAG: dag, Schedule: sched, From: from, To: to, Rerun: rerun},
				Options: backfill.Options{From: from, To: to, Rerun: rerun, MaxConcurrency: 1, OnFailure: backfill.OnFailureStop},
				DAGFile: "etl.yaml",
				Actor:   "alice",
			}
		},
	}
}

func TestSubmitRecordsTheSlotsToRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSubmitFixture(t)

	dry := f.request(at(1, 0), at(3, 23), backfill.RerunNone)
	dry.DryRun = true
	planned, err := f.service.Submit(ctx, dry)
	require.NoError(t, err)
	assert.Nil(t, planned.Record)
	records, err := f.store.List(ctx, "etl")
	require.NoError(t, err)
	assert.Empty(t, records, "a dry run records nothing")

	result, err := f.service.Submit(ctx, f.request(at(1, 0), at(3, 23), backfill.RerunNone))
	require.NoError(t, err)
	require.NotNil(t, result.Record)
	assert.False(t, result.Existing)
	assert.Equal(t, planned.Plan.Revision, result.Plan.Revision, "the dry run shows the plan the submit runs")
	assert.Len(t, result.Record.ID, 8)
	assert.Equal(t, "alice", result.Record.Actor)
	assert.Contains(t, string(result.Record.Definition), "0 2 * * *", "the definition is pinned")
	require.Len(t, result.Record.Slots, 3)
	assert.Equal(t, at(1, 2), result.Record.Slots[0].At)
	assert.Equal(t, at(1, 2), result.Record.Slots[1].Window.Start, "each slot keeps its window")
}

// Submitting the same backfill while it is open returns it; an overlapping
// one leaves the open backfill's slots to it.
func TestSubmitWhileAnotherIsOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSubmitFixture(t)
	first, err := f.service.Submit(ctx, f.request(at(1, 0), at(3, 23), backfill.RerunNone))
	require.NoError(t, err)

	for _, dryRun := range []bool{true, false} {
		req := f.request(at(1, 0), at(3, 23), backfill.RerunNone)
		req.DryRun = dryRun
		again, err := f.service.Submit(ctx, req)
		require.NoError(t, err)
		assert.True(t, again.Existing, "dry run %v", dryRun)
		assert.Equal(t, first.Record.ID, again.Record.ID)
	}

	overlap, err := f.service.Submit(ctx, f.request(at(3, 0), at(4, 23), backfill.RerunNone))
	require.NoError(t, err)
	require.NotNil(t, overlap.Record)
	assert.Equal(t, []backfill.Action{backfill.ActionSkipActive, backfill.ActionRun}, actions(overlap.Plan))
	require.Len(t, overlap.Record.Slots, 1)
	assert.Equal(t, at(4, 2), overlap.Record.Slots[0].At)

	records, err := f.store.List(ctx, "etl")
	require.NoError(t, err)
	assert.Len(t, records, 2)
}

func TestSubmitRefusesAStalePlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSubmitFixture(t)
	req := f.request(at(1, 0), at(3, 23), backfill.RerunNone)
	req.Revision = "stale"

	result, err := f.service.Submit(ctx, req)
	require.ErrorIs(t, err, backfill.ErrStalePlan)
	assert.NotEmpty(t, result.Plan.Revision, "the new plan is returned")
	assert.Nil(t, result.Record)
}

func TestSubmitWithNothingToRunRecordsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newSubmitFixture(t)
	// A range between two fires has no slot.
	result, err := f.service.Submit(ctx, f.request(at(1, 3), at(1, 23), backfill.RerunNone))
	require.NoError(t, err)
	assert.Nil(t, result.Record)
	assert.Empty(t, result.Plan.Slots)
}
