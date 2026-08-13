// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func newSchedulerStateStore(t *testing.T) schedulerstate.Store {
	t.Helper()
	return store.NewSchedulerStateStore(testutil.NewMemoryBackend().Collection("scheduler"))
}

var errCheckpointSave = errors.New("checkpoint save failed")

type checkpointFailCollection struct {
	persis.Collection
	failCheckpoint bool
}

func (c *checkpointFailCollection) Put(ctx context.Context, rec *persis.Record) error {
	if c.failCheckpoint && rec.ID == "checkpoint" {
		return errCheckpointSave
	}
	return c.Collection.Put(ctx, rec)
}

func (c *checkpointFailCollection) RecordVersion(ctx context.Context, id string) (string, error) {
	versioned := c.Collection.(interface {
		RecordVersion(context.Context, string) (string, error)
	})
	return versioned.RecordVersion(ctx, id)
}

func TestSchedulerStateLoadEmpty(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerStateStore(t)

	state, err := s.Load(ctx)
	require.NoError(t, err)
	assert.NotNil(t, state.DAGs)
	assert.Empty(t, state.DAGs)
}

func TestSchedulerStateSaveAndLoad(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerStateStore(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	state := &schedulerstate.State{
		LastTick: now,
		DAGs: map[string]schedulerstate.DAGWatermark{
			"my-dag": {LastScheduledTime: now},
		},
	}

	require.NoError(t, s.Save(ctx, state))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, now, got.LastTick)
	assert.Contains(t, got.DAGs, "my-dag")
	assert.Equal(t, now, got.DAGs["my-dag"].LastScheduledTime)
}

