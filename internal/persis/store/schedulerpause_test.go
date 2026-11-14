// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
