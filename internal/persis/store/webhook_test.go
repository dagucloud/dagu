// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newWebhookStore(t *testing.T) *store.WebhookStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("webhooks")
	s, err := store.NewWebhookStore(col, nil)
	require.NoError(t, err)
	return s
}

func newWebhook(dagName string) *auth.Webhook {
	now := time.Now().UTC()
	return &auth.Webhook{
		ID:          "wh-" + dagName,
		DAGName:     dagName,
		TokenHash:   "hash-" + dagName,
		TokenPrefix: "tok1",
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   "admin",
	}
}

func TestWebhookCreate(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("my-dag")

	require.NoError(t, s.Create(ctx, wh))

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.Equal(t, wh.ID, got.ID)
	assert.Equal(t, wh.DAGName, got.DAGName)
	assert.Equal(t, wh.TokenHash, got.TokenHash)
	assert.Equal(t, wh.Enabled, got.Enabled)
}

func TestWebhookCreate_DuplicateDAGName(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	require.NoError(t, s.Create(ctx, newWebhook("dag-a")))

	duplicate := newWebhook("dag-a")
	duplicate.ID = "different-id"
	err := s.Create(ctx, duplicate)
	assert.ErrorIs(t, err, auth.ErrWebhookAlreadyExists)
}

func TestWebhookGetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	_, err := s.GetByID(ctx, "no-such-id")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookGetByDAGName(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-x")
	require.NoError(t, s.Create(ctx, wh))

	got, err := s.GetByDAGName(ctx, "dag-x")
	require.NoError(t, err)
	assert.Equal(t, wh.ID, got.ID)
}

func TestWebhookGetByDAGName_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	_, err := s.GetByDAGName(ctx, "unknown-dag")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookList(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	for _, name := range []string{"d1", "d2", "d3"} {
		require.NoError(t, s.Create(ctx, newWebhook(name)))
	}

	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestWebhookUpdate(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-u")
	require.NoError(t, s.Create(ctx, wh))

	wh.Enabled = false
	wh.TokenPrefix = "tok2"
	require.NoError(t, s.Update(ctx, wh))

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	assert.Equal(t, "tok2", got.TokenPrefix)
}

func TestWebhookUpdate_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	err := s.Update(ctx, newWebhook("ghost"))
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookUpdate_DAGNameChange(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("old-dag")
	require.NoError(t, s.Create(ctx, wh))

	wh.DAGName = "new-dag"
	require.NoError(t, s.Update(ctx, wh))

	// old name no longer resolves
	_, err := s.GetByDAGName(ctx, "old-dag")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)

	// new name resolves
	got, err := s.GetByDAGName(ctx, "new-dag")
	require.NoError(t, err)
	assert.Equal(t, wh.ID, got.ID)
}

func TestWebhookDelete(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-del")
	require.NoError(t, s.Create(ctx, wh))

	require.NoError(t, s.Delete(ctx, wh.ID))

	_, err := s.GetByID(ctx, wh.ID)
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)

	// DAGName index cleaned up
	_, err = s.GetByDAGName(ctx, wh.DAGName)
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	err := s.Delete(ctx, "nonexistent")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookDeleteByDAGName(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-dbn")
	require.NoError(t, s.Create(ctx, wh))

	require.NoError(t, s.DeleteByDAGName(ctx, "dag-dbn"))

	_, err := s.GetByID(ctx, wh.ID)
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookDeleteByDAGName_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	err := s.DeleteByDAGName(ctx, "no-dag")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookUpdateLastUsed(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)
	wh := newWebhook("dag-lu")
	require.NoError(t, s.Create(ctx, wh))

	before := time.Now().UTC()
	require.NoError(t, s.UpdateLastUsed(ctx, wh.ID))

	got, err := s.GetByID(ctx, wh.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastUsedAt)
	assert.True(t, got.LastUsedAt.After(before) || got.LastUsedAt.Equal(before))
}

func TestWebhookUpdateLastUsed_NotFound(t *testing.T) {
	ctx := context.Background()
	s := newWebhookStore(t)

	err := s.UpdateLastUsed(ctx, "nonexistent")
	assert.ErrorIs(t, err, auth.ErrWebhookNotFound)
}

func TestWebhookIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()

	// Put records directly into the collection, then create a new store
	// to verify the index is rebuilt correctly from persisted data.
	col := testutil.NewMemoryBackend().Collection("webhooks")

	s1, err := store.NewWebhookStore(col, nil)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newWebhook("d1")))
	require.NoError(t, s1.Create(ctx, newWebhook("d2")))

	// Create a second store over the same collection (simulates restart).
	s2, err := store.NewWebhookStore(col, nil)
	require.NoError(t, err)

	got, err := s2.GetByDAGName(ctx, "d1")
	require.NoError(t, err)
	assert.Equal(t, "wh-d1", got.ID)

	got, err = s2.GetByDAGName(ctx, "d2")
	require.NoError(t, err)
	assert.Equal(t, "wh-d2", got.ID)
}
