// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMemoryDAGPinStore(t *testing.T) (*store.DAGPinStore, persis.Collection) {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection(persis.CollectionDAGPins)
	pinStore, err := store.NewDAGPinStore(col)
	require.NoError(t, err)
	return pinStore, col
}

func pinSet(ids ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func TestDAGPinStorePinUnpin(t *testing.T) {
	ctx := context.Background()
	pinStore, _ := newMemoryDAGPinStore(t)

	pins, err := pinStore.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, pins)

	require.NoError(t, pinStore.Pin(ctx, "etl"))
	require.NoError(t, pinStore.Pin(ctx, "billing"))
	require.NoError(t, pinStore.Pin(ctx, "etl"))
	require.NoError(t, pinStore.Unpin(ctx, "missing"))

	pins, err = pinStore.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, pinSet("billing", "etl"), pins)

	require.NoError(t, pinStore.Unpin(ctx, "etl"))
	pins, err = pinStore.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, pinSet("billing"), pins)
}

func TestDAGPinStoreRename(t *testing.T) {
	ctx := context.Background()
	pinStore, _ := newMemoryDAGPinStore(t)
	require.NoError(t, pinStore.Pin(ctx, "old"))
	require.NoError(t, pinStore.Pin(ctx, "other"))

	require.NoError(t, pinStore.Rename(ctx, "old", "new"))
	require.NoError(t, pinStore.Rename(ctx, "unpinned", "renamed"))
	require.NoError(t, pinStore.Rename(ctx, "new", "other"))

	pins, err := pinStore.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, pinSet("other"), pins)
}

func TestDAGPinStoreRejectsInvalidIDs(t *testing.T) {
	ctx := context.Background()
	pinStore, _ := newMemoryDAGPinStore(t)

	require.Error(t, pinStore.Pin(ctx, ""))
	require.Error(t, pinStore.Pin(ctx, "nested/dag"))
	require.Error(t, pinStore.Rename(ctx, "etl", ".."))
}

// Each pin rewrites the shared record, so concurrent pins must retry rather
// than overwrite one another.
func TestDAGPinStoreConcurrentPins(t *testing.T) {
	ctx := context.Background()
	pinStore, _ := newMemoryDAGPinStore(t)

	const count = 20
	var wg sync.WaitGroup
	errs := make(chan error, count)
	want := make([]string, 0, count)
	for i := range count {
		id := fmt.Sprintf("dag-%d", i)
		want = append(want, id)
		wg.Go(func() {
			errs <- pinStore.Pin(ctx, id)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	pins, err := pinStore.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, pinSet(want...), pins)
}

func TestDAGPinStoreCorruptRecord(t *testing.T) {
	ctx := context.Background()
	pinStore, col := newMemoryDAGPinStore(t)
	corrupt := []byte(`{"fileNames":`)
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "pins", Data: corrupt}))

	_, err := pinStore.List(ctx)
	require.ErrorIs(t, err, persis.ErrCorrupt)
	require.ErrorIs(t, pinStore.Pin(ctx, "etl"), persis.ErrCorrupt)

	rec, err := col.Get(ctx, "pins")
	require.NoError(t, err)
	assert.Equal(t, corrupt, rec.Data)
}

// The file backend stores indented JSON; updates must still match it when
// swapping the record.
func TestDAGPinStoreFileBackend(t *testing.T) {
	ctx := context.Background()
	backend := file.NewBackend(config.PathsConfig{DataDir: t.TempDir()})
	pinStore, err := store.NewDAGPinStore(backend.Collection(persis.CollectionDAGPins))
	require.NoError(t, err)

	require.NoError(t, pinStore.Pin(ctx, "etl"))
	require.NoError(t, pinStore.Pin(ctx, "billing"))
	require.NoError(t, pinStore.Unpin(ctx, "etl"))

	pins, err := pinStore.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, pinSet("billing"), pins)
}