func TestSchedulerStateStoreCopiesMutableState(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerStateStore(t)

	scheduledAt := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	nextRun := scheduledAt
	state := &schedulerstate.State{
		DAGs: map[string]schedulerstate.DAGWatermark{
			"my-dag": {
				NextRun: &nextRun,
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					"schedule": {
						ScheduledTime: scheduledAt,
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}
	require.NoError(t, s.Save(ctx, state))

	mutated := state.DAGs["my-dag"]
	*mutated.NextRun = scheduledAt.Add(time.Hour)
	mutated.OneOffs["schedule"] = schedulerstate.OneOffScheduleState{
		ScheduledTime: scheduledAt,
		Status:        schedulerstate.OneOffStatusConsumed,
	}
	state.DAGs["my-dag"] = mutated
	loaded, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, scheduledAt, *loaded.DAGs["my-dag"].NextRun)
	assert.Equal(t, schedulerstate.OneOffStatusPending, loaded.DAGs["my-dag"].OneOffs["schedule"].Status)

	mutated = loaded.DAGs["my-dag"]
	*mutated.NextRun = scheduledAt.Add(2 * time.Hour)
	mutated.OneOffs["schedule"] = schedulerstate.OneOffScheduleState{
		ScheduledTime: scheduledAt,
		Status:        schedulerstate.OneOffStatusConsumed,
	}
	loaded.DAGs["my-dag"] = mutated
	reloaded, err := s.Load(ctx)
	require.NoError(t, err)
	require.Contains(t, reloaded.DAGs, "my-dag")
	assert.Equal(t, scheduledAt, *reloaded.DAGs["my-dag"].NextRun)
	assert.Equal(t, schedulerstate.OneOffStatusPending, reloaded.DAGs["my-dag"].OneOffs["schedule"].Status)
}

func TestSchedulerStateStoreConcurrentLoadSave(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerStateStore(t)

	const iterations = 100
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range iterations {
			if err := s.Save(ctx, &schedulerstate.State{
				LastTick: time.Unix(int64(i), 0).UTC(),
				DAGs:     map[string]schedulerstate.DAGWatermark{"my-dag": {}},
			}); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range iterations {
			state, err := s.Load(ctx)
			if err != nil {
				errCh <- err
				return
			}
			if state.DAGs == nil {
				errCh <- errors.New("loaded state has a nil DAG map")
				return
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

func TestSchedulerStateFileLayoutCompatibility(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(filepath.Join(root, "scheduler"), file.WithIndentedJSON())
	s := store.NewSchedulerStateStore(col)
	now := time.Now().UTC()
	state := &schedulerstate.State{
		LastTick: now,
		DAGs: map[string]schedulerstate.DAGWatermark{
			"my-dag": {},
		},
	}

	require.NoError(t, s.Save(ctx, state))

	raw, err := os.ReadFile(filepath.Join(root, "scheduler", "state.json"))
	require.NoError(t, err)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.NotContains(t, body, "encoding")
	assert.NotContains(t, body, "data")
	assert.NotContains(t, body, "lastTick")
	assert.Contains(t, body, "version")
	assert.Contains(t, body, "dags")
	var version int
	require.NoError(t, json.Unmarshal(body["version"], &version))
	assert.Equal(t, 4, version)

	rawCheckpoint, err := os.ReadFile(filepath.Join(root, "scheduler", "checkpoint.json"))
	require.NoError(t, err)
	var checkpoint map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawCheckpoint, &checkpoint))
	assert.Contains(t, checkpoint, "lastTick")
}

func TestSchedulerStateLoadMigratesLegacyLastTickToCheckpoint(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	storeDir := filepath.Join(root, "scheduler")
	require.NoError(t, os.MkdirAll(storeDir, 0o700))

	lastTick := time.Date(2026, 2, 7, 12, 2, 0, 0, time.UTC)
	rawState := fmt.Appendf(nil, `{"version":3,"lastTick":%q,"dags":{"my-dag":{}}}`, lastTick.Format(time.RFC3339))
	require.NoError(t, os.WriteFile(filepath.Join(storeDir, "state.json"), rawState, 0o600))

	s := store.NewSchedulerStateStore(file.NewCollection(storeDir, file.WithIndentedJSON()))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, lastTick, got.LastTick)
	require.Contains(t, got.DAGs, "my-dag")

	require.NoError(t, s.Save(ctx, got))

	rawMigratedState, err := os.ReadFile(filepath.Join(storeDir, "state.json"))
	require.NoError(t, err)
	var stateBody map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawMigratedState, &stateBody))
	assert.NotContains(t, stateBody, "lastTick")

	rawCheckpoint, err := os.ReadFile(filepath.Join(storeDir, "checkpoint.json"))
	require.NoError(t, err)
	var checkpoint struct {
		LastTick time.Time `json:"lastTick"`
	}
	require.NoError(t, json.Unmarshal(rawCheckpoint, &checkpoint))
	assert.Equal(t, lastTick, checkpoint.LastTick)
}

func TestSchedulerStateSaveSkipsStateWriteForCheckpointOnlyChange(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("scheduler")
	s := store.NewSchedulerStateStore(col)

	state := &schedulerstate.State{
		LastTick: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		DAGs: map[string]schedulerstate.DAGWatermark{
			"my-dag": {},
		},
	}
	require.NoError(t, s.Save(ctx, state))
	versioned := col.(interface {
		RecordVersion(context.Context, string) (string, error)
	})
	stateVersion, err := versioned.RecordVersion(ctx, "state")
	require.NoError(t, err)

	state.LastTick = state.LastTick.Add(time.Minute)
	require.NoError(t, s.Save(ctx, state))

	nextStateVersion, err := versioned.RecordVersion(ctx, "state")
	require.NoError(t, err)
	assert.Equal(t, stateVersion, nextStateVersion)

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, state.LastTick, got.LastTick)
}

func TestSchedulerStateSaveSkipsSameCheckpoint(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("scheduler")
	s := store.NewSchedulerStateStore(col)

	state := &schedulerstate.State{
		LastTick: time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC),
		DAGs: map[string]schedulerstate.DAGWatermark{
			"my-dag": {},
		},
	}
	require.NoError(t, s.Save(ctx, state))
	versioned := col.(interface {
		RecordVersion(context.Context, string) (string, error)
	})
	checkpointVersion, err := versioned.RecordVersion(ctx, "checkpoint")
	require.NoError(t, err)

	require.NoError(t, s.Save(ctx, state))

	nextCheckpointVersion, err := versioned.RecordVersion(ctx, "checkpoint")
	require.NoError(t, err)
	assert.Equal(t, checkpointVersion, nextCheckpointVersion)
}

