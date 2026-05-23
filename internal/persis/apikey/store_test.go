// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package apikey_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/persis/apikey"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newStore(t *testing.T) *apikey.Store {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("api_keys")
	s, err := apikey.New(col)
	require.NoError(t, err)
	return s
}

func newKey(name string) *auth.APIKey {
	now := time.Now().UTC()
	return &auth.APIKey{
		ID:        "id-" + name,
		Name:      name,
		Role:      auth.RoleAdmin,
		KeyHash:   "hash-" + name,
		KeyPrefix: "pfx1",
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: "admin",
	}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	key := newKey("my-key")

	require.NoError(t, s.Create(ctx, key))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	assert.Equal(t, key.ID, got.ID)
	assert.Equal(t, key.Name, got.Name)
	assert.Equal(t, key.KeyHash, got.KeyHash)
	assert.Equal(t, key.Role, got.Role)
}

func TestCreate_DuplicateName(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Create(ctx, newKey("dup")))

	dupe := newKey("dup")
	dupe.ID = "other-id"
	assert.ErrorIs(t, s.Create(ctx, dupe), auth.ErrAPIKeyAlreadyExists)
}

func TestGetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newStore(t).GetByID(ctx, "missing")
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestList(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	for _, name := range []string{"k1", "k2", "k3"} {
		require.NoError(t, s.Create(ctx, newKey(name)))
	}

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	key := newKey("upd")
	require.NoError(t, s.Create(ctx, key))

	key.Description = "updated desc"
	key.Role = auth.RoleViewer
	require.NoError(t, s.Update(ctx, key))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated desc", got.Description)
	assert.Equal(t, auth.RoleViewer, got.Role)
}

func TestUpdate_NotFound(t *testing.T) {
	ctx := context.Background()
	err := newStore(t).Update(ctx, newKey("ghost"))
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestUpdate_NameChange(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	key := newKey("old-name")
	require.NoError(t, s.Create(ctx, key))

	key.Name = "new-name"
	require.NoError(t, s.Update(ctx, key))

	// old name slot is free
	another := newKey("old-name")
	another.ID = "another-id"
	assert.NoError(t, s.Create(ctx, another))
}

func TestUpdate_NameConflict(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	require.NoError(t, s.Create(ctx, newKey("a")))
	b := newKey("b")
	require.NoError(t, s.Create(ctx, b))

	b.Name = "a" // conflicts with existing "a"
	assert.ErrorIs(t, s.Update(ctx, b), auth.ErrAPIKeyAlreadyExists)
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	key := newKey("del")
	require.NoError(t, s.Create(ctx, key))

	require.NoError(t, s.Delete(ctx, key.ID))

	_, err := s.GetByID(ctx, key.ID)
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)

	// name slot freed
	another := newKey("del")
	another.ID = "fresh-id"
	assert.NoError(t, s.Create(ctx, another))
}

func TestDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newStore(t).Delete(ctx, "nope"), auth.ErrAPIKeyNotFound)
}

func TestUpdateLastUsed(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	key := newKey("lu")
	require.NoError(t, s.Create(ctx, key))

	before := time.Now().UTC()
	require.NoError(t, s.UpdateLastUsed(ctx, key.ID))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.False(t, got.LastUsedAt.Before(before))
}

func TestUpdateLastUsed_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newStore(t).UpdateLastUsed(ctx, "nope"), auth.ErrAPIKeyNotFound)
}

func TestIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("api_keys")

	s1, err := apikey.New(col)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newKey("k1")))
	require.NoError(t, s1.Create(ctx, newKey("k2")))

	// New Store over same collection simulates restart.
	s2, err := apikey.New(col)
	require.NoError(t, err)

	list, err := s2.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// Name uniqueness enforced after rebuild.
	dupe := newKey("k1")
	dupe.ID = "other"
	assert.ErrorIs(t, s2.Create(ctx, dupe), auth.ErrAPIKeyAlreadyExists)
}
