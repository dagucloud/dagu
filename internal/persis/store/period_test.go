// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

func newPeriodStore() *store.PeriodStore {
	return newPeriodStoreOn(testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState))
}

// newPeriodStoreOn returns a period store that keeps DAG records in col.
func newPeriodStoreOn(col persis.Collection) *store.PeriodStore {
	return store.NewPeriodStore(col, testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerProcess), nil)
}

// periodAt is an hour of a day counted from 25 days ago, inside the 30 days
// the store keeps.
func periodAt(day, hour int) time.Time {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -25)
	return start.AddDate(0, 0, day-1).Add(time.Duration(hour) * time.Hour)
}

// A DAG's periods join the scheduler's outages and the operator's pauses,
// which hold for every DAG, and not another DAG's.
func TestPeriodStoreGivesThePeriodsOfADAG(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	backend := testutil.NewMemoryBackend()
	pauses := store.NewSchedulerPauseStore(backend.Collection(persis.CollectionSchedulerProcess))
	s := store.NewPeriodStore(
		backend.Collection(persis.CollectionSchedulerState),
		backend.Collection(persis.CollectionSchedulerProcess),
		pauses,
	)
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: periodAt(2, 9), To: periodAt(2, 11)}))
	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: periodAt(1, 12), To: periodAt(1, 13)}))
	require.NoError(t, s.AddSuspended(ctx, "other", schedulerstate.Period{From: periodAt(1, 0), To: periodAt(1, 1)}))

	periods, _, err := s.Ended(ctx, "etl")
	require.NoError(t, err)
	require.Len(t, periods, 3)
	assert.Equal(t, []schedulerstate.Period{
		{From: periodAt(1, 12), To: periodAt(1, 13), Reason: schedulerstate.PeriodSuspended},
		{From: periodAt(2, 9), To: periodAt(2, 11), Reason: schedulerstate.PeriodDown},
	}, periods[:2])
	assert.Equal(t, schedulerstate.PeriodPaused, periods[2].Reason)
}

// Two projects' period stores share the scheduler's outages and the
// operator's pauses, and each keeps its DAGs' suspensions to itself.
func TestPeriodStoreSharesProcessPeriodsAcrossProjects(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	process := testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerProcess)
	pauses := store.NewSchedulerPauseStore(process)
	newProjectStore := func() *store.PeriodStore {
		return store.NewPeriodStore(testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState), process, pauses)
	}
	weather, billing := newProjectStore(), newProjectStore()

	down := schedulerstate.Period{From: periodAt(1, 0), To: periodAt(1, 1)}
	require.NoError(t, weather.AddDown(ctx, down))
	require.NoError(t, weather.AddSuspended(ctx, "etl", schedulerstate.Period{From: periodAt(1, 9), To: periodAt(1, 10)}))
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	periods, _, err := billing.Ended(ctx, "etl")
	require.NoError(t, err)
	require.Len(t, periods, 2)
	assert.Equal(t, schedulerstate.Period{From: down.From, To: down.To, Reason: schedulerstate.PeriodDown}, periods[0])
	assert.Equal(t, schedulerstate.PeriodPaused, periods[1].Reason)
}

// Periods with the same bounds come in one order on every read: outages,
// then pauses, then suspensions.
func TestPeriodStoreGivesPeriodsInOneOrder(t *testing.T) {
	t.Parallel()
	s := newPeriodStore()
	ctx := t.Context()
	period := schedulerstate.Period{From: periodAt(1, 9), To: periodAt(1, 10)}
	require.NoError(t, s.AddSuspended(ctx, "etl", period))
	require.NoError(t, s.AddDown(ctx, period))

	for range 20 {
		periods, _, err := s.Ended(ctx, "etl")
		require.NoError(t, err)
		require.Equal(t, []schedulerstate.Period{
			{From: period.From, To: period.To, Reason: schedulerstate.PeriodDown},
			{From: period.From, To: period.To, Reason: schedulerstate.PeriodSuspended},
		}, periods)
	}
}

