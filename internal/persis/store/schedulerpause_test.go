// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

func newSchedulerPauseStore() schedulerstate.PauseStore {
	return store.NewSchedulerPauseStore(testutil.NewMemoryBackend().Collection("scheduler"))
}

func TestSchedulerPauseDefaultsToRunning(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerPauseStore()

	paused, err := s.IsPaused(ctx)
	require.NoError(t, err)
	assert.False(t, paused)

	pause, err := s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, schedulerstate.Pause{}, pause)
}

func TestSchedulerPauseRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerPauseStore()

	require.NoError(t, s.Set(ctx, true, "alice", "db migration"))

	pause, err := s.Get(ctx)
	require.NoError(t, err)
	assert.True(t, pause.Paused)
	assert.Equal(t, "alice", pause.PausedBy)
	assert.Equal(t, "db migration", pause.Reason)
	assert.False(t, pause.PausedAt.IsZero())
}

func TestSchedulerPauseResumeClearsActorAndReason(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerPauseStore()

	require.NoError(t, s.Set(ctx, true, "alice", "db migration"))
	require.NoError(t, s.Set(ctx, false, "bob", "ignored"))

	pause, err := s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, schedulerstate.Pause{}, pause)
}

// The scheduler's wake hold is a pause only the store can mark: it goes in
// only where nothing is paused, comes out only while it still stands, and an
// operator's pause, even one by a user named as the scheduler is, is never
// taken for it or written over.
func TestSchedulerPauseHoldYieldsToAPause(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerPauseStore()

	held, err := s.SetHold(ctx, true, "waking")
	require.NoError(t, err)
	assert.True(t, held)
	pause, err := s.Get(ctx)
	require.NoError(t, err)
	assert.True(t, pause.WakeHold())
	assert.Equal(t, schedulerstate.PausedByScheduler, pause.PausedBy)
	assert.Equal(t, "waking", pause.Reason)
	assert.False(t, pause.PausedAt.IsZero())

	held, err = s.SetHold(ctx, true, "again")
	require.NoError(t, err)
	assert.False(t, held, "a hold already in place stays as it is")

	// Clearing the pause through the API ends the hold.
	require.NoError(t, s.Set(ctx, false, "", ""))
	lifted, err := s.SetHold(ctx, false, "")
	require.NoError(t, err)
	assert.False(t, lifted, "nothing left to lift")

	// An operator's pause is neither taken for a hold nor written over.
	require.NoError(t, s.Set(ctx, true, schedulerstate.PausedByScheduler, "a user so named"))
	pause, err = s.Get(ctx)
	require.NoError(t, err)
	assert.False(t, pause.WakeHold())
	held, err = s.SetHold(ctx, true, "waking")
	require.NoError(t, err)
	assert.False(t, held)
	lifted, err = s.SetHold(ctx, false, "")
	require.NoError(t, err)
	assert.False(t, lifted)
	pause, err = s.Get(ctx)
	require.NoError(t, err)
	assert.True(t, pause.Paused)
	assert.Equal(t, "a user so named", pause.Reason)

	// A hold is lifted by the scheduler alone, through the same store or another on the collection.
	require.NoError(t, s.Set(ctx, false, "", ""))
	held, err = s.SetHold(ctx, true, "waking")
	require.NoError(t, err)
	assert.True(t, held)
	lifted, err = s.SetHold(ctx, false, "")
	require.NoError(t, err)
	assert.True(t, lifted)
	pause, err = s.Get(ctx)
	require.NoError(t, err)
	assert.Equal(t, schedulerstate.Pause{}, pause)
}

// A pause written by another process must be observed without restarting the
// reader, because the scheduler and the API run as separate processes.
func TestSchedulerPauseObservesExternalWrite(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("scheduler")
	reader := store.NewSchedulerPauseStore(col)
	writer := store.NewSchedulerPauseStore(col)

	paused, err := reader.IsPaused(ctx)
	require.NoError(t, err)
	require.False(t, paused)

	require.NoError(t, writer.Set(ctx, true, "alice", "maintenance"))

	paused, err = reader.IsPaused(ctx)
	require.NoError(t, err)
	assert.True(t, paused)

	require.NoError(t, writer.Set(ctx, false, "", ""))

	paused, err = reader.IsPaused(ctx)
	require.NoError(t, err)
	assert.False(t, paused)
}

func TestSchedulerPauseCorruptRecordReportsRunning(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("scheduler")
	s := store.NewSchedulerPauseStore(col)

	require.NoError(t, col.Put(ctx, &persis.Record{ID: "paused", Data: []byte("{not json")}))

	paused, err := s.IsPaused(ctx)
	require.NoError(t, err)
	assert.False(t, paused)
}

