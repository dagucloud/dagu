// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/dagsettings"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGSettingsStoreUpsertGetDelete(t *testing.T) {
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	settingsStore, err := store.NewDAGSettingsStore(backend.Collection("dag-settings"))
	require.NoError(t, err)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	settings, err := dagsettings.New(dagsettings.UpdateInput{
		DAGName:   "example",
		Profile:   "prod",
		UpdatedBy: "user-1",
	}, now)
	require.NoError(t, err)

	require.NoError(t, settingsStore.Upsert(ctx, settings))

	got, err := settingsStore.Get(ctx, "example")
	require.NoError(t, err)
	assert.Equal(t, "prod", got.Profile)
	assert.Equal(t, "user-1", got.UpdatedBy)

	got.Profile = "mutated"
	gotAgain, err := settingsStore.Get(ctx, "example")
	require.NoError(t, err)
	assert.Equal(t, "prod", gotAgain.Profile)

	updated := gotAgain.Clone()
	require.NoError(t, updated.ApplyUpdate(dagsettings.UpdateInput{
		DAGName:   "example",
		Profile:   "staging",
		UpdatedBy: "user-2",
	}, now.Add(time.Hour)))

	require.NoError(t, settingsStore.Upsert(ctx, updated))
	got, err = settingsStore.Get(ctx, "example")
	require.NoError(t, err)
	assert.Equal(t, "staging", got.Profile)
	assert.Equal(t, "user-2", got.UpdatedBy)

	require.NoError(t, settingsStore.Delete(ctx, "example"))
	_, err = settingsStore.Get(ctx, "example")
	require.ErrorIs(t, err, dagsettings.ErrNotFound)
}

func TestDAGSettingsStoreDeleteMissingIsIdempotent(t *testing.T) {
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	settingsStore, err := store.NewDAGSettingsStore(backend.Collection("dag-settings"))
	require.NoError(t, err)

	err = settingsStore.Delete(ctx, "missing")
	require.NoError(t, err)
}
