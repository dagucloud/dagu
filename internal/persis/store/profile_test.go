// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/profile"
)

func newProfileStore(t *testing.T) *store.ProfileStore {
	t.Helper()
	s, err := store.NewProfileStore(testutil.NewMemoryBackend().Collection("profiles"))
	require.NoError(t, err)
	return s
}

func TestProfileStoreCreateGetList(t *testing.T) {
	ctx := context.Background()
	s := newProfileStore(t)

	local, err := profile.New(profile.CreateInput{Name: "local", CreatedBy: "alice"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, local.SetVariable("LOG_LEVEL", "debug", "alice", time.Now()))
	require.NoError(t, s.Create(ctx, local))

	prod, err := profile.New(profile.CreateInput{Name: "prod", Protected: true, CreatedBy: "alice"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, prod))

	got, err := s.GetByName(ctx, "local")
	require.NoError(t, err)
	assert.Equal(t, "local", got.Name)
	assert.Len(t, got.Entries, 1)

	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "local", all[0].Name)
	assert.Equal(t, "prod", all[1].Name)
}

func TestProfileStoreInheritedProfilesUseSameCollectionButNotRuntimeList(t *testing.T) {
	ctx := context.Background()
	s := newProfileStore(t)

	runtimeProfile, err := profile.New(profile.CreateInput{Name: "prod"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, runtimeProfile))

	globalProfile, err := profile.NewInherited(profile.GlobalInheritedRef(), profile.InheritedCreateInput{
		Description: "Global defaults",
		CreatedBy:   "alice",
	}, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, globalProfile))

	got, err := s.GetInherited(ctx, profile.GlobalInheritedRef())
	require.NoError(t, err)
	assert.Equal(t, "_global", got.Name)
	assert.Equal(t, "Global defaults", got.Description)

	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "prod", all[0].Name)
}

func TestProfileStoreInheritedProfilesMustStayActiveProtected(t *testing.T) {
	ctx := context.Background()
	s := newProfileStore(t)

	globalProfile, err := profile.NewInherited(profile.GlobalInheritedRef(), profile.InheritedCreateInput{
		Description: "Global defaults",
		CreatedBy:   "alice",
	}, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, globalProfile))

	globalProfile.Protected = false
	err = s.Update(ctx, globalProfile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inherited profiles must be protected")

	globalProfile.Protected = true
	globalProfile.Status = profile.StatusDisabled
	err = s.Update(ctx, globalProfile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inherited profiles must be active")
}

func TestProfileStoreCreateRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	s := newProfileStore(t)

	p1, err := profile.New(profile.CreateInput{Name: "local"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, p1))

	p2, err := profile.New(profile.CreateInput{Name: "local"}, time.Now())
	require.NoError(t, err)
	assert.ErrorIs(t, s.Create(ctx, p2), profile.ErrAlreadyExists)
}

func TestProfileStoreUpdate(t *testing.T) {
	ctx := context.Background()
	s := newProfileStore(t)

	p, err := profile.New(profile.CreateInput{Name: "local"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, p))

	require.NoError(t, p.SetVariable("LOG_LEVEL", "info", "alice", time.Now()))
	p.Description = "updated"
	require.NoError(t, s.Update(ctx, p))

	got, err := s.GetByName(ctx, "local")
	require.NoError(t, err)
	assert.Equal(t, "updated", got.Description)
	require.Len(t, got.Entries, 1)
	assert.Equal(t, "info", got.Entries[0].Value)
}

func TestProfileStoreListReturnsDecodeError(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("profiles")
	s, err := store.NewProfileStore(col)
	require.NoError(t, err)

	now := time.Now()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "broken",
		Data:      []byte("{"),
		CreatedAt: now,
		UpdatedAt: now,
	}))

	_, err = s.List(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `profile store: decode "broken"`)
}