// A period is recorded once by when it began: recording it again with a later
// end, as a resume retried after the DAG stayed suspended does, extends it,
// and with no later end writes nothing.
func TestPeriodStoreRecordsAPeriodOnce(t *testing.T) {
	t.Parallel()
	col := &swapCounter{Collection: testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState)}
	s := newPeriodStoreOn(col)
	ctx := t.Context()

	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: periodAt(1, 9), To: periodAt(1, 10)}))
	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: periodAt(1, 9), To: periodAt(1, 11)}))
	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: periodAt(1, 9), To: periodAt(1, 10)}))

	periods, _, err := s.Ended(ctx, "etl")
	require.NoError(t, err)
	assert.Equal(t, []schedulerstate.Period{{From: periodAt(1, 9), To: periodAt(1, 11), Reason: schedulerstate.PeriodSuspended}}, periods)
	assert.Equal(t, 1, col.swaps, "only the later end writes")
}

// A write keeps the 100 periods that ended last within the 30 days before it.
func TestPeriodStoreKeepsRecentPeriods(t *testing.T) {
	t.Parallel()
	s := newPeriodStore()
	ctx := t.Context()
	ended := time.Now().Add(-31 * 24 * time.Hour)
	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: ended.Add(-time.Hour), To: ended}))

	start := periodAt(1, 0)
	for i := range 101 {
		from := start.Add(time.Duration(i) * time.Hour)
		require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: from, To: from.Add(time.Minute)}))
	}

	periods, _, err := s.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, periods, 100)
	assert.Equal(t, start.Add(time.Hour), periods[0].From, "the period that ended first is dropped")
}

// A period that ended more than 30 days ago is not given, even before a write
// drops it.
func TestPeriodStoreGivesNoExpiredPeriod(t *testing.T) {
	t.Parallel()
	s := newPeriodStore()
	ctx := t.Context()
	ended := time.Now().Add(-31 * 24 * time.Hour)
	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: ended.Add(-time.Hour), To: ended}))

	periods, _, err := s.Ended(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, periods)
}

// The bound keeps the periods that ended last, whatever order they were
// recorded in, as when the hosts that record them disagree on the time.
func TestPeriodStoreKeepsThePeriodsThatEndedLast(t *testing.T) {
	t.Parallel()
	s := newPeriodStore()
	ctx := t.Context()
	for i := range 100 {
		from := periodAt(3, 0).Add(time.Duration(i) * time.Hour)
		require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: from, To: from.Add(time.Minute)}))
	}
	require.NoError(t, s.AddDown(ctx, schedulerstate.Period{From: periodAt(2, 0), To: periodAt(2, 1)}))

	periods, _, err := s.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, periods, 100)
	assert.Equal(t, periodAt(3, 0), periods[0].From, "the period that ended first is dropped")
}

// swapCounter counts the records a collection replaces.
type swapCounter struct {
	persis.Collection
	swaps int
}

func (c *swapCounter) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	c.swaps++
	return c.Collection.CompareAndSwap(ctx, id, expected, next)
}

// Each DAG keeps a record of its own on disk, also one named as a device
// that a host reserves, or as names that normalize alike.
func TestPeriodStoreKeepsARecordPerDAGName(t *testing.T) {
	t.Parallel()
	s := newPeriodStoreOn(file.NewCollection(t.TempDir()))
	ctx := t.Context()
	names := []string{"CON", "nul", "-", "etl."}
	for i, name := range names {
		from := periodAt(1, i)
		require.NoError(t, s.AddSuspended(ctx, name, schedulerstate.Period{From: from, To: from.Add(time.Minute)}), name)
	}
	for i, name := range names {
		periods, _, err := s.Ended(ctx, name)
		require.NoError(t, err, name)
		from := periodAt(1, i)
		assert.Equal(t, []schedulerstate.Period{{From: from, To: from.Add(time.Minute), Reason: schedulerstate.PeriodSuspended}}, periods, name)
	}
}

