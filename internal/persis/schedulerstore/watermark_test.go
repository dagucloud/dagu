// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schedulerstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/schedulerstore"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/service/scheduler"
)

func newWatermarkStore(t *testing.T) *schedulerstore.WatermarkStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("watermark")
	return schedulerstore.NewWatermarkStore(col)
}

func TestWatermarkLoad_Empty(t *testing.T) {
	ctx := context.Background()
	s := newWatermarkStore(t)

	state, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, scheduler.SchedulerStateVersion, state.Version)
	assert.NotNil(t, state.DAGs)
	assert.Empty(t, state.DAGs)
}

func TestWatermarkSaveAndLoad(t *testing.T) {
	ctx := context.Background()
	s := newWatermarkStore(t)

	now := time.Now().UTC().Truncate(time.Millisecond)
	state := &scheduler.SchedulerState{
		Version:  scheduler.SchedulerStateVersion,
		LastTick: now,
		DAGs: map[string]scheduler.DAGWatermark{
			"my-dag": {
				LastScheduledTime: now,
			},
		},
	}

	require.NoError(t, s.Save(ctx, state))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, scheduler.SchedulerStateVersion, got.Version)
	assert.Equal(t, now, got.LastTick)
	assert.Contains(t, got.DAGs, "my-dag")
	assert.Equal(t, now, got.DAGs["my-dag"].LastScheduledTime)
}

func TestWatermarkSaveFileLayoutCompatibility(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	backend, err := file.New(root)
	require.NoError(t, err)
	s := schedulerstore.NewWatermarkStore(backend.Collection("scheduler"))
	state := &scheduler.SchedulerState{
		Version: scheduler.SchedulerStateVersion,
		DAGs: map[string]scheduler.DAGWatermark{
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
	assert.Contains(t, body, "version")
	assert.Contains(t, body, "dags")
}

func TestWatermarkSave_Overwrite(t *testing.T) {
	ctx := context.Background()
	s := newWatermarkStore(t)

	now := time.Now().UTC()
	state1 := &scheduler.SchedulerState{
		Version:  scheduler.SchedulerStateVersion,
		LastTick: now,
		DAGs:     map[string]scheduler.DAGWatermark{"dag-a": {}},
	}
	require.NoError(t, s.Save(ctx, state1))

	state2 := &scheduler.SchedulerState{
		Version:  scheduler.SchedulerStateVersion,
		LastTick: now.Add(time.Minute),
		DAGs:     map[string]scheduler.DAGWatermark{"dag-b": {}},
	}
	require.NoError(t, s.Save(ctx, state2))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, now.Add(time.Minute), got.LastTick)
	assert.Contains(t, got.DAGs, "dag-b")
	assert.NotContains(t, got.DAGs, "dag-a")
}

func TestLoad_MigratesLegacyVersions(t *testing.T) {
	ctx := context.Background()

	for _, legacyVersion := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("version_%d", legacyVersion), func(t *testing.T) {
			col := testutil.NewMemoryBackend().Collection("watermark")
			s := schedulerstore.NewWatermarkStore(col)

			rawJSON := fmt.Appendf(nil, `{"version":%d,"dags":{}}`, legacyVersion)
			now := time.Now().UTC()
			require.NoError(t, col.Put(ctx, &persis.Record{
				ID:        "state",
				Data:      rawJSON,
				Encoding:  persis.EncodingJSON,
				CreatedAt: now,
				UpdatedAt: now,
			}))

			got, err := s.Load(ctx)
			require.NoError(t, err)
			assert.Equal(t, scheduler.SchedulerStateVersion, got.Version,
				"version %d should be migrated to current version", legacyVersion)
		})
	}
}

func TestLoad_UnknownVersionFallsBackToEmpty(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("watermark")
	s := schedulerstore.NewWatermarkStore(col)

	rawJSON := []byte(`{"version":999,"dags":{}}`)
	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "state",
		Data:      rawJSON,
		Encoding:  persis.EncodingJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, scheduler.SchedulerStateVersion, got.Version)
	assert.Empty(t, got.DAGs)
}

func TestLoad_CorruptDataFallsBackToEmpty(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("watermark")
	s := schedulerstore.NewWatermarkStore(col)

	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "state",
		Data:      []byte(`not valid json {{`),
		Encoding:  persis.EncodingJSON,
		CreatedAt: now,
		UpdatedAt: now,
	}))

	got, err := s.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, scheduler.SchedulerStateVersion, got.Version)
	assert.Empty(t, got.DAGs)
}