func TestSchedulerPauseFileLayout(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(filepath.Join(root, "scheduler"), file.WithIndentedJSON())
	s := store.NewSchedulerPauseStore(col)

	require.NoError(t, s.Set(ctx, true, "alice", "db migration"))

	raw, err := os.ReadFile(filepath.Join(root, "scheduler", "paused.json"))
	require.NoError(t, err)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Contains(t, body, "paused")
	assert.Contains(t, body, "pausedBy")
	assert.Contains(t, body, "reason")
}

func TestSchedulerPauseConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerPauseStore()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			if i%2 == 0 {
				require.NoError(t, s.Set(ctx, true, "alice", "maintenance"))
				return
			}
			_, err := s.IsPaused(ctx)
			require.NoError(t, err)
		})
	}
	wg.Wait()
}

// writeCounter counts the writes to a collection, and fails them while fail
// is set.
type writeCounter struct {
	persis.Collection
	writes int
	fail   bool
}

func (c *writeCounter) write(do func() error) error {
	if c.fail {
		return errors.New("disk full")
	}
	c.writes++
	return do()
}

func (c *writeCounter) Put(ctx context.Context, rec *persis.Record) error {
	return c.write(func() error { return c.Collection.Put(ctx, rec) })
}

func (c *writeCounter) Create(ctx context.Context, rec *persis.Record) error {
	return c.write(func() error { return c.Collection.Create(ctx, rec) })
}

func (c *writeCounter) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	return c.write(func() error { return c.Collection.CompareAndSwap(ctx, id, expected, next) })
}

// An operator's resume records the pause it ends in the same write, from
// when the pause began, however often the scheduler was paused meanwhile.
func TestSchedulerPauseResumeRecordsTheEndedPause(t *testing.T) {
	ctx := t.Context()
	col := &writeCounter{Collection: testutil.NewMemoryBackend().Collection("scheduler")}
	pauses := store.NewSchedulerPauseStore(col)
	periods := store.NewPeriodStore(
		testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState),
		testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerProcess),
		pauses,
	)

	require.NoError(t, pauses.Set(ctx, true, "alice", "db migration"))
	pause, err := pauses.Get(ctx)
	require.NoError(t, err)
	require.NoError(t, pauses.Set(ctx, true, "bob", "still migrating"))
	before := col.writes
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	assert.Equal(t, 1, col.writes-before, "the resume and its period are one write")
	ended, _, err := periods.Ended(ctx, "")
	require.NoError(t, err)
	require.Len(t, ended, 1)
	assert.True(t, pause.PausedAt.Equal(ended[0].From), "from %s", ended[0].From)
	assert.Equal(t, schedulerstate.PeriodPaused, ended[0].Reason)
	assert.False(t, ended[0].To.Before(ended[0].From))
}

// A resume whose write fails leaves the scheduler paused and records no
// period.
func TestSchedulerPauseFailedResumeRecordsNothing(t *testing.T) {
	ctx := t.Context()
	col := &writeCounter{Collection: testutil.NewMemoryBackend().Collection("scheduler")}
	pauses := store.NewSchedulerPauseStore(col)
	periods := store.NewPeriodStore(
		testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState),
		testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerProcess),
		pauses,
	)
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))

	col.fail = true
	require.Error(t, pauses.Set(ctx, false, "", ""))

	paused, err := pauses.IsPaused(ctx)
	require.NoError(t, err)
	assert.True(t, paused)
	ended, _, err := periods.Ended(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, ended)
}

// The scheduler's wake hold is not an operator's pause: ending it records
// no period, and placing or lifting it keeps the pauses recorded.
func TestSchedulerPauseHoldRecordsNoPeriod(t *testing.T) {
	ctx := t.Context()
	pauses := store.NewSchedulerPauseStore(testutil.NewMemoryBackend().Collection("scheduler"))
	periods := store.NewPeriodStore(
		testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerState),
		testutil.NewMemoryBackend().Collection(persis.CollectionSchedulerProcess),
		pauses,
	)
	require.NoError(t, pauses.Set(ctx, true, "alice", ""))
	require.NoError(t, pauses.Set(ctx, false, "", ""))

	for _, end := range []func() error{
		func() error { _, err := pauses.SetHold(ctx, false, ""); return err },
		func() error { return pauses.Set(ctx, false, "", "") },
	} {
		held, err := pauses.SetHold(ctx, true, "waking")
		require.NoError(t, err)
		require.True(t, held)
		require.NoError(t, end())
	}

	ended, _, err := periods.Ended(ctx, "")
	require.NoError(t, err)
	assert.Len(t, ended, 1, "the operator's pause only")
}