// The periods one process records are read by another, as the API records a
// suspension and the scheduler an outage, each in a record of its own next to
// the scheduler state, holding only when each period began and ended.
func TestPeriodStoreSharesPeriodsThroughFiles(t *testing.T) {
	t.Parallel()
	paths := config.PathsConfig{DataDir: t.TempDir()}
	pauses := store.NewSchedulerPauseStore(file.NewBackend(paths).Collection(persis.CollectionSchedulerProcess))
	open := func() *store.PeriodStore {
		backend := file.NewBackend(paths)
		return store.NewPeriodStore(
			backend.Collection(persis.CollectionSchedulerState),
			backend.Collection(persis.CollectionSchedulerProcess),
			pauses,
		)
	}
	writer, reader := open(), open()
	ctx := t.Context()

	down := schedulerstate.Period{From: periodAt(1, 0), To: periodAt(1, 1)}
	suspended := schedulerstate.Period{From: periodAt(1, 9), To: periodAt(1, 10)}
	require.NoError(t, writer.AddDown(ctx, down))
	require.NoError(t, writer.AddSuspended(ctx, "nested/etl", suspended))
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	periods, _, err := reader.Ended(ctx, "nested/etl")
	require.NoError(t, err)
	require.Len(t, periods, 3)
	assert.Equal(t, []schedulerstate.Period{
		{From: down.From, To: down.To, Reason: schedulerstate.PeriodDown},
		{From: suspended.From, To: suspended.To, Reason: schedulerstate.PeriodSuspended},
	}, periods[:2])
	assert.Equal(t, schedulerstate.PeriodPaused, periods[2].Reason)

	dir := file.SchedulerStateDir(paths)
	for name, list := range map[string]string{"down.json": "down", "dags/nested%252Fetl.json": "suspended", "paused.json": "history"} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		require.NoError(t, err, name)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields), name)
		var periods []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fields[list], &periods), name)
		require.Len(t, periods, 1, name)
		assert.ElementsMatch(t, []string{"from", "to"}, slices.Collect(maps.Keys(periods[0])), name)
	}
}

// A DAG's record is read where released versions wrote it: Windows escaped a
// device name such as aux, and every host escaped a subdirectory's slash.
func TestPeriodStoreReadsReleasedRecords(t *testing.T) {
	t.Parallel()
	for dag, rel := range map[string]string{"aux": "dags/%61ux.json", "nested/etl": "dags/nested%2Fetl.json"} {
		t.Run(dag, func(t *testing.T) {
			t.Parallel()
			paths := config.PathsConfig{DataDir: t.TempDir()}
			suspended := schedulerstate.Period{From: periodAt(1, 9), To: periodAt(1, 10)}
			data, err := json.Marshal(map[string][]schedulerstate.Period{"suspended": {suspended}})
			require.NoError(t, err)
			path := filepath.Join(file.SchedulerStateDir(paths), filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(t, os.WriteFile(path, data, 0o600))
			s := newPeriodStoreOn(file.NewBackend(paths).Collection(persis.CollectionSchedulerState))

			periods, _, err := s.Ended(t.Context(), dag)
			require.NoError(t, err)
			assert.Equal(t, []schedulerstate.Period{{From: suspended.From, To: suspended.To, Reason: schedulerstate.PeriodSuspended}}, periods)
		})
	}
}

// A dismissal of a DAG's missed slots is kept in the DAG's record, made if
// it has none yet. A later dismissal replaces it, and an earlier one, as
// from a host whose clock is behind, writes nothing.
func TestPeriodStoreKeepsTheLaterDismissal(t *testing.T) {
	t.Parallel()
	col := &swapCounter{Collection: testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState)}
	s := newPeriodStoreOn(col)
	ctx := t.Context()
	dismissed := func() schedulerstate.Dismissal {
		t.Helper()
		_, dismissal, err := s.Ended(ctx, "etl")
		require.NoError(t, err)
		return dismissal
	}

	require.NoError(t, s.Dismiss(ctx, "etl", schedulerstate.Dismissal{At: periodAt(2, 0), By: "alice"}))
	assert.Equal(t, schedulerstate.Dismissal{At: periodAt(2, 0), By: "alice"}, dismissed())

	require.NoError(t, s.Dismiss(ctx, "etl", schedulerstate.Dismissal{At: periodAt(1, 0), By: "bob"}))
	assert.Zero(t, col.swaps, "an earlier dismissal writes nothing")
	require.NoError(t, s.Dismiss(ctx, "etl", schedulerstate.Dismissal{At: periodAt(3, 0), By: "carol"}))
	assert.Equal(t, schedulerstate.Dismissal{At: periodAt(3, 0), By: "carol"}, dismissed())

	require.NoError(t, s.AddSuspended(ctx, "etl", schedulerstate.Period{From: periodAt(4, 0), To: periodAt(4, 1)}))
	assert.Equal(t, schedulerstate.Dismissal{At: periodAt(3, 0), By: "carol"}, dismissed(), "a suspension keeps it")
}
