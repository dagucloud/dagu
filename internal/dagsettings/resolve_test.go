// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagsettings_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/dagsettings"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveProfile(t *testing.T) {
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	settingsStore, err := store.NewDAGSettingsStore(backend.Collection("dag-settings"))
	require.NoError(t, err)
	profileStore, err := store.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)

	prof, err := profile.New(profile.CreateInput{Name: "prod"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(ctx, prof))
	settings, err := dagsettings.New(dagsettings.UpdateInput{
		DAGName: "example",
		Profile: "prod",
	}, time.Now())
	require.NoError(t, err)
	require.NoError(t, settingsStore.Upsert(ctx, settings))

	resolved, err := dagsettings.ResolveProfile(ctx, settingsStore, profileStore, "example")
	require.NoError(t, err)
	assert.Equal(t, "prod", resolved)
}

func TestResolveProfileMissingSettingsReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	settingsStore, err := store.NewDAGSettingsStore(backend.Collection("dag-settings"))
	require.NoError(t, err)
	profileStore, err := store.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)

	resolved, err := dagsettings.ResolveProfile(ctx, settingsStore, profileStore, "example")
	require.NoError(t, err)
	assert.Empty(t, resolved)
}

func TestResolveProfileReturnsDisabledProfileError(t *testing.T) {
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	settingsStore, err := store.NewDAGSettingsStore(backend.Collection("dag-settings"))
	require.NoError(t, err)
	profileStore, err := store.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)

	prof, err := profile.New(profile.CreateInput{Name: "prod"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, prof.SetStatus(profile.StatusDisabled, "test", time.Now()))
	require.NoError(t, profileStore.Create(ctx, prof))
	settings, err := dagsettings.New(dagsettings.UpdateInput{
		DAGName: "example",
		Profile: "prod",
	}, time.Now())
	require.NoError(t, err)
	require.NoError(t, settingsStore.Upsert(ctx, settings))

	_, err = dagsettings.ResolveProfile(ctx, settingsStore, profileStore, "example")
	require.ErrorIs(t, err, profile.ErrDisabled)
}