// racingCollection lets another writer change a record right after the
// first read of it returns, or, with raceWrite, right after the first write.
type racingCollection struct {
	*testutil.MemoryCollection
	race, raceWrite func()
}

func (c *racingCollection) Get(ctx context.Context, id string) (*persis.Record, error) {
	rec, err := c.MemoryCollection.Get(ctx, id)
	if race := c.race; race != nil {
		c.race = nil
		race()
	}
	return rec, err
}

func (c *racingCollection) Create(ctx context.Context, rec *persis.Record) error {
	err := c.MemoryCollection.Create(ctx, rec)
	c.wrote()
	return err
}

func (c *racingCollection) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	err := c.MemoryCollection.CompareAndSwap(ctx, id, expected, next)
	c.wrote()
	return err
}

func (c *racingCollection) wrote() {
	if race := c.raceWrite; race != nil {
		c.raceWrite = nil
		race()
	}
}

// A pause written by another process while the store reads the record is
// seen on the next read, not hidden behind the record read before it.
func TestSchedulerPauseSeesAWriteDuringARead(t *testing.T) {
	ctx := t.Context()
	inner := testutil.NewMemoryBackend().Collection("scheduler").(*testutil.MemoryCollection)
	col := &racingCollection{MemoryCollection: inner}
	reader := store.NewSchedulerPauseStore(col)
	col.race = func() { require.NoError(t, store.NewSchedulerPauseStore(inner).Set(ctx, true, "alice", "")) }

	_, err := reader.IsPaused(ctx)
	require.NoError(t, err)
	paused, err := reader.IsPaused(ctx)
	require.NoError(t, err)
	assert.True(t, paused)
}

// A resume by another process right after the store's own pause or wake hold
// is seen on the next read, not hidden behind the record the store wrote.
func TestSchedulerPauseSeesAWriteAfterItsOwn(t *testing.T) {
	for name, pause := range map[string]func(context.Context, *store.SchedulerPauseStore) error{
		"pause": func(ctx context.Context, s *store.SchedulerPauseStore) error { return s.Set(ctx, true, "alice", "") },
		"hold": func(ctx context.Context, s *store.SchedulerPauseStore) error {
			_, err := s.SetHold(ctx, true, "waking")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			inner := testutil.NewMemoryBackend().Collection("scheduler").(*testutil.MemoryCollection)
			col := &racingCollection{MemoryCollection: inner}
			writer := store.NewSchedulerPauseStore(col)
			col.raceWrite = func() { require.NoError(t, store.NewSchedulerPauseStore(inner).Set(ctx, false, "", "")) }

			require.NoError(t, pause(ctx, writer))
			paused, err := writer.IsPaused(ctx)
			require.NoError(t, err)
			assert.False(t, paused, "another process resumed")
		})
	}
}

// contendedCollection fails each swap with a conflict until released, as
// when another process keeps writing the record.
type contendedCollection struct {
	*testutil.MemoryCollection
	conflicted chan struct{}
	once       sync.Once
	released   atomic.Bool
}

func (c *contendedCollection) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	if !c.released.Load() {
		c.once.Do(func() { close(c.conflicted) })
		return persis.ErrConflict
	}
	return c.MemoryCollection.CompareAndSwap(ctx, id, expected, next)
}

// A write that retries on conflicts keeps no read waiting, as the scheduler
// checks the pause on every dispatch decision.
func TestSchedulerPauseReadsDuringAContendedWrite(t *testing.T) {
	ctx := t.Context()
	inner := testutil.NewMemoryBackend().Collection("scheduler").(*testutil.MemoryCollection)
	require.NoError(t, store.NewSchedulerPauseStore(inner).Set(ctx, true, "alice", ""))
	col := &contendedCollection{MemoryCollection: inner, conflicted: make(chan struct{})}
	s := store.NewSchedulerPauseStore(col)
	resumed := make(chan error, 1)
	go func() { resumed <- s.Set(ctx, false, "", "") }()
	<-col.conflicted

	read := make(chan bool, 1)
	go func() {
		paused, err := s.IsPaused(ctx)
		assert.NoError(t, err)
		read <- paused
	}()
	select {
	case paused := <-read:
		assert.True(t, paused, "the resume has not been written")
	case <-time.After(5 * time.Second):
		t.Fatal("the read waited for the contended write")
	}

	col.released.Store(true)
	require.NoError(t, <-resumed)
	paused, err := s.IsPaused(ctx)
	require.NoError(t, err)
	assert.False(t, paused)
}
