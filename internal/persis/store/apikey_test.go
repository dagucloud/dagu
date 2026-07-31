// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newAPIKeyStore(t *testing.T) *store.APIKeyStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("api_keys")
	s, err := store.NewAPIKeyStore(col)
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
		KeyDigest: "digest-" + name,
		KeyPrefix: "pfx1",
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: "admin",
	}
}

func TestAPIKeyCreate(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("my-key")

	require.NoError(t, s.Create(ctx, key))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	assert.Equal(t, key.ID, got.ID)
	assert.Equal(t, key.Name, got.Name)
	assert.Equal(t, key.KeyHash, got.KeyHash)
	assert.Equal(t, key.KeyDigest, got.KeyDigest)
	assert.Equal(t, key.Role, got.Role)
}

func TestAPIKeyGetByDigest(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("digest")
	require.NoError(t, s.Create(ctx, key))

	got, err := s.GetByDigest(ctx, key.KeyDigest)
	require.NoError(t, err)
	assert.Equal(t, key.ID, got.ID)

	_, err = s.GetByDigest(ctx, "missing")
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestAPIKeyCreate_DuplicateName(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)

	require.NoError(t, s.Create(ctx, newKey("dup")))

	dupe := newKey("dup")
	dupe.ID = "other-id"
	assert.ErrorIs(t, s.Create(ctx, dupe), auth.ErrAPIKeyAlreadyExists)
}

func TestAPIKeyGetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newAPIKeyStore(t).GetByID(ctx, "missing")
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestAPIKeyList(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)

	for _, name := range []string{"k1", "k2", "k3"} {
		require.NoError(t, s.Create(ctx, newKey(name)))
	}

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestAPIKeyUpdate(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
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

func TestAPIKeyUpdatePreservesCredentialAndLatestUsage(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("stale")
	key.KeyDigest = ""
	require.NoError(t, s.Create(ctx, key))

	stale, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NoError(t, s.PromoteDigest(ctx, key.ID, "promoted-digest"))
	require.NoError(t, s.UpdateLastUsed(ctx, key.ID))

	stale.Name = "updated-name"
	stale.KeyHash = "stale-hash"
	stale.KeyDigest = "stale-digest"
	oldUsage := time.Now().UTC().Add(-time.Hour)
	stale.LastUsedAt = &oldUsage
	require.NoError(t, s.Update(ctx, stale))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated-name", got.Name)
	assert.Equal(t, key.KeyHash, got.KeyHash)
	assert.Equal(t, "promoted-digest", got.KeyDigest)
	require.NotNil(t, got.LastUsedAt)
	assert.True(t, got.LastUsedAt.After(oldUsage))
}

func TestAPIKeyUpdate_NotFound(t *testing.T) {
	ctx := context.Background()
	err := newAPIKeyStore(t).Update(ctx, newKey("ghost"))
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestAPIKeyUpdate_NameChange(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("old-name")
	require.NoError(t, s.Create(ctx, key))

	key.Name = "new-name"
	require.NoError(t, s.Update(ctx, key))

	// old name slot is free
	another := newKey("old-name")
	another.ID = "another-id"
	another.KeyDigest = "another-digest"
	assert.NoError(t, s.Create(ctx, another))
}

func TestAPIKeyUpdate_NameConflict(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	require.NoError(t, s.Create(ctx, newKey("a")))
	b := newKey("b")
	require.NoError(t, s.Create(ctx, b))

	b.Name = "a" // conflicts with existing "a"
	assert.ErrorIs(t, s.Update(ctx, b), auth.ErrAPIKeyAlreadyExists)
}

func TestAPIKeyDelete(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("del")
	require.NoError(t, s.Create(ctx, key))

	require.NoError(t, s.Delete(ctx, key.ID))

	_, err := s.GetByID(ctx, key.ID)
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
	_, err = s.GetByDigest(ctx, key.KeyDigest)
	assert.ErrorIs(t, err, auth.ErrAPIKeyNotFound)

	// name slot freed
	another := newKey("del")
	another.ID = "fresh-id"
	assert.NoError(t, s.Create(ctx, another))
}

func TestAPIKeyDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newAPIKeyStore(t).Delete(ctx, "nope"), auth.ErrAPIKeyNotFound)
}

func TestAPIKeyPromoteDigest(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("legacy")
	key.KeyDigest = ""
	require.NoError(t, s.Create(ctx, key))

	require.NoError(t, s.PromoteDigest(ctx, key.ID, "promoted-digest"))
	require.NoError(t, s.PromoteDigest(ctx, key.ID, "promoted-digest"))

	got, err := s.GetByDigest(ctx, "promoted-digest")
	require.NoError(t, err)
	assert.Equal(t, key.ID, got.ID)
	assert.Equal(t, key.KeyHash, got.KeyHash)
}

func TestAPIKeyPromoteDigestRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	first := newKey("first")
	second := newKey("second")
	second.KeyDigest = ""
	require.NoError(t, s.Create(ctx, first))
	require.NoError(t, s.Create(ctx, second))

	assert.ErrorIs(t, s.PromoteDigest(ctx, second.ID, first.KeyDigest), auth.ErrAPIKeyAlreadyExists)
}

func TestAPIKeyUpdateLastUsed(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("lu")
	require.NoError(t, s.Create(ctx, key))

	before := time.Now().UTC()
	require.NoError(t, s.UpdateLastUsed(ctx, key.ID))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.False(t, got.LastUsedAt.Before(before))
}

func TestAPIKeyUpdateLastUsedKeepsRecentTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("recent")
	recent := time.Now().UTC().Add(-30 * time.Second)
	key.LastUsedAt = &recent
	require.NoError(t, s.Create(ctx, key))

	require.NoError(t, s.UpdateLastUsed(ctx, key.ID))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.Equal(t, recent, *got.LastUsedAt)
}

func TestAPIKeyUpdateLastUsedConcurrent(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("concurrent-usage")
	require.NoError(t, s.Create(ctx, key))

	const workers = 100
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			errs <- s.UpdateLastUsed(ctx, key.ID)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
}

func TestAPIKeyUpdateLastUsedRefreshesStaleTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newAPIKeyStore(t)
	key := newKey("stale-usage")
	stale := time.Now().UTC().Add(-time.Minute - time.Second)
	key.LastUsedAt = &stale
	require.NoError(t, s.Create(ctx, key))

	require.NoError(t, s.UpdateLastUsed(ctx, key.ID))

	got, err := s.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.True(t, got.LastUsedAt.After(stale))
}

func TestAPIKeyUpdateLastUsed_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newAPIKeyStore(t).UpdateLastUsed(ctx, "nope"), auth.ErrAPIKeyNotFound)
}

func TestAPIKeyIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("api_keys")

	s1, err := store.NewAPIKeyStore(col)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newKey("k1")))
	require.NoError(t, s1.Create(ctx, newKey("k2")))

	// New store over same collection simulates restart.
	s2, err := store.NewAPIKeyStore(col)
	require.NoError(t, err)

	list, err := s2.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// Name uniqueness enforced after rebuild.
	dupe := newKey("k1")
	dupe.ID = "other"
	assert.ErrorIs(t, s2.Create(ctx, dupe), auth.ErrAPIKeyAlreadyExists)

	// Digest lookup is available after rebuild.
	got, err := s2.GetByDigest(ctx, "digest-k2")
	require.NoError(t, err)
	assert.Equal(t, "id-k2", got.ID)
}
