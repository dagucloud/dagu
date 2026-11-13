// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newAIEndpointStore(t *testing.T) (*store.AIEndpointStore, persis.Collection) {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("ai_endpoints")
	s, err := store.NewAIEndpointStore(col)
	require.NoError(t, err)
	return s, col
}

func aiEndpoint(scope aiendpoint.Scope, name string) *aiendpoint.Endpoint {
	now := time.Now().UTC()
	return &aiendpoint.Endpoint{
		Name:           name,
		Preset:         "anthropic",
		Protocol:       aiendpoint.ProtocolAnthropic,
		BaseURL:        "https://api.anthropic.com",
		Auth:           aiendpoint.AuthXAPIKey,
		Scope:          scope,
		Allow:          aiendpoint.Allow{DAGs: []string{"*"}},
		SecretID:       "secret-" + name,
		KeyPrefix:      "sk-ant",
		KeyFingerprint: "0123456789ab",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestAIEndpointStoreCreateGetListDelete(t *testing.T) {
	ctx := context.Background()
	s, col := newAIEndpointStore(t)

	require.NoError(t, s.Create(ctx, aiEndpoint(aiendpoint.GlobalScope(), "cle1")))
	require.NoError(t, s.Create(ctx, aiEndpoint(aiendpoint.WorkspaceScope("team"), "cle1")))
	require.NoError(t, s.Create(ctx, aiEndpoint(aiendpoint.GlobalScope(), "alpha")))

	// The record IDs have the scope, so a name is unique in its scope only.
	_, err := col.Get(ctx, "global/cle1")
	require.NoError(t, err)
	_, err = col.Get(ctx, "workspaces/team/cle1")
	require.NoError(t, err)
	require.ErrorIs(t, s.Create(ctx, aiEndpoint(aiendpoint.GlobalScope(), "cle1")), aiendpoint.ErrAlreadyExists)

	got, err := s.Get(ctx, aiendpoint.WorkspaceScope("team"), "cle1")
	require.NoError(t, err)
	assert.Equal(t, "secret-cle1", got.SecretID)
	assert.Equal(t, "team", got.Scope.Workspace)

	// The global scope comes first, then the names in order.
	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "alpha", all[0].Name)
	assert.True(t, all[0].Scope.IsGlobal())
	assert.Equal(t, "cle1", all[1].Name)
	assert.True(t, all[1].Scope.IsGlobal())
	assert.Equal(t, "team", all[2].Scope.Workspace)

	require.NoError(t, s.Delete(ctx, aiendpoint.GlobalScope(), "cle1"))
	_, err = s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
	require.ErrorIs(t, s.Delete(ctx, aiendpoint.GlobalScope(), "cle1"), aiendpoint.ErrNotFound)
	_, err = s.Get(ctx, aiendpoint.WorkspaceScope("team"), "cle1")
	require.NoError(t, err, "the workspace endpoint stays")
}

func TestAIEndpointStoreUpdate(t *testing.T) {
	ctx := context.Background()
	s, _ := newAIEndpointStore(t)
	ep := aiEndpoint(aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, s.Create(ctx, ep))

	saved, err := s.Update(ctx, ep.Scope, "cle1", func(ep *aiendpoint.Endpoint) error {
		ep.Description = "Changed"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "Changed", saved.Description)
	got, err := s.Get(ctx, ep.Scope, "cle1")
	require.NoError(t, err)
	assert.Equal(t, "Changed", got.Description)

	_, err = s.Update(ctx, aiendpoint.GlobalScope(), "nope", func(*aiendpoint.Endpoint) error { return nil })
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
}

// Two servers share the data directory. A key rotation of one server between the
// read and the save of an update of the other is kept: the update reads again.
func TestAIEndpointStoreUpdateKeepsAWriteOfAnotherProcess(t *testing.T) {
	ctx := context.Background()
	col := &interleavedCollection{Collection: testutil.NewMemoryBackend().Collection("ai_endpoints")}
	s, err := store.NewAIEndpointStore(col)
	require.NoError(t, err)
	other, err := store.NewAIEndpointStore(col.Collection)
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, aiEndpoint(aiendpoint.GlobalScope(), "cle1")))

	col.before = func() {
		_, err := other.Update(ctx, aiendpoint.GlobalScope(), "cle1", func(ep *aiendpoint.Endpoint) error {
			ep.SecretID, ep.KeyFingerprint = "secret-rotated", "fedcba987654"
			return nil
		})
		require.NoError(t, err)
	}
	col.armed.Store(true)
	saved, err := s.Update(ctx, aiendpoint.GlobalScope(), "cle1", func(ep *aiendpoint.Endpoint) error {
		ep.Description = "Changed"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "Changed", saved.Description)
	assert.Equal(t, "secret-rotated", saved.SecretID, "the rotation of the other process stays")
	assert.Equal(t, "fedcba987654", saved.KeyFingerprint)
}

// One record that cannot be read does not hide the other endpoints.
func TestAIEndpointStoreListSkipsABadRecord(t *testing.T) {
	ctx := context.Background()
	s, col := newAIEndpointStore(t)
	require.NoError(t, s.Create(ctx, aiEndpoint(aiendpoint.GlobalScope(), "cle1")))
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "global/broken", Data: []byte(`{"other":1}`)}))
	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "cle1", all[0].Name)
}

// interleavedCollection, once armed, runs before before its next swap: the write
// of another process between a read and a save.
type interleavedCollection struct {
	persis.Collection
	armed  atomic.Bool
	before func()
}

func (c *interleavedCollection) CompareAndSwap(ctx context.Context, id string, expected, next []byte) error {
	if c.armed.CompareAndSwap(true, false) {
		c.before()
	}
	return c.Collection.CompareAndSwap(ctx, id, expected, next)
}

func TestAIEndpointStoreRejectsInvalidNames(t *testing.T) {
	ctx := context.Background()
	s, _ := newAIEndpointStore(t)

	// A name with a slash cannot reach another record.
	require.ErrorIs(t, s.Create(ctx, aiEndpoint(aiendpoint.GlobalScope(), "../x")), aiendpoint.ErrInvalid)
	_, err := s.Get(ctx, aiendpoint.GlobalScope(), "a/b")
	require.ErrorIs(t, err, aiendpoint.ErrInvalid)
}
