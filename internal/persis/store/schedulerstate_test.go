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
	assert.Equal(t, schedulerstate.CurrentVersion, state.Version)
	assert.NotNil(t, state.DAGs)
	assert.Empty(t, state.DAGs)
}

func TestSchedulerStateSaveAndLoad(t *testing.T) {
	ctx := context.Background()
	s := newSchedulerStateStore(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	state := &schedulerstate.State{
		Version:  schedulerstate.CurrentVersion,
		LastTick: now,
		DAGs: map[string]schedulerstate.DAGWatermark{
			"my-dag": {LastScheduledTime: now},
		},
	}

	require.NoError(t, s.Save(ctx, state))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, schedulerstate.CurrentVersion, got.Version)
	assert.Equal(t, now, got.LastTick)
	assert.Contains(t, got.DAGs, "my-dag")
	assert.Equal(t, now, got.DAGs["my-dag"].LastScheduledTime)
}

func TestSchedulerStateFileLayoutCompatibility(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(filepath.Join(root, "scheduler"), file.WithIndentedJSON())
	s := store.NewSchedulerStateStore(col)
	now := time.Now().UTC()
	state := &schedulerstate.State{
		Version:  schedulerstate.CurrentVersion,
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
	require.Equal(t, schedulerstate.CurrentVersion, got.Version)
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
		Version:  schedulerstate.CurrentVersion,
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
		Version:  schedulerstate.CurrentVersion,
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
		Version:  schedulerstate.CurrentVersion,
		LastTick: initialTick,
		DAGs:     map[string]schedulerstate.DAGWatermark{"dag-a": {}},
	}
	require.NoError(t, s.Save(ctx, initialState))

	col.failCheckpoint = true
	nextState := &schedulerstate.State{
		Version:  schedulerstate.CurrentVersion,
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
		Version:  schedulerstate.CurrentVersion,
		LastTick: now,
		DAGs:     map[string]schedulerstate.DAGWatermark{"dag-a": {}},
	}
	require.NoError(t, s.Save(ctx, state1))

	state2 := &schedulerstate.State{
		Version:  schedulerstate.CurrentVersion,
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
			assert.Equal(t, schedulerstate.CurrentVersion, got.Version,
				"version %d should be migrated to current version", legacyVersion)
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
	assert.Equal(t, schedulerstate.CurrentVersion, got.Version)
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
	assert.Equal(t, schedulerstate.CurrentVersion, got.Version)
	assert.Empty(t, got.DAGs)
}