func TestSchedulerStateSaveDoesNotAdvanceCacheWhenCheckpointWriteFails(t *testing.T) {
	ctx := context.Background()
	col := &checkpointFailCollection{Collection: testutil.NewMemoryBackend().Collection("scheduler")}
	s := store.NewSchedulerStateStore(col)

	initialTick := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	initialState := &schedulerstate.State{
		LastTick: initialTick,
		DAGs:     map[string]schedulerstate.DAGWatermark{"dag-a": {}},
	}
	require.NoError(t, s.Save(ctx, initialState))

	col.failCheckpoint = true
	nextState := &schedulerstate.State{
		LastTick: initialTick.Add(time.Minute),
		DAGs:     map[string]schedulerstate.DAGWatermark{"dag-b-longer-name": {}},
	}
	require.ErrorIs(t, s.Save(ctx, nextState), errCheckpointSave)

	col.failCheckpoint = false
	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialTick, got.LastTick)
	assert.Contains(t, got.DAGs, "dag-b-longer-name")
	assert.NotContains(t, got.DAGs, "dag-a")
}

func TestSchedulerStateSaveOverwrite(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerStateStore(t)

	now := time.Now().UTC()
	state1 := &schedulerstate.State{
		LastTick: now,
		DAGs:     map[string]schedulerstate.DAGWatermark{"dag-a": {}},
	}
	require.NoError(t, s.Save(ctx, state1))

	state2 := &schedulerstate.State{
		LastTick: now.Add(time.Minute),
		DAGs:     map[string]schedulerstate.DAGWatermark{"dag-b": {}},
	}
	require.NoError(t, s.Save(ctx, state2))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, now.Add(time.Minute), got.LastTick)
	assert.Contains(t, got.DAGs, "dag-b")
	assert.NotContains(t, got.DAGs, "dag-a")
}

func TestSchedulerStateLoadMigratesLegacyVersions(t *testing.T) {
	ctx := context.Background()

	for _, legacyVersion := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("version_%d", legacyVersion), func(t *testing.T) {
			col := testutil.NewMemoryBackend().Collection("scheduler")
			s := store.NewSchedulerStateStore(col)

			rawJSON := fmt.Appendf(nil, `{"version":%d,"dags":{}}`, legacyVersion)
			now := time.Now().UTC()
			require.NoError(t, col.Put(ctx, &persis.Record{
				ID:        "state",
				Data:      rawJSON,
				CreatedAt: now,
				UpdatedAt: now,
			}))

			got, err := s.Load(ctx)
			require.NoError(t, err)
			assert.NotNil(t, got.DAGs)
			require.NoError(t, s.Save(ctx, got))

			rec, err := col.Get(ctx, "state")
			require.NoError(t, err)
			var persisted struct {
				Version int `json:"version"`
			}
			require.NoError(t, json.Unmarshal(rec.Data, &persisted))
			assert.Equal(t, 4, persisted.Version)
		})
	}
}

func TestSchedulerStateLoadUnknownVersionFallsBackToEmpty(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("scheduler")
	s := store.NewSchedulerStateStore(col)

	rawJSON := []byte(`{"version":999,"dags":{}}`)
	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "state",
		Data:      rawJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Empty(t, got.DAGs)
}

func TestSchedulerStateLoadCorruptDataFallsBackToEmpty(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("scheduler")
	s := store.NewSchedulerStateStore(col)

	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "state",
		Data:      []byte(`not valid json {{`),
		CreatedAt: now,
		UpdatedAt: now,
	}))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Empty(t, got.DAGs)
}
